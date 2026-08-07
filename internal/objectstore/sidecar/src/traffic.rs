use http::Method;
use http_body::Body as HttpBody;
use http_body_util::BodyExt as _;
use s3s::Body;
use serde::Serialize;
use std::{
    collections::HashMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering},
    },
    time::{Duration, Instant},
};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OpClass {
    Get,
    Put,
    Delete,
    List,
    Other,
}

#[derive(Default)]
pub struct TrafficCounters {
    get: AtomicU64,
    put: AtomicU64,
    delete: AtomicU64,
    list: AtomicU64,
    other: AtomicU64,
    bytes_in: AtomicU64,
    bytes_out: AtomicU64,
    errors: AtomicU64,
    total_latency_micros: AtomicU64,
    active_requests: AtomicUsize,
}

impl TrafficCounters {
    pub fn record_start(&self) {
        self.active_requests.fetch_add(1, Ordering::Relaxed);
    }

    pub fn record_finish(
        &self,
        op: OpClass,
        bytes_in: u64,
        bytes_out: u64,
        latency: Duration,
        error: bool,
    ) {
        let _ = self.active_requests.fetch_update(Ordering::Relaxed, Ordering::Relaxed, |n| {
            Some(n.saturating_sub(1))
        });
        match op {
            OpClass::Get => self.get.fetch_add(1, Ordering::Relaxed),
            OpClass::Put => self.put.fetch_add(1, Ordering::Relaxed),
            OpClass::Delete => self.delete.fetch_add(1, Ordering::Relaxed),
            OpClass::List => self.list.fetch_add(1, Ordering::Relaxed),
            OpClass::Other => self.other.fetch_add(1, Ordering::Relaxed),
        };
        if bytes_in > 0 {
            self.bytes_in.fetch_add(bytes_in, Ordering::Relaxed);
        }
        if bytes_out > 0 {
            self.bytes_out.fetch_add(bytes_out, Ordering::Relaxed);
        }
        if error {
            self.errors.fetch_add(1, Ordering::Relaxed);
        }
        let micros = u64::try_from(latency.as_micros()).unwrap_or(u64::MAX);
        self.total_latency_micros
            .fetch_add(micros, Ordering::Relaxed);
    }

    pub fn snapshot(&self) -> TrafficSnapshot {
        TrafficSnapshot {
            ops: TrafficOps {
                get: self.get.load(Ordering::Relaxed),
                put: self.put.load(Ordering::Relaxed),
                delete: self.delete.load(Ordering::Relaxed),
                list: self.list.load(Ordering::Relaxed),
                other: self.other.load(Ordering::Relaxed),
            },
            bytes_in: self.bytes_in.load(Ordering::Relaxed),
            bytes_out: self.bytes_out.load(Ordering::Relaxed),
            errors: self.errors.load(Ordering::Relaxed),
            active_requests: self.active_requests.load(Ordering::Relaxed),
            total_latency_micros: self.total_latency_micros.load(Ordering::Relaxed),
        }
    }
}

/// Per-physical-bucket traffic so multi-store sidecars do not share counters.
#[derive(Default)]
pub struct TrafficRegistry {
    buckets: Mutex<HashMap<String, Arc<TrafficCounters>>>,
}

impl TrafficRegistry {
    pub fn for_bucket(&self, bucket: &str) -> Arc<TrafficCounters> {
        let mut buckets = self.buckets.lock().expect("traffic registry poisoned");
        buckets
            .entry(bucket.to_owned())
            .or_insert_with(|| Arc::new(TrafficCounters::default()))
            .clone()
    }

    /// Snapshot without inserting a registry entry for unknown buckets.
    pub fn snapshot(&self, bucket: &str) -> TrafficSnapshot {
        let buckets = self.buckets.lock().expect("traffic registry poisoned");
        match buckets.get(bucket) {
            Some(counters) => counters.snapshot(),
            None => TrafficSnapshot::default(),
        }
    }

    #[cfg(test)]
    fn len(&self) -> usize {
        self.buckets
            .lock()
            .expect("traffic registry poisoned")
            .len()
    }
}

/// Records traffic when dropped so streamed response bodies still attribute bytes.
pub struct PendingFinish {
    traffic: Arc<TrafficCounters>,
    op: OpClass,
    bytes_in: u64,
    bytes_out: Arc<AtomicU64>,
    started: Instant,
    error: bool,
    done: AtomicBool,
}

impl PendingFinish {
    pub fn new(
        traffic: Arc<TrafficCounters>,
        op: OpClass,
        bytes_in: u64,
        bytes_out: Arc<AtomicU64>,
        started: Instant,
        error: bool,
    ) -> Arc<Self> {
        Arc::new(Self {
            traffic,
            op,
            bytes_in,
            bytes_out,
            started,
            error,
            done: AtomicBool::new(false),
        })
    }

    pub fn finish(&self) {
        if self.done.swap(true, Ordering::AcqRel) {
            return;
        }
        self.traffic.record_finish(
            self.op,
            self.bytes_in,
            self.bytes_out.load(Ordering::Relaxed),
            self.started.elapsed(),
            self.error,
        );
    }
}

impl Drop for PendingFinish {
    fn drop(&mut self) {
        self.finish();
    }
}

/// Wraps a response body so transferred payload bytes are counted when Content-Length is absent.
pub fn count_response_body(
    body: Body,
    bytes_out: Arc<AtomicU64>,
    pending: Arc<PendingFinish>,
) -> Body {
    Body::http_body(body.map_frame(move |frame| {
        let _pending = &pending;
        if let Some(data) = frame.data_ref() {
            bytes_out.fetch_add(data.len() as u64, Ordering::Relaxed);
        }
        frame
    }))
}

/// Keeps `pending` alive until the response body is dropped so active_requests
/// and latency cover the full transfer even when bytes are already known.
pub fn hold_response_body(body: Body, pending: Arc<PendingFinish>) -> Body {
    Body::http_body(body.map_frame(move |frame| {
        let _pending = &pending;
        frame
    }))
}

pub fn body_size_hint(body: &Body) -> Option<u64> {
    HttpBody::size_hint(body).exact()
}

#[derive(Debug, Clone, Serialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct TrafficSnapshot {
    pub ops: TrafficOps,
    pub bytes_in: u64,
    pub bytes_out: u64,
    pub errors: u64,
    pub active_requests: usize,
    pub total_latency_micros: u64,
}

#[derive(Debug, Clone, Serialize, Default)]
#[serde(rename_all = "camelCase")]
pub struct TrafficOps {
    pub get: u64,
    pub put: u64,
    pub delete: u64,
    pub list: u64,
    pub other: u64,
}

/// Best-effort S3 op classification from HTTP method and path.
pub fn classify_s3(method: &Method, path: &str) -> OpClass {
    let path = path.trim_start_matches('/');
    let object_key = path
        .split_once('/')
        .map(|(_, key)| !key.is_empty())
        .unwrap_or(false);
    match *method {
        Method::GET | Method::HEAD if object_key => OpClass::Get,
        Method::GET if !object_key => OpClass::List,
        Method::PUT if object_key => OpClass::Put,
        Method::POST if object_key => OpClass::Put,
        Method::DELETE => OpClass::Delete,
        _ => OpClass::Other,
    }
}

/// Control-plane object routes that should count toward traffic.
pub fn classify_control(method: &Method, path: &str) -> Option<OpClass> {
    match (method, path) {
        (&Method::GET | &Method::HEAD, "/v1/object") => Some(OpClass::Get),
        (&Method::PUT, "/v1/object") => Some(OpClass::Put),
        (&Method::DELETE, "/v1/object") => Some(OpClass::Delete),
        (&Method::GET, "/v1/objects") => Some(OpClass::List),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::{OpClass, TrafficRegistry, classify_control, classify_s3};
    use http::Method;
    use std::time::Duration;

    #[test]
    fn classifies_s3_object_and_list_paths() {
        assert_eq!(
            classify_s3(&Method::GET, "/bucket/key"),
            OpClass::Get
        );
        assert_eq!(classify_s3(&Method::GET, "/bucket"), OpClass::List);
        assert_eq!(classify_s3(&Method::PUT, "/bucket/key"), OpClass::Put);
        assert_eq!(
            classify_s3(&Method::DELETE, "/bucket/key"),
            OpClass::Delete
        );
        assert_eq!(classify_s3(&Method::HEAD, "/bucket"), OpClass::Other);
    }

    #[test]
    fn classifies_control_object_routes() {
        assert_eq!(
            classify_control(&Method::GET, "/v1/object"),
            Some(OpClass::Get)
        );
        assert_eq!(
            classify_control(&Method::PUT, "/v1/object"),
            Some(OpClass::Put)
        );
        assert_eq!(
            classify_control(&Method::GET, "/v1/bucket/stats"),
            None
        );
    }

    #[test]
    fn registry_isolates_buckets() {
        let registry = TrafficRegistry::default();
        let left = registry.for_bucket("pd-aaaaaaaaaaaaaaaaaaaaaaaa");
        let right = registry.for_bucket("pd-bbbbbbbbbbbbbbbbbbbbbbbb");
        left.record_start();
        left.record_finish(OpClass::Put, 100, 0, Duration::from_millis(1), false);
        assert_eq!(registry.snapshot("pd-aaaaaaaaaaaaaaaaaaaaaaaa").bytes_in, 100);
        assert_eq!(registry.snapshot("pd-bbbbbbbbbbbbbbbbbbbbbbbb").bytes_in, 0);
        assert_eq!(right.snapshot().bytes_in, 0);
    }

    #[test]
    fn snapshot_of_unknown_bucket_does_not_insert() {
        let registry = TrafficRegistry::default();
        assert_eq!(registry.snapshot("missing").bytes_in, 0);
        assert_eq!(registry.len(), 0);
    }
}
