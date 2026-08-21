use std::future::Future;
use std::io::Read;
use std::net::SocketAddr;
use std::str::FromStr;
use std::time::{SystemTime, UNIX_EPOCH};

use axum::Router;
use axum::body::Bytes;
use axum::extract::{DefaultBodyLimit, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::post;
use base64::Engine as _;
use base64::engine::general_purpose::STANDARD as BASE64;
use jiff::Timestamp;
use opentelemetry_proto::tonic::collector::logs::v1::logs_service_server::{
    LogsService, LogsServiceServer,
};
use opentelemetry_proto::tonic::collector::logs::v1::{
    ExportLogsServiceRequest, ExportLogsServiceResponse,
};
use opentelemetry_proto::tonic::collector::metrics::v1::metrics_service_server::{
    MetricsService, MetricsServiceServer,
};
use opentelemetry_proto::tonic::collector::metrics::v1::{
    ExportMetricsServiceRequest, ExportMetricsServiceResponse,
};
use opentelemetry_proto::tonic::collector::trace::v1::trace_service_server::{
    TraceService, TraceServiceServer,
};
use opentelemetry_proto::tonic::collector::trace::v1::{
    ExportTraceServiceRequest, ExportTraceServiceResponse,
};
use opentelemetry_proto::tonic::common::v1::any_value::Value as AttributeValue;
use opentelemetry_proto::tonic::common::v1::{AnyValue, KeyValue};
use opentelemetry_proto::tonic::logs::v1::ResourceLogs;
use opentelemetry_proto::tonic::metrics::v1::{ResourceMetrics, metric};
use opentelemetry_proto::tonic::resource::v1::Resource;
use opentelemetry_proto::tonic::trace::v1::{ResourceSpans, SpanFlags};
use prost::Message;
use serde::Serialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use tokio::sync::{mpsc, oneshot};
use tonic::{Code, Request, Response as GrpcResponse, Status};
use uuid::Uuid;

use crate::error::{Error, Result};
use crate::gen_ai;
use crate::storage::{SignalTable, Store};

const QUEUE_BATCHES: usize = 256;
const OTLP_MAX_MESSAGE_BYTES: usize = 20 << 20;
const SERVICE_ID_HEADER: &str = "x-platformd-service-id";

struct OtlpIdentity {
    service_id: String,
}

#[derive(Clone)]
struct Receivers {
    traces: mpsc::Sender<SignalBatch<ResourceSpans>>,
    metrics: mpsc::Sender<SignalBatch<ResourceMetrics>>,
    logs: mpsc::Sender<SignalBatch<ResourceLogs>>,
}

#[derive(Clone)]
struct TraceReceiver(mpsc::Sender<SignalBatch<ResourceSpans>>);

#[derive(Clone)]
struct MetricReceiver(mpsc::Sender<SignalBatch<ResourceMetrics>>);

#[derive(Clone)]
struct LogReceiver(mpsc::Sender<SignalBatch<ResourceLogs>>);

struct SignalBatch<T> {
    items: Vec<T>,
    persisted: oneshot::Sender<std::result::Result<(), PersistFailure>>,
}

enum PersistFailure {
    InvalidRequest(String),
    Internal(String),
}

pub(crate) async fn serve(
    store: Store,
    grpc_endpoint: SocketAddr,
    http_endpoint: SocketAddr,
) -> anyhow::Result<()> {
    let (traces_tx, traces_rx) = mpsc::channel(QUEUE_BATCHES);
    let (metrics_tx, metrics_rx) = mpsc::channel(QUEUE_BATCHES);
    let (logs_tx, logs_rx) = mpsc::channel(QUEUE_BATCHES);
    let receivers = Receivers {
        traces: traces_tx.clone(),
        metrics: metrics_tx.clone(),
        logs: logs_tx.clone(),
    };

    let grpc = tonic::transport::Server::builder()
        .add_service(TraceServiceServer::new(TraceReceiver(traces_tx)))
        .add_service(MetricsServiceServer::new(MetricReceiver(metrics_tx)))
        .add_service(LogsServiceServer::new(LogReceiver(logs_tx)))
        .serve(grpc_endpoint);
    let http_listener = tokio::net::TcpListener::bind(http_endpoint).await?;
    let http = axum::serve(
        http_listener,
        Router::new()
            .route("/v1/traces", post(http_traces))
            .route("/v1/metrics", post(http_metrics))
            .route("/v1/logs", post(http_logs))
            .layer(DefaultBodyLimit::max(OTLP_MAX_MESSAGE_BYTES))
            .with_state(receivers),
    );
    tracing::info!(%grpc_endpoint, %http_endpoint, "embedded OTLP receiver is listening");

    let trace_store = store.clone();
    let metric_store = store.clone();
    tokio::try_join!(
        async move { grpc.await.map_err(anyhow::Error::from) },
        async move { http.await.map_err(anyhow::Error::from) },
        consume_traces(trace_store, traces_rx),
        consume_metrics(metric_store, metrics_rx),
        consume_logs(store, logs_rx),
    )?;
    Ok(())
}

#[tonic::async_trait]
impl TraceService for TraceReceiver {
    async fn export(
        &self,
        request: Request<ExportTraceServiceRequest>,
    ) -> std::result::Result<GrpcResponse<ExportTraceServiceResponse>, Status> {
        enqueue(&self.0, request.into_inner().resource_spans).await?;
        Ok(GrpcResponse::new(ExportTraceServiceResponse::default()))
    }
}

#[tonic::async_trait]
impl MetricsService for MetricReceiver {
    async fn export(
        &self,
        request: Request<ExportMetricsServiceRequest>,
    ) -> std::result::Result<GrpcResponse<ExportMetricsServiceResponse>, Status> {
        enqueue(&self.0, request.into_inner().resource_metrics).await?;
        Ok(GrpcResponse::new(ExportMetricsServiceResponse::default()))
    }
}

#[tonic::async_trait]
impl LogsService for LogReceiver {
    async fn export(
        &self,
        request: Request<ExportLogsServiceRequest>,
    ) -> std::result::Result<GrpcResponse<ExportLogsServiceResponse>, Status> {
        enqueue(&self.0, request.into_inner().resource_logs).await?;
        Ok(GrpcResponse::new(ExportLogsServiceResponse::default()))
    }
}

async fn enqueue<T>(
    sender: &mpsc::Sender<SignalBatch<T>>,
    batch: Vec<T>,
) -> std::result::Result<(), Status> {
    let (persisted, confirmation) = oneshot::channel();
    sender
        .try_send(SignalBatch {
            items: batch,
            persisted,
        })
        .map_err(|_| Status::resource_exhausted("telemetry ingestion queue is full"))?;
    match confirmation
        .await
        .map_err(|_| Status::unavailable("telemetry ingestion stopped"))?
    {
        Ok(()) => Ok(()),
        Err(PersistFailure::InvalidRequest(message)) => Err(Status::invalid_argument(message)),
        Err(PersistFailure::Internal(message)) => Err(Status::internal(message)),
    }
}

async fn http_traces(State(state): State<Receivers>, headers: HeaderMap, body: Bytes) -> Response {
    receive_http::<ExportTraceServiceRequest, _>(
        headers,
        body,
        &state.traces,
        |mut request, identity| {
            if let Some(identity) = identity {
                request.resource_spans.iter_mut().for_each(|item| {
                    force_resource_identity(&mut item.resource, identity);
                });
            }
            request.resource_spans
        },
    )
    .await
}

async fn http_metrics(State(state): State<Receivers>, headers: HeaderMap, body: Bytes) -> Response {
    receive_http::<ExportMetricsServiceRequest, _>(
        headers,
        body,
        &state.metrics,
        |mut request, identity| {
            if let Some(identity) = identity {
                request.resource_metrics.iter_mut().for_each(|item| {
                    force_resource_identity(&mut item.resource, identity);
                });
            }
            request.resource_metrics
        },
    )
    .await
}

async fn http_logs(State(state): State<Receivers>, headers: HeaderMap, body: Bytes) -> Response {
    receive_http::<ExportLogsServiceRequest, _>(
        headers,
        body,
        &state.logs,
        |mut request, identity| {
            if let Some(identity) = identity {
                request.resource_logs.iter_mut().for_each(|item| {
                    force_resource_identity(&mut item.resource, identity);
                });
            }
            request.resource_logs
        },
    )
    .await
}

async fn receive_http<Request, Item>(
    headers: HeaderMap,
    body: Bytes,
    sender: &mpsc::Sender<SignalBatch<Item>>,
    items: impl FnOnce(Request, Option<&OtlpIdentity>) -> Vec<Item>,
) -> Response
where
    Request: Message + Default + for<'de> serde::Deserialize<'de>,
{
    let identity = match otlp_identity(&headers) {
        Ok(identity) => identity,
        Err(status) => return status.into_response(),
    };
    let body = match decode_http_body(&headers, &body) {
        Ok(body) => body,
        Err(status) => return status.into_response(),
    };
    let json_request = headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.starts_with("application/json"));
    let request = if json_request {
        serde_json::from_slice::<Value>(&body)
            .map(|mut value| {
                normalize_otlp_json_integers(&mut value);
                value
            })
            .and_then(serde_json::from_value)
            .map_err(|_| StatusCode::BAD_REQUEST)
    } else {
        Request::decode(body.as_slice()).map_err(|_| StatusCode::BAD_REQUEST)
    };
    let Ok(request) = request else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    if let Err(error) = enqueue(sender, items(request, identity.as_ref())).await {
        return match error.code() {
            Code::InvalidArgument => StatusCode::BAD_REQUEST,
            Code::ResourceExhausted | Code::Unavailable => StatusCode::SERVICE_UNAVAILABLE,
            _ => StatusCode::INTERNAL_SERVER_ERROR,
        }
        .into_response();
    }
    if json_request {
        ([(header::CONTENT_TYPE, "application/json")], "{}").into_response()
    } else {
        (
            [(header::CONTENT_TYPE, "application/x-protobuf")],
            Vec::<u8>::new(),
        )
            .into_response()
    }
}

fn otlp_identity(headers: &HeaderMap) -> std::result::Result<Option<OtlpIdentity>, StatusCode> {
    let service_id = headers.get(SERVICE_ID_HEADER);
    if service_id.is_none() {
        return Ok(None);
    }
    let service_id = service_id
        .and_then(|value| value.to_str().ok())
        .filter(|value| valid_platformd_id(value))
        .ok_or(StatusCode::BAD_REQUEST)?;
    Ok(Some(OtlpIdentity {
        service_id: service_id.into(),
    }))
}

fn valid_platformd_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
}

fn force_resource_identity(resource: &mut Option<Resource>, identity: &OtlpIdentity) {
    let resource = resource.get_or_insert_default();
    set_string_attribute(&mut resource.attributes, "service.id", &identity.service_id);
}

fn set_string_attribute(attributes: &mut Vec<KeyValue>, key: &str, value: &str) {
    attributes.retain(|attribute| attribute.key != key);
    attributes.push(KeyValue {
        key: key.into(),
        value: Some(AnyValue {
            value: Some(AttributeValue::StringValue(value.into())),
        }),
        ..Default::default()
    });
}

// prost's generated serde representation expects signed oneof values as JSON
// numbers, while OTLP/JSON requires int64 values to be encoded as strings.
// Normalize that standards-mandated representation before deserializing it.
fn normalize_otlp_json_integers(value: &mut Value) {
    match value {
        Value::Object(object) => {
            for (key, value) in object {
                if key == "asInt"
                    && let Some(parsed) = value.as_str().and_then(|value| value.parse::<i64>().ok())
                {
                    *value = parsed.into();
                } else {
                    normalize_otlp_json_integers(value);
                }
            }
        }
        Value::Array(values) => values.iter_mut().for_each(normalize_otlp_json_integers),
        _ => {}
    }
}

fn decode_http_body(headers: &HeaderMap, body: &[u8]) -> std::result::Result<Vec<u8>, StatusCode> {
    match headers
        .get(header::CONTENT_ENCODING)
        .and_then(|value| value.to_str().ok())
    {
        None | Some("identity") => Ok(body.to_vec()),
        Some("gzip") => {
            let mut decoded = Vec::new();
            flate2::read::GzDecoder::new(body)
                .take((OTLP_MAX_MESSAGE_BYTES + 1) as u64)
                .read_to_end(&mut decoded)
                .map_err(|_| StatusCode::BAD_REQUEST)?;
            if decoded.len() > OTLP_MAX_MESSAGE_BYTES {
                Err(StatusCode::PAYLOAD_TOO_LARGE)
            } else {
                Ok(decoded)
            }
        }
        Some(_) => Err(StatusCode::UNSUPPORTED_MEDIA_TYPE),
    }
}

async fn consume_traces(
    store: Store,
    mut receiver: mpsc::Receiver<SignalBatch<ResourceSpans>>,
) -> anyhow::Result<()> {
    while let Some(batch) = receiver.recv().await {
        let SignalBatch { items, persisted } = batch;
        persist_batch(persisted, async {
            store
                .ingest_signal_rows(SignalTable::Spans, trace_rows(items)?)
                .await
        })
        .await?;
    }
    Err(anyhow::anyhow!("OTLP traces channel closed"))
}

async fn consume_logs(
    store: Store,
    mut receiver: mpsc::Receiver<SignalBatch<ResourceLogs>>,
) -> anyhow::Result<()> {
    while let Some(batch) = receiver.recv().await {
        let SignalBatch { items, persisted } = batch;
        persist_batch(persisted, async {
            store
                .ingest_signal_rows(SignalTable::Logs, log_rows(items)?)
                .await
        })
        .await?;
    }
    Err(anyhow::anyhow!("OTLP logs channel closed"))
}

async fn consume_metrics(
    store: Store,
    mut receiver: mpsc::Receiver<SignalBatch<ResourceMetrics>>,
) -> anyhow::Result<()> {
    while let Some(batch) = receiver.recv().await {
        let SignalBatch { items, persisted } = batch;
        persist_batch(persisted, async {
            store
                .ingest_signal_rows(SignalTable::Metrics, metric_rows(items)?)
                .await
        })
        .await?;
    }
    Err(anyhow::anyhow!("OTLP metrics channel closed"))
}

async fn persist_batch(
    confirmation: oneshot::Sender<std::result::Result<(), PersistFailure>>,
    operation: impl Future<Output = Result<()>>,
) -> anyhow::Result<()> {
    match operation.await {
        Ok(()) => {
            let _ = confirmation.send(Ok(()));
            Ok(())
        }
        Err(error) => {
            let message = error.to_string();
            if matches!(error, Error::InvalidRequest(_)) {
                let _ = confirmation.send(Err(PersistFailure::InvalidRequest(message)));
                Ok(())
            } else {
                let _ = confirmation.send(Err(PersistFailure::Internal(message.clone())));
                Err(anyhow::anyhow!(message))
            }
        }
    }
}

fn trace_rows(resources: Vec<ResourceSpans>) -> Result<Vec<Value>> {
    let received_at = unix_nanos()?;
    let mut rows = Vec::new();
    for resource_spans in resources {
        let service_id = service_id(resource_spans.resource.as_ref());
        let resource_replay_id = resource_spans.resource.as_ref().and_then(|resource| {
            resource.attributes.iter().find_map(|candidate| {
                replay_attribute_key(&candidate.key)
                    .then(|| attribute(&resource.attributes, &candidate.key))
                    .flatten()
            })
        });
        let resource = json_string(&resource_spans.resource, "trace resource")?;
        for scope_spans in resource_spans.scope_spans {
            let scope = json_string(&scope_spans.scope, "trace scope")?;
            for span in scope_spans.spans {
                let ai = gen_ai::index_span(&span);
                let replay_id = span
                    .attributes
                    .iter()
                    .find_map(|candidate| {
                        replay_attribute_key(&candidate.key)
                            .then(|| attribute(&span.attributes, &candidate.key))
                            .flatten()
                    })
                    .or_else(|| resource_replay_id.clone())
                    .and_then(|value| normalize_replay_id(&value))
                    .unwrap_or_default();
                let trace_id = valid_binary_id(&span.trace_id, 16)
                    .ok_or_else(|| Error::InvalidRequest("OTLP span trace ID is invalid".into()))?;
                let span_id = valid_binary_id(&span.span_id, 8)
                    .ok_or_else(|| Error::InvalidRequest("OTLP span ID is invalid".into()))?;
                let parent_span_id = if span.parent_span_id.is_empty() {
                    String::new()
                } else {
                    valid_binary_id(&span.parent_span_id, 8).ok_or_else(|| {
                        Error::InvalidRequest("OTLP parent span ID is invalid".into())
                    })?
                };
                let span_json = json_string(&span, "span")?;
                let status_code = span.status.as_ref().map_or(0, |status| status.code);
                let status_message = span
                    .status
                    .as_ref()
                    .map_or("", |status| status.message.as_str());
                let is_segment = parent_span_id.is_empty()
                    || span.flags & SpanFlags::ContextIsRemoteMask as u32 != 0;
                rows.push(json!({
                    "service_id": service_id,
                    "trace_id": trace_id,
                    "span_id": span_id,
                    "parent_span_id": parent_span_id,
                    "segment_id": trace_id,
                    "is_segment": is_segment,
                    "trace_state": span.trace_state,
                    "name": span.name,
                    "kind": span.kind,
                    "start_time_unix_nano": span.start_time_unix_nano,
                    "end_time_unix_nano": span.end_time_unix_nano,
                    "duration_nano": span.end_time_unix_nano.saturating_sub(span.start_time_unix_nano),
                    "status_code": status_code,
                    "status_message": status_message,
                    "flags": span.flags,
                    "resource": resource,
                    "scope": scope,
                    "span": span_json,
                    "received_at_unix_nano": received_at,
                    "version": (1_u64 << 63) | received_at,
                    "source": "otlp",
                    "replay_id": replay_id,
                    "ai_kind": ai.kind,
                    "ai_operation": ai.operation,
                    "ai_provider": ai.provider,
                    "ai_model": ai.model,
                    "ai_agent": ai.agent,
                    "ai_input_tokens": ai.input_tokens,
                    "ai_output_tokens": ai.output_tokens,
                    "ai_cache_read_tokens": ai.cache_read_tokens,
                    "ai_cache_write_tokens": ai.cache_write_tokens,
                    "ai_reasoning_tokens": ai.reasoning_tokens,
                    "ai_cost_usd": ai.cost_usd,
                    "ai_ttft_seconds": ai.ttft_seconds,
                    "ai_tokens_per_second": ai.tokens_per_second,
                    "search_text": ai.search_text,
                }));
            }
        }
    }
    Ok(rows)
}

pub(crate) fn sentry_error_trace_row(
    service_id: &str,
    event: &crate::ingest::IngestedEvent,
) -> Result<Option<Value>> {
    let Some(trace) = event
        .payload
        .pointer("/contexts/trace")
        .and_then(Value::as_object)
    else {
        return Ok(None);
    };
    let Some(trace_id) = trace
        .get("trace_id")
        .and_then(Value::as_str)
        .and_then(|value| normalize_hex_id(value, 32))
    else {
        return Ok(None);
    };
    let parent_span_id = trace
        .get("span_id")
        .and_then(Value::as_str)
        .and_then(|value| normalize_hex_id(value, 16))
        .unwrap_or_default();
    let Some(segment_id) = sentry_trace_segment_id(trace)
        .or_else(|| (!parent_span_id.is_empty()).then(|| parent_span_id.clone()))
    else {
        return Ok(None);
    };
    let timestamp = Timestamp::from_str(&event.timestamp)
        .ok()
        .and_then(|value| u64::try_from(value.as_nanosecond()).ok())
        .ok_or_else(|| Error::InvalidRequest("Sentry error timestamp is invalid".into()))?;
    let received_at = unix_nanos()?;
    let marker_id = format!("{:x}", Sha256::digest(format!("error:{}", event.event_id)));
    let span_id = &marker_id[..16];
    let resource = serde_json::to_string(&json!({
        "attributes": [{"key": "service.id", "value": {"stringValue": service_id}}]
    }))
    .map_err(|error| Error::Storage(format!("encode Sentry error resource: {error}")))?;
    let marker = json!({
        "event_id": event.event_id,
        "issue_id": event.issue_id,
        "title": event.title,
        "level": event.level,
        "replay_id": event.payload.get("replay_id"),
        "trace": trace,
    });
    Ok(Some(json!({
        "service_id": service_id,
        "trace_id": trace_id,
        "span_id": span_id,
        "parent_span_id": parent_span_id,
        "segment_id": segment_id,
        "is_segment": false,
        "trace_state": "",
        "name": event.title,
        "kind": 1,
        "start_time_unix_nano": timestamp,
        "end_time_unix_nano": timestamp,
        "duration_nano": 0,
        "status_code": 2,
        "status_message": event.level,
        "flags": 0,
        "resource": resource,
        "scope": r#"{"name":"sentry","version":"1"}"#,
        "span": serde_json::to_string(&marker)
            .map_err(|error| Error::Storage(format!("encode Sentry error marker: {error}")))?,
        "received_at_unix_nano": received_at,
        "version": received_at,
        "source": "sentry_error",
        "replay_id": sentry_replay_id(&event.payload).unwrap_or_default(),
        "search_text": format!("{} {} {}", event.title, event.event_id, event.issue_id),
    })))
}

fn sentry_trace_segment_id(trace: &serde_json::Map<String, Value>) -> Option<String> {
    trace
        .get("segment_id")
        .and_then(Value::as_str)
        .or_else(|| {
            trace
                .get("data")
                .and_then(Value::as_object)
                .and_then(|data| {
                    data.get("sentry.segment.id")
                        .or_else(|| data.get("sentry.segment_id"))
                })
                .and_then(Value::as_str)
        })
        .and_then(|value| normalize_hex_id(value, 16))
}

fn replay_attribute_key(key: &str) -> bool {
    matches!(
        key.chars()
            .filter(|character| character.is_ascii_alphanumeric())
            .flat_map(char::to_lowercase)
            .collect::<String>()
            .as_str(),
        "replayid" | "sentryreplayid"
    )
}

fn sentry_span_attribute_text<'a>(payload: &'a Value, key: &str) -> Option<&'a str> {
    let value = payload.get("attributes")?.get(key)?;
    value
        .as_str()
        .or_else(|| value.get("value").and_then(Value::as_str))
}

fn normalize_replay_id(value: &str) -> Option<String> {
    let value = value.replace('-', "").to_ascii_lowercase();
    (value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit())).then_some(value)
}

fn sentry_replay_id(payload: &Value) -> Option<String> {
    let direct = payload
        .get("replay_id")
        .and_then(Value::as_str)
        .or_else(|| {
            payload
                .pointer("/contexts/replay/replay_id")
                .and_then(Value::as_str)
        })
        .or_else(|| {
            payload.get("tags").and_then(|tags| match tags {
                Value::Object(tags) => tags
                    .iter()
                    .find(|(key, _)| replay_attribute_key(key))
                    .and_then(|(_, value)| value.as_str()),
                Value::Array(tags) => tags.iter().find_map(|tag| match tag {
                    Value::Array(pair)
                        if pair
                            .first()
                            .and_then(Value::as_str)
                            .is_some_and(replay_attribute_key) =>
                    {
                        pair.get(1).and_then(Value::as_str)
                    }
                    _ => None,
                }),
                _ => None,
            })
        })
        .or_else(|| {
            payload
                .get("data")
                .and_then(Value::as_object)
                .and_then(|data| {
                    data.iter()
                        .find(|(key, _)| replay_attribute_key(key))
                        .and_then(|(_, value)| value.as_str())
                })
        })
        .or_else(|| {
            ["sentry.replay_id", "sentry.replay.id", "replay_id"]
                .into_iter()
                .find_map(|key| sentry_span_attribute_text(payload, key))
        });
    direct.and_then(normalize_replay_id)
}

fn log_rows(resources: Vec<ResourceLogs>) -> Result<Vec<Value>> {
    let received_at = unix_nanos()?;
    let mut rows = Vec::new();
    for resource_logs in resources {
        let service_id = service_id(resource_logs.resource.as_ref());
        let deployment_id = resource_logs
            .resource
            .as_ref()
            .and_then(|resource| attribute(&resource.attributes, "platformd.deployment.id"))
            .unwrap_or_default();
        let attempt_id = resource_logs
            .resource
            .as_ref()
            .and_then(|resource| attribute(&resource.attributes, "service.instance.id"))
            .unwrap_or_default();
        let resource = json_string(&resource_logs.resource, "log resource")?;
        for scope_logs in resource_logs.scope_logs {
            let scope = json_string(&scope_logs.scope, "log scope")?;
            for record in scope_logs.log_records {
                let record_json = json_string(&record, "log record")?;
                let body = record
                    .body
                    .as_ref()
                    .map(any_value_json)
                    .unwrap_or(Value::Null);
                let parsed_body = structured_log_body(record.body.as_ref(), &body);
                let trace_id = valid_binary_id(&record.trace_id, 16)
                    .or_else(|| parsed_body.as_ref().and_then(|body| log_id(body, true)))
                    .unwrap_or_default();
                let span_id = valid_binary_id(&record.span_id, 8)
                    .or_else(|| parsed_body.as_ref().and_then(|body| log_id(body, false)))
                    .unwrap_or_default();
                let severity_text = if record.severity_text.is_empty() {
                    parsed_body
                        .as_ref()
                        .and_then(log_severity)
                        .unwrap_or_default()
                } else {
                    record.severity_text.clone()
                };
                let severity_number = if record.severity_number == 0 {
                    severity_number(&severity_text)
                } else {
                    record.severity_number
                };
                let body_text = match record.body.as_ref().and_then(|body| body.value.as_ref()) {
                    Some(AttributeValue::StringValue(value)) => value.clone(),
                    _ => serde_json::to_string(&body)
                        .map_err(|error| Error::Storage(format!("encode log body: {error}")))?,
                };
                let body_json = parsed_body
                    .as_ref()
                    .map(serde_json::to_string)
                    .transpose()
                    .map_err(|error| {
                        Error::Storage(format!("encode structured log body: {error}"))
                    })?;
                let message = parsed_body
                    .as_ref()
                    .and_then(log_message)
                    .unwrap_or_else(|| body_text.clone());
                let stream = attribute(&record.attributes, "log.iostream").unwrap_or_default();
                let partial = attribute(&record.attributes, "platformd.log.partial")
                    .is_some_and(|value| value == "true");
                rows.push(json!({
                    "id": Uuid::new_v4().to_string(),
                    "service_id": service_id,
                    "deployment_id": deployment_id,
                    "attempt_id": attempt_id,
                    "stream": stream,
                    "partial": partial,
                    "time_unix_nano": record.time_unix_nano,
                    "observed_time_unix_nano": record.observed_time_unix_nano,
                    "trace_id": trace_id,
                    "span_id": span_id,
                    "severity_number": severity_number,
                    "severity_text": severity_text,
                    "message": message,
                    "body": body_text,
                    "body_json": body_json,
                    "attributes": json_string(&record.attributes, "log attributes")?,
                    "flags": record.flags,
                    "resource": resource,
                    "scope": scope,
                    "record": record_json,
                    "received_at_unix_nano": received_at,
                }));
            }
        }
    }
    Ok(rows)
}

fn metric_rows(resources: Vec<ResourceMetrics>) -> Result<Vec<Value>> {
    let received_at = unix_nanos()?;
    let mut rows = Vec::new();
    for resource_metrics in resources {
        let service_id = service_id(resource_metrics.resource.as_ref());
        let resource = json_string(&resource_metrics.resource, "metric resource")?;
        for scope_metrics in resource_metrics.scope_metrics {
            let scope = json_string(&scope_metrics.scope, "metric scope")?;
            for metric in scope_metrics.metrics {
                let metric_value = serde_json::to_value(&metric)
                    .map_err(|error| Error::Storage(format!("encode OTLP metric: {error}")))?;
                let metric_json = serde_json::to_string(&metric_value)
                    .map_err(|error| Error::Storage(format!("encode OTLP metric: {error}")))?;
                let (kind, data, temporality, monotonic) = match metric.data.as_ref() {
                    Some(metric::Data::Gauge(_)) => ("gauge", "gauge", 0, false),
                    Some(metric::Data::Sum(sum)) => {
                        ("sum", "sum", sum.aggregation_temporality, sum.is_monotonic)
                    }
                    Some(metric::Data::Histogram(histogram)) => (
                        "histogram",
                        "histogram",
                        histogram.aggregation_temporality,
                        false,
                    ),
                    Some(metric::Data::ExponentialHistogram(histogram)) => (
                        "exponential_histogram",
                        "exponentialHistogram",
                        histogram.aggregation_temporality,
                        false,
                    ),
                    Some(metric::Data::Summary(_)) => ("summary", "summary", 0, false),
                    None => continue,
                };
                let Some(points) = metric_value
                    .get(data)
                    .and_then(|value| value.get("dataPoints"))
                    .and_then(Value::as_array)
                else {
                    continue;
                };
                for point in points {
                    let scope_kind = json_attribute(point, "platformd.scope.kind");
                    let scope_id = json_attribute(point, "platformd.scope.id")
                        .filter(|value| !value.is_empty())
                        .unwrap_or_else(|| service_id.clone());
                    let field = json_attribute(point, "platformd.field").unwrap_or_default();
                    let (attribute_keys, attribute_values) = metric_attributes(point);
                    rows.push(json!({
                        "id": Uuid::new_v4().to_string(),
                        "service_id": service_id,
                        "scope_kind": scope_kind,
                        "scope_id": scope_id,
                        "field": field,
                        "name": metric.name,
                        "description": metric.description,
                        "unit": metric.unit,
                        "kind": kind,
                        "start_time_unix_nano": json_u64(point.get("startTimeUnixNano")),
                        "time_unix_nano": json_u64(point.get("timeUnixNano")),
                        "value_int": json_i64(point.get("asInt")),
                        "value_double": json_f64(point.get("asDouble")),
                        "count": json_u64_optional(point.get("count")),
                        "sum": json_f64(point.get("sum")),
                        "min": json_f64(point.get("min")),
                        "max": json_f64(point.get("max")),
                        "explicit_bounds": json_f64_array(point.get("explicitBounds")),
                        "bucket_counts": json_u64_array(point.get("bucketCounts")),
                        "exponential_scale": json_i64(point.get("scale")).unwrap_or_default(),
                        "positive_offset": json_i64(json_path(point, &["positive", "offset"])).unwrap_or_default(),
                        "positive_counts": json_u64_array(json_path(point, &["positive", "bucketCounts"])),
                        "negative_offset": json_i64(json_path(point, &["negative", "offset"])).unwrap_or_default(),
                        "negative_counts": json_u64_array(json_path(point, &["negative", "bucketCounts"])),
                        "zero_count": json_u64(point.get("zeroCount")),
                        "aggregation_temporality": temporality,
                        "is_monotonic": monotonic,
                        "flags": json_u64(point.get("flags")),
                        "attribute_keys": attribute_keys,
                        "attribute_values": attribute_values,
                        "attributes": json_value_string(point.get("attributes"), &json!([]))?,
                        "exemplars": json_value_string(point.get("exemplars"), &json!([]))?,
                        "point": serde_json::to_string(point).map_err(|error| Error::Storage(format!("encode OTLP metric point: {error}")))?,
                        "resource": resource,
                        "scope": scope,
                        "metric": metric_json,
                        "received_at_unix_nano": received_at,
                    }));
                }
            }
        }
    }
    Ok(rows)
}

fn metric_attributes(value: &Value) -> (Vec<String>, Vec<String>) {
    let Some(attributes) = value.get("attributes").and_then(Value::as_array) else {
        return (Vec::new(), Vec::new());
    };
    attributes
        .iter()
        .filter_map(|attribute| {
            let key = attribute.get("key")?.as_str()?.to_owned();
            let value = attribute.get("value")?.as_object()?.values().next()?;
            let value = match value {
                Value::String(value) => value.clone(),
                Value::Number(value) => value.to_string(),
                Value::Bool(value) => value.to_string(),
                _ => return None,
            };
            Some((key, value))
        })
        .unzip()
}

fn json_attribute(value: &Value, key: &str) -> Option<String> {
    value
        .get("attributes")
        .and_then(Value::as_array)?
        .iter()
        .find(|attribute| attribute.get("key").and_then(Value::as_str) == Some(key))?
        .get("value")?
        .as_object()?
        .values()
        .next()
        .and_then(|value| match value {
            Value::String(value) => Some(value.clone()),
            Value::Number(value) => Some(value.to_string()),
            Value::Bool(value) => Some(value.to_string()),
            _ => None,
        })
}

fn any_value_json(value: &AnyValue) -> Value {
    match value.value.as_ref() {
        Some(AttributeValue::StringValue(value)) => Value::String(value.clone()),
        Some(AttributeValue::BoolValue(value)) => Value::Bool(*value),
        Some(AttributeValue::IntValue(value)) => (*value).into(),
        Some(AttributeValue::DoubleValue(value)) => json!(value),
        Some(AttributeValue::ArrayValue(value)) => {
            Value::Array(value.values.iter().map(any_value_json).collect())
        }
        Some(AttributeValue::KvlistValue(value)) => Value::Object(
            value
                .values
                .iter()
                .map(|entry| {
                    (
                        entry.key.clone(),
                        entry
                            .value
                            .as_ref()
                            .map(any_value_json)
                            .unwrap_or(Value::Null),
                    )
                })
                .collect(),
        ),
        Some(AttributeValue::BytesValue(value)) => Value::String(BASE64.encode(value)),
        Some(AttributeValue::StringValueStrindex(value)) => (*value).into(),
        None => Value::Null,
    }
}

fn structured_log_body(source: Option<&AnyValue>, value: &Value) -> Option<Value> {
    match source.and_then(|source| source.value.as_ref()) {
        Some(AttributeValue::StringValue(text)) => serde_json::from_str::<Value>(text)
            .ok()
            .filter(Value::is_object),
        Some(AttributeValue::KvlistValue(_)) => Some(value.clone()),
        _ => None,
    }
}

fn log_id(body: &Value, trace: bool) -> Option<String> {
    let paths: &[&[&str]] = if trace {
        &[&["trace_id"], &["traceId"], &["trace", "id"]]
    } else {
        &[&["span_id"], &["spanId"], &["span", "id"]]
    };
    let length = if trace { 32 } else { 16 };
    paths
        .iter()
        .filter_map(|path| json_path(body, path).and_then(Value::as_str))
        .find_map(|value| normalize_hex_id(value, length))
}

fn log_severity(body: &Value) -> Option<String> {
    ["level", "severity", "severity_text", "severityText"]
        .iter()
        .find_map(|key| body.get(key).and_then(Value::as_str))
        .map(str::to_owned)
}

fn log_message(body: &Value) -> Option<String> {
    ["message", "msg"]
        .iter()
        .find_map(|key| body.get(key).and_then(Value::as_str))
        .map(str::to_owned)
}

fn severity_number(value: &str) -> i32 {
    match value.to_ascii_lowercase().as_str() {
        "trace" => 1,
        "debug" => 5,
        "info" | "information" => 9,
        "warn" | "warning" => 13,
        "error" => 17,
        "fatal" | "critical" => 21,
        _ => 0,
    }
}

fn json_path<'a>(value: &'a Value, path: &[&str]) -> Option<&'a Value> {
    path.iter().try_fold(value, |current, key| current.get(key))
}

fn valid_binary_id(value: &[u8], bytes: usize) -> Option<String> {
    (value.len() == bytes && value.iter().any(|byte| *byte != 0)).then(|| hex_id(value))
}

fn normalize_hex_id(value: &str, length: usize) -> Option<String> {
    (value.len() == length
        && value.bytes().all(|byte| byte.is_ascii_hexdigit())
        && value.bytes().any(|byte| byte != b'0'))
    .then(|| value.to_ascii_lowercase())
}

fn json_u64(value: Option<&Value>) -> u64 {
    json_u64_optional(value).unwrap_or_default()
}

fn json_u64_optional(value: Option<&Value>) -> Option<u64> {
    value.and_then(|value| {
        value
            .as_u64()
            .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
    })
}

fn json_i64(value: Option<&Value>) -> Option<i64> {
    value.and_then(|value| {
        value
            .as_i64()
            .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
    })
}

fn json_f64(value: Option<&Value>) -> Option<f64> {
    value.and_then(|value| {
        value
            .as_f64()
            .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
    })
}

fn json_u64_array(value: Option<&Value>) -> Vec<u64> {
    value
        .and_then(Value::as_array)
        .map(|values| {
            values
                .iter()
                .filter_map(|value| json_u64_optional(Some(value)))
                .collect()
        })
        .unwrap_or_default()
}

fn json_f64_array(value: Option<&Value>) -> Vec<f64> {
    value
        .and_then(Value::as_array)
        .map(|values| {
            values
                .iter()
                .filter_map(|value| json_f64(Some(value)))
                .collect()
        })
        .unwrap_or_default()
}

fn json_value_string(value: Option<&Value>, fallback: &Value) -> Result<String> {
    serde_json::to_string(value.unwrap_or(fallback))
        .map_err(|error| Error::Storage(format!("encode OTLP value: {error}")))
}

fn service_id(resource: Option<&opentelemetry_proto::tonic::resource::v1::Resource>) -> String {
    resource
        .and_then(|resource| attribute(&resource.attributes, "service.id"))
        .or_else(|| resource.and_then(|resource| attribute(&resource.attributes, "service.name")))
        .unwrap_or_else(|| "unknown".into())
}

fn attribute(attributes: &[KeyValue], key: &str) -> Option<String> {
    let value = attributes
        .iter()
        .find(|item| item.key == key)?
        .value
        .as_ref()?;
    match value.value.as_ref()? {
        AttributeValue::StringValue(value) => Some(value.clone()),
        AttributeValue::IntValue(value) => Some(value.to_string()),
        AttributeValue::BoolValue(value) => Some(value.to_string()),
        _ => None,
    }
}

fn json_string(value: &impl Serialize, description: &str) -> Result<String> {
    serde_json::to_string(value)
        .map_err(|error| Error::Storage(format!("encode OTLP {description}: {error}")))
}

fn hex_id(value: &[u8]) -> String {
    const HEX: &[u8; 16] = b"0123456789abcdef";
    let mut output = String::with_capacity(value.len() * 2);
    for byte in value {
        output.push(HEX[(byte >> 4) as usize] as char);
        output.push(HEX[(byte & 0x0f) as usize] as char);
    }
    output
}

fn unix_nanos() -> Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|error| Error::Storage(format!("read system clock: {error}")))?
        .as_nanos()
        .try_into()
        .map_err(|_| Error::Storage("system clock exceeds OTLP timestamp range".into()))
}

#[cfg(test)]
mod tests {
    use opentelemetry_proto::tonic::common::v1::{AnyValue, KeyValue};
    use opentelemetry_proto::tonic::logs::v1::{LogRecord, ResourceLogs, ScopeLogs};
    use opentelemetry_proto::tonic::resource::v1::Resource;
    use opentelemetry_proto::tonic::trace::v1::{ResourceSpans, ScopeSpans, Span};

    use super::*;

    #[test]
    fn service_identity_prefers_platformd_service_id() {
        let resource = Resource {
            attributes: vec![
                KeyValue {
                    key: "service.name".into(),
                    value: Some(AnyValue {
                        value: Some(AttributeValue::StringValue("web".into())),
                    }),
                    key_strindex: 0,
                },
                KeyValue {
                    key: "service.id".into(),
                    value: Some(AnyValue {
                        value: Some(AttributeValue::StringValue("service-1".into())),
                    }),
                    key_strindex: 0,
                },
            ],
            dropped_attributes_count: 0,
            entity_refs: Vec::new(),
        };
        assert_eq!(service_id(Some(&resource)), "service-1");
    }

    #[test]
    fn gateway_identity_overrides_spoofed_resource_attributes() {
        let mut resource = Some(Resource {
            attributes: vec![KeyValue {
                key: "service.id".into(),
                value: Some(AnyValue {
                    value: Some(AttributeValue::StringValue("spoofed-service".into())),
                }),
                ..Default::default()
            }],
            ..Default::default()
        });
        force_resource_identity(
            &mut resource,
            &OtlpIdentity {
                service_id: "service-1".into(),
            },
        );

        let resource = resource.as_ref().unwrap();
        assert_eq!(
            attribute(&resource.attributes, "service.id").as_deref(),
            Some("service-1")
        );
        assert_eq!(
            resource
                .attributes
                .iter()
                .filter(|attribute| attribute.key == "service.id")
                .count(),
            1
        );
    }

    #[test]
    fn structured_container_logs_supply_correlation_without_overwriting_otlp_fields() {
        let rows = log_rows(vec![ResourceLogs {
            resource: Some(Resource {
                attributes: vec![KeyValue {
                    key: "service.id".into(),
                    value: Some(AnyValue {
                        value: Some(AttributeValue::StringValue("service-1".into())),
                    }),
                    ..Default::default()
                }],
                ..Default::default()
            }),
            scope_logs: vec![ScopeLogs {
                log_records: vec![LogRecord {
                    body: Some(AnyValue {
                        value: Some(AttributeValue::StringValue(
                            r#"{"message":"checkout failed","level":"error","trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef"}"#.into(),
                        )),
                    }),
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        }])
        .unwrap();

        assert_eq!(rows[0]["service_id"], "service-1");
        assert_eq!(rows[0]["message"], "checkout failed");
        assert_eq!(rows[0]["severity_text"], "error");
        assert_eq!(rows[0]["severity_number"], 17);
        assert_eq!(rows[0]["trace_id"], "0123456789abcdef0123456789abcdef");
        assert_eq!(rows[0]["span_id"], "0123456789abcdef");
        assert!(
            rows[0]["body_json"]
                .as_str()
                .unwrap()
                .contains("checkout failed")
        );
    }

    #[test]
    fn invalid_otlp_span_ids_are_rejected_before_storage() {
        let result = trace_rows(vec![ResourceSpans {
            scope_spans: vec![ScopeSpans {
                spans: vec![Span {
                    trace_id: vec![1; 15],
                    span_id: vec![1; 8],
                    ..Default::default()
                }],
                ..Default::default()
            }],
            ..Default::default()
        }]);

        assert!(matches!(result, Err(Error::InvalidRequest(_))));
    }

    #[test]
    fn otlp_spans_keep_one_distributed_trace_across_remote_parents() {
        let trace_id = vec![1; 16];
        let rows = trace_rows(vec![ResourceSpans {
            scope_spans: vec![ScopeSpans {
                spans: vec![
                    Span {
                        trace_id: trace_id.clone(),
                        span_id: vec![1; 8],
                        ..Default::default()
                    },
                    Span {
                        trace_id: trace_id.clone(),
                        span_id: vec![2; 8],
                        parent_span_id: vec![1; 8],
                        flags: (1 << 8) | (1 << 9),
                        ..Default::default()
                    },
                    Span {
                        trace_id,
                        span_id: vec![3; 8],
                        parent_span_id: vec![2; 8],
                        ..Default::default()
                    },
                ],
                ..Default::default()
            }],
            ..Default::default()
        }])
        .unwrap();

        assert_eq!(rows[0]["segment_id"], "01010101010101010101010101010101");
        assert_eq!(rows[0]["is_segment"], true);
        assert_eq!(rows[1]["segment_id"], rows[0]["segment_id"]);
        assert_eq!(rows[1]["is_segment"], true);
        assert_eq!(rows[2]["segment_id"], rows[0]["segment_id"]);
        assert_eq!(rows[2]["is_segment"], false);
    }

    #[tokio::test]
    async fn invalid_otlp_batch_does_not_stop_the_consumer() {
        let (confirmation, received) = oneshot::channel();
        let result = persist_batch(confirmation, async {
            Err(Error::InvalidRequest("invalid span".into()))
        })
        .await;

        assert!(result.is_ok());
        assert!(matches!(
            received.await,
            Ok(Err(PersistFailure::InvalidRequest(_)))
        ));
    }

    #[test]
    fn metrics_are_stored_as_queryable_data_points() {
        let mut payload = json!({
            "resourceMetrics": [{
                "resource": {"attributes": [{"key": "service.id", "value": {"stringValue": "service-1"}}]},
                "scopeMetrics": [{"metrics": [{
                    "name": "http.server.active_requests",
                    "unit": "{request}",
                    "gauge": {"dataPoints": [{
                        "timeUnixNano": "42",
                        "asInt": "7",
                        "attributes": [
                            {"key": "http.request.method", "value": {"stringValue": "GET"}},
                            {"key": "platformd.scope.kind", "value": {"stringValue": "resource_service"}},
                            {"key": "platformd.scope.id", "value": {"stringValue": "service-1"}},
                            {"key": "platformd.field", "value": {"stringValue": "MemoryBytes"}}
                        ]
                    }]}
                }]}]
            }]
        });
        normalize_otlp_json_integers(&mut payload);
        let request: ExportMetricsServiceRequest = serde_json::from_value(payload).unwrap();
        let rows = metric_rows(request.resource_metrics).unwrap();

        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0]["service_id"], "service-1");
        assert_eq!(rows[0]["kind"], "gauge");
        assert_eq!(rows[0]["time_unix_nano"], 42);
        assert_eq!(rows[0]["value_int"], 7);
        assert_eq!(rows[0]["scope_kind"], "resource_service");
        assert_eq!(rows[0]["scope_id"], "service-1");
        assert_eq!(rows[0]["field"], "MemoryBytes");
        assert!(
            rows[0]["attributes"]
                .as_str()
                .unwrap()
                .contains("http.request.method")
        );
    }

    #[test]
    fn sentry_errors_become_trace_issue_markers() {
        let row = sentry_error_trace_row(
            "service-1",
            &crate::ingest::IngestedEvent {
                event_id: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa".into(),
                issue_id: "issue-checkout".into(),
                title: "CheckoutInvariantError".into(),
                level: "error".into(),
                platform: "javascript".into(),
                timestamp: "2026-08-15T10:00:00Z".into(),
                payload: json!({
                    "contexts": {"trace": {
                        "trace_id": "0123456789abcdef0123456789abcdef",
                        "span_id": "0123456789abcdef"
                    }}
                }),
            },
        )
        .unwrap()
        .unwrap();

        assert_eq!(row["source"], "sentry_error");
        assert_eq!(row["trace_id"], "0123456789abcdef0123456789abcdef");
        assert_eq!(row["parent_span_id"], "0123456789abcdef");
        assert_eq!(row["status_code"], 2);
        assert_eq!(row["span_id"].as_str().unwrap().len(), 16);
    }
}
