use jiff::Timestamp;
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::config::AppConfig;
use crate::envelope::Envelope;
use crate::error::{Error, Result};
use crate::model::Document;

const CONTENT_CHUNK_BYTES: usize = 3 << 20;
const MAX_INDEXED_JSON_BYTES: usize = 2 << 20;
const MAX_REPLAY_HEADER_BYTES: usize = 64 << 10;
const MAX_TITLE_BYTES: usize = 2048;
const MAX_MESSAGE_BYTES: usize = 16 << 10;
const MAX_ATTRIBUTE_BYTES: usize = 2048;
const MAX_FINGERPRINT_COMPONENTS: usize = 32;

pub struct PreparedIngest {
    pub documents: Vec<Document>,
    pub events: Vec<IngestedEvent>,
    pub response_event_id: String,
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

pub fn prepare(app: &AppConfig, envelope: Envelope) -> Result<PreparedIngest> {
    let received_at = Timestamp::now().to_string();
    let ingest_id = Uuid::new_v4().simple().to_string();
    let envelope_event_id = string(&envelope.headers, "event_id").and_then(normalize_event_id);
    let envelope_replay_id = envelope
        .headers
        .get("trace")
        .and_then(Value::as_object)
        .and_then(|trace| string(trace, "replay_id"))
        .and_then(normalize_event_id);
    let mut documents = Vec::new();
    let mut events = Vec::new();

    let mut envelope_document = Document::base(
        "envelope",
        &app.id,
        &app.project_id,
        &received_at,
        &received_at,
    );
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

    for (item_index, item) in envelope.items.into_iter().enumerate() {
        let mut parsed =
            if item.item_type == "event" || item.payload.len() <= MAX_INDEXED_JSON_BYTES {
                match serde_json::from_slice::<Value>(&item.payload) {
                    Ok(value) => Some(value),
                    Err(error) if item.item_type == "event" => {
                        return Err(Error::InvalidRequest(format!(
                            "invalid event JSON: {error}"
                        )));
                    }
                    Err(_) => None,
                }
            } else {
                None
            };
        let replay_header = (item.item_type == "replay_recording")
            .then(|| replay_recording_header(&item.payload))
            .flatten();
        let replay_metadata =
            matches!(item.item_type.as_str(), "replay_event" | "replay_recording").then(|| {
                let payload = parsed
                    .as_ref()
                    .or(replay_header.as_ref())
                    .and_then(Value::as_object);
                let replay_id = payload
                    .and_then(|payload| {
                        string(payload, "replay_id").or_else(|| string(payload, "event_id"))
                    })
                    .and_then(normalize_event_id)
                    .or_else(|| envelope_event_id.clone());
                let segment_id = payload
                    .and_then(|payload| payload.get("segment_id"))
                    .and_then(replay_segment_id);
                (replay_id, segment_id)
            });
        let event = if item.item_type == "event" {
            Some(
                normalize_event(
                    parsed.take(),
                    envelope_event_id.as_deref(),
                    envelope_replay_id.as_deref(),
                    &received_at,
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
            &app.id,
            &app.project_id,
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
        if item.payload.len() <= MAX_INDEXED_JSON_BYTES {
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
            app,
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
        response_event_id,
    })
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

fn normalize_event(
    payload: Option<Value>,
    envelope_event_id: Option<&str>,
    envelope_replay_id: Option<&str>,
    received_at: &str,
) -> Option<NormalizedEvent> {
    let payload = match payload? {
        Value::Object(payload) => payload,
        _ => return None,
    };
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
    let issue_id = issue_id(&payload, exception, &title, &platform);
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

fn issue_id(
    payload: &Map<String, Value>,
    exception: Option<&Map<String, Value>>,
    title: &str,
    platform: &str,
) -> String {
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
            if value.replace(' ', "") == "{{default}}" {
                append_default_fingerprint(&mut canonical, exception, title, platform);
            } else {
                canonical.push_str("custom\0");
                canonical.push_str(&bounded(value, MAX_ATTRIBUTE_BYTES));
                canonical.push('\0');
            }
        }
    } else {
        append_default_fingerprint(&mut canonical, exception, title, platform);
    }
    format!("{:x}", Sha256::digest(canonical.as_bytes()))
}

fn append_default_fingerprint(
    canonical: &mut String,
    exception: Option<&Map<String, Value>>,
    title: &str,
    platform: &str,
) {
    canonical.push_str("default\0");
    canonical.push_str(platform);
    canonical.push('\0');
    canonical.push_str(title);
    if let Some(frame) = exception
        .and_then(|value| value.get("stacktrace"))
        .and_then(Value::as_object)
        .and_then(|value| value.get("frames"))
        .and_then(Value::as_array)
        .and_then(|frames| {
            frames
                .iter()
                .rev()
                .find(|frame| frame.get("in_app").and_then(Value::as_bool).unwrap_or(true))
        })
        .and_then(Value::as_object)
    {
        for field in ["module", "function", "filename", "abs_path", "lineno"] {
            canonical.push('\0');
            if let Some(value) = frame.get(field) {
                match value {
                    Value::String(value) => {
                        canonical.push_str(&bounded(value, MAX_ATTRIBUTE_BYTES));
                    }
                    Value::Number(_) | Value::Bool(_) | Value::Null => {
                        canonical.push_str(&value.to_string());
                    }
                    Value::Array(_) | Value::Object(_) => {}
                }
            }
        }
    }
}

struct ContentInput<'a> {
    item_index: usize,
    timestamp: &'a str,
    received_at: &'a str,
    payload: &'a [u8],
}

fn append_content_chunks(
    documents: &mut Vec<Document>,
    app: &AppConfig,
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
            &app.id,
            &app.project_id,
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
    use crate::config::AppConfig;
    use crate::envelope::{Envelope, Item};

    fn fixture() -> AppConfig {
        AppConfig {
            id: "app".into(),
            project_id: "1".into(),
            name: "App".into(),
            slug: "app".into(),
            public_key: "0123456789abcdef0123456789abcdef".into(),
            auth_token_hash: "0".repeat(64),
            webhooks: Vec::new(),
            created_at: "now".into(),
            updated_at: "now".into(),
        }
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
    fn links_events_to_replays_from_sdk_tags_or_envelope_trace() {
        let tagged = normalize_event(
            Some(json!({
                "message": "failure",
                "tags": { "replayId": "cccccccc-cccc-4ccc-8ccc-cccccccccccc" }
            })),
            None,
            Some("dddddddddddd4ddd8ddddddddddddddd"),
            "2026-01-01T00:00:00Z",
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
        )
        .unwrap();
        assert_eq!(
            traced.replay_id.as_deref(),
            Some("dddddddddddd4ddd8ddddddddddddddd")
        );
    }

    #[test]
    fn explicit_fingerprint_overrides_frame_grouping() {
        let payload_a = json!({"fingerprint":["billing"],"message":"one"});
        let payload_b = json!({"fingerprint":["billing"],"message":"two"});
        let a = normalize_event(Some(payload_a), None, None, "2026-01-01T00:00:00Z").unwrap();
        let b = normalize_event(Some(payload_b), None, None, "2026-01-01T00:00:00Z").unwrap();
        assert_eq!(a.issue_id, b.issue_id);
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
        let a = normalize_event(Some(payload_a), None, None, "2026-01-01T00:00:00Z").unwrap();
        let b = normalize_event(Some(payload_b), None, None, "2026-01-01T00:00:00Z").unwrap();
        assert_ne!(a.issue_id, b.issue_id);
    }

    #[test]
    fn caps_custom_fingerprint_cardinality() {
        let mut first = vec![Value::String("shared".into()); MAX_FINGERPRINT_COMPONENTS];
        let mut second = first.clone();
        first.push(Value::String("ignored-a".into()));
        second.push(Value::String("ignored-b".into()));
        let first = json!({ "fingerprint": first, "message": "failure" });
        let second = json!({ "fingerprint": second, "message": "failure" });

        let first = normalize_event(Some(first), None, None, "2026-01-01T00:00:00Z").unwrap();
        let second = normalize_event(Some(second), None, None, "2026-01-01T00:00:00Z").unwrap();

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

        let event =
            normalize_event(Some(payload.clone()), None, None, "2026-01-01T00:00:00Z").unwrap();

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
        )
        .err()
        .expect("malformed events must be rejected");
        assert!(error.to_string().contains("invalid event JSON"));
    }
}
