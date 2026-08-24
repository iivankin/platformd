use std::cmp::Ordering;
use std::collections::HashSet;
use std::io::Read;
use std::net::{IpAddr, SocketAddr};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use axum::body::{Body, Bytes};
use axum::extract::{
    ConnectInfo, DefaultBodyLimit, Extension, Multipart, Path, Query, RawQuery, State,
};
use axum::http::{HeaderMap, Method, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{delete, get, post};
use axum::{Json, Router};
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD as BASE64_URL;
use jiff::Timestamp;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use tokio::sync::Mutex;
use tokio_util::io::ReaderStream;
use tower::limit::ConcurrencyLimitLayer;
use tower_http::cors::{Any, CorsLayer};
use tower_http::limit::RequestBodyLimitLayer;
use tower_http::trace::TraceLayer;
use url::Url;

use crate::MAX_STORED_ITEM_BYTES;
use crate::ai_price::AiPriceEstimator;
use crate::artifact::{self, ArtifactMetadata};
use crate::auth::sentry_public_key;
use crate::envelope::{Envelope, Item, parse};
use crate::error::{Error, Result};
use crate::geoip::{Geo, GeoIpLookup, GeoIpSource};
use crate::ingest::{IngestedEvent, PreparedIngest, prepare};
use crate::metric_sql::MetricSqlQuery;
use crate::model::{Document, SearchRequest};
use crate::service::{
    ARTIFACT_AUTHORIZED_HEADER, SENTRY_ORGANIZATION, SENTRY_PROJECT_ID, SERVICE_ID_HEADER,
    ServiceContext, WEBHOOK_EVENTS_HEADER,
};
use crate::storage::{LogFieldFilter, LogFieldOperator, LogPage, LogQuery, Store, all, any, term};
use crate::symbolicator::{
    CrashFileKind, Dispatcher as SymbolicationDispatcher, Symbolicator, crash_event,
};

const MAX_COMPRESSED_BODY_BYTES: usize = 64 << 20;
const MAX_CONCURRENT_REQUESTS: usize = 32;
const MAX_CONCURRENT_INGEST_REQUESTS: usize = 4;
const MAX_CHUNKS_PER_REQUEST: usize = 16;
const MAX_SEARCH_QUERY_CHARS: usize = 256;
const MAX_REPLAY_PLAYER_BYTES: usize = 64 << 20;
const MAX_REPLAY_RECORDING_HEADER_BYTES: usize = 64 << 10;
const MAX_WEBHOOK_NOTIFICATIONS: usize = 32;

#[derive(Clone, Debug)]
pub struct TelemetryOptions {
    pub listen: SocketAddr,
    pub otlp_grpc_listen: SocketAddr,
    pub otlp_http_listen: SocketAddr,
    pub volume: PathBuf,
    pub geoip_source: GeoIpSource,
}

pub struct TelemetryServer {
    listen: SocketAddr,
    otlp_grpc_listen: SocketAddr,
    otlp_http_listen: SocketAddr,
    ai_prices: Arc<AiPriceEstimator>,
    state: Arc<ServerState>,
}

pub(crate) struct ServerState {
    pub(crate) store: Store,
    pub(crate) symbolicator: Symbolicator,
    symbolication: SymbolicationDispatcher,
    geoip: GeoIpLookup,
    ingest_lock: Mutex<()>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Health {
    status: &'static str,
    version: &'static str,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct BackupView {
    id: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct DiskUsageView {
    recording_bytes: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct MetricHistoryQuery {
    scope_kind: String,
    scope_ids: Option<String>,
    from: u64,
    to: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct LogHistoryQuery {
    service_ids: String,
    deployment_id: Option<String>,
    contains: Option<String>,
    field_filters: Option<String>,
    severity_text: Option<String>,
    trace_id: Option<String>,
    span_id: Option<String>,
    from: Option<u64>,
    to: Option<u64>,
    after_time_unix_nano: Option<u64>,
    after_id: Option<String>,
    limit: Option<usize>,
    order: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct TraceListQuery {
    #[serde(default = "default_limit")]
    limit: usize,
    #[serde(default)]
    offset: usize,
    from: Option<u64>,
    to: Option<u64>,
    query: Option<String>,
    #[serde(default)]
    status: TraceListStatus,
    #[serde(default)]
    sort: TraceListSort,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct AiOverviewQuery {
    from: u64,
    to: u64,
    step: u64,
}

#[derive(Clone, Copy, Default, Deserialize)]
#[serde(rename_all = "lowercase")]
enum TraceListStatus {
    #[default]
    All,
    Error,
    Ok,
}

#[derive(Clone, Copy, Default, Deserialize)]
#[serde(rename_all = "lowercase")]
enum TraceListSort {
    #[default]
    Latest,
    Slowest,
    Spans,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct ReplayRecordingQuery {
    #[serde(default)]
    offset: usize,
    limit: Option<usize>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct MetricSqlRequest {
    sql: String,
    from: u64,
    to: u64,
    step: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct MetricScopeRequest {
    service_ids: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct AnchoredServiceScopeRequest {
    anchor_service_id: Option<String>,
    service_ids: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct IssueScopeRequest {
    service_ids: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ScopedMetricSqlRequest {
    service_ids: Vec<String>,
    query: MetricSqlRequest,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ListResponse {
    pub(crate) data: Vec<Value>,
    pub(crate) total: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct ListQuery {
    #[serde(default = "default_limit")]
    pub(crate) limit: usize,
    #[serde(default)]
    pub(crate) offset: usize,
    pub(crate) query: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct IssueDetailQuery {
    #[serde(default = "default_limit")]
    limit: usize,
    #[serde(default)]
    offset: usize,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ItemQuery {
    item_type: String,
    #[serde(default = "default_limit")]
    limit: usize,
    #[serde(default)]
    offset: usize,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct IssueView {
    service_id: String,
    id: String,
    title: String,
    level: String,
    platform: String,
    status: String,
    first_seen: String,
    last_seen: String,
    event_count: u64,
    last_event_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct IssueStatusInput {
    pub(crate) status: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct WebhookNotification {
    event_type: &'static str,
    timestamp: String,
    issue: WebhookIssue,
    event: WebhookOccurrence,
}

#[derive(Serialize)]
struct WebhookIssue {
    id: String,
    title: String,
    level: String,
    platform: String,
}

#[derive(Serialize)]
struct WebhookOccurrence {
    id: String,
    timestamp: String,
}

struct IngestResult {
    event_id: String,
    notifications: Vec<WebhookNotification>,
}

impl TelemetryServer {
    pub async fn new(options: TelemetryOptions) -> Result<Self> {
        if options.volume.as_os_str().is_empty() {
            return Err(Error::Configuration("volume path must not be empty".into()));
        }
        let store = Store::open(options.volume.clone()).await?;
        let ai_prices =
            Arc::new(AiPriceEstimator::new(&options.volume).map_err(Error::Configuration)?);
        let geoip = GeoIpLookup::from_volume(&options.volume, options.geoip_source).await;
        let symbolicator = Symbolicator::new(store.clone());
        let symbolication = SymbolicationDispatcher::new(symbolicator.clone(), store.clone());
        Ok(Self {
            listen: options.listen,
            otlp_grpc_listen: options.otlp_grpc_listen,
            otlp_http_listen: options.otlp_http_listen,
            ai_prices,
            state: Arc::new(ServerState {
                store,
                symbolicator,
                symbolication,
                geoip,
                ingest_lock: Mutex::new(()),
            }),
        })
    }

    pub fn router(&self) -> Router {
        let internal = service_routes();
        let ingestion = Router::new()
            .route("/api/{project_id}/envelope/", post(envelope))
            .route("/api/{project_id}/store/", post(store))
            .route("/api/{project_id}/minidump/", post(minidump))
            .route(
                "/api/{project_id}/apple-crash-report/",
                post(apple_crash_report),
            )
            .route(
                "/api/0/organizations/{org}/chunk-upload/",
                get(chunk_upload_options).post(upload_chunks),
            )
            .route(
                "/api/0/organizations/{org}/artifactbundle/assemble/",
                post(assemble_artifact_bundle),
            )
            .route(
                "/api/0/projects/{org}/{project}/files/difs/assemble/",
                post(assemble_difs),
            )
            .layer(ConcurrencyLimitLayer::new(MAX_CONCURRENT_INGEST_REQUESTS))
            .layer(browser_cors());
        Router::new()
            .route("/health", get(health))
            .route("/internal/backups", post(create_internal_backup))
            .route("/internal/disk-usage", get(internal_disk_usage))
            .route("/internal/logs", get(internal_logs))
            .route("/internal/metrics", get(internal_metrics))
            .route("/internal/issue-scopes", post(internal_issues))
            .route("/internal/trace-scopes", post(internal_traces))
            .route("/internal/trace-scopes/{trace_id}", post(internal_trace))
            .route("/internal/ai-scopes/overview", post(internal_ai_overview))
            .route(
                "/internal/metric-scopes/catalog",
                post(internal_metric_catalog),
            )
            .route("/internal/metric-scopes/query", post(internal_metric_sql))
            .route(
                "/internal/analytics/ingest",
                post(internal_analytics_ingest),
            )
            .route(
                "/internal/analytics/{tracker_id}/query",
                post(internal_analytics_query),
            )
            .route(
                "/internal/backups/{backup_id}",
                get(download_internal_backup).delete(delete_internal_backup),
            )
            .route(
                "/internal/services/{service_id}",
                delete(delete_internal_service),
            )
            .merge(ingestion)
            .nest("/internal/services/{service_id}", internal)
            .layer(DefaultBodyLimit::disable())
            .layer(RequestBodyLimitLayer::new(MAX_COMPRESSED_BODY_BYTES))
            .layer(TraceLayer::new_for_http())
            .layer(ConcurrencyLimitLayer::new(MAX_CONCURRENT_REQUESTS))
            .with_state(self.state.clone())
    }

    pub async fn serve(self) -> anyhow::Result<()> {
        self.ai_prices.initialize().await;
        let listener = tokio::net::TcpListener::bind(self.listen).await?;
        let listen = listener.local_addr()?;
        tracing::info!(listen = %listen, "Sentry-compatible receiver is listening");
        let http = axum::serve(
            listener,
            self.router()
                .into_make_service_with_connect_info::<SocketAddr>(),
        );
        let store = self.state.store.clone();
        let reclaim = tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(60 * 60));
            loop {
                interval.tick().await;
                if let Err(error) = store.reclaim_expired_recordings().await {
                    tracing::warn!(error = %error, "telemetry recording reclaim failed");
                }
            }
        });
        let ai_prices = self.ai_prices.clone();
        let price_refresh = tokio::spawn(async move {
            ai_prices.refresh_forever().await;
        });
        let result = tokio::try_join!(
            async move { http.await.map_err(anyhow::Error::from) },
            crate::telemetry::serve(
                self.state.store.clone(),
                self.otlp_grpc_listen,
                self.otlp_http_listen,
                self.ai_prices,
            ),
        );
        reclaim.abort();
        price_refresh.abort();
        result?;
        Ok(())
    }
}

fn browser_cors() -> CorsLayer {
    CorsLayer::new()
        .allow_origin(Any)
        .allow_headers(Any)
        .allow_methods([Method::GET, Method::POST, Method::PATCH, Method::DELETE])
}

fn service_routes() -> Router<Arc<ServerState>> {
    Router::new()
        .route("/events", get(events))
        .route("/events/{event_id}", get(event))
        .route("/replays", get(replays))
        .route("/replays/{replay_id}", get(replay))
        .route("/replays/{replay_id}/recording", get(replay_recording))
        .route("/replays/{replay_id}/video/{segment_id}", get(replay_video))
        .route("/issues", get(issues))
        .route("/issues/{issue_id}", get(issue).patch(update_issue))
        .route("/artifacts", get(artifacts))
        .route("/items", get(items))
        .route("/content/{content_id}", get(content))
        .route("/traces", get(traces))
        .route("/traces/{trace_id}", get(trace))
        .route("/ai/overview", get(ai_overview))
        .route("/metrics/catalog", get(metric_catalog))
        .route("/metrics/query", post(metric_sql))
}

async fn traces(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<TraceListQuery>,
) -> Result<Json<Vec<crate::storage::TraceSummary>>> {
    ServiceContext::parse(&service_id)?;
    trace_summaries(state, Some(service_id.clone()), vec![service_id], query).await
}

async fn ai_overview(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<AiOverviewQuery>,
) -> Result<Json<crate::storage::AiOverview>> {
    ServiceContext::parse(&service_id)?;
    ai_overview_for_scope(state, Some(service_id.clone()), vec![service_id], query).await
}

async fn internal_ai_overview(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Query(query): Query<AiOverviewQuery>,
    Json(scope): Json<AnchoredServiceScopeRequest>,
) -> Result<Json<crate::storage::AiOverview>> {
    require_loopback(peer)?;
    validate_service_ids(&scope.service_ids)?;
    validate_scope_anchor(scope.anchor_service_id.as_deref(), &scope.service_ids)?;
    ai_overview_for_scope(state, scope.anchor_service_id, scope.service_ids, query).await
}

async fn ai_overview_for_scope(
    state: Arc<ServerState>,
    anchor_service_id: Option<String>,
    service_ids: Vec<String>,
    query: AiOverviewQuery,
) -> Result<Json<crate::storage::AiOverview>> {
    if query.to <= query.from || query.step < 1_000 {
        return Err(Error::InvalidRequest(
            "invalid AI overview time range".into(),
        ));
    }
    let duration = query.to - query.from;
    if duration / query.step + u64::from(!duration.is_multiple_of(query.step)) > 2_000 {
        return Err(Error::InvalidRequest(
            "AI overview contains too many time buckets".into(),
        ));
    }
    let nanos = |value: u64, field: &str| {
        value
            .checked_mul(1_000_000)
            .ok_or_else(|| Error::InvalidRequest(format!("AI overview {field} overflows")))
    };
    state
        .store
        .ai_overview(crate::storage::AiOverviewQuery {
            anchor_service_id,
            service_ids,
            from_unix_nano: nanos(query.from, "start")?,
            to_unix_nano: nanos(query.to, "end")?,
            step_nano: nanos(query.step, "step")?,
        })
        .await
        .map(Json)
}

async fn internal_traces(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Query(query): Query<TraceListQuery>,
    Json(scope): Json<AnchoredServiceScopeRequest>,
) -> Result<Json<Vec<crate::storage::TraceSummary>>> {
    require_loopback(peer)?;
    validate_service_ids(&scope.service_ids)?;
    validate_scope_anchor(scope.anchor_service_id.as_deref(), &scope.service_ids)?;
    trace_summaries(state, scope.anchor_service_id, scope.service_ids, query).await
}

async fn trace_summaries(
    state: Arc<ServerState>,
    anchor_service_id: Option<String>,
    service_ids: Vec<String>,
    query: TraceListQuery,
) -> Result<Json<Vec<crate::storage::TraceSummary>>> {
    if query.limit == 0 || query.limit > 200 || query.offset > 10_000 {
        return Err(Error::InvalidRequest("invalid trace page".into()));
    }
    if query.from.zip(query.to).is_some_and(|(from, to)| to < from) {
        return Err(Error::InvalidRequest("invalid trace time range".into()));
    }
    if query
        .query
        .as_ref()
        .is_some_and(|value| value.chars().count() > MAX_SEARCH_QUERY_CHARS)
    {
        return Err(Error::InvalidRequest("trace search is too long".into()));
    }
    let millis_to_nanos = |value: Option<u64>, field: &str| {
        value
            .map(|value| {
                value
                    .checked_mul(1_000_000)
                    .ok_or_else(|| Error::InvalidRequest(format!("trace {field} overflows")))
            })
            .transpose()
    };
    state
        .store
        .trace_summaries(crate::storage::TraceSummaryQuery {
            anchor_service_id,
            service_ids,
            from_unix_nano: millis_to_nanos(query.from, "start")?,
            to_unix_nano: millis_to_nanos(query.to, "end")?,
            search: query.query,
            status: match query.status {
                TraceListStatus::All => crate::storage::TraceSummaryStatus::All,
                TraceListStatus::Error => crate::storage::TraceSummaryStatus::Error,
                TraceListStatus::Ok => crate::storage::TraceSummaryStatus::Ok,
            },
            order: match query.sort {
                TraceListSort::Latest => crate::storage::TraceSummaryOrder::Latest,
                TraceListSort::Slowest => crate::storage::TraceSummaryOrder::Slowest,
                TraceListSort::Spans => crate::storage::TraceSummaryOrder::Spans,
            },
            limit: query.limit,
            offset: query.offset,
        })
        .await
        .map(Json)
}

async fn trace(
    State(state): State<Arc<ServerState>>,
    Path((service_id, trace_id)): Path<(String, String)>,
) -> Result<Json<crate::storage::TraceDetail>> {
    ServiceContext::parse(&service_id)?;
    if trace_id.len() != 32 || !trace_id.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(Error::InvalidRequest("invalid trace identifier".into()));
    }
    state
        .store
        .trace(
            vec![service_id.clone()],
            Some(service_id),
            trace_id.to_ascii_lowercase(),
        )
        .await
        .map(Json)
}

async fn internal_trace(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Path(trace_id): Path<String>,
    Json(scope): Json<AnchoredServiceScopeRequest>,
) -> Result<Json<crate::storage::TraceDetail>> {
    require_loopback(peer)?;
    validate_service_ids(&scope.service_ids)?;
    validate_scope_anchor(scope.anchor_service_id.as_deref(), &scope.service_ids)?;
    if trace_id.len() != 32 || !trace_id.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(Error::InvalidRequest("invalid trace identifier".into()));
    }
    state
        .store
        .trace(
            scope.service_ids,
            scope.anchor_service_id,
            trace_id.to_ascii_lowercase(),
        )
        .await
        .map(Json)
}

fn validate_scope_anchor(anchor: Option<&str>, service_ids: &[String]) -> Result<()> {
    let Some(anchor) = anchor else {
        return Ok(());
    };
    ServiceContext::parse(anchor)?;
    if service_ids.iter().any(|service_id| service_id == anchor) {
        Ok(())
    } else {
        Err(Error::InvalidRequest(
            "anchor is outside the service scope".into(),
        ))
    }
}

async fn metric_catalog(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
) -> Result<Json<Vec<crate::storage::MetricDescriptor>>> {
    ServiceContext::parse(&service_id)?;
    state.store.metric_catalog(vec![service_id]).await.map(Json)
}

async fn metric_sql(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Json(query): Json<MetricSqlRequest>,
) -> Result<Json<Vec<crate::metric_sql::MetricSqlRow>>> {
    ServiceContext::parse(&service_id)?;
    state
        .store
        .metric_sql(vec![service_id], metric_sql_query(query)?)
        .await
        .map(Json)
}

async fn internal_metric_catalog(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(scope): Json<MetricScopeRequest>,
) -> Result<Json<Vec<crate::storage::MetricDescriptor>>> {
    require_loopback(peer)?;
    validate_service_ids(&scope.service_ids)?;
    state
        .store
        .metric_catalog(scope.service_ids)
        .await
        .map(Json)
}

async fn internal_metric_sql(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(request): Json<ScopedMetricSqlRequest>,
) -> Result<Json<Vec<crate::metric_sql::MetricSqlRow>>> {
    require_loopback(peer)?;
    validate_service_ids(&request.service_ids)?;
    state
        .store
        .metric_sql(request.service_ids, metric_sql_query(request.query)?)
        .await
        .map(Json)
}

async fn internal_analytics_ingest(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(event): Json<crate::product_analytics::ProductAnalyticsEvent>,
) -> Result<StatusCode> {
    require_loopback(peer)?;
    if event.tracker_id.is_empty() || event.event_name.is_empty() {
        return Err(Error::InvalidRequest(
            "analytics event is incomplete".into(),
        ));
    }
    state.store.ingest_product_analytics(event).await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn internal_analytics_query(
    State(state): State<Arc<ServerState>>,
    Path(tracker_id): Path<String>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Json(query): Json<crate::product_analytics::ProductAnalyticsQuery>,
) -> Result<Json<Value>> {
    require_loopback(peer)?;
    if tracker_id.is_empty() {
        return Err(Error::InvalidRequest("tracker id is required".into()));
    }
    state
        .store
        .query_product_analytics(tracker_id, query)
        .await
        .map(Json)
}

fn metric_sql_query(query: MetricSqlRequest) -> Result<MetricSqlQuery> {
    let nanos = |value: u64, field: &str| {
        value
            .checked_mul(1_000_000)
            .ok_or_else(|| Error::InvalidRequest(format!("metric {field} overflows")))
    };
    Ok(MetricSqlQuery {
        sql: query.sql,
        from_unix_nano: nanos(query.from, "start")?,
        to_unix_nano: nanos(query.to, "end")?,
        step_nano: nanos(query.step, "step")?,
    })
}

fn validate_service_ids(service_ids: &[String]) -> Result<()> {
    if service_ids.len() > 10_000 {
        return Err(Error::InvalidRequest(
            "telemetry scope contains too many services".into(),
        ));
    }
    for service_id in service_ids {
        ServiceContext::parse(service_id)?;
    }
    Ok(())
}

async fn health() -> Json<Health> {
    Json(Health {
        status: "ok",
        version: env!("CARGO_PKG_VERSION"),
    })
}

async fn internal_disk_usage(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<Json<DiskUsageView>> {
    require_loopback(peer)?;
    Ok(Json(DiskUsageView {
        recording_bytes: state.store.recording_bytes().await?,
    }))
}

async fn delete_internal_service(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<StatusCode> {
    require_loopback(peer)?;
    ServiceContext::parse(&service_id)?;
    state.store.delete_service(service_id).await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn create_internal_backup(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<(StatusCode, Json<BackupView>)> {
    require_loopback(peer)?;
    let (id, _) = state.store.create_backup().await?;
    Ok((StatusCode::CREATED, Json(BackupView { id })))
}

async fn internal_metrics(
    State(state): State<Arc<ServerState>>,
    Query(query): Query<MetricHistoryQuery>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<Json<Vec<Value>>> {
    require_loopback(peer)?;
    let from = query
        .from
        .checked_mul(1_000_000)
        .ok_or_else(|| Error::InvalidRequest("metric query start overflows".into()))?;
    let to = query
        .to
        .checked_mul(1_000_000)
        .ok_or_else(|| Error::InvalidRequest("metric query end overflows".into()))?;
    let scope_ids = query
        .scope_ids
        .map(|value| {
            value
                .split(',')
                .filter(|item| !item.is_empty())
                .map(str::to_owned)
                .collect()
        })
        .unwrap_or_default();
    state
        .store
        .metric_samples(query.scope_kind, scope_ids, from, to)
        .await
        .map(Json)
}

async fn internal_logs(
    State(state): State<Arc<ServerState>>,
    Query(query): Query<LogHistoryQuery>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<Json<LogPage>> {
    require_loopback(peer)?;
    let valid_id = |value: &str| {
        !value.is_empty()
            && value.len() <= 128
            && value
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
    };
    let service_ids = query
        .service_ids
        .split(',')
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .collect::<Vec<_>>();
    if service_ids.is_empty()
        || service_ids.len() > 10_000
        || service_ids.iter().any(|value| !valid_id(value))
        || query
            .deployment_id
            .as_deref()
            .is_some_and(|value| !valid_id(value))
        || query
            .contains
            .as_deref()
            .is_some_and(|value| value.len() > 256 || value.contains('\0'))
        || query
            .severity_text
            .as_deref()
            .is_some_and(|value| value.len() > 64 || value.contains('\0'))
        || query.trace_id.as_deref().is_some_and(|value| {
            value.len() != 32 || !value.bytes().all(|byte| byte.is_ascii_hexdigit())
        })
        || query.span_id.as_deref().is_some_and(|value| {
            value.len() != 16 || !value.bytes().all(|byte| byte.is_ascii_hexdigit())
        })
    {
        return Err(Error::InvalidRequest("invalid telemetry log query".into()));
    }
    let field_filters = parse_log_field_filters(query.field_filters.as_deref())?;
    let limit = query.limit.unwrap_or(500);
    if !(1..=2000).contains(&limit) {
        return Err(Error::InvalidRequest("invalid telemetry log limit".into()));
    }
    let ascending = match query.order.as_deref() {
        None | Some("desc") => false,
        Some("asc") => true,
        Some(_) => return Err(Error::InvalidRequest("invalid telemetry log order".into())),
    };
    let millis_to_nanos = |value: Option<u64>, name: &str| {
        value
            .map(|value| {
                value
                    .checked_mul(1_000_000)
                    .ok_or_else(|| Error::InvalidRequest(format!("log query {name} overflows")))
            })
            .transpose()
    };
    let from = millis_to_nanos(query.from, "start")?;
    let to = millis_to_nanos(query.to, "end")?;
    if from.zip(to).is_some_and(|(from, to)| to < from)
        || query.after_time_unix_nano.is_some() != query.after_id.is_some()
        || query
            .after_id
            .as_deref()
            .is_some_and(|id| uuid::Uuid::parse_str(id).is_err())
    {
        return Err(Error::InvalidRequest("invalid telemetry log cursor".into()));
    }
    state
        .store
        .log_records(LogQuery {
            service_ids,
            deployment_id: query.deployment_id,
            contains: query.contains.filter(|value| !value.is_empty()),
            field_filters,
            severity_text: query.severity_text.filter(|value| !value.is_empty()),
            trace_id: query.trace_id.map(|value| value.to_ascii_lowercase()),
            span_id: query.span_id.map(|value| value.to_ascii_lowercase()),
            from_unix_nano: from,
            to_unix_nano: to,
            after_time_unix_nano: query.after_time_unix_nano,
            after_id: query.after_id,
            limit,
            ascending,
        })
        .await
        .map(Json)
}

fn parse_log_field_filters(value: Option<&str>) -> Result<Vec<LogFieldFilter>> {
    let Some(value) = value.filter(|value| !value.is_empty()) else {
        return Ok(Vec::new());
    };
    if value.len() > 8 << 10 {
        return Err(Error::InvalidRequest(
            "telemetry log field filters are too large".into(),
        ));
    }
    let filters: Vec<LogFieldFilter> = serde_json::from_str(value)
        .map_err(|_| Error::InvalidRequest("invalid telemetry log field filters".into()))?;
    if filters.len() > 8 || filters.iter().any(|filter| !valid_log_field_filter(filter)) {
        return Err(Error::InvalidRequest(
            "invalid telemetry log field filters".into(),
        ));
    }
    Ok(filters)
}

fn valid_log_field_filter(filter: &LogFieldFilter) -> bool {
    let valid_path = filter.path.len() <= 256
        && filter.path.split('.').all(|segment| {
            let mut bytes = segment.bytes();
            bytes
                .next()
                .is_some_and(|byte| byte.is_ascii_alphabetic() || byte == b'_')
                && bytes.all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
        });
    valid_path
        && filter.value.len() <= 512
        && !filter.value.contains('\0')
        && match filter.operator {
            LogFieldOperator::Equals => true,
            LogFieldOperator::Contains => !filter.value.is_empty(),
            LogFieldOperator::Exists => filter.value.is_empty(),
        }
}

async fn download_internal_backup(
    State(state): State<Arc<ServerState>>,
    Path(backup_id): Path<String>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<Response> {
    require_loopback(peer)?;
    let path = state.store.backup_path(&backup_id)?;
    let file = tokio::fs::File::open(&path).await.map_err(|error| {
        if error.kind() == std::io::ErrorKind::NotFound {
            Error::NotFound
        } else {
            Error::Storage(format!("open telemetry backup: {error}"))
        }
    })?;
    let size = file
        .metadata()
        .await
        .map_err(|error| Error::Storage(format!("inspect telemetry backup: {error}")))?
        .len();
    Response::builder()
        .status(StatusCode::OK)
        .header(header::CONTENT_TYPE, "application/x-tar")
        .header(header::CONTENT_LENGTH, size)
        .header(
            header::CONTENT_DISPOSITION,
            format!("attachment; filename=telemetry-{backup_id}.tar"),
        )
        .body(Body::from_stream(ReaderStream::new(file)))
        .map_err(|error| Error::Storage(format!("build telemetry backup response: {error}")))
}

async fn delete_internal_backup(
    State(state): State<Arc<ServerState>>,
    Path(backup_id): Path<String>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
) -> Result<StatusCode> {
    require_loopback(peer)?;
    state.store.remove_backup(&backup_id).await?;
    Ok(StatusCode::NO_CONTENT)
}

fn require_loopback(peer: Option<Extension<ConnectInfo<SocketAddr>>>) -> Result<()> {
    match peer {
        Some(Extension(ConnectInfo(address))) if address.ip().is_loopback() => Ok(()),
        _ => Err(Error::Forbidden),
    }
}

async fn envelope(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    body: Bytes,
) -> Result<impl IntoResponse> {
    let body = decode_body(&headers, body)?;
    let envelope = parse(&body)?;
    drop(body);
    if envelope.items.is_empty() {
        return Err(Error::InvalidRequest("envelope contains no items".into()));
    }
    let public_key = sentry_public_key(&headers, query.as_deref())
        .or_else(|| envelope_dsn_key(&envelope, &project_id))
        .ok_or(Error::Authentication)?;
    let service = sentry_service(&headers, &project_id, &public_key)?;
    let result = ingest(
        &state,
        &service,
        prepare(
            &service,
            envelope,
            client_ip(&headers, peer_address(peer)),
            request_geo(&headers, &state.geoip),
            &state.geoip,
        )?,
    )
    .await?;
    ingest_response(result)
}

async fn store(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    body: Bytes,
) -> Result<impl IntoResponse> {
    let body = decode_body(&headers, body)?;
    let payload: Value = serde_json::from_slice(&body)
        .map_err(|error| Error::InvalidRequest(format!("invalid event JSON: {error}")))?;
    if !payload.is_object() {
        return Err(Error::InvalidRequest(
            "event payload must be an object".into(),
        ));
    }
    let public_key = sentry_public_key(&headers, query.as_deref()).ok_or(Error::Authentication)?;
    let service = sentry_service(&headers, &project_id, &public_key)?;
    let mut envelope_headers = Map::new();
    if let Some(event_id) = payload.get("event_id").and_then(Value::as_str) {
        envelope_headers.insert("event_id".into(), Value::String(event_id.into()));
    }
    let mut item_headers = Map::new();
    item_headers.insert("type".into(), Value::String("event".into()));
    item_headers.insert("length".into(), Value::from(body.len()));
    let prepared = prepare(
        &service,
        Envelope {
            headers: envelope_headers,
            items: vec![Item {
                headers: item_headers,
                item_type: "event".into(),
                payload: body,
            }],
        },
        client_ip(&headers, peer_address(peer)),
        request_geo(&headers, &state.geoip),
        &state.geoip,
    )?;
    ingest_response(ingest(&state, &service, prepared).await?)
}

async fn minidump(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    multipart: Multipart,
) -> Result<impl IntoResponse> {
    ingest_crash_file(
        state,
        project_id,
        query,
        headers,
        peer_address(peer),
        multipart,
        CrashFileKind::Minidump,
    )
    .await
}

async fn apple_crash_report(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    headers: HeaderMap,
    multipart: Multipart,
) -> Result<impl IntoResponse> {
    ingest_crash_file(
        state,
        project_id,
        query,
        headers,
        peer_address(peer),
        multipart,
        CrashFileKind::AppleCrashReport,
    )
    .await
}

async fn ingest_crash_file(
    state: Arc<ServerState>,
    project_id: String,
    query: Option<String>,
    headers: HeaderMap,
    peer: Option<SocketAddr>,
    mut multipart: Multipart,
    kind: CrashFileKind,
) -> Result<impl IntoResponse> {
    let public_key = sentry_public_key(&headers, query.as_deref()).ok_or(Error::Authentication)?;
    let service = sentry_service(&headers, &project_id, &public_key)?;
    let field_name = match kind {
        CrashFileKind::Minidump => "upload_file_minidump",
        CrashFileKind::AppleCrashReport => "apple_crash_report",
    };
    let attachment_type = match kind {
        CrashFileKind::Minidump => "event.minidump",
        CrashFileKind::AppleCrashReport => "event.applecrashreport",
    };
    let mut crash_file = None;
    let mut extra = None;
    let mut attachment_filename = None;
    while let Some(field) = multipart
        .next_field()
        .await
        .map_err(|error| Error::InvalidRequest(format!("decode crash multipart: {error}")))?
    {
        let name = field.name().map(str::to_owned);
        let filename = field.file_name().map(str::to_owned);
        let bytes = field
            .bytes()
            .await
            .map_err(|error| Error::InvalidRequest(format!("read crash multipart: {error}")))?;
        if name.as_deref() == Some(field_name) {
            attachment_filename = filename;
            crash_file = Some(bytes.to_vec());
        } else if name.as_deref() == Some("sentry") {
            extra = serde_json::from_slice(&bytes).ok();
        }
    }
    let crash_file = crash_file.ok_or_else(|| {
        Error::InvalidRequest(format!("multipart field {field_name} is required"))
    })?;
    let event_id = uuid::Uuid::new_v4().simple().to_string();
    let decoded = match state
        .symbolicator
        .process_crash_file(&service, kind, crash_file.clone())
        .await
    {
        Ok(decoded) => decoded,
        Err(error) => {
            tracing::warn!(%error, %event_id, "crash processing failed; storing raw file");
            json!({
                "status": "failed",
                "crash_reason": "Crash processing failed",
                "crash_details": error.to_string(),
                "stacktraces": [],
                "modules": []
            })
        }
    };
    let event = crash_event(&event_id, decoded, extra, kind);
    let event_bytes = serde_json::to_vec(&event)
        .map_err(|error| Error::Storage(format!("encode decoded crash event: {error}")))?;
    let mut envelope_headers = Map::new();
    envelope_headers.insert("event_id".into(), Value::String(event_id.clone()));
    let mut event_headers = Map::new();
    event_headers.insert("type".into(), Value::String("event".into()));
    event_headers.insert("length".into(), Value::from(event_bytes.len()));
    let mut attachment_headers = Map::new();
    attachment_headers.insert("type".into(), Value::String("attachment".into()));
    attachment_headers.insert("length".into(), Value::from(crash_file.len()));
    attachment_headers.insert(
        "attachment_type".into(),
        Value::String(attachment_type.into()),
    );
    attachment_headers.insert(
        "filename".into(),
        Value::String(attachment_filename.unwrap_or_else(|| field_name.into())),
    );
    let prepared = prepare(
        &service,
        Envelope {
            headers: envelope_headers,
            items: vec![
                Item {
                    headers: event_headers,
                    item_type: "event".into(),
                    payload: event_bytes,
                },
                Item {
                    headers: attachment_headers,
                    item_type: "attachment".into(),
                    payload: crash_file,
                },
            ],
        },
        client_ip(&headers, peer),
        request_geo(&headers, &state.geoip),
        &state.geoip,
    )?;
    ingest_response(ingest(&state, &service, prepared).await?)
}

async fn chunk_upload_options(
    State(_state): State<Arc<ServerState>>,
    Path(org): Path<String>,
    headers: HeaderMap,
) -> Result<Json<Value>> {
    let _ = artifact_service(&headers, &org, None)?;
    Ok(Json(json!({
        "url": format!("/api/0/organizations/{SENTRY_ORGANIZATION}/chunk-upload/"),
        "chunksPerRequest": MAX_CHUNKS_PER_REQUEST,
        "maxRequestSize": 64 << 20,
        "maxFileSize": artifact::MAX_FILE_BYTES,
        "maxWait": 0,
        "hashAlgorithm": "sha1",
        "chunkSize": artifact::CHUNK_BYTES,
        "concurrency": 4,
        "compression": ["gzip"]
    })))
}

async fn upload_chunks(
    State(state): State<Arc<ServerState>>,
    Path(org): Path<String>,
    headers: HeaderMap,
    mut multipart: Multipart,
) -> Result<StatusCode> {
    let service = artifact_service(&headers, &org, None)?;
    let mut count = 0usize;
    while let Some(field) = multipart
        .next_field()
        .await
        .map_err(|error| Error::InvalidRequest(format!("decode chunk multipart: {error}")))?
    {
        let compressed = field.name() == Some("file_gzip");
        if field.name() != Some("file") && !compressed {
            continue;
        }
        if count == MAX_CHUNKS_PER_REQUEST {
            return Err(Error::InvalidRequest(format!(
                "chunk upload contains more than {MAX_CHUNKS_PER_REQUEST} files"
            )));
        }
        let checksum = field
            .file_name()
            .filter(|value| artifact::valid_sha1(value))
            .map(str::to_ascii_lowercase)
            .ok_or_else(|| Error::InvalidRequest("chunk filename must be its SHA1".into()))?;
        let bytes = field
            .bytes()
            .await
            .map_err(|error| Error::InvalidRequest(format!("read uploaded chunk: {error}")))?;
        let bytes = if compressed {
            read_artifact_chunk(flate2::read::GzDecoder::new(bytes.as_ref()))?
        } else {
            bytes.to_vec()
        };
        artifact::store_upload_chunk(&state.store, &service, &checksum, &bytes).await?;
        count += 1;
    }
    if count == 0 {
        return Err(Error::InvalidRequest("multipart contains no chunks".into()));
    }
    Ok(StatusCode::CREATED)
}

#[derive(Deserialize)]
struct ArtifactBundleRequest {
    checksum: String,
    chunks: Vec<String>,
    #[serde(default)]
    projects: Vec<String>,
    version: Option<String>,
    dist: Option<String>,
}

async fn assemble_artifact_bundle(
    State(state): State<Arc<ServerState>>,
    Path(org): Path<String>,
    headers: HeaderMap,
    Json(input): Json<ArtifactBundleRequest>,
) -> Result<Json<Value>> {
    let service = artifact_service(&headers, &org, None)?;
    if !input.projects.is_empty() && !input.projects.iter().any(|project| project == &service.id) {
        return Err(Error::NotFound);
    }
    let missing = artifact::missing_upload_chunks(&state.store, &service.id, &input.chunks).await?;
    if !missing.is_empty() {
        return Ok(Json(json!({
            "state": "not_found",
            "missingChunks": missing,
            "detail": null
        })));
    }
    let assembled = artifact::assemble(
        &state.store,
        &service,
        &ArtifactMetadata {
            checksum: input.checksum,
            kind: "artifact_bundle".into(),
            name: "artifact-bundle.zip".into(),
            debug_id: None,
            code_id: None,
            symbol_type: "sourcebundle".into(),
            release: input.version,
            dist: input.dist,
        },
        &input.chunks,
    )
    .await?;
    if assembled {
        state.symbolication.enqueue_service(&service).await;
    }
    Ok(Json(
        json!({ "state": "ok", "missingChunks": [], "detail": null }),
    ))
}

#[derive(Deserialize)]
struct DifRequest {
    name: String,
    #[serde(rename = "debug_id")]
    debug_id: Option<String>,
    chunks: Vec<String>,
}

async fn assemble_difs(
    State(state): State<Arc<ServerState>>,
    Path((org, project)): Path<(String, String)>,
    headers: HeaderMap,
    Json(input): Json<std::collections::HashMap<String, DifRequest>>,
) -> Result<Json<Value>> {
    let service = artifact_service(&headers, &org, Some(&project))?;
    let mut response = serde_json::Map::new();
    let mut assembled_any = false;
    for (checksum, request) in input {
        let missing =
            artifact::missing_upload_chunks(&state.store, &service.id, &request.chunks).await?;
        if !missing.is_empty() {
            response.insert(
                checksum,
                json!({ "state": "not_found", "missingChunks": missing, "detail": null, "dif": null }),
            );
            continue;
        }
        let symbol_type = symbol_type(&request.name);
        let debug_id = request
            .debug_id
            .map(|value| value.to_ascii_lowercase())
            .or_else(|| proguard_debug_id_from_name(&request.name));
        assembled_any |= artifact::assemble(
            &state.store,
            &service,
            &ArtifactMetadata {
                checksum: checksum.clone(),
                kind: "debug_file".into(),
                name: request.name.clone(),
                debug_id: debug_id.clone(),
                code_id: None,
                symbol_type: symbol_type.into(),
                release: None,
                dist: None,
            },
            &request.chunks,
        )
        .await?;
        response.insert(
            checksum.clone(),
            json!({
                "state": "ok",
                "missingChunks": [],
                "detail": null,
                "dif": {
                    "id": checksum,
                    "uuid": debug_id,
                    "debugId": debug_id,
                    "objectName": request.name,
                    "cpuName": "unknown",
                    "sha1": checksum,
                    "data": { "features": [] }
                }
            }),
        );
    }
    if assembled_any {
        state.symbolication.enqueue_service(&service).await;
    }
    Ok(Json(Value::Object(response)))
}

async fn ingest(
    state: &ServerState,
    service: &ServiceContext,
    prepared: PreparedIngest,
) -> Result<IngestResult> {
    // Keep the existence checks and commits in one critical section so event IDs
    // and first-issue notifications remain idempotent.
    let ingest_guard = state.ingest_lock.lock().await;
    let PreparedIngest {
        mut documents,
        mut events,
        response_event_id,
    } = prepared;
    let mut event_ids = HashSet::new();
    let mut duplicate_event_ids = HashSet::new();
    for event in &events {
        if !event_ids.insert(event.event_id.clone()) {
            return Err(Error::InvalidRequest(
                "envelope contains the same event ID more than once".into(),
            ));
        }
    }
    if !event_ids.is_empty() {
        let existing = state
            .store
            .search(SearchRequest {
                query: all([
                    term("doc_kind", "event"),
                    term("service_id", &service.id),
                    any(event_ids.iter().map(|event_id| term("event_id", event_id))),
                ]),
                max_hits: event_ids.len(),
                start_offset: None,
                sort_by: String::new(),
            })
            .await?;
        duplicate_event_ids.extend(existing.hits.into_iter().filter_map(|document| {
            document
                .get("event_id")
                .and_then(Value::as_str)
                .map(str::to_owned)
        }));
    }
    if !events.is_empty() && duplicate_event_ids.len() == events.len() {
        return Ok(IngestResult {
            event_id: response_event_id,
            notifications: Vec::new(),
        });
    }
    if !duplicate_event_ids.is_empty() {
        let duplicate_content_ids = documents
            .iter()
            .filter(|document| {
                document.doc_kind == "event"
                    && document
                        .event_id
                        .as_ref()
                        .is_some_and(|event_id| duplicate_event_ids.contains(event_id))
            })
            .filter_map(|document| document.content_id.clone())
            .collect::<HashSet<_>>();
        documents.retain(|document| {
            let duplicate_event = document.doc_kind == "event"
                && document
                    .event_id
                    .as_ref()
                    .is_some_and(|event_id| duplicate_event_ids.contains(event_id));
            let duplicate_content = document.doc_kind == "content_chunk"
                && document
                    .content_id
                    .as_ref()
                    .is_some_and(|content_id| duplicate_content_ids.contains(content_id));
            !duplicate_event && !duplicate_content
        });
        events.retain(|event| !duplicate_event_ids.contains(&event.event_id));
    }
    let mut new_issues = HashSet::new();
    let mut regressed_issues = HashSet::new();
    let mut checked_issues = HashSet::new();
    for event in &events {
        if !checked_issues.insert(event.issue_id.clone()) {
            continue;
        }
        let current = load_issue_document(state, &service.id, &event.issue_id).await?;
        let status = current
            .as_ref()
            .and_then(|document| document.status.as_deref())
            .unwrap_or("open");
        let (status, is_regressed) = issue_status_after_event(status);
        if is_regressed {
            regressed_issues.insert(event.issue_id.clone());
        }
        if current.is_none() {
            new_issues.insert(event.issue_id.clone());
        }
        let occurrences = events
            .iter()
            .filter(|candidate| candidate.issue_id == event.issue_id)
            .count() as u64;
        let latest = events
            .iter()
            .filter(|candidate| candidate.issue_id == event.issue_id)
            .max_by(|left, right| compare_timestamps(&left.timestamp, &right.timestamp))
            .unwrap_or(event);
        let first_seen = events
            .iter()
            .filter(|candidate| candidate.issue_id == event.issue_id)
            .map(|candidate| candidate.timestamp.as_str())
            .min_by(|left, right| compare_timestamps(left, right))
            .unwrap_or(&event.timestamp);
        documents.push(issue_document(
            service,
            latest,
            first_seen,
            current.as_ref(),
            occurrences,
            status,
        )?);
    }
    let mut trace_markers = Vec::new();
    for event in &events {
        if let Some(marker) = crate::telemetry::sentry_error_trace_row(&service.id, event)? {
            trace_markers.push(marker);
        }
    }
    // Persist replaceable error markers first. If the document commit fails, an SDK
    // retry can safely upsert the marker; committing documents first could make the
    // duplicate-event fast path permanently skip it after a partial failure.
    state
        .store
        .ingest_signal_rows(crate::storage::SignalTable::Spans, trace_markers)
        .await?;
    state.store.ingest(documents).await?;
    drop(ingest_guard);
    let mut notifications = Vec::new();
    for event in events {
        let is_new_issue = new_issues.remove(&event.issue_id);
        let is_regressed = regressed_issues.remove(&event.issue_id);
        push_webhook_notification(&mut notifications, "event_received", &event);
        if is_new_issue {
            push_webhook_notification(&mut notifications, "issue_created", &event);
        }
        if is_regressed {
            push_webhook_notification(&mut notifications, "issue_regressed", &event);
        }
        state.symbolication.enqueue(service, event);
    }
    Ok(IngestResult {
        event_id: response_event_id,
        notifications,
    })
}

fn push_webhook_notification(
    notifications: &mut Vec<WebhookNotification>,
    event_type: &'static str,
    event: &IngestedEvent,
) {
    if notifications.len() == MAX_WEBHOOK_NOTIFICATIONS {
        tracing::warn!(
            limit = MAX_WEBHOOK_NOTIFICATIONS,
            "telemetry webhook notification limit reached"
        );
        return;
    }
    notifications.push(WebhookNotification {
        event_type,
        timestamp: event.timestamp.clone(),
        issue: WebhookIssue {
            id: event.issue_id.clone(),
            title: event.title.clone(),
            level: event.level.clone(),
            platform: event.platform.clone(),
        },
        event: WebhookOccurrence {
            id: event.event_id.clone(),
            timestamp: event.timestamp.clone(),
        },
    });
}

fn ingest_response(result: IngestResult) -> Result<Response> {
    let mut response = (StatusCode::OK, Json(json!({ "id": result.event_id }))).into_response();
    attach_webhook_notifications(&mut response, &result.notifications)?;
    Ok(response)
}

fn attach_webhook_notifications(
    response: &mut Response,
    notifications: &[WebhookNotification],
) -> Result<()> {
    if notifications.is_empty() {
        return Ok(());
    }
    let encoded = serde_json::to_vec(notifications)
        .map(|value| BASE64_URL.encode(value))
        .map_err(|error| Error::Storage(format!("encode webhook notifications: {error}")))?;
    response.headers_mut().insert(
        WEBHOOK_EVENTS_HEADER,
        encoded.parse().map_err(|error| {
            Error::Storage(format!("build webhook notification header: {error}"))
        })?,
    );
    Ok(())
}

fn issue_status_after_event(status: &str) -> (&str, bool) {
    if status == "resolved" {
        ("open", true)
    } else {
        (status, false)
    }
}

fn issue_document(
    service: &ServiceContext,
    event: &IngestedEvent,
    incoming_first_seen: &str,
    current: Option<&Document>,
    occurrences: u64,
    status: &str,
) -> Result<Document> {
    let now = Timestamp::now().to_string();
    let current_is_newer = current.is_some_and(|document| {
        compare_timestamps(&document.timestamp, &event.timestamp) == Ordering::Greater
    });
    let timestamp = current
        .filter(|_| current_is_newer)
        .map_or(event.timestamp.as_str(), |document| &document.timestamp);
    let mut document = Document::base("issue", &service.id, timestamp, &now);
    document.event_id = current
        .filter(|_| current_is_newer)
        .and_then(|document| document.event_id.clone())
        .or_else(|| Some(event.event_id.clone()));
    document.issue_id = Some(event.issue_id.clone());
    document.title = current
        .filter(|_| current_is_newer)
        .and_then(|document| document.title.clone())
        .or_else(|| Some(event.title.clone()));
    document.level = current
        .filter(|_| current_is_newer)
        .and_then(|document| document.level.clone())
        .or_else(|| Some(event.level.clone()));
    document.platform = current
        .filter(|_| current_is_newer)
        .and_then(|document| document.platform.clone())
        .or_else(|| Some(event.platform.clone()));
    document.status = Some(status.into());
    let previous_first_seen = current.map(|document| {
        document
            .first_seen
            .as_deref()
            .unwrap_or(&document.timestamp)
    });
    document.first_seen = Some(
        previous_first_seen
            .map_or(incoming_first_seen, |value| {
                if compare_timestamps(value, incoming_first_seen) == Ordering::Greater {
                    incoming_first_seen
                } else {
                    value
                }
            })
            .to_owned(),
    );
    document.event_count = Some(
        current
            .and_then(|document| document.event_count)
            .unwrap_or_default()
            .checked_add(occurrences)
            .ok_or_else(|| Error::Storage("issue event count overflow".into()))?,
    );
    document.revision = Some(next_issue_revision(
        current.and_then(|document| document.revision),
    )?);
    Ok(document)
}

fn next_issue_revision(current: Option<u64>) -> Result<u64> {
    current
        .unwrap_or_default()
        .checked_add(1)
        .ok_or_else(|| Error::Storage("issue revision overflow".into()))
}

fn compare_timestamps(left: &str, right: &str) -> Ordering {
    match (left.parse::<Timestamp>(), right.parse::<Timestamp>()) {
        (Ok(left), Ok(right)) => left.cmp(&right),
        _ => left.cmp(right),
    }
}

async fn load_issue_document(
    state: &ServerState,
    service_id: &str,
    issue_id: &str,
) -> Result<Option<Document>> {
    let response = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "issue"),
                term("service_id", service_id),
                term("issue_id", issue_id),
            ]),
            max_hits: 1,
            start_offset: None,
            sort_by: "timestamp".into(),
        })
        .await?;
    response
        .hits
        .into_iter()
        .next()
        .map(|value| {
            serde_json::from_value(value)
                .map_err(|error| Error::Storage(format!("decode issue document: {error}")))
        })
        .transpose()
}

pub(crate) async fn events(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<ListQuery>,
) -> Result<Json<ListResponse>> {
    ServiceContext::parse(&service_id)?;
    search_documents(&state, &service_id, "event", query).await
}

pub(crate) async fn event(
    State(state): State<Arc<ServerState>>,
    Path((service_id, event_id)): Path<(String, String)>,
) -> Result<Json<Value>> {
    ServiceContext::parse(&service_id)?;
    let event_id = event_id.replace('-', "").to_ascii_lowercase();
    let response = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "event"),
                term("service_id", &service_id),
                term("event_id", &event_id),
            ]),
            max_hits: 1,
            start_offset: None,
            sort_by: "timestamp".into(),
        })
        .await?;
    let event = response.hits.into_iter().next().ok_or(Error::NotFound)?;
    let symbolication = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "symbolication"),
                term("service_id", &service_id),
                term("event_id", &event_id),
            ]),
            max_hits: 1,
            start_offset: None,
            sort_by: "received_at".into(),
        })
        .await?
        .hits
        .into_iter()
        .next();
    Ok(Json(
        json!({ "event": event, "symbolication": symbolication }),
    ))
}

pub(crate) async fn replays(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<ListQuery>,
) -> Result<Json<ListResponse>> {
    ServiceContext::parse(&service_id)?;
    search_documents(&state, &service_id, "replay", query).await
}

pub(crate) async fn replay(
    State(state): State<Arc<ServerState>>,
    Path((service_id, replay_id)): Path<(String, String)>,
) -> Result<Json<Value>> {
    ServiceContext::parse(&service_id)?;
    let replay_id = replay_id.replace('-', "").to_ascii_lowercase();
    let mut hits = state
        .store
        .search_all(
            all([
                any([
                    term("doc_kind", "replay_event"),
                    term("doc_kind", "replay_recording"),
                    term("doc_kind", "replay_video"),
                ]),
                term("service_id", &service_id),
                term("replay_id", &replay_id),
            ]),
            "timestamp",
        )
        .await?;
    hits.sort_by_key(|hit| {
        (
            hit.get("segment_id")
                .and_then(Value::as_u64)
                .unwrap_or_default(),
            hit.get("sequence")
                .and_then(Value::as_u64)
                .unwrap_or_default(),
        )
    });
    if hits.is_empty() {
        return Err(Error::NotFound);
    }
    let total = hits.len();
    Ok(Json(json!({ "items": hits, "total": total })))
}

pub(crate) async fn replay_recording(
    State(state): State<Arc<ServerState>>,
    Path((service_id, replay_id)): Path<(String, String)>,
    Query(query): Query<ReplayRecordingQuery>,
) -> Result<Json<Value>> {
    ServiceContext::parse(&service_id)?;
    if query.offset > 1_000_000
        || query
            .limit
            .is_some_and(|limit| !(1..=2_000).contains(&limit))
    {
        return Err(Error::InvalidRequest(
            "invalid replay recording page".into(),
        ));
    }
    let replay_id = replay_id.replace('-', "").to_ascii_lowercase();
    let mut recordings = state
        .store
        .search_all(
            all([
                any([
                    term("doc_kind", "replay_recording"),
                    term("doc_kind", "replay_video"),
                ]),
                term("service_id", &service_id),
                term("replay_id", &replay_id),
            ]),
            "timestamp",
        )
        .await?;
    recordings.sort_by_key(|recording| {
        (
            recording
                .get("segment_id")
                .and_then(Value::as_u64)
                .unwrap_or_default(),
            recording
                .get("sequence")
                .and_then(Value::as_u64)
                .unwrap_or_default(),
        )
    });
    if recordings.is_empty() {
        return Err(Error::NotFound);
    }

    let replay_events = state
        .store
        .search_all(
            all([
                any([
                    term("doc_kind", "replay_event"),
                    term("doc_kind", "replay_video"),
                ]),
                term("service_id", &service_id),
                term("replay_id", &replay_id),
            ]),
            "timestamp",
        )
        .await?;
    let (started_at, finished_at) = replay_time_bounds(&replay_events);
    let mut trace_ids = replay_trace_ids(&replay_events)
        .into_iter()
        .collect::<HashSet<_>>();
    let millis_to_nanos = |value: Option<f64>| {
        value
            .filter(|value| value.is_finite() && *value >= 0.0)
            .map(|value| (value * 1_000_000.0).round() as u64)
    };
    let padding = 1_000_000_000;
    let indexed_trace_ids = state
        .store
        .replay_trace_ids(
            service_id.clone(),
            replay_id.clone(),
            millis_to_nanos(started_at).map(|value| value.saturating_sub(padding)),
            millis_to_nanos(finished_at).map(|value| value.saturating_add(padding)),
        )
        .await?;
    trace_ids.extend(indexed_trace_ids);
    let mut trace_ids = trace_ids.into_iter().collect::<Vec<_>>();
    trace_ids.sort_unstable();

    let segment_count = recordings.len();
    let mut decoded_bytes = 0;
    let mut events = Vec::new();
    let mut warnings = Vec::new();
    let mut truncated = false;
    for (index, recording) in recordings.into_iter().enumerate() {
        let Some(content_id) = recording.get("content_id").and_then(Value::as_str) else {
            warnings.push(format!("Segment {} has no recording content", index + 1));
            continue;
        };
        let content = match load_item_content(&state, &service_id, content_id).await {
            Ok(content) => content,
            Err(error) => {
                warnings.push(format!(
                    "Segment {} could not be loaded: {error}",
                    index + 1
                ));
                continue;
            }
        };
        let remaining = MAX_REPLAY_PLAYER_BYTES.saturating_sub(decoded_bytes);
        if remaining == 0 {
            truncated = true;
            break;
        }
        let recording_content =
            if recording.get("doc_kind").and_then(Value::as_str) == Some("replay_video") {
                match crate::replay_video::decode(&content) {
                    Ok(video) => video.recording,
                    Err(error) => {
                        warnings.push(format!("Segment {} is corrupt: {error}", index + 1));
                        continue;
                    }
                }
            } else {
                content
            };
        let (segment_events, segment_bytes) =
            match decode_replay_recording(&recording_content, remaining) {
                Ok(decoded) => decoded,
                Err(error) if error.to_string().contains("player limit") => {
                    truncated = true;
                    break;
                }
                Err(error) => {
                    warnings.push(format!("Segment {} is corrupt: {error}", index + 1));
                    continue;
                }
            };
        decoded_bytes += segment_bytes;
        events.extend(segment_events);
    }

    let error_events = state
        .store
        .search_all(
            all([
                term("doc_kind", "event"),
                term("service_id", &service_id),
                term("replay_id", &replay_id),
            ]),
            "timestamp",
        )
        .await?
        .into_iter()
        .map(event_summary)
        .collect::<Vec<_>>();

    let total_event_count = events.len();
    let events = events
        .into_iter()
        .skip(query.offset)
        .take(query.limit.unwrap_or(total_event_count))
        .collect::<Vec<_>>();
    Ok(Json(json!({
        "errorEvents": error_events,
        "events": events,
        "finishedAt": finished_at,
        "replayId": replay_id,
        "segmentCount": segment_count,
        "startedAt": started_at,
        "traceIds": trace_ids,
        "truncated": truncated,
        "totalEventCount": total_event_count,
        "warnings": warnings,
    })))
}

async fn replay_video(
    State(state): State<Arc<ServerState>>,
    Path((service_id, replay_id, segment_id)): Path<(String, String, u64)>,
    headers: HeaderMap,
) -> Result<Response> {
    ServiceContext::parse(&service_id)?;
    let replay_id = replay_id.replace('-', "").to_ascii_lowercase();
    let recordings = state
        .store
        .search_all(
            all([
                term("doc_kind", "replay_video"),
                term("service_id", &service_id),
                term("replay_id", &replay_id),
            ]),
            "timestamp",
        )
        .await?;
    let recording = recordings
        .into_iter()
        .find(|recording| recording.get("segment_id").and_then(Value::as_u64) == Some(segment_id))
        .ok_or(Error::NotFound)?;
    let content_id = recording
        .get("content_id")
        .and_then(Value::as_str)
        .ok_or_else(|| Error::Storage("replay video has no content".into()))?;
    let content = load_item_content(&state, &service_id, content_id).await?;
    let video = crate::replay_video::decode(&content)?.video;
    let range = headers
        .get(header::RANGE)
        .and_then(|value| value.to_str().ok());
    let selected = parse_byte_range(range, video.len());
    let (status, start, end) = match selected {
        Ok(Some((start, end))) => (StatusCode::PARTIAL_CONTENT, start, end),
        Ok(None) => (StatusCode::OK, 0, video.len().saturating_sub(1)),
        Err(()) => {
            return Response::builder()
                .status(StatusCode::RANGE_NOT_SATISFIABLE)
                .header(header::CONTENT_RANGE, format!("bytes */{}", video.len()))
                .body(Body::empty())
                .map_err(|error| Error::Storage(format!("build replay video response: {error}")));
        }
    };
    let body = if video.is_empty() {
        Vec::new()
    } else {
        video[start..=end].to_vec()
    };
    let mut response = Response::builder()
        .status(status)
        .header(header::ACCEPT_RANGES, "bytes")
        .header(header::CONTENT_TYPE, "video/mp4")
        .header(header::CONTENT_LENGTH, body.len());
    if status == StatusCode::PARTIAL_CONTENT {
        response = response.header(
            header::CONTENT_RANGE,
            format!("bytes {start}-{end}/{}", video.len()),
        );
    }
    response
        .body(Body::from(body))
        .map_err(|error| Error::Storage(format!("build replay video response: {error}")))
}

fn parse_byte_range(
    range: Option<&str>,
    size: usize,
) -> std::result::Result<Option<(usize, usize)>, ()> {
    let Some(range) = range else {
        return Ok(None);
    };
    if size == 0 || !range.starts_with("bytes=") || range.contains(',') {
        return Err(());
    }
    let (start, end) = range[6..].split_once('-').ok_or(())?;
    if start.is_empty() {
        let suffix = end.parse::<usize>().map_err(|_| ())?;
        if suffix == 0 {
            return Err(());
        }
        return Ok(Some((size.saturating_sub(suffix), size - 1)));
    }
    let start = start.parse::<usize>().map_err(|_| ())?;
    if start >= size {
        return Err(());
    }
    let end = if end.is_empty() {
        size - 1
    } else {
        end.parse::<usize>().map_err(|_| ())?.min(size - 1)
    };
    if end < start {
        return Err(());
    }
    Ok(Some((start, end)))
}

fn replay_trace_ids(events: &[Value]) -> Vec<String> {
    fn insert(value: &str, trace_ids: &mut HashSet<String>) {
        let value = value.replace('-', "").to_ascii_lowercase();
        if value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
            trace_ids.insert(value);
        }
    }

    fn collect(value: &Value, trace_ids: &mut HashSet<String>) {
        match value {
            Value::Array(values) => {
                for value in values {
                    collect(value, trace_ids);
                }
            }
            Value::Object(object) => {
                for (key, value) in object {
                    let key = key
                        .chars()
                        .filter(|character| character.is_ascii_alphanumeric())
                        .flat_map(char::to_lowercase)
                        .collect::<String>();
                    if matches!(key.as_str(), "traceid" | "traceids") {
                        if let Some(value) = value.as_str() {
                            insert(value, trace_ids);
                        } else if let Some(values) = value.as_array() {
                            for value in values.iter().filter_map(Value::as_str) {
                                insert(value, trace_ids);
                            }
                        }
                    }
                    collect(value, trace_ids);
                }
            }
            _ => {}
        }
    }

    let mut trace_ids = HashSet::new();
    for event in events {
        if let Some(payload) = event.get("payload") {
            collect(payload, &mut trace_ids);
        }
    }
    let mut trace_ids = trace_ids.into_iter().collect::<Vec<_>>();
    trace_ids.sort_unstable();
    trace_ids
}

fn replay_time_bounds(events: &[Value]) -> (Option<f64>, Option<f64>) {
    let timestamp = |event: &Value, field: &str| {
        event
            .get("payload")
            .and_then(|payload| payload.get(field))
            .and_then(Value::as_f64)
            .filter(|value| value.is_finite())
            .map(|value| value * 1000.0)
    };
    let started_at = events
        .iter()
        .filter_map(|event| timestamp(event, "replay_start_timestamp"))
        .min_by(f64::total_cmp);
    let finished_at = events
        .iter()
        .filter_map(|event| timestamp(event, "timestamp"))
        .max_by(f64::total_cmp);
    (started_at, finished_at)
}

pub(crate) async fn issues(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<ListQuery>,
) -> Result<Json<Value>> {
    ServiceContext::parse(&service_id)?;
    issue_list(&state, vec![service_id], query).await
}

async fn internal_issues(
    State(state): State<Arc<ServerState>>,
    peer: Option<Extension<ConnectInfo<SocketAddr>>>,
    Query(query): Query<ListQuery>,
    Json(scope): Json<IssueScopeRequest>,
) -> Result<Json<Value>> {
    require_loopback(peer)?;
    validate_service_ids(&scope.service_ids)?;
    issue_list(&state, scope.service_ids, query).await
}

async fn issue_list(
    state: &ServerState,
    service_ids: Vec<String>,
    query: ListQuery,
) -> Result<Json<Value>> {
    if service_ids.is_empty() {
        return Ok(Json(json!({ "data": [], "total": 0 })));
    }
    let service_clause = any(service_ids
        .iter()
        .map(|service_id| term("service_id", service_id)));
    let mut search_query = all([term("doc_kind", "issue"), service_clause]);
    if let Some(search) = search_text(query.query.as_deref())? {
        search_query.text = Some(search.to_owned());
    }
    let response = state
        .store
        .search(SearchRequest {
            query: search_query,
            max_hits: query.limit.clamp(1, 100),
            start_offset: Some(query.offset),
            sort_by: "timestamp".into(),
        })
        .await?;
    let data = response
        .hits
        .into_iter()
        .map(|document| issue_view(&document))
        .collect::<Result<Vec<_>>>()?;
    Ok(Json(json!({ "data": data, "total": response.num_hits })))
}

fn issue_view(document: &Value) -> Result<IssueView> {
    let id = document
        .get("issue_id")
        .and_then(Value::as_str)
        .ok_or_else(|| Error::Storage("issue document has no issue ID".into()))?;
    let last_seen = value_string(document, "timestamp", "");
    Ok(IssueView {
        service_id: value_string(document, "service_id", ""),
        id: id.to_owned(),
        title: value_string(document, "title", "Unknown error"),
        level: value_string(document, "level", "error"),
        platform: value_string(document, "platform", "other"),
        status: value_string(document, "status", "open"),
        first_seen: value_string(document, "first_seen", &last_seen),
        last_seen,
        event_count: document
            .get("event_count")
            .and_then(Value::as_u64)
            .unwrap_or(1),
        last_event_id: value_string(document, "event_id", ""),
    })
}

fn issue_document_view(document: &Document) -> Result<IssueView> {
    Ok(IssueView {
        service_id: document.service_id.clone(),
        id: document
            .issue_id
            .clone()
            .ok_or_else(|| Error::Storage("issue document has no issue ID".into()))?,
        title: document
            .title
            .clone()
            .unwrap_or_else(|| "Unknown error".into()),
        level: document.level.clone().unwrap_or_else(|| "error".into()),
        platform: document.platform.clone().unwrap_or_else(|| "other".into()),
        status: document.status.clone().unwrap_or_else(|| "open".into()),
        first_seen: document
            .first_seen
            .clone()
            .unwrap_or_else(|| document.timestamp.clone()),
        last_seen: document.timestamp.clone(),
        event_count: document.event_count.unwrap_or(1),
        last_event_id: document.event_id.clone().unwrap_or_default(),
    })
}

async fn load_issue_view(
    state: &ServerState,
    service_id: &str,
    issue_id: &str,
) -> Result<IssueView> {
    let document = load_issue_document(state, service_id, issue_id)
        .await?
        .ok_or(Error::NotFound)?;
    issue_document_view(&document)
}

async fn load_issue_for_update(
    state: &ServerState,
    service_id: &str,
    issue_id: &str,
) -> Result<(Document, IssueView)> {
    let document = load_issue_document(state, service_id, issue_id)
        .await?
        .ok_or(Error::NotFound)?;
    let view = issue_document_view(&document)?;
    Ok((document, view))
}

pub(crate) async fn issue(
    State(state): State<Arc<ServerState>>,
    Path((service_id, issue_id)): Path<(String, String)>,
    Query(query): Query<IssueDetailQuery>,
) -> Result<Json<Value>> {
    ServiceContext::parse(&service_id)?;
    if query.limit == 0 || query.limit > 100 || query.offset > 1_000_000 {
        return Err(Error::InvalidRequest("invalid issue event page".into()));
    }
    let issue = load_issue_view(&state, &service_id, &issue_id).await?;
    let events = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "event"),
                term("service_id", &service_id),
                term("issue_id", &issue_id),
            ]),
            max_hits: query.limit,
            start_offset: Some(query.offset),
            sort_by: "timestamp".into(),
        })
        .await?;
    let event_total = events.num_hits;
    let events = events
        .hits
        .into_iter()
        .map(event_summary)
        .collect::<Vec<_>>();
    let insights = state.store.issue_insights(service_id, issue_id).await?;
    Ok(Json(json!({
        "issue": issue,
        "events": events,
        "eventTotal": event_total,
        "userCount": insights.user_count,
        "distributions": insights.distributions,
        "activity": insights.activity,
        "firstEventId": insights.first_event_id,
        "latestEventId": insights.latest_event_id,
        "recommendedEventId": insights.recommended_event_id,
    })))
}

pub(crate) async fn update_issue(
    State(state): State<Arc<ServerState>>,
    Path((service_id, issue_id)): Path<(String, String)>,
    Json(input): Json<IssueStatusInput>,
) -> Result<Response> {
    ServiceContext::parse(&service_id)?;
    if !matches!(input.status.as_str(), "open" | "resolved" | "ignored") {
        return Err(Error::InvalidRequest(
            "issue status must be open, resolved, or ignored".into(),
        ));
    }
    let ingest_guard = state.ingest_lock.lock().await;
    let (mut document, current) = load_issue_for_update(&state, &service_id, &issue_id).await?;
    document.status = Some(input.status.clone());
    document.received_at = Timestamp::now().to_string();
    document.revision = Some(next_issue_revision(document.revision)?);
    state.store.ingest(vec![document]).await?;
    drop(ingest_guard);
    let resolved = input.status == "resolved" && current.status != "resolved";
    let updated = IssueView {
        status: input.status,
        ..current
    };
    let mut response = Json(&updated).into_response();
    if resolved {
        attach_webhook_notifications(
            &mut response,
            &[WebhookNotification {
                event_type: "issue_resolved",
                timestamp: updated.last_seen.clone(),
                issue: WebhookIssue {
                    id: updated.id.clone(),
                    title: updated.title.clone(),
                    level: updated.level.clone(),
                    platform: updated.platform.clone(),
                },
                event: WebhookOccurrence {
                    id: updated.last_event_id.clone(),
                    timestamp: updated.last_seen.clone(),
                },
            }],
        )?;
    }
    Ok(response)
}

pub(crate) async fn artifacts(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<ListQuery>,
) -> Result<Json<ListResponse>> {
    ServiceContext::parse(&service_id)?;
    search_documents(&state, &service_id, "artifact", query).await
}

async fn items(
    State(state): State<Arc<ServerState>>,
    Path(service_id): Path<String>,
    Query(query): Query<ItemQuery>,
) -> Result<Json<ListResponse>> {
    ServiceContext::parse(&service_id)?;
    search_documents(
        &state,
        &service_id,
        &query.item_type,
        ListQuery {
            limit: query.limit,
            offset: query.offset,
            query: None,
        },
    )
    .await
}

async fn content(
    State(state): State<Arc<ServerState>>,
    Path((service_id, content_id)): Path<(String, String)>,
) -> Result<impl IntoResponse> {
    ServiceContext::parse(&service_id)?;
    let bytes = load_item_content(&state, &service_id, &content_id).await?;
    Ok((
        StatusCode::OK,
        [("content-type", "application/octet-stream")],
        bytes,
    ))
}

async fn load_item_content(
    state: &ServerState,
    service_id: &str,
    content_id: &str,
) -> Result<Vec<u8>> {
    state
        .store
        .load_content(
            "content_chunk",
            service_id,
            content_id,
            MAX_STORED_ITEM_BYTES,
        )
        .await
}

fn decode_replay_recording(input: &[u8], byte_limit: usize) -> Result<(Vec<Value>, usize)> {
    let newline = input
        .iter()
        .position(|byte| *byte == b'\n')
        .ok_or_else(|| Error::Storage("replay recording has no header separator".into()))?;
    if newline > MAX_REPLAY_RECORDING_HEADER_BYTES {
        return Err(Error::Storage(
            "replay recording header is too large".into(),
        ));
    }
    serde_json::from_slice::<Map<String, Value>>(&input[..newline])
        .map_err(|error| Error::Storage(format!("invalid replay recording header: {error}")))?;
    let body = &input[newline + 1..];
    if body.iter().find(|byte| !byte.is_ascii_whitespace()) == Some(&b'[') {
        if body.len() > byte_limit {
            return Err(replay_player_limit_error());
        }
        let events = serde_json::from_slice::<Vec<Value>>(body)
            .map_err(|error| Error::Storage(format!("invalid replay recording events: {error}")))?;
        return Ok((events, body.len()));
    }

    let decoded = read_replay_recording_bounded(flate2::read::ZlibDecoder::new(body), byte_limit)?;
    let events = serde_json::from_slice::<Vec<Value>>(&decoded)
        .map_err(|error| Error::Storage(format!("invalid replay recording events: {error}")))?;
    Ok((events, decoded.len()))
}

fn read_replay_recording_bounded(reader: impl Read, byte_limit: usize) -> Result<Vec<u8>> {
    let mut output = Vec::new();
    reader
        .take(byte_limit.saturating_add(1) as u64)
        .read_to_end(&mut output)
        .map_err(|error| Error::Storage(format!("invalid compressed replay recording: {error}")))?;
    if output.len() > byte_limit {
        return Err(replay_player_limit_error());
    }
    Ok(output)
}

fn replay_player_limit_error() -> Error {
    Error::Storage(format!(
        "decoded replay recording exceeds the {MAX_REPLAY_PLAYER_BYTES} byte player limit"
    ))
}

fn value_string(value: &Value, field: &str, default: &str) -> String {
    value
        .get(field)
        .and_then(Value::as_str)
        .unwrap_or(default)
        .to_owned()
}

async fn search_documents(
    state: &ServerState,
    service_id: &str,
    doc_kind: &str,
    list: ListQuery,
) -> Result<Json<ListResponse>> {
    let kind_query = if doc_kind == "replay" {
        any([
            term("doc_kind", "replay_event"),
            term("doc_kind", "replay_video"),
        ])
    } else {
        term("doc_kind", doc_kind)
    };
    let mut query = all([kind_query, term("service_id", service_id)]);
    if let Some(search) = search_text(list.query.as_deref())? {
        query.text = Some(search.to_owned());
    }
    let response = state
        .store
        .search(SearchRequest {
            query,
            max_hits: list.limit.clamp(1, 100),
            start_offset: Some(list.offset),
            sort_by: "timestamp".into(),
        })
        .await?;
    let data = match doc_kind {
        "event" => response.hits.into_iter().map(event_summary).collect(),
        "replay" => response.hits.into_iter().map(replay_summary).collect(),
        _ => response.hits,
    };
    Ok(Json(ListResponse {
        data,
        total: response.num_hits,
    }))
}

fn event_summary(mut document: Value) -> Value {
    if let Some(document) = document.as_object_mut() {
        document.remove("payload");
    }
    document
}

fn replay_summary(mut document: Value) -> Value {
    let sdk = document.pointer("/payload/sdk").cloned();
    if let Some(document) = document.as_object_mut() {
        if let Some(sdk) = sdk {
            document.insert("payload".into(), json!({ "sdk": sdk }));
        } else {
            document.remove("payload");
        }
    }
    document
}

fn search_text(value: Option<&str>) -> Result<Option<&str>> {
    let value = value.map(str::trim).filter(|value| !value.is_empty());
    if value.is_some_and(|value| value.chars().count() > MAX_SEARCH_QUERY_CHARS) {
        return Err(Error::InvalidRequest(format!(
            "search query must not exceed {MAX_SEARCH_QUERY_CHARS} characters"
        )));
    }
    Ok(value)
}

fn artifact_service(
    headers: &HeaderMap,
    org: &str,
    project: Option<&str>,
) -> Result<ServiceContext> {
    if org != SENTRY_ORGANIZATION {
        return Err(Error::NotFound);
    }
    if headers
        .get(ARTIFACT_AUTHORIZED_HEADER)
        .and_then(|value| value.to_str().ok())
        != Some("1")
    {
        return Err(Error::Authentication);
    }
    let service = trusted_service(headers)?;
    if project.is_some_and(|project| project != service.id) {
        return Err(Error::NotFound);
    }
    Ok(service)
}

fn sentry_service(
    headers: &HeaderMap,
    project_id: &str,
    public_key: &str,
) -> Result<ServiceContext> {
    let service = trusted_service(headers)?;
    if project_id != SENTRY_PROJECT_ID || public_key != service.id {
        return Err(Error::Authentication);
    }
    Ok(service)
}

fn trusted_service(headers: &HeaderMap) -> Result<ServiceContext> {
    headers
        .get(SERVICE_ID_HEADER)
        .and_then(|value| value.to_str().ok())
        .ok_or(Error::Authentication)
        .and_then(ServiceContext::parse)
}

fn symbol_type(name: &str) -> &'static str {
    let name = name.to_ascii_lowercase();
    if name.ends_with(".pdb") {
        "pdb"
    } else if name.ends_with(".dll") || name.ends_with(".exe") {
        "pe"
    } else if name.ends_with(".wasm") {
        "wasm"
    } else if name.ends_with(".dylib") || name.contains(".dsym") {
        "macho"
    } else if name.ends_with(".sym") {
        "breakpad"
    } else if name.ends_with(".bcsymbolmap") {
        "bcsymbolmap"
    } else if name.ends_with(".plist") {
        "uuidmap"
    } else if (name.starts_with("/proguard/") && name.ends_with(".txt"))
        || name.ends_with("mapping.txt")
        || name.ends_with("proguard.txt")
    {
        "proguard"
    } else if name.ends_with(".symbols") {
        "dart_symbol_map"
    } else if name.ends_with(".zip") {
        "sourcebundle"
    } else {
        "elf"
    }
}

fn proguard_debug_id_from_name(name: &str) -> Option<String> {
    let name = name.strip_prefix("/proguard/")?.strip_suffix(".txt")?;
    uuid::Uuid::parse_str(name)
        .ok()
        .map(|id| id.hyphenated().to_string())
}

fn envelope_dsn_key(envelope: &Envelope, project_id: &str) -> Option<String> {
    let dsn = envelope.headers.get("dsn")?.as_str()?;
    let url = Url::parse(dsn).ok()?;
    let dsn_project = url.path_segments()?.next_back()?;
    (dsn_project == project_id && !url.username().is_empty()).then(|| url.username().to_owned())
}

fn peer_address(peer: Option<Extension<ConnectInfo<SocketAddr>>>) -> Option<SocketAddr> {
    peer.map(|Extension(ConnectInfo(address))| address)
}

fn request_geo(headers: &HeaderMap, geoip: &GeoIpLookup) -> Option<Geo> {
    geoip.request_geo(
        headers
            .get("cf-ipcountry")
            .and_then(|value| value.to_str().ok()),
        headers
            .get("cf-region")
            .and_then(|value| value.to_str().ok()),
        headers
            .get("cf-ipcity")
            .and_then(|value| value.to_str().ok()),
    )
}

fn client_ip(headers: &HeaderMap, peer: Option<SocketAddr>) -> Option<IpAddr> {
    let cloudflare = headers
        .get("cf-connecting-ip")
        .and_then(|value| value.to_str().ok())
        .and_then(parse_ip);
    cloudflare
        .or_else(|| {
            headers
                .get("x-forwarded-for")
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.split(',').next())
                .and_then(parse_ip)
        })
        .or_else(|| {
            headers
                .get("x-real-ip")
                .and_then(|value| value.to_str().ok())
                .and_then(parse_ip)
        })
        .or_else(|| peer.and_then(|address| normalize_ip(address.ip())))
}

fn parse_ip(value: &str) -> Option<IpAddr> {
    value.trim().parse().ok().and_then(normalize_ip)
}

fn normalize_ip(address: IpAddr) -> Option<IpAddr> {
    let address = match address {
        IpAddr::V6(address) => address
            .to_ipv4_mapped()
            .map_or(IpAddr::V6(address), IpAddr::V4),
        address => address,
    };
    (!address.is_unspecified()).then_some(address)
}

fn decode_body(headers: &HeaderMap, input: Bytes) -> Result<Vec<u8>> {
    let encoding = headers
        .get("content-encoding")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("identity")
        .trim()
        .to_ascii_lowercase();
    let decoded = match encoding.as_str() {
        "" | "identity" | "utf-8" => return Ok(input.to_vec()),
        "gzip" => read_bounded(flate2::read::GzDecoder::new(input.as_ref()))?,
        "deflate" => {
            let zlib = read_bounded(flate2::read::ZlibDecoder::new(input.as_ref()));
            match zlib {
                Ok(value) => value,
                Err(_) => read_bounded(flate2::read::DeflateDecoder::new(input.as_ref()))?,
            }
        }
        "br" => read_bounded(brotli::Decompressor::new(input.as_ref(), 4096))?,
        "zstd" => {
            let decoder = zstd::stream::read::Decoder::new(input.as_ref())
                .map_err(|error| Error::InvalidRequest(format!("invalid zstd body: {error}")))?;
            read_bounded(decoder)?
        }
        _ => {
            return Err(Error::InvalidRequest(format!(
                "unsupported content encoding: {encoding}"
            )));
        }
    };
    Ok(decoded)
}

fn read_bounded(reader: impl Read) -> Result<Vec<u8>> {
    let mut output = Vec::new();
    reader
        .take((MAX_STORED_ITEM_BYTES + 1) as u64)
        .read_to_end(&mut output)
        .map_err(|error| Error::InvalidRequest(format!("invalid compressed body: {error}")))?;
    if output.len() > MAX_STORED_ITEM_BYTES {
        return Err(Error::InvalidRequest(
            "decompressed request body is too large".into(),
        ));
    }
    Ok(output)
}

fn read_artifact_chunk(reader: impl Read) -> Result<Vec<u8>> {
    let mut output = Vec::new();
    reader
        .take((artifact::CHUNK_BYTES + 1) as u64)
        .read_to_end(&mut output)
        .map_err(|error| Error::InvalidRequest(format!("invalid compressed chunk: {error}")))?;
    if output.len() > artifact::CHUNK_BYTES {
        return Err(Error::InvalidRequest(
            "decompressed chunk exceeds the chunk size limit".into(),
        ));
    }
    Ok(output)
}

const fn default_limit() -> usize {
    50
}

// Platformd exercises the external integration surface; these tests cover local identity invariants.
#[cfg(test)]
mod tests {
    use axum::http::HeaderValue;

    use super::*;

    #[test]
    fn sentry_identity_is_the_trusted_service_and_transport_project_one() {
        let mut headers = HeaderMap::new();
        headers.insert(
            SERVICE_ID_HEADER,
            HeaderValue::from_static("tz4a98xxat96iws9zmbrgj3a"),
        );

        assert_eq!(
            sentry_service(&headers, SENTRY_PROJECT_ID, "tz4a98xxat96iws9zmbrgj3a")
                .unwrap()
                .id,
            "tz4a98xxat96iws9zmbrgj3a"
        );
        assert!(sentry_service(&headers, "2", "tz4a98xxat96iws9zmbrgj3a").is_err());
        assert!(sentry_service(&headers, SENTRY_PROJECT_ID, "another-service").is_err());
    }

    #[test]
    fn artifact_identity_requires_platformd_authorization() {
        let mut headers = HeaderMap::new();
        headers.insert(SERVICE_ID_HEADER, HeaderValue::from_static("service-123"));
        assert!(artifact_service(&headers, SENTRY_ORGANIZATION, None).is_err());

        headers.insert(ARTIFACT_AUTHORIZED_HEADER, HeaderValue::from_static("1"));
        assert!(artifact_service(&headers, SENTRY_ORGANIZATION, Some("service-123")).is_ok());
        assert!(artifact_service(&headers, SENTRY_ORGANIZATION, Some("other")).is_err());
    }

    #[test]
    fn validates_structured_log_filters_before_building_sql() {
        let filters = parse_log_field_filters(Some(
            r#"[{"path":"http.status_code","operator":"equals","value":"500"}]"#,
        ))
        .unwrap();
        assert_eq!(filters.len(), 1);
        assert!(
            parse_log_field_filters(Some(
                r#"[{"path":"caller') OR 1=1","operator":"equals","value":"x"}]"#
            ))
            .is_err()
        );
        assert!(
            parse_log_field_filters(Some(
                r#"[{"path":"caller","operator":"exists","value":"unexpected"}]"#
            ))
            .is_err()
        );
    }

    #[test]
    fn replay_bounds_cover_all_replay_event_segments() {
        let events = [
            json!({
                "payload": {
                    "replay_start_timestamp": 1_786_756_532.5,
                    "timestamp": 1_786_756_540.0
                }
            }),
            json!({
                "payload": {
                    "replay_start_timestamp": 1_786_756_533.0,
                    "timestamp": 1_786_756_550.25
                }
            }),
        ];

        assert_eq!(
            replay_time_bounds(&events),
            (Some(1_786_756_532_500.0), Some(1_786_756_550_250.0))
        );
    }

    #[test]
    fn replay_trace_ids_are_normalized_and_deduplicated() {
        let events = [
            json!({"payload": {"trace_ids": ["4b25bc58-f142-43d8-b208-d1e22a054164"]}}),
            json!({"payload": {"contexts": {"trace": {"trace_id": "4b25bc58f14243d8b208d1e22a054164"}}}}),
        ];

        assert_eq!(
            replay_trace_ids(&events),
            ["4b25bc58f14243d8b208d1e22a054164"]
        );
    }

    #[test]
    fn replay_video_ranges_support_browser_seek_requests() {
        assert_eq!(parse_byte_range(None, 100), Ok(None));
        assert_eq!(
            parse_byte_range(Some("bytes=10-19"), 100),
            Ok(Some((10, 19)))
        );
        assert_eq!(parse_byte_range(Some("bytes=90-"), 100), Ok(Some((90, 99))));
        assert_eq!(parse_byte_range(Some("bytes=-10"), 100), Ok(Some((90, 99))));
        assert_eq!(parse_byte_range(Some("bytes=-200"), 100), Ok(Some((0, 99))));
        assert!(parse_byte_range(Some("bytes=100-"), 100).is_err());
        assert!(parse_byte_range(Some("bytes=20-10"), 100).is_err());
        assert!(parse_byte_range(Some("items=0-10"), 100).is_err());
        assert!(parse_byte_range(Some("bytes=0-1,4-5"), 100).is_err());
    }
}
