use crate::{
    bucket::physical_bucket_name,
    s3_backend::S3Backend,
    traffic::{self, TrafficRegistry},
};
use async_trait::async_trait;
use http::{HeaderMap, HeaderValue, Method, Request, Response, StatusCode};
use hyper::{body::Incoming, service::service_fn};
use hyper_util::rt::TokioIo;
use rustfs_ecstore::api::storage::ECStore;
use s3s::{
    Body,
    auth::{S3Auth, SecretKey},
    service::{S3Service, S3ServiceBuilder},
};
use serde::Deserialize;
use socket2::{Domain, Protocol, SockAddr, Socket, Type};
use std::{
    collections::{HashMap, HashSet},
    net::SocketAddr,
    sync::{Arc, Mutex as StdMutex, RwLock as StdRwLock},
    time::Instant,
};
use tokio::{
    net::TcpListener,
    sync::{Mutex, Notify, RwLock, Semaphore},
    task::{JoinHandle, JoinSet},
};
use tokio_util::sync::CancellationToken;

type BoxError = Box<dyn std::error::Error + Send + Sync>;
const MAX_DATA_PLANE_CONNECTIONS: usize = 1024;

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DataPlaneStore {
    pub store_id: String,
    pub bucket_name: String,
    pub access_key: String,
    pub secret: String,
    pub permission: String,
    #[serde(default)]
    pub cors_origins: Vec<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProjectConfig {
    pub project_id: String,
    pub listen_address: String,
    pub stores: Vec<DataPlaneStore>,
}

struct ConfiguredStore {
    store_id: Box<str>,
    bucket_name: Box<str>,
    physical_bucket: Box<str>,
    secret: Box<str>,
    read_write: bool,
    gate: Arc<StoreGate>,
}

#[derive(Clone)]
pub struct ResolvedStore(Arc<ConfiguredStore>);

struct ProjectStores {
    by_access_key: HashMap<String, Arc<ConfiguredStore>>,
    by_logical_bucket: HashMap<String, Arc<ConfiguredStore>>,
    cors_by_bucket: HashMap<String, Arc<[String]>>,
    store_ids: Vec<String>,
}

pub struct ProjectState {
    stores: StdRwLock<ProjectStores>,
    gates: Arc<RwLock<HashMap<String, Arc<StoreGate>>>>,
}

impl ProjectState {
    async fn new(
        stores: Vec<DataPlaneStore>,
        gates: Arc<RwLock<HashMap<String, Arc<StoreGate>>>>,
    ) -> Result<Self, String> {
        let stores = resolve_stores(stores, &gates).await?;
        Ok(Self {
            stores: StdRwLock::new(stores),
            gates,
        })
    }

    async fn replace(&self, stores: Vec<DataPlaneStore>) -> Result<(), String> {
        let stores = resolve_stores(stores, &self.gates).await?;
        let retained: HashSet<String> = stores.store_ids.iter().cloned().collect();
        let removed = {
            let mut current = self
                .stores
                .write()
                .map_err(|_| "project store snapshot is poisoned")?;
            let removed = current
                .store_ids
                .iter()
                .filter(|store_id| !retained.contains(*store_id))
                .cloned()
                .collect::<Vec<_>>();
            *current = stores;
            removed
        };
        if !removed.is_empty() {
            self.gates
                .write()
                .await
                .retain(|store_id, _| !removed.contains(store_id));
        }
        Ok(())
    }

    pub fn store_for_access_key(&self, access_key: &str) -> Option<ResolvedStore> {
        self.stores
            .read()
            .expect("project store snapshot poisoned")
            .by_access_key
            .get(access_key)
            .cloned()
            .map(ResolvedStore)
    }

    fn secret_for_access_key(&self, access_key: &str) -> Option<SecretKey> {
        self.stores
            .read()
            .expect("project store snapshot poisoned")
            .by_access_key
            .get(access_key)
            .map(|store| SecretKey::from(store.secret.as_ref()))
    }

    fn cors_origins(&self, bucket: &str) -> Option<Arc<[String]>> {
        self.stores
            .read()
            .expect("project store snapshot poisoned")
            .cors_by_bucket
            .get(bucket)
            .cloned()
    }

    pub fn physical_bucket_for_logical(&self, bucket: &str) -> Option<String> {
        self.stores
            .read()
            .expect("project store snapshot poisoned")
            .by_logical_bucket
            .get(bucket)
            .map(|store| store.physical_bucket.to_string())
    }

    fn store_ids(&self) -> Vec<String> {
        self.stores
            .read()
            .expect("project store snapshot poisoned")
            .store_ids
            .clone()
    }
}

impl ResolvedStore {
    pub fn store_id(&self) -> &str {
        &self.0.store_id
    }

    pub fn bucket_name(&self) -> &str {
        &self.0.bucket_name
    }

    pub fn physical_bucket(&self) -> &str {
        &self.0.physical_bucket
    }

    pub fn can_write(&self) -> bool {
        self.0.read_write
    }

    pub async fn enter(&self, write: bool) -> s3s::S3Result<RequestLease> {
        self.0.gate.enter(write).await
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum MaintenanceMode {
    Backup,
    Restore,
}

#[derive(Clone, Copy, Default, PartialEq, Eq)]
enum GateMode {
    #[default]
    Normal,
    Backup,
    Restore,
}

#[derive(Default)]
struct GateState {
    mode: GateMode,
    active_requests: usize,
    active_writes: usize,
}

#[derive(Default)]
struct StoreGate {
    state: StdMutex<GateState>,
    changed: Notify,
}

impl StoreGate {
    async fn enter(self: &Arc<Self>, write: bool) -> s3s::S3Result<RequestLease> {
        loop {
            let notified = self.changed.notified();
            {
                let mut state = self.state.lock().expect("object store gate mutex poisoned");
                match state.mode {
                    GateMode::Normal => {
                        state.active_requests += 1;
                        if write {
                            state.active_writes += 1;
                        }
                        return Ok(RequestLease {
                            gate: self.clone(),
                            write,
                        });
                    }
                    GateMode::Backup if !write => {
                        state.active_requests += 1;
                        return Ok(RequestLease {
                            gate: self.clone(),
                            write,
                        });
                    }
                    GateMode::Backup => {}
                    GateMode::Restore => {
                        return Err(s3s::s3_error!(
                            ServiceUnavailable,
                            "object store restore is in progress"
                        ));
                    }
                }
            }
            notified.await;
        }
    }

    async fn begin(&self, mode: MaintenanceMode) -> Result<(), String> {
        {
            let mut state = self
                .state
                .lock()
                .map_err(|_| "object store gate mutex poisoned")?;
            if state.mode != GateMode::Normal {
                return Err("object store is already in maintenance".to_owned());
            }
            state.mode = match mode {
                MaintenanceMode::Backup => GateMode::Backup,
                MaintenanceMode::Restore => GateMode::Restore,
            };
        }
        self.changed.notify_waiters();
        let mut rollback = MaintenanceRollback {
            gate: self,
            mode,
            armed: true,
        };
        loop {
            let notified = self.changed.notified();
            let drained = {
                let state = self
                    .state
                    .lock()
                    .map_err(|_| "object store gate mutex poisoned")?;
                match mode {
                    MaintenanceMode::Backup => state.active_writes == 0,
                    MaintenanceMode::Restore => state.active_requests == 0,
                }
            };
            if drained {
                rollback.armed = false;
                return Ok(());
            }
            notified.await;
        }
    }

    fn end(&self, mode: MaintenanceMode) -> Result<(), String> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| "object store gate mutex poisoned")?;
        let expected = match mode {
            MaintenanceMode::Backup => GateMode::Backup,
            MaintenanceMode::Restore => GateMode::Restore,
        };
        if state.mode != expected {
            return Err("object store maintenance mode does not match".to_owned());
        }
        state.mode = GateMode::Normal;
        drop(state);
        self.changed.notify_waiters();
        Ok(())
    }
}

struct MaintenanceRollback<'a> {
    gate: &'a StoreGate,
    mode: MaintenanceMode,
    armed: bool,
}

impl Drop for MaintenanceRollback<'_> {
    fn drop(&mut self) {
        if !self.armed {
            return;
        }
        let expected = match self.mode {
            MaintenanceMode::Backup => GateMode::Backup,
            MaintenanceMode::Restore => GateMode::Restore,
        };
        if let Ok(mut state) = self.gate.state.lock()
            && state.mode == expected
        {
            state.mode = GateMode::Normal;
        }
        self.gate.changed.notify_waiters();
    }
}

pub struct RequestLease {
    gate: Arc<StoreGate>,
    write: bool,
}

impl Drop for RequestLease {
    fn drop(&mut self) {
        let notify = if let Ok(mut state) = self.gate.state.lock() {
            state.active_requests -= 1;
            if self.write {
                state.active_writes -= 1;
            }
            matches!(state.mode, GateMode::Restore)
                || self.write && matches!(state.mode, GateMode::Backup)
        } else {
            false
        };
        if notify {
            self.gate.changed.notify_waiters();
        }
    }
}

#[derive(Clone)]
struct ProjectAuth {
    project: Arc<ProjectState>,
}

#[async_trait]
impl S3Auth for ProjectAuth {
    async fn get_secret_key(&self, access_key: &str) -> s3s::S3Result<SecretKey> {
        self.project
            .secret_for_access_key(access_key)
            .ok_or_else(|| s3s::s3_error!(InvalidAccessKeyId))
    }
}

struct Endpoint {
    address: SocketAddr,
    project: Arc<ProjectState>,
    cancel: CancellationToken,
    task: JoinHandle<()>,
}

#[derive(Clone)]
pub struct DataPlane {
    store: Arc<ECStore>,
    traffic: Arc<TrafficRegistry>,
    endpoints: Arc<Mutex<HashMap<String, Endpoint>>>,
    gates: Arc<RwLock<HashMap<String, Arc<StoreGate>>>>,
    connections: Arc<Semaphore>,
}

impl DataPlane {
    pub fn new(store: Arc<ECStore>, traffic: Arc<TrafficRegistry>) -> Self {
        Self {
            store,
            traffic,
            endpoints: Arc::new(Mutex::new(HashMap::new())),
            gates: Arc::new(RwLock::new(HashMap::new())),
            connections: Arc::new(Semaphore::new(MAX_DATA_PLANE_CONNECTIONS)),
        }
    }

    pub async fn configure(&self, config: ProjectConfig) -> Result<(), String> {
        if config.project_id.is_empty() {
            return Err("project ID is required".to_owned());
        }
        let address: SocketAddr = config
            .listen_address
            .parse()
            .map_err(|error| format!("listen address is invalid: {error}"))?;

        let mut endpoints = self.endpoints.lock().await;
        if let Some(endpoint) = endpoints.get(&config.project_id)
            && endpoint.address == address
        {
            endpoint.project.replace(config.stores).await?;
            return Ok(());
        }

        let project = Arc::new(ProjectState::new(config.stores, self.gates.clone()).await?);
        let listener = bind_project_listener(address)?;
        let cancel = CancellationToken::new();
        let task = tokio::spawn(serve_project(
            listener,
            self.store.clone(),
            project.clone(),
            self.traffic.clone(),
            cancel.clone(),
            self.connections.clone(),
        ));
        let previous = endpoints.insert(
            config.project_id,
            Endpoint {
                address,
                project,
                cancel,
                task,
            },
        );
        drop(endpoints);
        if let Some(previous) = previous {
            stop_endpoint(previous).await;
        }
        Ok(())
    }

    pub async fn remove(&self, project_id: &str) {
        if let Some(endpoint) = self.endpoints.lock().await.remove(project_id) {
            let store_ids = endpoint.project.store_ids();
            stop_endpoint(endpoint).await;
            self.gates
                .write()
                .await
                .retain(|store_id, _| !store_ids.contains(store_id));
        }
    }

    pub async fn begin_store_maintenance(
        &self,
        store_id: &str,
        mode: MaintenanceMode,
    ) -> Result<(), String> {
        self.store_gate(store_id).await.begin(mode).await
    }

    pub async fn end_store_maintenance(
        &self,
        store_id: &str,
        mode: MaintenanceMode,
    ) -> Result<(), String> {
        let gate = self
            .gates
            .read()
            .await
            .get(store_id)
            .cloned()
            .ok_or_else(|| "object store data-plane gate was not found".to_owned())?;
        gate.end(mode)
    }

    pub async fn begin_quiesce(&self) -> Result<(), String> {
        let gates = self.all_gates().await;
        let mut acquired = GateBatchRollback {
            gates: Vec::with_capacity(gates.len()),
            armed: true,
        };
        for gate in gates {
            gate.begin(MaintenanceMode::Backup).await?;
            acquired.gates.push(gate);
        }
        acquired.armed = false;
        Ok(())
    }

    pub async fn end_quiesce(&self) -> Result<(), String> {
        let gates = self.all_gates().await;
        let mut failure = None;
        for gate in gates {
            if let Err(error) = gate.end(MaintenanceMode::Backup) {
                failure.get_or_insert(error);
            }
        }
        failure.map_or(Ok(()), Err)
    }

    async fn all_gates(&self) -> Vec<Arc<StoreGate>> {
        self.gates.read().await.values().cloned().collect()
    }

    async fn store_gate(&self, store_id: &str) -> Arc<StoreGate> {
        if let Some(gate) = self.gates.read().await.get(store_id).cloned() {
            return gate;
        }
        self.gates
            .write()
            .await
            .entry(store_id.to_owned())
            .or_insert_with(|| Arc::new(StoreGate::default()))
            .clone()
    }

    pub async fn shutdown(&self) {
        let endpoints = std::mem::take(&mut *self.endpoints.lock().await);
        for endpoint in endpoints.into_values() {
            stop_endpoint(endpoint).await;
        }
    }
}

fn bind_project_listener(address: SocketAddr) -> Result<TcpListener, String> {
    let socket = Socket::new(
        Domain::for_address(address),
        Type::STREAM,
        Some(Protocol::TCP),
    )
    .map_err(|error| format!("create project S3 socket: {error}"))?;
    // Netavark materializes a project bridge on its first attached container,
    // while the S3 endpoint must be ready as soon as the project is created.
    #[cfg(target_os = "linux")]
    match address {
        SocketAddr::V4(_) => socket.set_freebind_v4(true),
        SocketAddr::V6(_) => socket.set_freebind_v6(true),
    }
    .map_err(|error| format!("enable project S3 freebind: {error}"))?;
    socket
        .set_nonblocking(true)
        .map_err(|error| format!("make project S3 socket nonblocking: {error}"))?;
    socket
        .bind(&SockAddr::from(address))
        .map_err(|error| format!("bind project S3 endpoint: {error}"))?;
    let backlog = i32::try_from(MAX_DATA_PLANE_CONNECTIONS)
        .map_err(|_| "project S3 listen backlog is too large".to_owned())?;
    socket
        .listen(backlog)
        .map_err(|error| format!("listen on project S3 endpoint: {error}"))?;
    TcpListener::from_std(socket.into())
        .map_err(|error| format!("register project S3 listener: {error}"))
}

async fn ensure_store_gates(
    gates: &RwLock<HashMap<String, Arc<StoreGate>>>,
    stores: &HashMap<String, DataPlaneStore>,
) {
    let mut gates = gates.write().await;
    for store in stores.values() {
        gates
            .entry(store.store_id.clone())
            .or_insert_with(|| Arc::new(StoreGate::default()));
    }
}

async fn resolve_stores(
    stores: Vec<DataPlaneStore>,
    gates: &RwLock<HashMap<String, Arc<StoreGate>>>,
) -> Result<ProjectStores, String> {
    let stores = validate_stores(stores)?;
    ensure_store_gates(gates, &stores).await;
    let gates = gates.read().await;
    let mut by_access_key = HashMap::with_capacity(stores.len());
    let mut by_logical_bucket = HashMap::with_capacity(stores.len());
    let mut cors_by_bucket = HashMap::with_capacity(stores.len());
    let mut store_ids = Vec::with_capacity(stores.len());

    for (access_key, store) in stores {
        let gate = gates
            .get(&store.store_id)
            .cloned()
            .ok_or_else(|| "object store data-plane gate was not found".to_owned())?;
        let store_id = store.store_id;
        let physical_bucket = physical_bucket_name(&store_id)?.into_boxed_str();
        cors_by_bucket.insert(
            store.bucket_name.clone(),
            Arc::<[String]>::from(store.cors_origins),
        );
        store_ids.push(store_id.clone());
        let configured = Arc::new(ConfiguredStore {
            store_id: store_id.into_boxed_str(),
            bucket_name: store.bucket_name.clone().into_boxed_str(),
            physical_bucket,
            secret: store.secret.into_boxed_str(),
            read_write: store.permission == "read_write",
            gate,
        });
        by_logical_bucket.insert(store.bucket_name, configured.clone());
        by_access_key.insert(access_key, configured);
    }

    Ok(ProjectStores {
        by_access_key,
        by_logical_bucket,
        cors_by_bucket,
        store_ids,
    })
}

struct GateBatchRollback {
    gates: Vec<Arc<StoreGate>>,
    armed: bool,
}

impl Drop for GateBatchRollback {
    fn drop(&mut self) {
        if self.armed {
            for gate in &self.gates {
                let _ = gate.end(MaintenanceMode::Backup);
            }
        }
    }
}

fn validate_stores(stores: Vec<DataPlaneStore>) -> Result<HashMap<String, DataPlaneStore>, String> {
    let mut result = HashMap::with_capacity(stores.len());
    let mut buckets = HashSet::with_capacity(stores.len());
    for store in stores {
        if store.store_id.is_empty()
            || store.bucket_name.is_empty()
            || store.access_key.is_empty()
            || store.secret.is_empty()
            || (store.permission != "read" && store.permission != "read_write")
        {
            return Err("data-plane store configuration is incomplete".to_owned());
        }
        if !buckets.insert(store.bucket_name.clone()) {
            return Err("duplicate data-plane bucket name".to_owned());
        }
        if result.insert(store.access_key.clone(), store).is_some() {
            return Err("duplicate data-plane access key".to_owned());
        }
    }
    Ok(result)
}

async fn serve_project(
    listener: TcpListener,
    store: Arc<ECStore>,
    project: Arc<ProjectState>,
    traffic: Arc<TrafficRegistry>,
    cancel: CancellationToken,
    connection_limit: Arc<Semaphore>,
) {
    let mut builder = S3ServiceBuilder::new(S3Backend::new(store, project.clone()));
    builder.set_auth(ProjectAuth {
        project: project.clone(),
    });
    let service = builder.build();

    let mut connections = JoinSet::new();
    loop {
        let accepted = tokio::select! {
            _ = cancel.cancelled() => break,
            completed = connections.join_next(), if !connections.is_empty() => {
                if let Some(Err(error)) = completed {
                    eprintln!("objectstore S3 task: {error}");
                }
                continue;
            }
            accepted = listener.accept() => accepted,
        };
        let (connection, _) = match accepted {
            Ok(value) => value,
            Err(error) => {
                eprintln!("objectstore S3 accept: {error}");
                continue;
            }
        };
        if let Err(error) = connection.set_nodelay(true) {
            eprintln!("objectstore S3 TCP_NODELAY: {error}");
        }
        let Ok(permit) = connection_limit.clone().try_acquire_owned() else {
            drop(connection);
            continue;
        };
        let service = service.clone();
        let project = project.clone();
        let traffic = traffic.clone();
        connections.spawn(async move {
            let _permit = permit;
            let handler = service_fn(move |request| {
                serve_s3_request(service.clone(), project.clone(), traffic.clone(), request)
            });
            if let Err(error) = hyper::server::conn::http1::Builder::new()
                .keep_alive(true)
                .serve_connection(TokioIo::new(connection), handler)
                .await
            {
                eprintln!("objectstore S3 connection: {error}");
            }
        });
    }
    connections.abort_all();
    while connections.join_next().await.is_some() {}
}

async fn stop_endpoint(endpoint: Endpoint) {
    endpoint.cancel.cancel();
    if let Err(error) = endpoint.task.await
        && !error.is_cancelled()
    {
        eprintln!("objectstore S3 endpoint: {error}");
    }
}

async fn serve_s3_request(
    service: S3Service,
    project: Arc<ProjectState>,
    traffic: Arc<TrafficRegistry>,
    request: Request<Incoming>,
) -> Result<Response<Body>, BoxError> {
    let origin = request
        .headers()
        .get("origin")
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    let logical_bucket = request
        .uri()
        .path()
        .trim_start_matches('/')
        .split('/')
        .next()
        .unwrap_or("")
        .to_owned();
    let cors_origins = origin
        .as_deref()
        .and_then(|_| project.cors_origins(&logical_bucket));

    if request.method() == Method::OPTIONS {
        return Ok(cors_preflight(
            request.headers(),
            origin.as_deref(),
            cors_origins.as_deref(),
        ));
    }

    let physical_bucket = project.physical_bucket_for_logical(&logical_bucket);
    // Only attribute traffic to known logical→physical mappings. Unknown path
    // prefixes use ephemeral counters so probes cannot grow the registry or
    // alias onto a real physical bucket name from the URL.
    let traffic = match physical_bucket.as_deref() {
        Some(bucket) => traffic.for_bucket(bucket),
        None => Arc::new(traffic::TrafficCounters::default()),
    };
    let method = request.method().clone();
    let op = traffic::classify_s3(&method, request.uri().path());
    let bytes_in = request_payload_bytes(request.headers()).unwrap_or(0);
    traffic.record_start();
    let started = Instant::now();
    let result = hyper::service::Service::call(&service, request)
        .await
        .map_err(|error| -> BoxError { error.into() });
    match result {
        Ok(mut response) => {
            response.headers_mut().insert(
                "cache-control",
                HeaderValue::from_static("private, no-store"),
            );
            response.headers_mut().insert(
                "cloudflare-cdn-cache-control",
                HeaderValue::from_static("no-store"),
            );
            if let (Some(origin), Some(origins)) = (origin.as_deref(), cors_origins.as_deref())
                && origins.iter().any(|allowed| allowed == origin)
            {
                apply_cors(response.headers_mut(), origin);
            }
            let error = !response.status().is_success();
            // HEAD advertises Content-Length but transfers no body. Prefer a
            // deferred finish so active_requests stays non-zero while streaming.
            let known_out = response_transfer_bytes(&method, response.headers(), response.body());
            let bytes_out = Arc::new(std::sync::atomic::AtomicU64::new(known_out.unwrap_or(0)));
            let pending = traffic::PendingFinish::new(
                traffic,
                op,
                bytes_in,
                bytes_out.clone(),
                started,
                error,
            );
            let (parts, body) = response.into_parts();
            let body = if known_out.is_some() {
                traffic::hold_response_body(body, pending)
            } else {
                traffic::count_response_body(body, bytes_out, pending)
            };
            Ok(Response::from_parts(parts, body))
        }
        Err(error) => {
            traffic.record_finish(op, bytes_in, 0, started.elapsed(), true);
            Err(error)
        }
    }
}

fn response_transfer_bytes(
    method: &Method,
    headers: &HeaderMap,
    body: &Body,
) -> Option<u64> {
    if *method == Method::HEAD {
        return Some(0);
    }
    header_content_length(headers).or_else(|| traffic::body_size_hint(body))
}

fn request_payload_bytes(headers: &HeaderMap) -> Option<u64> {
    // Prefer decoded object size for aws-chunked uploads; Content-Length is the
    // encoded stream size when both headers are present.
    header_u64(headers, "x-amz-decoded-content-length")
        .or_else(|| header_content_length(headers))
}

fn header_content_length(headers: &HeaderMap) -> Option<u64> {
    header_u64(headers, "content-length")
}

fn header_u64(headers: &HeaderMap, name: &str) -> Option<u64> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse().ok())
}

fn cors_preflight(
    headers: &HeaderMap,
    origin: Option<&str>,
    cors_origins: Option<&[String]>,
) -> Response<Body> {
    let method = headers
        .get("access-control-request-method")
        .and_then(|value| value.to_str().ok());
    let requested_headers = headers
        .get("access-control-request-headers")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("");
    let allowed = origin.zip(cors_origins).is_some_and(|(origin, origins)| {
        origins.iter().any(|allowed| allowed == origin)
            && matches!(method, Some("GET" | "HEAD" | "PUT"))
            && allowed_cors_headers(requested_headers)
    });
    if !allowed {
        let mut response = Response::new(Body::from(
            "<Error><Code>AccessDenied</Code><Message>CORS preflight is not allowed</Message></Error>"
                .to_owned(),
        ));
        *response.status_mut() = StatusCode::FORBIDDEN;
        response
            .headers_mut()
            .insert("content-type", HeaderValue::from_static("application/xml"));
        return response;
    }

    let origin = origin.expect("allowed CORS origin is present");
    let mut response = Response::new(Body::empty());
    *response.status_mut() = StatusCode::NO_CONTENT;
    apply_cors(response.headers_mut(), origin);
    response.headers_mut().insert(
        "access-control-allow-methods",
        HeaderValue::from_static("GET, HEAD, PUT"),
    );
    if !requested_headers.is_empty()
        && let Ok(value) = HeaderValue::from_str(requested_headers)
    {
        response
            .headers_mut()
            .insert("access-control-allow-headers", value);
    }
    response
        .headers_mut()
        .insert("access-control-max-age", HeaderValue::from_static("600"));
    append_vary(response.headers_mut(), "Access-Control-Request-Method");
    append_vary(response.headers_mut(), "Access-Control-Request-Headers");
    response
}

fn apply_cors(headers: &mut HeaderMap, origin: &str) {
    if let Ok(value) = HeaderValue::from_str(origin) {
        headers.insert("access-control-allow-origin", value);
    }
    headers.insert(
        "access-control-expose-headers",
        HeaderValue::from_static(
            "Accept-Ranges, Content-Length, Content-Range, ETag, Last-Modified, X-Amz-Request-Id",
        ),
    );
    append_vary(headers, "Origin");
}

fn append_vary(headers: &mut HeaderMap, value: &'static str) {
    headers.append("vary", HeaderValue::from_static(value));
}

fn allowed_cors_headers(value: &str) -> bool {
    if value.is_empty() {
        return true;
    }
    value.split(',').all(|header| {
        let header = header.trim().to_ascii_lowercase();
        !header.is_empty()
            && (matches!(
                header.as_str(),
                "authorization"
                    | "content-type"
                    | "range"
                    | "x-amz-content-sha256"
                    | "x-amz-date"
                    | "x-amz-security-token"
                    | "x-amz-user-agent"
            ) || header.starts_with("x-amz-meta-"))
    })
}

#[cfg(test)]
mod tests {
    #[cfg(target_os = "linux")]
    use super::bind_project_listener;
    use super::{
        DataPlaneStore, MaintenanceMode, ProjectState, StoreGate, response_transfer_bytes,
    };
    #[cfg(target_os = "linux")]
    use std::net::SocketAddr;
    use std::{collections::HashMap, sync::Arc};
    use http::{HeaderMap, HeaderValue, Method};
    use s3s::Body;
    use tokio::sync::RwLock;

    #[test]
    fn head_responses_transfer_zero_bytes_even_with_content_length() {
        let mut headers = HeaderMap::new();
        headers.insert("content-length", HeaderValue::from_static("1048576"));
        assert_eq!(
            response_transfer_bytes(&Method::HEAD, &headers, &Body::empty()),
            Some(0)
        );
        assert_eq!(
            response_transfer_bytes(&Method::GET, &headers, &Body::empty()),
            Some(1_048_576)
        );
    }

    #[tokio::test]
    async fn project_snapshot_resolves_physical_bucket_once() {
        let gates = Arc::new(RwLock::new(HashMap::new()));
        let project = ProjectState::new(
            vec![configured_store(
                "v1w2x3y4z5a6b7c8d9e0f1g2",
                "lance-data",
                "access",
            )],
            gates,
        )
        .await
        .expect("project snapshot");

        let store = project
            .store_for_access_key("access")
            .expect("resolved store");
        assert_eq!(store.bucket_name(), "lance-data");
        assert_eq!(store.physical_bucket(), "pd-v1w2x3y4z5a6b7c8d9e0f1g2");
        assert_eq!(
            project
                .secret_for_access_key("access")
                .expect("secret")
                .expose(),
            "secret"
        );
    }

    #[tokio::test]
    async fn project_snapshot_rejects_ambiguous_bucket_mapping() {
        let gates = Arc::new(RwLock::new(HashMap::new()));
        let result = ProjectState::new(
            vec![
                configured_store("v1w2x3y4z5a6b7c8d9e0f1g2", "lance-data", "first"),
                configured_store("w2x3y4z5a6b7c8d9e0f1g2h3", "lance-data", "second"),
            ],
            gates,
        )
        .await;
        let Err(error) = result else {
            panic!("duplicate bucket must fail");
        };
        assert_eq!(error, "duplicate data-plane bucket name");
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn project_listener_binds_before_gateway_interface_exists() {
        let address: SocketAddr = "192.0.2.1:0".parse().expect("test address");
        let listener = bind_project_listener(address).expect("freebind listener");
        assert_eq!(
            listener.local_addr().expect("listener address").ip(),
            address.ip()
        );
    }

    #[tokio::test]
    async fn backup_drains_writes_but_keeps_reads_available() {
        let gate = Arc::new(StoreGate::default());
        let write = gate.enter(true).await.expect("enter write");
        let begin_gate = gate.clone();
        let begin = tokio::spawn(async move { begin_gate.begin(MaintenanceMode::Backup).await });
        tokio::task::yield_now().await;
        assert!(!begin.is_finished(), "backup did not wait for active write");

        let read = gate
            .enter(false)
            .await
            .expect("backup must keep reads available");
        drop(write);
        wait_until_finished(&begin).await;
        begin.await.expect("backup task").expect("begin backup");
        drop(read);
        gate.end(MaintenanceMode::Backup).expect("end backup");
    }

    #[tokio::test]
    async fn restore_rejects_new_requests_and_drains_active_reads() {
        let gate = Arc::new(StoreGate::default());
        let read = gate.enter(false).await.expect("enter read");
        let begin_gate = gate.clone();
        let begin = tokio::spawn(async move { begin_gate.begin(MaintenanceMode::Restore).await });
        tokio::task::yield_now().await;
        assert!(!begin.is_finished(), "restore did not wait for active read");
        assert!(
            gate.enter(false).await.is_err(),
            "restore accepted a new request"
        );

        drop(read);
        wait_until_finished(&begin).await;
        begin.await.expect("restore task").expect("begin restore");
        gate.end(MaintenanceMode::Restore).expect("end restore");
    }

    async fn wait_until_finished<T>(task: &tokio::task::JoinHandle<T>) {
        for _ in 0..10_000 {
            if task.is_finished() {
                return;
            }
            tokio::task::yield_now().await;
        }
        panic!("maintenance task did not finish");
    }

    fn configured_store(store_id: &str, bucket_name: &str, access_key: &str) -> DataPlaneStore {
        DataPlaneStore {
            store_id: store_id.to_owned(),
            bucket_name: bucket_name.to_owned(),
            access_key: access_key.to_owned(),
            secret: "secret".to_owned(),
            permission: "read_write".to_owned(),
            cors_origins: vec!["https://example.com".to_owned()],
        }
    }
}
