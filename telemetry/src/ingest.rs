use std::net::IpAddr;

use jiff::Timestamp;
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::envelope::Envelope;
use crate::error::{Error, Result};
use crate::geoip::{Geo, GeoIpLookup};
use crate::model::Document;
use crate::service::ServiceContext;

const CONTENT_CHUNK_BYTES: usize = 3 << 20;
const MAX_INDEXED_JSON_BYTES: usize = 2 << 20;
const MAX_REPLAY_HEADER_BYTES: usize = 64 << 10;
const MAX_TITLE_BYTES: usize = 2048;
const MAX_MESSAGE_BYTES: usize = 16 << 10;
const MAX_ATTRIBUTE_BYTES: usize = 2048;
const MAX_FINGERPRINT_COMPONENTS: usize = 32;
const MAX_GROUPING_FRAMES: usize = 5;
const SPAN_V2_CONTENT_TYPE: &str = "application/vnd.sentry.items.span.v2+json";

pub struct PreparedIngest {
    pub documents: Vec<Document>,
    pub events: Vec<IngestedEvent>,
    pub profiles: Vec<Value>,
    pub standalone_spans: Vec<StandaloneSpan>,
    pub transactions: Vec<Value>,
    pub response_event_id: String,
}

#[derive(Clone, Debug)]
pub struct StandaloneSpan {
    pub payload: Value,
    pub version: StandaloneSpanVersion,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StandaloneSpanVersion {
    Legacy,
    V2,
}

#[derive(Clone, Debug)]
pub struct IngestedEvent {
    pub event_id: String,
    pub issue_id: String,
    pub title: String,
    pub level: String,
    pub platform: String,
    pub timestamp: String,
    pub payload: Value,
}

pub fn prepare(
    service: &ServiceContext,
    envelope: Envelope,
    client_ip: Option<IpAddr>,
    request_geo: Option<Geo>,
    geoip: &GeoIpLookup,
) -> Result<PreparedIngest> {
    let received_at = Timestamp::now().to_string();
    let ingest_id = Uuid::new_v4().simple().to_string();
    let envelope_event_id = string(&envelope.headers, "event_id").and_then(normalize_event_id);
    let envelope_replay_id = envelope
        .headers
        .get("trace")
        .and_then(Value::as_object)
        .and_then(|trace| string(trace, "replay_id"))
        .and_then(normalize_event_id);
    validate_span_items(&envelope.items)?;
    let mut documents = Vec::new();
    let mut events = Vec::new();
    let mut profiles = Vec::new();
    let mut standalone_spans = Vec::new();
    let mut transactions = Vec::new();

    let mut envelope_document = Document::base("envelope", &service.id, &received_at, &received_at);
    envelope_document.ingest_id = Some(ingest_id.clone());
    envelope_document.event_id = envelope_event_id.clone();
    envelope_document.item_type = Some("envelope".into());
    envelope_document.payload = Some(Value::Object(envelope.headers.clone()));
    envelope_document.size_bytes = Some(
        envelope
            .items
            .iter()
            .map(|item| item.payload.len() as u64)
            .sum(),
    );
    documents.push(envelope_document);

    for (item_index, mut item) in envelope.items.into_iter().enumerate() {
        let replay_video = (item.item_type == "replay_video")
            .then(|| crate::replay_video::decode(&item.payload))
            .transpose()?;
        let is_required_json = matches!(
            item.item_type.as_str(),
            "event" | "transaction" | "profile" | "profile_chunk"
        ) || is_span_v2_container(&item);
        let mut parsed = if is_required_json || item.payload.len() <= MAX_INDEXED_JSON_BYTES {
            match serde_json::from_slice::<Value>(&item.payload) {
                Ok(value) => Some(value),
                Err(error) if is_required_json => {
                    return Err(Error::InvalidRequest(format!(
                        "invalid {} JSON: {error}",
                        item.item_type
                    )));
                }
                Err(_) => None,
            }
        } else {
            None
        };
        if matches!(item.item_type.as_str(), "event" | "transaction" | "span")
            && let Some(payload) = parsed.as_mut()
        {
            scrub_sensitive_data(payload);
            item.payload = serde_json::to_vec(payload).map_err(|error| {
                Error::Storage(format!(
                    "encode scrubbed {} payload: {error}",
                    item.item_type
                ))
            })?;
        }
        if let Some(replay_video) = replay_video.as_ref() {
            parsed = Some(replay_video.event.clone());
        }
        let replay_header = (item.item_type == "replay_recording")
            .then(|| replay_recording_header(&item.payload))
            .flatten();
        let replay_metadata = matches!(
            item.item_type.as_str(),
            "replay_event" | "replay_recording" | "replay_video"
        )
        .then(|| {
            let payload = parsed
                .as_ref()
                .or(replay_header.as_ref())
                .and_then(Value::as_object);
            let replay_id = payload
                .and_then(|payload| {
                    string(payload, "replay_id").or_else(|| string(payload, "event_id"))
                })
                .and_then(normalize_event_id)
                .or_else(|| {
                    replay_video
                        .as_ref()
                        .and_then(|replay_video| replay_video.replay_id.clone())
                })
                .or_else(|| envelope_event_id.clone());
            let segment_id = payload
                .and_then(|payload| payload.get("segment_id"))
                .and_then(replay_segment_id);
            let segment_id = segment_id.or_else(|| {
                replay_video
                    .as_ref()
                    .and_then(|replay_video| replay_video.segment_id)
            });
            (replay_id, segment_id)
        });
        if item.item_type == "transaction"
            && let Some(transaction) = parsed.as_ref()
        {
            transactions.push(transaction.clone());
        }
        if item.item_type == "span" {
            standalone_spans.extend(standalone_span_items(&item, parsed.as_ref())?);
        }
        if matches!(item.item_type.as_str(), "profile" | "profile_chunk")
            && let Some(profile) = parsed.as_ref()
        {
            let received_at_unix_nano =
                u64::try_from(Timestamp::now().as_nanosecond()).unwrap_or_default();
            profiles.extend(crate::profile::sentry_profile_rows(
                &service.id,
                profile,
                received_at_unix_nano,
            ));
        }
        let event = if item.item_type == "event" {
            Some(
                normalize_event_with_geoip(
                    parsed.take(),
                    envelope_event_id.as_deref(),
                    envelope_replay_id.as_deref(),
                    &received_at,
                    client_ip,
                    request_geo.clone(),
                    geoip,
                )
                .ok_or_else(|| Error::InvalidRequest("event payload must be an object".into()))?,
            )
        } else {
            None
        };
        let timestamp = event
            .as_ref()
            .map_or_else(|| received_at.clone(), |event| event.timestamp.clone());
        let content_id = format!("{:x}", Sha256::digest(&item.payload));
        let chunk_count = item.payload.len().div_ceil(CONTENT_CHUNK_BYTES).max(1) as u64;
        let indexed_item_type = bounded(&item.item_type, MAX_ATTRIBUTE_BYTES);
        let mut document = Document::base(
            indexed_item_type.clone(),
            &service.id,
            &timestamp,
            &received_at,
        );
        document.ingest_id = Some(ingest_id.clone());
        document.item_type = Some(indexed_item_type);
        document.sequence = Some(item_index as u64);
        document.content_id = Some(content_id.clone());
        document.chunk_count = Some(chunk_count);
        document.size_bytes = Some(item.payload.len() as u64);
        document.filename =
            string(&item.headers, "filename").map(|value| bounded(value, MAX_ATTRIBUTE_BYTES));
        document.content_type =
            string(&item.headers, "content_type").map(|value| bounded(value, MAX_ATTRIBUTE_BYTES));
        if item.payload.len() <= MAX_INDEXED_JSON_BYTES || replay_video.is_some() {
            document.payload = event
                .as_ref()
                .map(|event| Value::Object(event.payload.clone()))
                .or(parsed)
                .or(replay_header);
        }
        if let Some(event) = event {
            document.event_id = Some(event.event_id.clone());
            document.issue_id = Some(event.issue_id.clone());
            document.title = Some(event.title.clone());
            document.message = event.message.clone();
            document.level = Some(event.level.clone());
            document.platform = Some(event.platform.clone());
            document.environment = event.environment.clone();
            document.release = event.release.clone();
            document.dist = event.dist.clone();
            document.transaction = event.transaction.clone();
            document.sdk_name = event.sdk_name.clone();
            document.sdk_version = event.sdk_version.clone();
            document.user = event.user.clone();
            document.replay_id = event.replay_id.clone();
            events.push(IngestedEvent {
                event_id: event.event_id,
                issue_id: event.issue_id,
                title: event.title,
                level: event.level,
                platform: event.platform,
                timestamp: event.timestamp,
                payload: Value::Object(event.payload),
            });
        } else {
            document.event_id = envelope_event_id.clone();
            if let Some((replay_id, segment_id)) = replay_metadata {
                document.replay_id = replay_id.clone();
                document.event_id = replay_id;
                document.segment_id = segment_id;
            }
        }
        documents.push(document);
        append_content_chunks(
            &mut documents,
            service,
            &ingest_id,
            &content_id,
            ContentInput {
                item_index,
                timestamp: &timestamp,
                received_at: &received_at,
                payload: &item.payload,
            },
        );
    }
    let response_event_id = events
        .first()
        .map(|event| event.event_id.clone())
        .or(envelope_event_id)
        .unwrap_or(ingest_id);
    Ok(PreparedIngest {
        documents,
        events,
        profiles,
        standalone_spans,
        transactions,
        response_event_id,
    })
}

fn scrub_sensitive_data(value: &mut Value) {
    match value {
        Value::Object(object) => {
            for (key, value) in object {
                let normalized = key
                    .chars()
                    .filter(|character| character.is_ascii_alphanumeric())
                    .flat_map(char::to_lowercase)
                    .collect::<String>();
                if normalized == "cookies" {
                    scrub_container_values(value);
                } else if sensitive_key(&normalized) {
                    *value = Value::String("[Filtered]".into());
                } else if normalized == "headers" {
                    scrub_headers(value);
                    scrub_sensitive_data(value);
                } else {
                    scrub_sensitive_data(value);
                }
            }
        }
        Value::Array(values) => values.iter_mut().for_each(scrub_sensitive_data),
        _ => {}
    }
}

fn scrub_container_values(value: &mut Value) {
    match value {
        Value::Object(values) => {
            for value in values.values_mut() {
                *value = Value::String("[Filtered]".into());
            }
        }
        Value::Array(values) => {
            for value in values {
                if let Some(pair) = value.as_array_mut()
                    && let Some(value) = pair.get_mut(1)
                {
                    *value = Value::String("[Filtered]".into());
                } else {
                    *value = Value::String("[Filtered]".into());
                }
            }
        }
        Value::Null => {}
        _ => *value = Value::String("[Filtered]".into()),
    }
}

fn scrub_headers(value: &mut Value) {
    let Value::Array(headers) = value else {
        return;
    };
    for header in headers {
        let Some(pair) = header.as_array_mut() else {
            continue;
        };
        let sensitive = pair
            .first()
            .and_then(Value::as_str)
            .map(normalized_sensitive_key)
            .is_some_and(|key| sensitive_key(&key));
        if sensitive && let Some(value) = pair.get_mut(1) {
            *value = Value::String("[Filtered]".into());
        }
    }
}

fn normalized_sensitive_key(value: &str) -> String {
    value
        .chars()
        .filter(|character| character.is_ascii_alphanumeric())
        .flat_map(char::to_lowercase)
        .collect()
}

fn sensitive_key(value: &str) -> bool {
    matches!(
        value,
        "authorization"
            | "proxyauthorization"
            | "cookie"
            | "setcookie"
            | "password"
            | "passwd"
            | "secret"
            | "apikey"
            | "accesstoken"
            | "refreshtoken"
            | "sessionid"
            | "csrftoken"
            | "xcsrftoken"
            | "xsrftoken"
    )
}

fn standalone_span_items(
    item: &crate::envelope::Item,
    parsed: Option<&Value>,
) -> Result<Vec<StandaloneSpan>> {
    if is_span_v2_container(item) {
        let items = parsed
            .and_then(Value::as_object)
            .and_then(|container| container.get("items"))
            .and_then(Value::as_array)
            .ok_or_else(|| Error::InvalidRequest("invalid Sentry span container".into()))?;
        let expected = item.headers.get("item_count").and_then(Value::as_u64);
        if expected != Some(items.len() as u64) {
            return Err(Error::InvalidRequest(format!(
                "Sentry span container item_count {expected:?} does not match {} items",
                items.len()
            )));
        }
        return Ok(items
            .iter()
            .filter(|span| span.is_object())
            .cloned()
            .map(|payload| StandaloneSpan {
                payload,
                version: StandaloneSpanVersion::V2,
            })
            .collect());
    }
    Ok(parsed
        .filter(|span| span.is_object())
        .cloned()
        .map(|payload| {
            vec![StandaloneSpan {
                payload,
                version: StandaloneSpanVersion::Legacy,
            }]
        })
        .unwrap_or_default())
}

fn is_span_v2_container(item: &crate::envelope::Item) -> bool {
    item.item_type == "span"
        && string(&item.headers, "content_type")
            .is_some_and(|value| value.eq_ignore_ascii_case(SPAN_V2_CONTENT_TYPE))
}

fn validate_span_items(items: &[crate::envelope::Item]) -> Result<()> {
    let container_count = items
        .iter()
        .filter(|item| is_span_v2_container(item))
        .count();
    let legacy_count = items
        .iter()
        .filter(|item| item.item_type == "span" && !is_span_v2_container(item))
        .count();
    if container_count > 1 || (container_count == 1 && legacy_count > 0) {
        return Err(Error::InvalidRequest(
            "duplicate or mixed Sentry span items in one envelope".into(),
        ));
    }
    Ok(())
}

fn replay_recording_header(payload: &[u8]) -> Option<Value> {
    let end = payload.iter().position(|byte| *byte == b'\n')?;
    if end > MAX_REPLAY_HEADER_BYTES {
        return None;
    }
    serde_json::from_slice(&payload[..end])
        .ok()
        .filter(Value::is_object)
}

fn replay_segment_id(value: &Value) -> Option<u64> {
    match value {
        Value::Number(value) => value.as_u64(),
        Value::String(value) => value.parse().ok(),
        _ => None,
    }
}

struct NormalizedEvent {
    event_id: String,
    issue_id: String,
    title: String,
    message: Option<String>,
    level: String,
    platform: String,
    environment: Option<String>,
    release: Option<String>,
    dist: Option<String>,
    transaction: Option<String>,
    sdk_name: Option<String>,
    sdk_version: Option<String>,
    user: Option<String>,
    replay_id: Option<String>,
    timestamp: String,
    payload: Map<String, Value>,
}

fn normalize_event_with_geoip(
    payload: Option<Value>,
    envelope_event_id: Option<&str>,
    envelope_replay_id: Option<&str>,
    received_at: &str,
    client_ip: Option<IpAddr>,
    request_geo: Option<Geo>,
    geoip: &GeoIpLookup,
) -> Option<NormalizedEvent> {
    let mut payload = match payload? {
        Value::Object(payload) => payload,
        _ => return None,
    };
    resolve_auto_user_ip(&mut payload, client_ip);
    enrich_user_geo(&mut payload, client_ip, |address| {
        request_geo.or_else(|| geoip.lookup(address))
    });
    let event_id = envelope_event_id
        .map(str::to_owned)
        .or_else(|| string(&payload, "event_id").and_then(normalize_event_id))
        .unwrap_or_else(|| Uuid::new_v4().simple().to_string());
    let exception = payload
        .get("exception")
        .and_then(Value::as_object)
        .and_then(|value| value.get("values"))
        .and_then(Value::as_array)
        .and_then(|values| values.last())
        .and_then(Value::as_object);
    let exception_type = exception.and_then(|value| string(value, "type"));
    let exception_value = exception.and_then(|value| string(value, "value"));
    let message =
        event_message(&payload, exception_value).map(|value| bounded(&value, MAX_MESSAGE_BYTES));
    let title = bounded(
        &match (exception_type, exception_value) {
            (Some(kind), Some(value)) if !value.is_empty() => format!("{kind}: {value}"),
            (Some(kind), _) => kind.to_owned(),
            _ => message.clone().unwrap_or_else(|| "Unknown error".into()),
        },
        MAX_TITLE_BYTES,
    );
    let platform = bounded(
        string(&payload, "platform").unwrap_or("other"),
        MAX_ATTRIBUTE_BYTES,
    );
    let issue_id = issue_id(&payload, &title, &platform);
    let timestamp =
        event_timestamp(payload.get("timestamp")).unwrap_or_else(|| received_at.to_owned());
    let sdk = payload.get("sdk").and_then(Value::as_object);
    Some(NormalizedEvent {
        event_id,
        issue_id,
        title,
        message,
        level: bounded(
            string(&payload, "level").unwrap_or("error"),
            MAX_ATTRIBUTE_BYTES,
        ),
        platform,
        environment: bounded_string(&payload, "environment"),
        release: bounded_string(&payload, "release"),
        dist: bounded_string(&payload, "dist"),
        transaction: bounded_string(&payload, "transaction"),
        sdk_name: sdk
            .and_then(|value| string(value, "name"))
            .map(|value| bounded(value, MAX_ATTRIBUTE_BYTES)),
        sdk_version: sdk
            .and_then(|value| string(value, "version"))
            .map(|value| bounded(value, MAX_ATTRIBUTE_BYTES)),
        user: event_user(payload.get("user")).map(|value| bounded(&value, MAX_ATTRIBUTE_BYTES)),
        replay_id: event_replay_id(&payload).or_else(|| envelope_replay_id.map(str::to_owned)),
        timestamp,
        payload,
    })
}

#[cfg(test)]
fn normalize_event(
    payload: Option<Value>,
    envelope_event_id: Option<&str>,
    envelope_replay_id: Option<&str>,
    received_at: &str,
    client_ip: Option<IpAddr>,
) -> Option<NormalizedEvent> {
    normalize_event_with_geoip(
        payload,
        envelope_event_id,
        envelope_replay_id,
        received_at,
        client_ip,
        None,
        &GeoIpLookup::empty(),
    )
}

fn enrich_user_geo(
    payload: &mut Map<String, Value>,
    client_ip: Option<IpAddr>,
    lookup: impl FnOnce(IpAddr) -> Option<Geo>,
) {
    let existing_user = payload.get("user");
    if existing_user
        .and_then(Value::as_object)
        .and_then(|user| user.get("geo"))
        .is_some_and(|geo| !geo.is_null())
    {
        return;
    }
    if existing_user.is_some_and(|user| !user.is_object()) {
        return;
    }

    let user_ip = existing_user
        .and_then(Value::as_object)
        .and_then(|user| string(user, "ip_address"));
    let address = match user_ip {
        Some(address) => address.parse().ok(),
        None => client_ip,
    };
    let Some(geo) = address.and_then(lookup) else {
        return;
    };
    let user = payload
        .entry("user")
        .or_insert_with(|| Value::Object(Map::new()))
        .as_object_mut()
        .expect("user was checked to be an object");
    user.insert("geo".into(), geo.into_value());
}

fn resolve_auto_user_ip(payload: &mut Map<String, Value>, client_ip: Option<IpAddr>) {
    let Some(user) = payload.get_mut("user").and_then(Value::as_object_mut) else {
        return;
    };
    if string(user, "ip_address") != Some("{{auto}}") {
        return;
    }
    match client_ip {
        Some(address) => {
            user.insert("ip_address".into(), Value::String(address.to_string()));
        }
        None => {
            user.remove("ip_address");
        }
    }
}

fn event_replay_id(payload: &Map<String, Value>) -> Option<String> {
    let direct = string(payload, "replay_id");
    let context = payload
        .get("contexts")
        .and_then(Value::as_object)
        .and_then(|contexts| contexts.get("replay"))
        .and_then(Value::as_object)
        .and_then(|replay| string(replay, "replay_id"));
    direct
        .or_else(|| event_tag(payload.get("tags"), "replayId"))
        .or_else(|| event_tag(payload.get("tags"), "replay_id"))
        .or(context)
        .and_then(normalize_event_id)
}

fn event_tag<'a>(tags: Option<&'a Value>, key: &str) -> Option<&'a str> {
    match tags? {
        Value::Object(tags) => string(tags, key),
        Value::Array(tags) => tags.iter().find_map(|tag| match tag {
            Value::Array(tag) if tag.first().and_then(Value::as_str) == Some(key) => {
                tag.get(1).and_then(Value::as_str)
            }
            Value::Object(tag) if string(tag, "key") == Some(key) => string(tag, "value"),
            _ => None,
        }),
        _ => None,
    }
}

fn issue_id(payload: &Map<String, Value>, title: &str, platform: &str) -> String {
    let mut canonical = String::new();
    let fingerprint = payload
        .get("fingerprint")
        .and_then(Value::as_array)
        .filter(|values| values.iter().any(Value::is_string));
    if let Some(fingerprint) = fingerprint {
        for value in fingerprint
            .iter()
            .filter_map(Value::as_str)
            .take(MAX_FINGERPRINT_COMPONENTS)
        {
            match fingerprint_variable(value) {
                Some("default") => {
                    append_default_fingerprint(&mut canonical, payload, title, platform);
                }
                Some(variable) => {
                    canonical.push_str("custom\0");
                    canonical.push_str(&bounded(
                        &resolve_fingerprint_variable(variable, payload),
                        MAX_ATTRIBUTE_BYTES,
                    ));
                    canonical.push('\0');
                }
                None => {
                    canonical.push_str("custom\0");
                    canonical.push_str(&bounded(
                        &normalize_fingerprint_literal(value, payload),
                        MAX_ATTRIBUTE_BYTES,
                    ));
                    canonical.push('\0');
                }
            }
        }
    } else {
        append_default_fingerprint(&mut canonical, payload, title, platform);
    }
    format!("{:x}", Sha256::digest(canonical.as_bytes()))
}

fn fingerprint_variable(value: &str) -> Option<&str> {
    let value = value.trim();
    let variable = value.strip_prefix("{{")?.strip_suffix("}}")?.trim();
    (!variable.is_empty() && !variable.chars().any(char::is_whitespace)).then_some(variable)
}

fn resolve_fingerprint_variable(variable: &str, payload: &Map<String, Value>) -> String {
    let exception = last_exception(payload);
    let frame = crash_frame(payload);
    let resolved = match variable {
        "transaction" => string(payload, "transaction")
            .unwrap_or("<no-transaction>")
            .to_owned(),
        "message" | "raw_message" => fingerprint_message(payload)
            .unwrap_or("<no-message>")
            .to_owned(),
        "type" | "error.type" => exception
            .and_then(|exception| string(exception, "type"))
            .unwrap_or("<no-type>")
            .to_owned(),
        "value" | "raw_value" | "error.value" | "error.raw_value" => exception
            .and_then(|exception| string(exception, "value"))
            .unwrap_or("<no-value>")
            .to_owned(),
        "function" | "stack.function" => frame
            .and_then(|frame| string(frame, "function"))
            .unwrap_or("<no-function>")
            .to_owned(),
        "path" | "stack.abs_path" => frame
            .and_then(|frame| string(frame, "abs_path").or_else(|| string(frame, "filename")))
            .unwrap_or("<no-abs-path>")
            .to_owned(),
        "stack.filename" => frame
            .and_then(|frame| string(frame, "filename").or_else(|| string(frame, "abs_path")))
            .unwrap_or("<no-filename>")
            .to_owned(),
        "module" | "stack.module" => frame
            .and_then(|frame| string(frame, "module"))
            .unwrap_or("<no-module>")
            .to_owned(),
        "package" | "stack.package" => frame
            .and_then(|frame| string(frame, "package"))
            .map(path_basename)
            .unwrap_or("<no-package>")
            .to_owned(),
        "level" => string(payload, "level").unwrap_or("<no-level>").to_owned(),
        "logger" => string(payload, "logger")
            .unwrap_or("<no-logger>")
            .to_owned(),
        variable if variable.starts_with("tags.") => {
            return event_tag(payload.get("tags"), &variable[5..])
                .map(str::to_owned)
                .unwrap_or_else(|| format!("<no-value-for-tag-{}>", &variable[5..]));
        }
        _ => return format!("<unrecognized-variable-{variable}>"),
    };
    if matches!(variable, "message" | "value" | "error.value")
        && !matches!(resolved.as_str(), "<no-message>" | "<no-value>")
    {
        normalize_grouping_message(&resolved)
    } else {
        resolved
    }
}

fn normalize_fingerprint_literal(value: &str, payload: &Map<String, Value>) -> String {
    let matches_message = fingerprint_messages(payload).any(|message| message == value);
    if matches_message {
        normalize_grouping_message(value)
    } else {
        value.to_owned()
    }
}

fn fingerprint_message(payload: &Map<String, Value>) -> Option<&str> {
    payload
        .get("logentry")
        .and_then(Value::as_object)
        .and_then(|entry| string(entry, "formatted").or_else(|| string(entry, "message")))
        .or_else(|| string(payload, "message"))
        .or_else(|| last_exception(payload).and_then(|exception| string(exception, "value")))
}

fn fingerprint_messages(payload: &Map<String, Value>) -> impl Iterator<Item = &str> {
    let canonical = fingerprint_message(payload).into_iter();
    let exceptions = payload
        .get("exception")
        .and_then(Value::as_object)
        .and_then(|exception| exception.get("values"))
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(Value::as_object)
        .filter_map(|exception| string(exception, "value"));
    canonical.chain(exceptions)
}

fn last_exception(payload: &Map<String, Value>) -> Option<&Map<String, Value>> {
    payload
        .get("exception")?
        .as_object()?
        .get("values")?
        .as_array()?
        .last()?
        .as_object()
}

fn crash_frame(payload: &Map<String, Value>) -> Option<&Map<String, Value>> {
    let frames = last_exception(payload)
        .and_then(|exception| exception.get("stacktrace"))
        .and_then(Value::as_object)
        .and_then(|stacktrace| stacktrace.get("frames"))
        .and_then(Value::as_array)
        .or_else(|| {
            payload
                .get("stacktrace")
                .and_then(Value::as_object)
                .and_then(|stacktrace| stacktrace.get("frames"))
                .and_then(Value::as_array)
        })
        .or_else(|| {
            let threads = payload
                .get("threads")?
                .as_object()?
                .get("values")?
                .as_array()?;
            (threads.len() == 1)
                .then(|| threads[0].as_object())
                .flatten()?
                .get("stacktrace")?
                .as_object()?
                .get("frames")?
                .as_array()
        })?;
    let mut fallback = None;
    for frame in frames.iter().rev().filter_map(Value::as_object) {
        if frame.get("in_app").and_then(Value::as_bool) == Some(true) {
            return Some(frame);
        }
        fallback.get_or_insert(frame);
    }
    fallback
}

fn path_basename(value: &str) -> &str {
    value.rsplit(['/', '\\']).next().unwrap_or(value)
}

fn append_default_fingerprint(
    canonical: &mut String,
    payload: &Map<String, Value>,
    title: &str,
    platform: &str,
) {
    canonical.push_str("default\0");
    canonical.push_str(platform);
    let exceptions = payload
        .get("exception")
        .and_then(Value::as_object)
        .and_then(|exception| exception.get("values"))
        .and_then(Value::as_array);
    if let Some(exceptions) = exceptions {
        for exception in exceptions.iter().filter_map(Value::as_object) {
            canonical.push_str("\0exception\0");
            let synthetic = exception
                .get("mechanism")
                .and_then(Value::as_object)
                .and_then(|mechanism| mechanism.get("synthetic"))
                .and_then(Value::as_bool)
                == Some(true);
            if !synthetic {
                canonical.push_str(string(exception, "type").unwrap_or("Error"));
            }
            let stacktrace = exception.get("stacktrace").and_then(Value::as_object);
            if !append_grouping_stack(canonical, stacktrace) {
                canonical.push_str("\0value\0");
                canonical.push_str(&normalize_grouping_message(
                    string(exception, "value").unwrap_or(title),
                ));
            }
        }
    }
    if exceptions.is_none_or(|values| values.is_empty()) {
        if append_grouping_stack(
            canonical,
            payload.get("stacktrace").and_then(Value::as_object),
        ) {
            return;
        }
        if let Some(stacktrace) = grouping_thread_stacktrace(payload)
            && append_grouping_stack(canonical, Some(stacktrace))
        {
            return;
        }
        canonical.push_str("\0message\0");
        canonical.push_str(&normalize_grouping_message(title));
    }
}

fn append_grouping_stack(canonical: &mut String, stacktrace: Option<&Map<String, Value>>) -> bool {
    let Some(frames) = stacktrace
        .and_then(|stacktrace| stacktrace.get("frames"))
        .and_then(Value::as_array)
    else {
        return false;
    };
    let mut meaningful = Vec::new();
    let mut previous = None;
    for frame in frames.iter().filter_map(Value::as_object) {
        let recursive = previous.is_some_and(|prior| same_grouping_frame(frame, prior));
        previous = Some(frame);
        if !recursive && grouping_frame(frame).is_some() {
            meaningful.push(frame);
        }
    }
    let in_app = meaningful
        .iter()
        .copied()
        .filter(|frame| frame.get("in_app").and_then(Value::as_bool) != Some(false))
        .collect::<Vec<_>>();
    let selected = if in_app.is_empty() {
        &meaningful
    } else {
        &in_app
    };
    let mut contributed = false;
    for frame in selected.iter().rev().take(MAX_GROUPING_FRAMES).rev() {
        let Some(frame) = grouping_frame(frame) else {
            continue;
        };
        contributed = true;
        canonical.push_str("\0frame\0");
        canonical.push_str(&frame);
    }
    contributed
}

fn same_grouping_frame(left: &Map<String, Value>, right: &Map<String, Value>) -> bool {
    [
        "abs_path", "package", "module", "filename", "function", "lineno", "colno",
    ]
    .iter()
    .all(|key| left.get(*key) == right.get(*key))
}

fn grouping_thread_stacktrace(payload: &Map<String, Value>) -> Option<&Map<String, Value>> {
    let threads = payload
        .get("threads")?
        .as_object()?
        .get("values")?
        .as_array()?;
    let threads = threads
        .iter()
        .filter_map(Value::as_object)
        .collect::<Vec<_>>();
    let select_one = |key: &str| {
        let selected = threads
            .iter()
            .copied()
            .filter(|thread| thread.get(key).and_then(Value::as_bool) == Some(true))
            .collect::<Vec<_>>();
        (selected.len() == 1).then_some(selected[0])
    };
    let thread = select_one("crashed")
        .or_else(|| select_one("current"))
        .or_else(|| (threads.len() == 1).then_some(threads[0]))?;
    thread.get("stacktrace")?.as_object()
}

fn grouping_frame(frame: &Map<String, Value>) -> Option<String> {
    let module = string(frame, "module").unwrap_or_default();
    let function = string(frame, "function").unwrap_or_default();
    let path = string(frame, "filename")
        .or_else(|| string(frame, "abs_path"))
        .unwrap_or_default();
    let filename = path
        .split(['?', '#'])
        .next()
        .unwrap_or(path)
        .rsplit(['/', '\\'])
        .next()
        .unwrap_or_default();
    if module.is_empty() && function.is_empty() && filename.is_empty() {
        return None;
    }
    Some(format!(
        "{}\0{}\0{}",
        normalize_grouping_message(module),
        normalize_grouping_message(function),
        normalize_grouping_message(filename)
    ))
}

fn normalize_grouping_message(value: &str) -> String {
    value
        .split_whitespace()
        .map(|token| {
            let trimmed = token.trim_matches(|character: char| {
                !character.is_ascii_alphanumeric() && character != '-' && character != '_'
            });
            let compact = trimmed.replace('-', "");
            let dynamic = (compact.len() >= 16
                && compact.bytes().all(|byte| byte.is_ascii_hexdigit()))
                || (trimmed.len() >= 3 && trimmed.bytes().all(|byte| byte.is_ascii_digit()));
            if dynamic { "#" } else { token }
        })
        .collect::<Vec<_>>()
        .join(" ")
}

struct ContentInput<'a> {
    item_index: usize,
    timestamp: &'a str,
    received_at: &'a str,
    payload: &'a [u8],
}

fn append_content_chunks(
    documents: &mut Vec<Document>,
    service: &ServiceContext,
    ingest_id: &str,
    content_id: &str,
    input: ContentInput<'_>,
) {
    let chunks: Vec<&[u8]> = if input.payload.is_empty() {
        vec![&[]]
    } else {
        input.payload.chunks(CONTENT_CHUNK_BYTES).collect()
    };
    let chunk_count = chunks.len() as u64;
    for (chunk_index, chunk) in chunks.into_iter().enumerate() {
        let mut document = Document::base(
            "content_chunk",
            &service.id,
            input.timestamp,
            input.received_at,
        );
        document.ingest_id = Some(ingest_id.to_owned());
        document.content_id = Some(content_id.to_owned());
        document.sequence = Some(chunk_index as u64);
        document.chunk_count = Some(chunk_count);
        document.size_bytes = Some(chunk.len() as u64);
        document.item_type = Some(format!("item:{}", input.item_index));
        document.content = Some(chunk.to_vec());
        documents.push(document);
    }
}

fn event_message(payload: &Map<String, Value>, exception_value: Option<&str>) -> Option<String> {
    string(payload, "message")
        .or_else(|| {
            payload
                .get("logentry")
                .and_then(Value::as_object)
                .and_then(|entry| string(entry, "formatted").or_else(|| string(entry, "message")))
        })
        .or(exception_value)
        .map(str::to_owned)
}

fn event_user(value: Option<&Value>) -> Option<String> {
    let user = value?.as_object()?;
    ["id", "email", "username", "ip_address"]
        .into_iter()
        .find_map(|field| string(user, field).map(str::to_owned))
}

fn event_timestamp(value: Option<&Value>) -> Option<String> {
    match value? {
        Value::String(value) => value
            .parse::<Timestamp>()
            .ok()
            .map(|value| value.to_string()),
        Value::Number(value) => {
            let seconds = value.as_f64()?;
            if !seconds.is_finite() || seconds < 0.0 {
                return None;
            }
            let whole = seconds.trunc();
            if whole > i64::MAX as f64 {
                return None;
            }
            let mut whole = whole as i64;
            let mut nanos = (seconds.fract() * 1_000_000_000.0).round() as i64;
            if nanos == 1_000_000_000 {
                whole = whole.checked_add(1)?;
                nanos = 0;
            }
            Timestamp::new(whole, i32::try_from(nanos).ok()?)
                .ok()
                .map(|value| value.to_string())
        }
        _ => None,
    }
}

fn normalize_event_id(value: &str) -> Option<String> {
    let normalized = value.replace('-', "").to_ascii_lowercase();
    (normalized.len() == 32 && normalized.bytes().all(|byte| byte.is_ascii_hexdigit()))
        .then_some(normalized)
}

fn string<'a>(object: &'a Map<String, Value>, key: &str) -> Option<&'a str> {
    object.get(key).and_then(Value::as_str)
}

fn bounded_string(object: &Map<String, Value>, key: &str) -> Option<String> {
    string(object, key).map(|value| bounded(value, MAX_ATTRIBUTE_BYTES))
}

fn bounded(value: &str, maximum_bytes: usize) -> String {
    if value.len() <= maximum_bytes {
        return value.to_owned();
    }
    let mut end = maximum_bytes;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_owned()
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;
    use crate::envelope::{Envelope, Item};

    fn fixture() -> ServiceContext {
        ServiceContext { id: "app".into() }
    }

    #[test]
    fn normalizes_event_and_preserves_raw_payload() {
        let app = fixture();
        let payload = json!({
            "event_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
            "platform":"javascript",
            "exception":{"values":[{"type":"TypeError","value":"broken","stacktrace":{"frames":[{"filename":"app.js","function":"run","lineno":10,"in_app":true}]}}]},
            "sdk":{"name":"sentry.javascript.browser","version":"10.0.0"}
        });
        let bytes = serde_json::to_vec(&payload).unwrap();
        let prepared = prepare(
            &app,
            Envelope {
                headers: Map::new(),
                items: vec![Item {
                    headers: Map::new(),
                    item_type: "event".into(),
                    payload: bytes.clone(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .unwrap();
        assert_eq!(prepared.events[0].title, "TypeError: broken");
        assert_eq!(prepared.events[0].platform, "javascript");
        let event = prepared
            .documents
            .iter()
            .find(|document| document.doc_kind == "event")
            .unwrap();
        assert_eq!(
            event.event_id.as_deref(),
            Some("aaaaaaaaaaaa4aaa8aaaaaaaaaaaaaaa")
        );
        let chunks = prepared
            .documents
            .iter()
            .filter(|document| document.doc_kind == "content_chunk")
            .collect::<Vec<_>>();
        assert_eq!(chunks[0].content.as_deref(), Some(bytes.as_slice()));
    }

    #[test]
    fn scrubs_credentials_before_indexing_or_storing_event_content() {
        let prepared = prepare(
            &fixture(),
            Envelope {
                headers: Map::new(),
                items: vec![Item {
                    headers: Map::new(),
                    item_type: "event".into(),
                    payload: serde_json::to_vec(&json!({
                        "event_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                        "message": "failed request",
                        "request": {
                            "cookies": {"session": "cookie-secret"},
                            "headers": [
                                ["Authorization", "Bearer header-secret"],
                                ["Content-Type", "application/json"]
                            ]
                        },
                        "contexts": {"auth": {
                            "access_token": "context-secret",
                            "account": "visible"
                        }}
                    }))
                    .unwrap(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .unwrap();

        let event = &prepared.events[0].payload;
        assert_eq!(event["request"]["cookies"]["session"], "[Filtered]");
        assert_eq!(event["request"]["headers"][0][1], "[Filtered]");
        assert_eq!(event["request"]["headers"][1][1], "application/json");
        assert_eq!(event["contexts"]["auth"]["access_token"], "[Filtered]");
        assert_eq!(event["contexts"]["auth"]["account"], "visible");
        let content = prepared
            .documents
            .iter()
            .filter(|document| document.doc_kind == "content_chunk")
            .flat_map(|document| document.content.iter().flatten().copied())
            .collect::<Vec<_>>();
        let content = String::from_utf8(content).unwrap();
        assert!(!content.contains("cookie-secret"));
        assert!(!content.contains("header-secret"));
        assert!(!content.contains("context-secret"));
    }

    #[test]
    fn links_events_to_replays_from_sdk_tags_or_envelope_trace() {
        let tagged = normalize_event(
            Some(json!({
                "message": "failure",
                "tags": { "replayId": "cccccccc-cccc-4ccc-8ccc-cccccccccccc" }
            })),
            None,
            Some("dddddddddddd4ddd8ddddddddddddddd"),
            "2026-01-01T00:00:00Z",
            None,
        )
        .unwrap();
        assert_eq!(
            tagged.replay_id.as_deref(),
            Some("cccccccccccc4ccc8ccccccccccccccc")
        );

        let traced = normalize_event(
            Some(json!({ "message": "failure" })),
            None,
            Some("dddddddddddd4ddd8ddddddddddddddd"),
            "2026-01-01T00:00:00Z",
            None,
        )
        .unwrap();
        assert_eq!(
            traced.replay_id.as_deref(),
            Some("dddddddddddd4ddd8ddddddddddddddd")
        );
    }

    #[test]
    fn resolves_automatic_user_ip_without_overwriting_explicit_values() {
        let resolved = normalize_event(
            Some(json!({ "message": "failure", "user": { "ip_address": "{{auto}}" } })),
            None,
            None,
            "2026-01-01T00:00:00Z",
            Some("203.0.113.42".parse().unwrap()),
        )
        .unwrap();
        assert_eq!(resolved.payload["user"]["ip_address"], "203.0.113.42");

        let explicit = normalize_event(
            Some(json!({ "message": "failure", "user": { "ip_address": "198.51.100.7" } })),
            None,
            None,
            "2026-01-01T00:00:00Z",
            Some("203.0.113.42".parse().unwrap()),
        )
        .unwrap();
        assert_eq!(explicit.payload["user"]["ip_address"], "198.51.100.7");

        let unavailable = normalize_event(
            Some(json!({ "message": "failure", "user": { "ip_address": "{{auto}}" } })),
            None,
            None,
            "2026-01-01T00:00:00Z",
            None,
        )
        .unwrap();
        assert!(unavailable.payload["user"].get("ip_address").is_none());
    }

    #[test]
    fn enriches_geo_from_the_resolved_ip_and_preserves_sdk_geo() {
        let address = "2.125.160.216".parse().unwrap();
        let mut payload = json!({ "message": "failure" }).as_object().unwrap().clone();
        enrich_user_geo(&mut payload, Some(address), |actual| {
            assert_eq!(actual, address);
            Some(Geo::fixture())
        });
        assert_eq!(
            payload["user"]["geo"],
            json!({
                "country_code": "GB",
                "city": "Boxford",
                "subdivision": "England",
                "region": "United Kingdom"
            })
        );

        let mut supplied = json!({
            "user": {
                "ip_address": "2.125.160.216",
                "geo": { "country_code": "RS", "city": "Belgrade" }
            }
        })
        .as_object()
        .unwrap()
        .clone();
        enrich_user_geo(&mut supplied, None, |_| panic!("supplied geo must win"));
        assert_eq!(
            supplied["user"]["geo"],
            json!({ "country_code": "RS", "city": "Belgrade" })
        );
    }

    #[test]
    fn explicit_fingerprint_overrides_frame_grouping() {
        let payload_a = json!({"fingerprint":["billing"],"message":"one"});
        let payload_b = json!({"fingerprint":["billing"],"message":"two"});
        let a = normalize_event(Some(payload_a), None, None, "2026-01-01T00:00:00Z", None).unwrap();
        let b = normalize_event(Some(payload_b), None, None, "2026-01-01T00:00:00Z", None).unwrap();
        assert_eq!(a.issue_id, b.issue_id);
    }

    #[test]
    fn groups_dynamic_exception_values_with_the_same_stack() {
        let event = |value: &str, line: u64| {
            normalize_event(
                Some(json!({
                    "platform": "javascript",
                    "exception": {"values": [{
                        "type": "CheckoutError",
                        "value": value,
                        "stacktrace": {"frames": [{
                            "filename": "webpack:///src/checkout.ts",
                            "function": "finalizeOrder",
                            "in_app": true,
                            "lineno": line
                        }]}
                    }]}
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_eq!(
            event("order 019f9a10f2187ddfb150cfede57eff49 failed", 71).issue_id,
            event("order 019f9a10f2187ddfb150cfede57eff50 failed", 96).issue_id
        );
    }

    #[test]
    fn distinguishes_different_in_app_call_sites() {
        let event = |function: &str| {
            normalize_event(
                Some(json!({
                    "exception": {"values": [{
                        "type": "Error",
                        "value": "failed",
                        "stacktrace": {"frames": [{
                            "filename": "/src/orders.ts",
                            "function": function,
                            "in_app": true
                        }]}
                    }]}
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_ne!(
            event("reserveOrder").issue_id,
            event("cancelOrder").issue_id
        );
    }

    #[test]
    fn normalizes_dynamic_values_when_no_stack_is_available() {
        let event = |value: &str| {
            normalize_event(
                Some(json!({
                    "exception": {"values": [{"type": "Error", "value": value}]}
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_eq!(
            event("request 12345 failed").issue_id,
            event("request 67890 failed").issue_id
        );
    }

    #[test]
    fn chained_exception_without_stack_still_contributes_its_value() {
        let event = |cause: &str| {
            normalize_event(
                Some(json!({
                    "exception": {"values": [
                        {
                            "type": "DatabaseError",
                            "value": "query failed",
                            "stacktrace": {"frames": [{
                                "filename": "/src/db.rs",
                                "function": "execute",
                                "in_app": true
                            }]}
                        },
                        {"type": "RequestError", "value": cause}
                    ]}
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_ne!(
            event("checkout failed").issue_id,
            event("login failed").issue_id
        );
    }

    #[test]
    fn synthetic_exception_type_does_not_split_the_same_stack() {
        let event = |exception_type: &str| {
            normalize_event(
                Some(json!({
                    "exception": {"values": [{
                        "type": exception_type,
                        "mechanism": {"synthetic": true},
                        "stacktrace": {"frames": [{
                            "filename": "/src/tasks.ts",
                            "function": "runTask",
                            "in_app": true
                        }]}
                    }]}
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_eq!(
            event("Error").issue_id,
            event("SyntheticException").issue_id
        );
    }

    #[test]
    fn groups_by_event_and_thread_stacktraces_when_exception_is_absent() {
        let direct = normalize_event(
            Some(json!({
                "message": "dynamic 12345",
                "stacktrace": {"frames": [{
                    "filename": "/src/worker.go",
                    "function": "poll",
                    "in_app": true
                }]}
            })),
            None,
            None,
            "2026-01-01T00:00:00Z",
            None,
        )
        .unwrap();
        let thread = normalize_event(
            Some(json!({
                "message": "dynamic 67890",
                "threads": {"values": [{
                    "crashed": true,
                    "stacktrace": {"frames": [{
                        "filename": "/src/worker.go",
                        "function": "poll",
                        "in_app": true
                    }]}
                }]}
            })),
            None,
            None,
            "2026-01-01T00:00:00Z",
            None,
        )
        .unwrap();

        assert_eq!(direct.issue_id, thread.issue_id);
    }

    #[test]
    fn default_fingerprint_keeps_custom_components() {
        let payload_a = json!({
            "fingerprint": ["{{ default }}", "tenant-a"],
            "message": "same failure"
        });
        let payload_b = json!({
            "fingerprint": ["{{ default }}", "tenant-b"],
            "message": "same failure"
        });
        let a = normalize_event(Some(payload_a), None, None, "2026-01-01T00:00:00Z", None).unwrap();
        let b = normalize_event(Some(payload_b), None, None, "2026-01-01T00:00:00Z", None).unwrap();
        assert_ne!(a.issue_id, b.issue_id);
    }

    #[test]
    fn resolves_transaction_and_tag_fingerprint_variables() {
        let event = |transaction: &str, tenant: &str| {
            normalize_event(
                Some(json!({
                    "fingerprint": ["{{ transaction }}", "{{ tags.tenant }}"],
                    "message": "shared failure",
                    "transaction": transaction,
                    "tags": [["tenant", tenant]]
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_ne!(
            event("checkout", "tenant-a").issue_id,
            event("checkout", "tenant-b").issue_id
        );
        assert_ne!(
            event("checkout", "tenant-a").issue_id,
            event("login", "tenant-a").issue_id
        );
    }

    #[test]
    fn stack_fingerprint_uses_highest_in_app_crash_frame() {
        let event = |function: &str, sdk_function: &str| {
            normalize_event(
                Some(json!({
                    "fingerprint": ["{{ stack.function }}"],
                    "exception": {"values": [{
                        "type": "Failure",
                        "stacktrace": {"frames": [
                            {"function": function, "in_app": true},
                            {"function": sdk_function, "in_app": false}
                        ]}
                    }]}
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_eq!(
            event("checkout", "sdkA").issue_id,
            event("checkout", "sdkB").issue_id
        );
        assert_ne!(
            event("checkout", "sdkA").issue_id,
            event("login", "sdkA").issue_id
        );
    }

    #[test]
    fn message_fingerprint_variable_parameterizes_dynamic_values() {
        let event = |variable: &str, message: &str| {
            normalize_event(
                Some(json!({
                    "fingerprint": [variable],
                    "message": message
                })),
                None,
                None,
                "2026-01-01T00:00:00Z",
                None,
            )
            .unwrap()
        };

        assert_eq!(
            event("{{ message }}", "request 12345 failed").issue_id,
            event("{{ message }}", "request 67890 failed").issue_id
        );
        assert_ne!(
            event("{{ raw_message }}", "request 12345 failed").issue_id,
            event("{{ raw_message }}", "request 67890 failed").issue_id
        );
    }

    #[test]
    fn caps_custom_fingerprint_cardinality() {
        let mut first = vec![Value::String("shared".into()); MAX_FINGERPRINT_COMPONENTS];
        let mut second = first.clone();
        first.push(Value::String("ignored-a".into()));
        second.push(Value::String("ignored-b".into()));
        let first = json!({ "fingerprint": first, "message": "failure" });
        let second = json!({ "fingerprint": second, "message": "failure" });

        let first = normalize_event(Some(first), None, None, "2026-01-01T00:00:00Z", None).unwrap();
        let second =
            normalize_event(Some(second), None, None, "2026-01-01T00:00:00Z", None).unwrap();

        assert_eq!(first.issue_id, second.issue_id);
    }

    #[test]
    fn rounds_numeric_timestamps_across_the_second_boundary() {
        let timestamp = serde_json::Number::from_f64(1.999_999_999_6).unwrap();

        assert_eq!(
            event_timestamp(Some(&Value::Number(timestamp))).as_deref(),
            Some("1970-01-01T00:00:02Z")
        );
    }

    #[test]
    fn extracts_sentry_v2_span_container_only_when_item_count_matches() {
        let payload = json!({
            "version": 2,
            "items": [{
                "trace_id": "0123456789abcdef0123456789abcdef",
                "span_id": "0123456789abcdef"
            }]
        });
        let item = Item {
            headers: Map::from_iter([
                (
                    "content_type".into(),
                    Value::String("application/vnd.sentry.items.span.v2+json".into()),
                ),
                ("item_count".into(), Value::from(1)),
            ]),
            item_type: "span".into(),
            payload: Vec::new(),
        };

        let spans = standalone_span_items(&item, Some(&payload)).unwrap();
        assert_eq!(spans.len(), 1);
        assert_eq!(spans[0].version, StandaloneSpanVersion::V2);

        let mut mismatched = item;
        mismatched
            .headers
            .insert("item_count".into(), Value::from(2));
        assert!(standalone_span_items(&mismatched, Some(&payload)).is_err());
    }

    #[test]
    fn bounds_indexed_metadata_without_changing_the_raw_payload() {
        let long = "я".repeat(MAX_ATTRIBUTE_BYTES);
        let payload = json!({
            "message": long,
            "platform": long,
            "level": long,
            "environment": long,
            "release": long,
            "dist": long,
            "transaction": long,
            "sdk": { "name": long, "version": long },
            "user": { "id": long },
            "fingerprint": [long]
        });

        let event = normalize_event(
            Some(payload.clone()),
            None,
            None,
            "2026-01-01T00:00:00Z",
            None,
        )
        .unwrap();

        for value in [
            &event.title,
            &event.level,
            &event.platform,
            event.environment.as_ref().unwrap(),
            event.release.as_ref().unwrap(),
            event.dist.as_ref().unwrap(),
            event.transaction.as_ref().unwrap(),
            event.sdk_name.as_ref().unwrap(),
            event.sdk_version.as_ref().unwrap(),
            event.user.as_ref().unwrap(),
        ] {
            assert!(value.len() <= MAX_ATTRIBUTE_BYTES);
            assert!(value.is_char_boundary(value.len()));
        }
        assert_eq!(event.payload["release"], payload["release"]);
    }

    #[test]
    fn bounds_non_event_metadata_and_preserves_large_payloads() {
        let app = fixture();
        let long = "x".repeat(MAX_ATTRIBUTE_BYTES + 1);
        let bytes =
            format!("{{\"value\":\"{}\"}}", "x".repeat(MAX_INDEXED_JSON_BYTES)).into_bytes();
        let prepared = prepare(
            &app,
            Envelope {
                headers: Map::new(),
                items: vec![Item {
                    headers: Map::from_iter([
                        ("filename".into(), Value::String(long.clone())),
                        ("content_type".into(), Value::String(long.clone())),
                    ]),
                    item_type: long,
                    payload: bytes.clone(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .unwrap();

        let item = &prepared.documents[1];
        assert_eq!(item.doc_kind.len(), MAX_ATTRIBUTE_BYTES);
        assert_eq!(item.item_type.as_ref().unwrap().len(), MAX_ATTRIBUTE_BYTES);
        assert_eq!(item.filename.as_ref().unwrap().len(), MAX_ATTRIBUTE_BYTES);
        assert_eq!(
            item.content_type.as_ref().unwrap().len(),
            MAX_ATTRIBUTE_BYTES
        );
        assert!(item.payload.is_none());
        let raw = prepared
            .documents
            .iter()
            .filter(|document| document.doc_kind == "content_chunk")
            .flat_map(|document| document.content.as_deref().unwrap_or_default())
            .copied()
            .collect::<Vec<_>>();
        assert_eq!(raw, bytes);
    }

    #[test]
    fn reads_replay_recording_header_and_preserves_all_bytes() {
        let app = fixture();
        let bytes = b"{\"segment_id\":\"7\"}\n[{\"type\":5}]".to_vec();
        let prepared = prepare(
            &app,
            Envelope {
                headers: Map::from_iter([(
                    "event_id".into(),
                    Value::String("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb".into()),
                )]),
                items: vec![Item {
                    headers: Map::new(),
                    item_type: "replay_recording".into(),
                    payload: bytes.clone(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .unwrap();
        let recording = prepared
            .documents
            .iter()
            .find(|document| document.doc_kind == "replay_recording")
            .unwrap();
        assert_eq!(
            prepared.response_event_id,
            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        );
        assert_eq!(
            recording
                .payload
                .as_ref()
                .and_then(|value| value["segment_id"].as_str()),
            Some("7")
        );
        assert_eq!(recording.segment_id, Some(7));
        assert_eq!(
            recording.replay_id.as_deref(),
            Some("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
        );
        let chunk = prepared
            .documents
            .iter()
            .find(|document| document.doc_kind == "content_chunk")
            .unwrap();
        assert_eq!(chunk.content.as_deref(), Some(bytes.as_slice()));
    }

    #[test]
    fn rejects_malformed_event_items() {
        let app = fixture();
        let error = prepare(
            &app,
            Envelope {
                headers: Map::new(),
                items: vec![Item {
                    headers: Map::new(),
                    item_type: "event".into(),
                    payload: b"not-json".to_vec(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .err()
        .expect("malformed events must be rejected");
        assert!(error.to_string().contains("invalid event JSON"));
    }

    #[test]
    fn rejects_malformed_v2_span_containers_but_drops_malformed_legacy_spans() {
        let app = fixture();
        let error = prepare(
            &app,
            Envelope {
                headers: Map::new(),
                items: vec![Item {
                    headers: Map::from_iter([
                        (
                            "content_type".into(),
                            Value::String(SPAN_V2_CONTENT_TYPE.into()),
                        ),
                        ("item_count".into(), Value::from(1)),
                    ]),
                    item_type: "span".into(),
                    payload: b"not-json".to_vec(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .err()
        .expect("malformed v2 span containers must be rejected");
        assert!(error.to_string().contains("invalid span JSON"));

        let prepared = prepare(
            &app,
            Envelope {
                headers: Map::new(),
                items: vec![Item {
                    headers: Map::new(),
                    item_type: "span".into(),
                    payload: b"not-json".to_vec(),
                }],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .expect("Relay discards malformed legacy spans individually");
        assert!(prepared.standalone_spans.is_empty());
    }

    #[test]
    fn rejects_mixed_legacy_and_v2_span_items() {
        let app = fixture();
        let error = prepare(
            &app,
            Envelope {
                headers: Map::new(),
                items: vec![
                    Item {
                        headers: Map::new(),
                        item_type: "span".into(),
                        payload: b"{}".to_vec(),
                    },
                    Item {
                        headers: Map::from_iter([
                            (
                                "content_type".into(),
                                Value::String(SPAN_V2_CONTENT_TYPE.into()),
                            ),
                            ("item_count".into(), Value::from(0)),
                        ]),
                        item_type: "span".into(),
                        payload: br#"{"items":[]}"#.to_vec(),
                    },
                ],
            },
            None,
            None,
            &GeoIpLookup::empty(),
        )
        .err()
        .expect("mixed standalone span ingress must be rejected");
        assert!(error.to_string().contains("duplicate or mixed"));
    }
}
