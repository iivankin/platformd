use std::cmp::Ordering;
use std::collections::HashSet;
use std::io::Read;
use std::net::SocketAddr;
use std::path::PathBuf;
use std::sync::Arc;

use axum::body::Bytes;
use axum::extract::{
    DefaultBodyLimit, Extension, Multipart, Path, Query, RawQuery, Request, State,
};
use axum::http::{HeaderMap, Method, StatusCode};
use axum::middleware::{self, Next};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, patch, post};
use axum::{Json, Router};
use jiff::Timestamp;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use tokio::sync::Mutex;
use tower::limit::ConcurrencyLimitLayer;
use tower_http::cors::{Any, CorsLayer};
use tower_http::limit::RequestBodyLimitLayer;
use tower_http::trace::TraceLayer;
use url::Url;

use crate::MAX_STORED_ITEM_BYTES;
use crate::access::AccessIdentity;
use crate::artifact::{self, ArtifactMetadata};
use crate::auth::{
    api_token_id, bearer_token, bearer_token_hash, require_bearer, sentry_public_key, token_hash,
};
use crate::config::{
    ApiTokenView, AppView, ConfigRepository, CreateApiToken, CreateApp, CreateWebhook,
    CreatedApiToken, CreatedApp, CreatedWebhook, TrackerConfig, UpdateWebhook, WebhookEvent,
    WebhookView,
};
use crate::envelope::{Envelope, Item, parse};
use crate::error::{Error, Result};
use crate::ingest::{IngestedEvent, PreparedIngest, prepare};
use crate::model::{Document, SearchRequest};
use crate::storage::{Store, all, any, term};
use crate::symbolicator::{
    CrashFileKind, Dispatcher as SymbolicationDispatcher, Symbolicator, crash_event,
};
use crate::ui;
use crate::webhook::Dispatcher;

const MAX_COMPRESSED_BODY_BYTES: usize = 64 << 20;
const MAX_CONCURRENT_REQUESTS: usize = 32;
const MAX_CONCURRENT_INGEST_REQUESTS: usize = 4;
const MAX_CHUNKS_PER_REQUEST: usize = 16;
const MAX_SEARCH_QUERY_CHARS: usize = 256;
const MAX_REPLAY_PLAYER_BYTES: usize = 64 << 20;
const MAX_REPLAY_RECORDING_HEADER_BYTES: usize = 64 << 10;

#[derive(Clone, Debug)]
pub struct ServerOptions {
    pub listen: SocketAddr,
    pub volume: PathBuf,
    pub public_url: String,
    pub name: String,
    pub slug: String,
    pub admin_token: Option<String>,
    pub ui_enabled: bool,
    pub endpoint_file: Option<PathBuf>,
}

pub struct Server {
    listen: SocketAddr,
    ui_enabled: bool,
    endpoint_file: Option<PathBuf>,
    state: Arc<ServerState>,
}

pub(crate) struct ServerState {
    pub(crate) config: ConfigRepository,
    pub(crate) store: Store,
    pub(crate) webhooks: Dispatcher,
    admin_token_hash: Option<String>,
    pub(crate) symbolicator: Symbolicator,
    symbolication: SymbolicationDispatcher,
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
pub(crate) struct TrackerView {
    name: String,
    slug: String,
    public_url: String,
    admin_auth_required: bool,
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

impl Server {
    pub async fn new(options: ServerOptions) -> Result<Self> {
        if options.volume.as_os_str().is_empty() {
            return Err(Error::Configuration("volume path must not be empty".into()));
        }
        if options.admin_token.as_deref().is_some_and(|token| {
            token.len() < 24 || !token.bytes().all(|byte| byte.is_ascii_graphic())
        }) {
            return Err(Error::Configuration(
                "admin token must contain at least 24 visible ASCII characters without whitespace"
                    .into(),
            ));
        }
        let config = ConfigRepository::open(
            options.volume.join("config.json"),
            options.name,
            options.slug,
            options.public_url,
        )
        .await?;
        let store = Store::open(options.volume.clone()).await?;
        let webhooks = Dispatcher::new()
            .map_err(|error| Error::Configuration(format!("build webhook client: {error}")))?;
        let tracker = config.tracker().await;
        let symbolicator = Symbolicator::new(store.clone());
        let symbolication =
            SymbolicationDispatcher::new(symbolicator.clone(), store.clone(), &tracker.apps);
        Ok(Self {
            listen: options.listen,
            ui_enabled: options.ui_enabled,
            endpoint_file: options.endpoint_file,
            state: Arc::new(ServerState {
                config,
                store,
                webhooks,
                admin_token_hash: options.admin_token.as_deref().map(token_hash),
                symbolicator,
                symbolication,
                ingest_lock: Mutex::new(()),
            }),
        })
    }

    pub fn router(&self) -> Router {
        let management_api = api_routes()
            .route("/tokens", get(api_tokens).post(create_api_token))
            .route(
                "/tokens/{token_id}",
                axum::routing::delete(revoke_api_token),
            )
            .route_layer(middleware::from_fn_with_state(
                self.state.clone(),
                management_auth,
            ));
        let public = Router::new()
            .nest("/api/v1", api_routes().route("/me", get(current_identity)))
            .route(
                "/mcp",
                post(crate::mcp::handle)
                    .layer(RequestBodyLimitLayer::new(crate::mcp::MAX_BODY_BYTES)),
            )
            .route_layer(middleware::from_fn_with_state(
                self.state.clone(),
                public_auth,
            ))
            .layer(browser_cors());
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
        let mut router = Router::new()
            .route("/health", get(health))
            .merge(ingestion)
            .nest("/api/v1", management_api)
            .nest("/public", public);
        if self.ui_enabled {
            router = router
                .route("/", get(ui::index))
                .route("/assets/index.css", get(ui::styles))
                .route("/assets/index.js", get(ui::application));
        }
        router
            .layer(DefaultBodyLimit::disable())
            .layer(RequestBodyLimitLayer::new(MAX_COMPRESSED_BODY_BYTES))
            .layer(TraceLayer::new_for_http())
            .layer(ConcurrencyLimitLayer::new(MAX_CONCURRENT_REQUESTS))
            .with_state(self.state.clone())
    }

    pub async fn serve(self) -> anyhow::Result<()> {
        let listener = tokio::net::TcpListener::bind(self.listen).await?;
        let listen = listener.local_addr()?;
        let _endpoint_file = match self.endpoint_file.as_deref() {
            Some(path) => Some(EndpointFile::publish(path, listen).await?),
            None => None,
        };
        tracing::info!(listen = %listen, "error tracker is listening");
        axum::serve(listener, self.router()).await?;
        Ok(())
    }
}

struct EndpointFile(PathBuf);

impl EndpointFile {
    async fn publish(path: &std::path::Path, listen: SocketAddr) -> anyhow::Result<Self> {
        let parent = path
            .parent()
            .ok_or_else(|| anyhow::anyhow!("endpoint file has no parent directory"))?;
        tokio::fs::create_dir_all(parent).await?;
        let temporary = path.with_extension(format!("tmp-{}", std::process::id()));
        tokio::fs::write(&temporary, listen.to_string()).await?;
        tokio::fs::rename(&temporary, path).await?;
        Ok(Self(path.to_owned()))
    }
}

impl Drop for EndpointFile {
    fn drop(&mut self) {
        let _ = std::fs::remove_file(&self.0);
    }
}

fn browser_cors() -> CorsLayer {
    CorsLayer::new()
        .allow_origin(Any)
        .allow_headers(Any)
        .allow_methods([Method::GET, Method::POST, Method::PATCH, Method::DELETE])
}

fn api_routes() -> Router<Arc<ServerState>> {
    Router::new()
        .route("/tracker", get(tracker))
        .route("/apps", get(apps).post(create_app))
        .route("/apps/{app_id}", get(app))
        .route("/apps/{app_id}/upload-token", post(rotate_app_auth_token))
        .route("/apps/{app_id}/events", get(events))
        .route("/apps/{app_id}/events/{event_id}", get(event))
        .route("/apps/{app_id}/replays", get(replays))
        .route("/apps/{app_id}/replays/{replay_id}", get(replay))
        .route(
            "/apps/{app_id}/replays/{replay_id}/recording",
            get(replay_recording),
        )
        .route("/apps/{app_id}/issues", get(issues))
        .route(
            "/apps/{app_id}/issues/{issue_id}",
            get(issue).patch(update_issue),
        )
        .route("/apps/{app_id}/artifacts", get(artifacts))
        .route("/apps/{app_id}/items", get(items))
        .route("/apps/{app_id}/content/{content_id}", get(content))
        .route("/apps/{app_id}/webhooks", post(create_webhook))
        .route(
            "/apps/{app_id}/webhooks/{webhook_id}",
            patch(update_webhook).delete(delete_webhook),
        )
}

async fn management_auth(
    State(state): State<Arc<ServerState>>,
    mut request: Request,
    next: Next,
) -> Response {
    if let Some(expected_hash) = state.admin_token_hash.as_deref()
        && require_bearer(request.headers(), expected_hash).is_err()
    {
        return Error::Authentication.into_response();
    }
    request
        .extensions_mut()
        .insert(AccessIdentity::management());
    let mut response = next.run(request).await;
    response.headers_mut().insert(
        axum::http::header::CACHE_CONTROL,
        axum::http::HeaderValue::from_static("private, no-store"),
    );
    response
}

async fn public_auth(
    State(state): State<Arc<ServerState>>,
    mut request: Request,
    next: Next,
) -> Response {
    let Some(value) = bearer_token(request.headers()) else {
        return Error::Authentication.into_response();
    };
    let Some(id) = api_token_id(value) else {
        return Error::Authentication.into_response();
    };
    let Some(token) = state
        .config
        .authenticate_api_token(id, &token_hash(value))
        .await
    else {
        return Error::Authentication.into_response();
    };
    request
        .extensions_mut()
        .insert(AccessIdentity::api_token(&token));
    let mut response = next.run(request).await;
    response.headers_mut().insert(
        axum::http::header::CACHE_CONTROL,
        axum::http::HeaderValue::from_static("private, no-store"),
    );
    response
}

async fn current_identity(
    Extension(identity): Extension<AccessIdentity>,
) -> Json<crate::access::AccessIdentityView> {
    Json(identity.view())
}

async fn api_tokens(State(state): State<Arc<ServerState>>) -> Json<Vec<ApiTokenView>> {
    Json(state.config.api_tokens().await)
}

async fn create_api_token(
    State(state): State<Arc<ServerState>>,
    Json(input): Json<CreateApiToken>,
) -> Result<(StatusCode, Json<CreatedApiToken>)> {
    let created = state
        .config
        .create_api_token(input, &Timestamp::now().to_string())
        .await?;
    Ok((StatusCode::CREATED, Json(created)))
}

async fn revoke_api_token(
    State(state): State<Arc<ServerState>>,
    Path(token_id): Path<String>,
) -> Result<StatusCode> {
    state.config.revoke_api_token(&token_id).await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn health() -> Json<Health> {
    Json(Health {
        status: "ok",
        version: env!("CARGO_PKG_VERSION"),
    })
}

async fn envelope(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
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
    let app = state
        .config
        .app_by_project_and_key(&project_id, &public_key)
        .await
        .ok_or(Error::Authentication)?;
    let tracker = state.config.tracker().await;
    let event_id = ingest(&state, &tracker, &app, prepare(&app, envelope)?).await?;
    Ok((StatusCode::OK, Json(json!({ "id": event_id }))))
}

async fn store(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
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
    let app = state
        .config
        .app_by_project_and_key(&project_id, &public_key)
        .await
        .ok_or(Error::Authentication)?;
    let mut envelope_headers = Map::new();
    if let Some(event_id) = payload.get("event_id").and_then(Value::as_str) {
        envelope_headers.insert("event_id".into(), Value::String(event_id.into()));
    }
    let mut item_headers = Map::new();
    item_headers.insert("type".into(), Value::String("event".into()));
    item_headers.insert("length".into(), Value::from(body.len()));
    let tracker = state.config.tracker().await;
    let prepared = prepare(
        &app,
        Envelope {
            headers: envelope_headers,
            items: vec![Item {
                headers: item_headers,
                item_type: "event".into(),
                payload: body,
            }],
        },
    )?;
    let event_id = ingest(&state, &tracker, &app, prepared).await?;
    Ok((StatusCode::OK, Json(json!({ "id": event_id }))))
}

async fn minidump(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
    headers: HeaderMap,
    multipart: Multipart,
) -> Result<impl IntoResponse> {
    ingest_crash_file(
        state,
        project_id,
        query,
        headers,
        multipart,
        CrashFileKind::Minidump,
    )
    .await
}

async fn apple_crash_report(
    State(state): State<Arc<ServerState>>,
    Path(project_id): Path<String>,
    RawQuery(query): RawQuery,
    headers: HeaderMap,
    multipart: Multipart,
) -> Result<impl IntoResponse> {
    ingest_crash_file(
        state,
        project_id,
        query,
        headers,
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
    mut multipart: Multipart,
    kind: CrashFileKind,
) -> Result<impl IntoResponse> {
    let public_key = sentry_public_key(&headers, query.as_deref()).ok_or(Error::Authentication)?;
    let app = state
        .config
        .app_by_project_and_key(&project_id, &public_key)
        .await
        .ok_or(Error::Authentication)?;
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
    let tracker = state.config.tracker().await;
    let event_id = uuid::Uuid::new_v4().simple().to_string();
    let decoded = match state
        .symbolicator
        .process_crash_file(&app, kind, crash_file.clone())
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
        &app,
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
    )?;
    ingest(&state, &tracker, &app, prepared).await?;
    Ok((StatusCode::OK, Json(json!({ "id": event_id }))))
}

async fn chunk_upload_options(
    State(state): State<Arc<ServerState>>,
    Path(org): Path<String>,
    headers: HeaderMap,
) -> Result<Json<Value>> {
    let (tracker, _) = artifact_app(&state, &headers, &org, None).await?;
    let mut url = Url::parse(state.config.public_url())
        .map_err(|error| Error::Configuration(format!("parse tracker public URL: {error}")))?;
    url.set_path(&format!(
        "/api/0/organizations/{}/chunk-upload/",
        tracker.slug
    ));
    Ok(Json(json!({
        "url": url.to_string(),
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
    let (_, app) = artifact_app(&state, &headers, &org, None).await?;
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
        artifact::store_upload_chunk(&state.store, &app, &checksum, &bytes).await?;
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
    let (_, app) = artifact_app(&state, &headers, &org, None).await?;
    if !input.projects.is_empty() && !input.projects.iter().any(|project| project == &app.slug) {
        return Err(Error::NotFound);
    }
    let missing = artifact::missing_upload_chunks(&state.store, &app.id, &input.chunks).await?;
    if !missing.is_empty() {
        return Ok(Json(json!({
            "state": "not_found",
            "missingChunks": missing,
            "detail": null
        })));
    }
    let assembled = artifact::assemble(
        &state.store,
        &app,
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
        state.symbolication.enqueue_application(&app).await;
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
    let (_, app) = artifact_app(&state, &headers, &org, Some(&project)).await?;
    let mut response = serde_json::Map::new();
    let mut assembled_any = false;
    for (checksum, request) in input {
        let missing =
            artifact::missing_upload_chunks(&state.store, &app.id, &request.chunks).await?;
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
            &app,
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
        state.symbolication.enqueue_application(&app).await;
    }
    Ok(Json(Value::Object(response)))
}

async fn ingest(
    state: &ServerState,
    tracker: &TrackerConfig,
    app: &crate::config::AppConfig,
    prepared: PreparedIngest,
) -> Result<String> {
    // Tantivy commits are serialized as well. Keeping the existence checks in the
    // same critical section makes event IDs and first-issue notifications idempotent.
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
        if state
            .store
            .exists(all([
                term("doc_kind", "event"),
                term("app_id", &app.id),
                term("event_id", &event.event_id),
            ]))
            .await?
        {
            duplicate_event_ids.insert(event.event_id.clone());
        }
    }
    if !events.is_empty() && duplicate_event_ids.len() == events.len() {
        return Ok(response_event_id);
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
        let current = load_issue_document(state, &app.id, &event.issue_id).await?;
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
            app,
            latest,
            first_seen,
            current.as_ref(),
            occurrences,
            status,
        ));
    }
    state.store.ingest(documents).await?;
    drop(ingest_guard);
    for event in events {
        let is_new_issue = new_issues.remove(&event.issue_id);
        let is_regressed = regressed_issues.remove(&event.issue_id);
        state
            .webhooks
            .dispatch(tracker, app, &event, is_new_issue, is_regressed);
        state.symbolication.enqueue(app, event);
    }
    Ok(response_event_id)
}

fn issue_status_after_event(status: &str) -> (&str, bool) {
    if status == "resolved" {
        ("open", true)
    } else {
        (status, false)
    }
}

fn issue_document(
    app: &crate::config::AppConfig,
    event: &IngestedEvent,
    incoming_first_seen: &str,
    current: Option<&Document>,
    occurrences: u64,
    status: &str,
) -> Document {
    let now = Timestamp::now().to_string();
    let current_is_newer = current.is_some_and(|document| {
        compare_timestamps(&document.timestamp, &event.timestamp) == Ordering::Greater
    });
    let timestamp = current
        .filter(|_| current_is_newer)
        .map_or(event.timestamp.as_str(), |document| &document.timestamp);
    let mut document = Document::base("issue", &app.id, &app.project_id, timestamp, &now);
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
            .saturating_add(occurrences),
    );
    document
}

fn compare_timestamps(left: &str, right: &str) -> Ordering {
    match (left.parse::<Timestamp>(), right.parse::<Timestamp>()) {
        (Ok(left), Ok(right)) => left.cmp(&right),
        _ => left.cmp(right),
    }
}

async fn load_issue_document(
    state: &ServerState,
    app_id: &str,
    issue_id: &str,
) -> Result<Option<Document>> {
    let response = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "issue"),
                term("app_id", app_id),
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

pub(crate) async fn tracker(
    State(state): State<Arc<ServerState>>,
    Extension(_identity): Extension<AccessIdentity>,
) -> Result<Json<TrackerView>> {
    let tracker = state.config.tracker().await;
    Ok(Json(TrackerView {
        name: tracker.name,
        slug: tracker.slug,
        public_url: state.config.public_url().to_owned(),
        admin_auth_required: state.admin_token_hash.is_some(),
    }))
}

pub(crate) async fn apps(
    State(state): State<Arc<ServerState>>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<Vec<AppView>>> {
    let mut apps = state.config.apps().await?;
    if let Some(app_id) = identity.app_id() {
        apps.retain(|app| app.id == app_id);
    }
    Ok(Json(apps))
}

pub(crate) async fn app(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<AppView>> {
    identity.require_app_read(&app_id)?;
    state
        .config
        .app_view(&app_id)
        .await?
        .map(Json)
        .ok_or(Error::NotFound)
}

pub(crate) async fn create_app(
    State(state): State<Arc<ServerState>>,
    Extension(identity): Extension<AccessIdentity>,
    Json(input): Json<CreateApp>,
) -> Result<(StatusCode, Json<CreatedApp>)> {
    identity.require_unscoped_admin()?;
    let created = state
        .config
        .create_app(input, &Timestamp::now().to_string())
        .await?;
    Ok((StatusCode::CREATED, Json(created)))
}

pub(crate) async fn rotate_app_auth_token(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<Value>> {
    identity.require_app_admin(&app_id)?;
    let auth_token = state
        .config
        .rotate_app_auth_token(&app_id, &Timestamp::now().to_string())
        .await?;
    Ok(Json(json!({ "authToken": auth_token })))
}

pub(crate) async fn events(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
    Query(query): Query<ListQuery>,
) -> Result<Json<ListResponse>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    search_documents(&state, &app_id, "event", query).await
}

pub(crate) async fn event(
    State(state): State<Arc<ServerState>>,
    Path((app_id, event_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<Value>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    let event_id = event_id.replace('-', "").to_ascii_lowercase();
    let response = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "event"),
                term("app_id", &app_id),
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
                term("app_id", &app_id),
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
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
    Query(query): Query<ListQuery>,
) -> Result<Json<ListResponse>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    search_documents(&state, &app_id, "replay_event", query).await
}

pub(crate) async fn replay(
    State(state): State<Arc<ServerState>>,
    Path((app_id, replay_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<Value>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    let replay_id = replay_id.replace('-', "").to_ascii_lowercase();
    let mut hits = state
        .store
        .search_all(
            all([
                any([
                    term("doc_kind", "replay_event"),
                    term("doc_kind", "replay_recording"),
                ]),
                term("app_id", &app_id),
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
    Path((app_id, replay_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<Value>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    let replay_id = replay_id.replace('-', "").to_ascii_lowercase();
    let mut recordings = state
        .store
        .search_all(
            all([
                term("doc_kind", "replay_recording"),
                term("app_id", &app_id),
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

    let segment_count = recordings.len();
    let mut decoded_bytes = 0;
    let mut events = Vec::new();
    for recording in recordings {
        let content_id = recording
            .get("content_id")
            .and_then(Value::as_str)
            .ok_or_else(|| Error::Storage("replay recording has no content ID".into()))?;
        let content = load_item_content(&state, &app_id, content_id).await?;
        let remaining = MAX_REPLAY_PLAYER_BYTES.saturating_sub(decoded_bytes);
        let (segment_events, segment_bytes) = decode_replay_recording(&content, remaining)?;
        decoded_bytes += segment_bytes;
        events.extend(segment_events);
    }

    let error_events = state
        .store
        .search_all(
            all([
                term("doc_kind", "event"),
                term("app_id", &app_id),
                term("replay_id", &replay_id),
            ]),
            "timestamp",
        )
        .await?
        .into_iter()
        .map(event_summary)
        .collect::<Vec<_>>();

    Ok(Json(json!({
        "errorEvents": error_events,
        "events": events,
        "replayId": replay_id,
        "segmentCount": segment_count,
    })))
}

pub(crate) async fn issues(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
    Query(query): Query<ListQuery>,
) -> Result<Json<Value>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    let mut search_query = all([term("doc_kind", "issue"), term("app_id", &app_id)]);
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

async fn load_issue_view(state: &ServerState, app_id: &str, issue_id: &str) -> Result<IssueView> {
    let document = load_issue_document(state, app_id, issue_id)
        .await?
        .ok_or(Error::NotFound)?;
    issue_document_view(&document)
}

async fn load_issue_for_update(
    state: &ServerState,
    app_id: &str,
    issue_id: &str,
) -> Result<(Document, IssueView)> {
    let document = load_issue_document(state, app_id, issue_id)
        .await?
        .ok_or(Error::NotFound)?;
    let view = issue_document_view(&document)?;
    Ok((document, view))
}

pub(crate) async fn issue(
    State(state): State<Arc<ServerState>>,
    Path((app_id, issue_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<Json<Value>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    let issue = load_issue_view(&state, &app_id, &issue_id).await?;
    let events = state
        .store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "event"),
                term("app_id", &app_id),
                term("issue_id", &issue_id),
            ]),
            max_hits: 100,
            start_offset: None,
            sort_by: "timestamp".into(),
        })
        .await?;
    let event_total = events.num_hits;
    let events = events
        .hits
        .into_iter()
        .map(event_summary)
        .collect::<Vec<_>>();
    Ok(Json(json!({
        "issue": issue,
        "events": events,
        "eventTotal": event_total
    })))
}

pub(crate) async fn update_issue(
    State(state): State<Arc<ServerState>>,
    Path((app_id, issue_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
    Json(input): Json<IssueStatusInput>,
) -> Result<Json<IssueView>> {
    identity.require_app_admin(&app_id)?;
    if !matches!(input.status.as_str(), "open" | "resolved" | "ignored") {
        return Err(Error::InvalidRequest(
            "issue status must be open, resolved, or ignored".into(),
        ));
    }
    let app = state.config.app(&app_id).await.ok_or(Error::NotFound)?;
    let tracker = state.config.tracker().await;
    let ingest_guard = state.ingest_lock.lock().await;
    let (mut document, current) = load_issue_for_update(&state, &app_id, &issue_id).await?;
    let previous_status = current.status.clone();
    let event = IngestedEvent {
        event_id: current.last_event_id.clone(),
        issue_id: current.id.clone(),
        title: current.title.clone(),
        level: current.level.clone(),
        platform: current.platform.clone(),
        timestamp: Timestamp::now().to_string(),
        payload: Value::Null,
    };
    document.status = Some(input.status.clone());
    document.received_at = event.timestamp.clone();
    state.store.ingest(vec![document]).await?;
    drop(ingest_guard);
    if input.status == "resolved" && previous_status != "resolved" {
        state
            .webhooks
            .dispatch_state(&tracker, &app, &event, WebhookEvent::IssueResolved);
    }
    Ok(Json(IssueView {
        status: input.status,
        ..current
    }))
}

pub(crate) async fn artifacts(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
    Query(query): Query<ListQuery>,
) -> Result<Json<ListResponse>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    search_documents(&state, &app_id, "artifact", query).await
}

async fn items(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
    Query(query): Query<ItemQuery>,
) -> Result<Json<ListResponse>> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    search_documents(
        &state,
        &app_id,
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
    Path((app_id, content_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<impl IntoResponse> {
    identity.require_app_read(&app_id)?;
    require_app(&state, &app_id).await?;
    let bytes = load_item_content(&state, &app_id, &content_id).await?;
    Ok((
        StatusCode::OK,
        [("content-type", "application/octet-stream")],
        bytes,
    ))
}

async fn load_item_content(state: &ServerState, app_id: &str, content_id: &str) -> Result<Vec<u8>> {
    state
        .store
        .load_content("content_chunk", app_id, content_id, MAX_STORED_ITEM_BYTES)
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
    app_id: &str,
    doc_kind: &str,
    list: ListQuery,
) -> Result<Json<ListResponse>> {
    let mut query = all([term("doc_kind", doc_kind), term("app_id", app_id)]);
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
        "replay_event" => response.hits.into_iter().map(replay_summary).collect(),
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

pub(crate) async fn create_webhook(
    State(state): State<Arc<ServerState>>,
    Path(app_id): Path<String>,
    Extension(identity): Extension<AccessIdentity>,
    Json(input): Json<CreateWebhook>,
) -> Result<(StatusCode, Json<CreatedWebhook>)> {
    identity.require_app_admin(&app_id)?;
    let created = state
        .config
        .create_webhook(&app_id, input, &Timestamp::now().to_string())
        .await?;
    Ok((StatusCode::CREATED, Json(created)))
}

pub(crate) async fn update_webhook(
    State(state): State<Arc<ServerState>>,
    Path((app_id, webhook_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
    Json(input): Json<UpdateWebhook>,
) -> Result<Json<WebhookView>> {
    identity.require_app_admin(&app_id)?;
    state
        .config
        .update_webhook(&app_id, &webhook_id, input, &Timestamp::now().to_string())
        .await
        .map(Json)
}

pub(crate) async fn delete_webhook(
    State(state): State<Arc<ServerState>>,
    Path((app_id, webhook_id)): Path<(String, String)>,
    Extension(identity): Extension<AccessIdentity>,
) -> Result<StatusCode> {
    identity.require_app_admin(&app_id)?;
    state
        .config
        .delete_webhook(&app_id, &webhook_id, &Timestamp::now().to_string())
        .await?;
    Ok(StatusCode::NO_CONTENT)
}

async fn require_app(state: &ServerState, app_id: &str) -> Result<()> {
    state
        .config
        .app(app_id)
        .await
        .map(|_| ())
        .ok_or(Error::NotFound)
}

async fn artifact_app(
    state: &ServerState,
    headers: &HeaderMap,
    org: &str,
    project: Option<&str>,
) -> Result<(TrackerConfig, crate::config::AppConfig)> {
    let tracker = state.config.tracker().await;
    if tracker.slug != org {
        return Err(Error::NotFound);
    }
    let hash = bearer_token_hash(headers).ok_or(Error::Authentication)?;
    let app = state
        .config
        .app_by_auth_token_hash(&hash)
        .await
        .ok_or(Error::Authentication)?;
    if project.is_some_and(|project| project != app.slug) {
        return Err(Error::NotFound);
    }
    Ok((tracker, app))
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

#[cfg(test)]
mod tests {
    use std::io::Write;

    use axum::body::{Body, to_bytes};
    use axum::http::{HeaderValue, Request};
    use tempfile::tempdir;
    use tower::ServiceExt;

    use super::*;
    use crate::config::{ApiTokenRole, CreateApiToken};

    async fn test_server(volume: PathBuf, admin_token: Option<String>, ui_enabled: bool) -> Server {
        Server::new(ServerOptions {
            listen: "127.0.0.1:0".parse().unwrap(),
            volume,
            public_url: "https://errors.example.com".into(),
            name: "Tracker".into(),
            slug: "tracker".into(),
            admin_token,
            ui_enabled,
            endpoint_file: None,
        })
        .await
        .unwrap()
    }

    #[test]
    fn historical_utf8_content_encoding_is_identity() {
        let mut headers = HeaderMap::new();
        headers.insert("content-encoding", HeaderValue::from_static("UTF-8"));

        assert_eq!(
            decode_body(&headers, Bytes::from_static(b"{\"message\":\"ok\"}")).unwrap(),
            b"{\"message\":\"ok\"}"
        );
    }

    #[test]
    fn decodes_plain_and_zlib_replay_recordings() {
        let body = br#"[{"type":4,"timestamp":1000},{"type":2,"timestamp":1001}]"#;
        let mut plain = br#"{"segment_id":0}"#.to_vec();
        plain.push(b'\n');
        plain.extend_from_slice(body);
        let (events, bytes) = decode_replay_recording(&plain, 1024).unwrap();
        assert_eq!(events.len(), 2);
        assert_eq!(bytes, body.len());

        let mut encoder =
            flate2::write::ZlibEncoder::new(Vec::new(), flate2::Compression::default());
        encoder.write_all(body).unwrap();
        let mut compressed = br#"{"segment_id":1}"#.to_vec();
        compressed.push(b'\n');
        compressed.extend_from_slice(&encoder.finish().unwrap());
        let (events, bytes) = decode_replay_recording(&compressed, 1024).unwrap();
        assert_eq!(events.len(), 2);
        assert_eq!(bytes, body.len());

        let mut oversized_header = vec![b' '; MAX_REPLAY_RECORDING_HEADER_BYTES + 1];
        oversized_header.push(b'\n');
        oversized_header.extend_from_slice(b"[]");
        assert!(decode_replay_recording(&oversized_header, 1024).is_err());
    }

    #[test]
    fn recognizes_sentry_cli_proguard_names() {
        let name = "/proguard/AABBCCDD-1122-3344-5566-778899AABBCC.txt";

        assert_eq!(symbol_type(name), "proguard");
        assert_eq!(
            proguard_debug_id_from_name(name).as_deref(),
            Some("aabbccdd-1122-3344-5566-778899aabbcc")
        );
        assert_eq!(symbol_type("notes.txt"), "elf");
        assert!(proguard_debug_id_from_name("notes.txt").is_none());
    }

    #[tokio::test]
    async fn optional_admin_auth_and_ui_override_are_independent() {
        let open_directory = tempdir().unwrap();
        let open = test_server(open_directory.path().into(), None, false).await;
        let response = open
            .router()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/tracker")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);

        let response = open
            .router()
            .oneshot(Request::builder().uri("/").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::NOT_FOUND);

        let protected_directory = tempdir().unwrap();
        let protected = test_server(
            protected_directory.path().into(),
            Some("0123456789abcdef0123456789abcdef".into()),
            true,
        )
        .await;
        let response = protected
            .router()
            .oneshot(
                Request::builder()
                    .uri("/api/v1/tracker")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);

        let response = protected
            .router()
            .oneshot(Request::builder().uri("/").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);

        let open_ui_directory = tempdir().unwrap();
        let open_ui = test_server(open_ui_directory.path().into(), None, true).await;
        let response = open_ui
            .router()
            .oneshot(
                Request::builder()
                    .uri("/assets/index.js")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let script = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let script = std::str::from_utf8(&script).unwrap();
        assert!(script.contains("/api/v1/tracker"));
        assert!(script.contains("error-tracker-admin-token"));
    }

    #[tokio::test]
    async fn rejects_unusable_startup_paths_and_admin_tokens() {
        let invalid_volume = Server::new(ServerOptions {
            listen: "127.0.0.1:0".parse().unwrap(),
            volume: PathBuf::new(),
            public_url: "https://errors.example.com".into(),
            name: "Tracker".into(),
            slug: "tracker".into(),
            admin_token: None,
            ui_enabled: false,
            endpoint_file: None,
        })
        .await;
        assert!(matches!(invalid_volume, Err(Error::Configuration(_))));

        let directory = tempdir().unwrap();
        let invalid_token = Server::new(ServerOptions {
            listen: "127.0.0.1:0".parse().unwrap(),
            volume: directory.path().into(),
            public_url: "https://errors.example.com".into(),
            name: "Tracker".into(),
            slug: "tracker".into(),
            admin_token: Some(" ".repeat(24)),
            ui_enabled: false,
            endpoint_file: None,
        })
        .await;
        assert!(matches!(invalid_token, Err(Error::Configuration(_))));
    }

    #[test]
    fn search_query_limit_counts_characters() {
        assert!(search_text(Some(&"я".repeat(MAX_SEARCH_QUERY_CHARS))).is_ok());
        assert!(search_text(Some(&"я".repeat(MAX_SEARCH_QUERY_CHARS + 1))).is_err());
    }

    #[test]
    fn ignored_issues_stay_ignored_when_new_events_arrive() {
        assert_eq!(issue_status_after_event("ignored"), ("ignored", false));
        assert_eq!(issue_status_after_event("resolved"), ("open", true));
    }

    #[test]
    fn event_summaries_do_not_copy_the_full_payload() {
        let summary = event_summary(json!({
            "event_id": "event",
            "title": "Failure",
            "payload": { "large": "content" }
        }));

        assert_eq!(summary["title"], "Failure");
        assert!(summary.get("payload").is_none());
    }

    #[test]
    fn replay_summaries_keep_only_sdk_metadata() {
        let summary = replay_summary(json!({
            "replay_id": "replay",
            "payload": {
                "sdk": { "name": "sentry.javascript.browser" },
                "large": "content"
            }
        }));

        assert_eq!(
            summary.pointer("/payload/sdk/name"),
            Some(&json!("sentry.javascript.browser"))
        );
        assert!(summary.pointer("/payload/large").is_none());
    }

    #[tokio::test]
    async fn startup_recovers_pending_symbolication_from_durable_events() {
        let directory = tempdir().unwrap();
        let config = ConfigRepository::open(
            directory.path().join("config.json"),
            "Tracker".into(),
            "tracker".into(),
            "https://errors.example.com".into(),
        )
        .await
        .unwrap();
        let created = config
            .create_app(
                CreateApp {
                    name: "Web".into(),
                    slug: "web".into(),
                },
                "2026-08-09T10:00:00Z",
            )
            .await
            .unwrap();
        let timestamp = "2026-08-09T10:01:00Z";
        let mut event = Document::base(
            "event",
            &created.app.id,
            &created.app.project_id,
            timestamp,
            timestamp,
        );
        event.event_id = Some("pending-event".into());
        event.issue_id = Some("pending-issue".into());
        event.title = Some("Pending event".into());
        event.level = Some("error".into());
        event.platform = Some("javascript".into());
        event.payload = Some(json!({
            "platform": "javascript",
            "message": "pending symbolication"
        }));
        let store = Store::open(directory.path().to_owned()).await.unwrap();
        store.ingest(vec![event]).await.unwrap();
        drop(store);
        drop(config);

        let server = test_server(directory.path().into(), None, false).await;
        let recovered = tokio::time::timeout(std::time::Duration::from_secs(2), async {
            loop {
                if server
                    .state
                    .store
                    .exists(all([
                        term("doc_kind", "symbolication"),
                        term("event_id", "pending-event"),
                    ]))
                    .await
                    .unwrap()
                {
                    break;
                }
                tokio::time::sleep(std::time::Duration::from_millis(10)).await;
            }
        })
        .await;

        assert!(recovered.is_ok());
    }

    #[tokio::test]
    async fn public_token_enforces_app_scope_for_rest_and_serves_mcp() {
        let directory = tempdir().unwrap();
        let server = test_server(directory.path().into(), None, false).await;
        let first = server
            .state
            .config
            .create_app(
                CreateApp {
                    name: "First".into(),
                    slug: "first".into(),
                },
                "2026-08-09T10:00:00Z",
            )
            .await
            .unwrap();
        let second = server
            .state
            .config
            .create_app(
                CreateApp {
                    name: "Second".into(),
                    slug: "second".into(),
                },
                "2026-08-09T10:01:00Z",
            )
            .await
            .unwrap();
        let credential = server
            .state
            .config
            .create_api_token(
                CreateApiToken {
                    name: "First reader".into(),
                    role: ApiTokenRole::Read,
                    app_id: Some(first.app.id.clone()),
                },
                "2026-08-09T10:02:00Z",
            )
            .await
            .unwrap();
        let authorization = format!("Bearer {}", credential.token);
        let admin_credential = server
            .state
            .config
            .create_api_token(
                CreateApiToken {
                    name: "Automation admin".into(),
                    role: ApiTokenRole::Admin,
                    app_id: None,
                },
                "2026-08-09T10:03:00Z",
            )
            .await
            .unwrap();

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .uri("/public/api/v1/apps")
                    .header("authorization", &authorization)
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let apps: Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(apps.as_array().map(Vec::len), Some(1));
        assert_eq!(apps.pointer("/0/id"), Some(&json!(first.app.id)));

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .uri(format!("/public/api/v1/apps/{}", second.app.id))
                    .header("authorization", &authorization)
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::FORBIDDEN);

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::PATCH)
                    .uri("/public/api/v1/tracker")
                    .header(
                        "authorization",
                        format!("Bearer {}", admin_credential.token),
                    )
                    .header("content-type", "application/json")
                    .body(Body::from(r#"{"publicUrl":"https://public.example.com"}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/public/mcp")
                    .header("authorization", authorization)
                    .header("accept", "application/json, text/event-stream")
                    .header("content-type", "application/json")
                    .body(Body::from(
                        r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}"#,
                    ))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let body = to_bytes(response.into_body(), 1 << 20).await.unwrap();
        let message: Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(
            message.pointer("/result/serverInfo/name"),
            Some(&json!("error-tracker"))
        );

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/public/mcp")
                    .header("authorization", format!("Bearer {}", credential.token))
                    .header("accept", "application/json, text/event-stream")
                    .header("content-type", "application/json")
                    .header("mcp-protocol-version", "2025-11-25")
                    .body(Body::from(r#"{"jsonrpc":"2.0","method":"ping"}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert!(
            to_bytes(response.into_body(), 1 << 20)
                .await
                .unwrap()
                .is_empty()
        );

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/public/mcp")
                    .header("authorization", format!("Bearer {}", credential.token))
                    .header("accept", "application/json, text/event-stream")
                    .header("content-type", "application/json")
                    .body(Body::from(vec![b' '; crate::mcp::MAX_BODY_BYTES + 1]))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    }

    #[tokio::test]
    async fn rejects_empty_envelopes_and_deduplicates_event_ids() {
        let directory = tempdir().unwrap();
        let server = test_server(directory.path().into(), None, false).await;
        let created = server
            .state
            .config
            .create_app(
                CreateApp {
                    name: "Web".into(),
                    slug: "web".into(),
                },
                "2026-08-09T10:00:00Z",
            )
            .await
            .unwrap();
        let uri = format!(
            "/api/{}/envelope/?sentry_key={}",
            created.app.project_id, created.app.public_key
        );

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri(&uri)
                    .body(Body::from("{}\n"))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);

        let envelope = |event_id: &str, timestamp: &str| {
            let payload = format!(
                r#"{{"event_id":"{event_id}","platform":"javascript","message":"duplicate","timestamp":"{timestamp}"}}"#
            );
            format!(
                "{{\"event_id\":\"{event_id}\"}}\n{{\"type\":\"event\",\"length\":{}}}\n{}",
                payload.len(),
                payload
            )
        };
        let event_id = "abcdefabcdefabcdefabcdefabcdefab";
        let body = envelope(event_id, "2026-08-09T10:00:00Z");
        for _ in 0..2 {
            let response = server
                .router()
                .oneshot(
                    Request::builder()
                        .method(Method::POST)
                        .uri(&uri)
                        .body(Body::from(body.clone()))
                        .unwrap(),
                )
                .await
                .unwrap();
            assert_eq!(response.status(), StatusCode::OK);
        }

        let events = server
            .state
            .store
            .search(SearchRequest {
                query: all([
                    term("doc_kind", "event"),
                    term("app_id", &created.app.id),
                    term("event_id", event_id),
                ]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(events.num_hits, 1);

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .uri(format!(
                        "/api/v1/apps/{}/events/ABCDEFAB-CDEF-ABCD-EFAB-CDEFABCDEFAB",
                        created.app.id
                    ))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);

        let mixed_event_id = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";
        let duplicate_payload = format!(
            r#"{{"event_id":"{event_id}","platform":"javascript","message":"duplicate","timestamp":"2026-08-09T10:00:00Z"}}"#
        );
        let mixed_payload = format!(
            r#"{{"event_id":"{mixed_event_id}","platform":"javascript","message":"duplicate","timestamp":"2026-08-09T10:00:00.9Z"}}"#
        );
        let mixed_envelope = format!(
            "{{}}\n{{\"type\":\"event\",\"length\":{}}}\n{}\n{{\"type\":\"event\",\"length\":{}}}\n{}",
            duplicate_payload.len(),
            duplicate_payload,
            mixed_payload.len(),
            mixed_payload
        );
        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri(&uri)
                    .body(Body::from(mixed_envelope))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);

        let older_event_id = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri(&uri)
                    .body(Body::from(envelope(older_event_id, "2026-08-09T09:00:00Z")))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);

        let issues = server
            .state
            .store
            .search(SearchRequest {
                query: all([term("doc_kind", "issue"), term("app_id", &created.app.id)]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(issues.num_hits, 1);
        assert_eq!(issues.hits[0]["event_count"], 3);
        assert_eq!(issues.hits[0]["first_seen"], "2026-08-09T09:00:00Z");
        assert_eq!(issues.hits[0]["timestamp"], "2026-08-09T10:00:00.9Z");
        assert_eq!(issues.hits[0]["event_id"], mixed_event_id);
    }

    #[tokio::test]
    async fn accepts_envelopes_larger_than_axums_default_body_limit() {
        let directory = tempdir().unwrap();
        let server = test_server(directory.path().into(), None, false).await;
        let created = server
            .state
            .config
            .create_app(
                CreateApp {
                    name: "Web".into(),
                    slug: "web".into(),
                },
                "2026-08-09T10:00:00Z",
            )
            .await
            .unwrap();
        let event_id = "0123456789abcdef0123456789abcdef";
        let payload = serde_json::to_vec(&json!({
            "event_id": event_id,
            "platform": "other",
            "message": "large envelope",
            "extra": { "padding": "x".repeat((2 << 20) + 1024) }
        }))
        .unwrap();
        let mut body = format!(
            "{{\"event_id\":\"{event_id}\"}}\n{{\"type\":\"event\",\"length\":{}}}\n",
            payload.len()
        )
        .into_bytes();
        body.extend_from_slice(&payload);
        assert!(body.len() > 2 << 20);

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri(format!(
                        "/api/{}/envelope/?sentry_key={}",
                        created.app.project_id, created.app.public_key
                    ))
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert!(
            server
                .state
                .store
                .exists(all([
                    term("doc_kind", "event"),
                    term("app_id", &created.app.id),
                    term("event_id", event_id),
                ]))
                .await
                .unwrap()
        );
    }

    #[tokio::test]
    async fn accepts_a_full_sized_artifact_chunk() {
        let directory = tempdir().unwrap();
        let server = test_server(directory.path().into(), None, false).await;
        let created = server
            .state
            .config
            .create_app(
                CreateApp {
                    name: "Web".into(),
                    slug: "web".into(),
                },
                "2026-08-09T10:00:00Z",
            )
            .await
            .unwrap();
        let chunk = vec![b'x'; artifact::CHUNK_BYTES];
        let checksum = artifact::sha1_hex(&chunk);
        let boundary = "error-tracker-full-chunk";
        let mut multipart = format!(
            "--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{checksum}\"\r\nContent-Type: application/octet-stream\r\n\r\n"
        )
        .into_bytes();
        multipart.extend_from_slice(&chunk);
        multipart.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/api/0/organizations/tracker/chunk-upload/")
                    .header("authorization", format!("Bearer {}", created.auth_token))
                    .header(
                        "content-type",
                        format!("multipart/form-data; boundary={boundary}"),
                    )
                    .body(Body::from(multipart))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::CREATED);
        assert!(
            server
                .state
                .store
                .exists(all([
                    term("doc_kind", "upload_chunk"),
                    term("app_id", &created.app.id),
                    term("content_id", &checksum),
                ]))
                .await
                .unwrap()
        );
    }

    #[tokio::test]
    async fn invalid_minidump_is_retained_as_a_failed_event() {
        let directory = tempdir().unwrap();
        let server = test_server(directory.path().into(), None, false).await;
        let created = server
            .state
            .config
            .create_app(
                CreateApp {
                    name: "Native".into(),
                    slug: "native".into(),
                },
                "2026-08-09T10:00:00Z",
            )
            .await
            .unwrap();
        let raw = b"not a minidump";
        let boundary = "error-tracker-test-boundary";
        let mut multipart = format!(
            "--{boundary}\r\nContent-Disposition: form-data; name=\"upload_file_minidump\"; filename=\"crash.dmp\"\r\nContent-Type: application/octet-stream\r\n\r\n"
        )
        .into_bytes();
        multipart.extend_from_slice(raw);
        multipart.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());

        let response = server
            .router()
            .oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri(format!(
                        "/api/{}/minidump/?sentry_key={}",
                        created.app.project_id, created.app.public_key
                    ))
                    .header(
                        "content-type",
                        format!("multipart/form-data; boundary={boundary}"),
                    )
                    .body(Body::from(multipart))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);

        let events = server
            .state
            .store
            .search_all(
                all([term("doc_kind", "event"), term("app_id", &created.app.id)]),
                "timestamp",
            )
            .await
            .unwrap();
        assert_eq!(events.len(), 1);
        assert_eq!(
            events[0].pointer("/payload/contexts/symbolicator/status"),
            Some(&json!("failed"))
        );

        let attachments = server
            .state
            .store
            .search_all(
                all([
                    term("doc_kind", "attachment"),
                    term("app_id", &created.app.id),
                ]),
                "timestamp",
            )
            .await
            .unwrap();
        assert_eq!(attachments.len(), 1);
        let content_id = attachments[0]["content_id"].as_str().unwrap();
        assert_eq!(
            load_item_content(&server.state, &created.app.id, content_id)
                .await
                .unwrap(),
            raw
        );
    }
}
