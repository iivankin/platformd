mod crash;
mod javascript;
mod jvm;
mod native;

use std::collections::{HashSet, VecDeque};

use jiff::Timestamp;
use serde_json::{Value, json};
use tokio::sync::mpsc::{self, error::TryRecvError, error::TrySendError};

use crate::MAX_STORED_ITEM_BYTES;
use crate::config::AppConfig;
use crate::error::{Error, Result};
use crate::ingest::IngestedEvent;
use crate::model::{Document, SearchRequest};
use crate::storage::{Store, all, term};

const JOB_QUEUE_CAPACITY: usize = 128;
const RESCAN_BATCH_SIZE: usize = 50;

#[derive(Clone)]
pub struct Symbolicator {
    store: Store,
}

#[derive(Clone)]
pub struct Dispatcher {
    sender: mpsc::Sender<Job>,
}

enum Job {
    Event {
        context: SymbolicationContext,
        event: IngestedEvent,
    },
    Application {
        context: SymbolicationContext,
    },
}

#[derive(Clone)]
struct SymbolicationContext {
    app_id: String,
    project_id: String,
}

struct ApplicationScan {
    context: SymbolicationContext,
    offset: usize,
    only_missing: bool,
}

impl SymbolicationContext {
    fn new(app: &AppConfig) -> Self {
        Self {
            app_id: app.id.clone(),
            project_id: app.project_id.clone(),
        }
    }
}

impl Symbolicator {
    pub fn new(store: Store) -> Self {
        Self { store }
    }

    async fn symbolicate(
        &self,
        context: &SymbolicationContext,
        event: &IngestedEvent,
    ) -> Result<Option<Document>> {
        if event
            .payload
            .get("_platformd_symbolicated")
            .and_then(Value::as_bool)
            .unwrap_or(false)
        {
            return Ok(None);
        }

        let platform = event.platform.as_str();
        let payload = match platform {
            "javascript" | "node" => {
                javascript::symbolicate(&self.store, &context.app_id, &event.payload).await?
            }
            "java" | "android" => {
                jvm::symbolicate(&self.store, &context.app_id, &event.payload).await?
            }
            _ if has_native_images(&event.payload) => {
                native::symbolicate(&self.store, &context.app_id, &event.payload).await?
            }
            _ => return Ok(None),
        };

        let now = Timestamp::now().to_string();
        let mut document = Document::base(
            "symbolication",
            &context.app_id,
            &context.project_id,
            &event.timestamp,
            &now,
        );
        document.event_id = Some(event.event_id.clone());
        document.issue_id = Some(event.issue_id.clone());
        document.item_type = Some(platform.to_owned());
        document.platform = Some(platform.to_owned());
        document.status = payload
            .get("status")
            .and_then(Value::as_str)
            .map(str::to_owned);
        document.payload = Some(payload);
        Ok(Some(document))
    }

    pub async fn process_crash_file(
        &self,
        app: &AppConfig,
        kind: CrashFileKind,
        bytes: Vec<u8>,
    ) -> Result<Value> {
        match kind {
            CrashFileKind::Minidump => crash::process_minidump(&self.store, &app.id, bytes).await,
            CrashFileKind::AppleCrashReport => {
                crash::process_apple_crash_report(&self.store, &app.id, bytes).await
            }
        }
    }
}

impl Dispatcher {
    pub fn new(symbolicator: Symbolicator, store: Store, apps: &[AppConfig]) -> Self {
        let (sender, mut receiver) = mpsc::channel::<Job>(JOB_QUEUE_CAPACITY);
        let mut scans = apps
            .iter()
            .map(|app| ApplicationScan {
                context: SymbolicationContext::new(app),
                offset: 0,
                only_missing: true,
            })
            .collect::<VecDeque<_>>();
        let mut scanning_apps = scans
            .iter()
            .map(|scan| scan.context.app_id.clone())
            .collect::<HashSet<_>>();
        tokio::spawn(async move {
            loop {
                // Alternate queued events with one rescan batch. Always draining the
                // receiver first would starve historical resymbolication under load.
                let job = match receiver.try_recv() {
                    Ok(job) => Some(job),
                    Err(TryRecvError::Empty) => None,
                    Err(TryRecvError::Disconnected) if scans.is_empty() => break,
                    Err(TryRecvError::Disconnected) => None,
                };
                let processed_job = if let Some(job) = job {
                    schedule_or_process(job, &symbolicator, &store, &mut scans, &mut scanning_apps)
                        .await;
                    true
                } else {
                    false
                };
                if let Some(mut scan) = scans.pop_front() {
                    if process_application_batch(&symbolicator, &store, &scan).await {
                        scan.offset = scan.offset.saturating_add(RESCAN_BATCH_SIZE);
                        scans.push_back(scan);
                    } else {
                        scanning_apps.remove(&scan.context.app_id);
                    }
                    continue;
                }
                if processed_job {
                    continue;
                }
                let Some(job) = receiver.recv().await else {
                    break;
                };
                schedule_or_process(job, &symbolicator, &store, &mut scans, &mut scanning_apps)
                    .await;
            }
        });
        Self { sender }
    }

    pub fn enqueue(&self, app: &AppConfig, event: IngestedEvent) {
        let event_id = event.event_id.clone();
        let job = Job::Event {
            context: SymbolicationContext::new(app),
            event,
        };
        if let Err(error) = self.sender.try_send(job) {
            log_enqueue_error(error, "event", &event_id);
        }
    }

    pub async fn enqueue_application(&self, app: &AppConfig) {
        let job = Job::Application {
            context: SymbolicationContext::new(app),
        };
        if self.sender.send(job).await.is_err() {
            tracing::warn!(app_id = app.id, "symbolication worker stopped");
        }
    }
}

fn log_enqueue_error(error: TrySendError<Job>, kind: &str, id: &str) {
    match error {
        TrySendError::Full(_) => {
            tracing::warn!(job_kind = kind, job_id = id, "symbolication queue is full")
        }
        TrySendError::Closed(_) => {
            tracing::warn!(job_kind = kind, job_id = id, "symbolication worker stopped")
        }
    }
}

async fn schedule_or_process(
    job: Job,
    symbolicator: &Symbolicator,
    store: &Store,
    scans: &mut VecDeque<ApplicationScan>,
    scanning_apps: &mut HashSet<String>,
) {
    match job {
        Job::Event { context, event } => process_event(symbolicator, store, &context, &event).await,
        Job::Application { context } => schedule_application(context, scans, scanning_apps),
    }
}

fn schedule_application(
    context: SymbolicationContext,
    scans: &mut VecDeque<ApplicationScan>,
    scanning_apps: &mut HashSet<String>,
) {
    if scanning_apps.insert(context.app_id.clone()) {
        scans.push_back(ApplicationScan {
            context,
            offset: 0,
            only_missing: false,
        });
    } else if let Some(scan) = scans
        .iter_mut()
        .find(|scan| scan.context.app_id == context.app_id)
    {
        // A later artifact upload may contain symbols absent from the active scan.
        // Restart it so already-visited events are processed with the new artifacts.
        *scan = ApplicationScan {
            context,
            offset: 0,
            only_missing: false,
        };
    }
}

async fn process_event(
    symbolicator: &Symbolicator,
    store: &Store,
    context: &SymbolicationContext,
    event: &IngestedEvent,
) {
    match symbolicator.symbolicate(context, event).await {
        Ok(Some(document)) => {
            if let Err(error) = store.ingest(vec![document]).await {
                tracing::warn!(%error, event_id = event.event_id, "store symbolication failed");
            }
        }
        Ok(None) => {}
        Err(error) => {
            tracing::warn!(%error, event_id = event.event_id, "event symbolication failed");
        }
    }
}

async fn process_application_batch(
    symbolicator: &Symbolicator,
    store: &Store,
    scan: &ApplicationScan,
) -> bool {
    let response = store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "event"),
                term("app_id", &scan.context.app_id),
            ]),
            max_hits: RESCAN_BATCH_SIZE,
            start_offset: Some(scan.offset),
            sort_by: "timestamp".into(),
        })
        .await;
    let hits = match response {
        Ok(response) => response.hits,
        Err(error) => {
            tracing::warn!(
                %error,
                app_id = scan.context.app_id,
                "load events for resymbolication failed"
            );
            return false;
        }
    };
    let has_more = hits.len() == RESCAN_BATCH_SIZE;
    let completed = if scan.only_missing {
        match completed_event_ids(store, &scan.context.app_id, &hits).await {
            Ok(event_ids) => event_ids,
            Err(error) => {
                tracing::warn!(
                    %error,
                    app_id = scan.context.app_id,
                    "load completed symbolication jobs failed"
                );
                return false;
            }
        }
    } else {
        HashSet::new()
    };
    let mut documents = Vec::new();
    for hit in hits {
        if hit
            .get("event_id")
            .and_then(Value::as_str)
            .is_some_and(|event_id| completed.contains(event_id))
        {
            continue;
        }
        let event = match stored_event(store, &hit).await {
            Ok(Some(event)) => event,
            Ok(None) => continue,
            Err(error) => {
                let event_id = hit
                    .get("event_id")
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or("unknown");
                tracing::warn!(
                    %error,
                    event_id,
                    "restore event for resymbolication failed"
                );
                continue;
            }
        };
        match symbolicator.symbolicate(&scan.context, &event).await {
            Ok(Some(document)) => documents.push(document),
            Ok(None) => {}
            Err(error) => tracing::warn!(
                %error,
                event_id = event.event_id,
                "event resymbolication failed"
            ),
        }
    }
    if !documents.is_empty()
        && let Err(error) = store.ingest(documents).await
    {
        tracing::warn!(
            %error,
            app_id = scan.context.app_id,
            "store resymbolication batch failed"
        );
        return false;
    }
    has_more
}

async fn completed_event_ids(
    store: &Store,
    app_id: &str,
    events: &[Value],
) -> Result<HashSet<String>> {
    let event_ids = events
        .iter()
        .filter_map(|event| event.get("event_id").and_then(Value::as_str))
        .map(|event_id| term("event_id", event_id))
        .collect::<Vec<_>>();
    if event_ids.is_empty() {
        return Ok(HashSet::new());
    }
    let response = store
        .search(SearchRequest {
            query: all([
                term("doc_kind", "symbolication"),
                term("app_id", app_id),
                crate::storage::any(event_ids),
            ]),
            max_hits: events.len(),
            start_offset: None,
            sort_by: String::new(),
        })
        .await?;
    Ok(response
        .hits
        .into_iter()
        .filter_map(|document| {
            document
                .get("event_id")
                .and_then(Value::as_str)
                .map(str::to_owned)
        })
        .collect())
}

async fn stored_event(store: &Store, document: &Value) -> Result<Option<IngestedEvent>> {
    let Some(event_id) = document.get("event_id").and_then(Value::as_str) else {
        return Ok(None);
    };
    let Some(issue_id) = document.get("issue_id").and_then(Value::as_str) else {
        return Ok(None);
    };
    let Some(timestamp) = document.get("timestamp").and_then(Value::as_str) else {
        return Ok(None);
    };
    let payload = if let Some(payload) = document.get("payload") {
        payload.clone()
    } else {
        let Some(app_id) = document.get("app_id").and_then(Value::as_str) else {
            return Ok(None);
        };
        let Some(content_id) = document.get("content_id").and_then(Value::as_str) else {
            return Ok(None);
        };
        let bytes = store
            .load_content("content_chunk", app_id, content_id, MAX_STORED_ITEM_BYTES)
            .await?;
        serde_json::from_slice(&bytes)
            .map_err(|error| Error::Storage(format!("decode stored event payload: {error}")))?
    };
    Ok(Some(IngestedEvent {
        event_id: event_id.to_owned(),
        issue_id: issue_id.to_owned(),
        title: document
            .get("title")
            .and_then(Value::as_str)
            .unwrap_or("Unknown error")
            .to_owned(),
        level: document
            .get("level")
            .and_then(Value::as_str)
            .unwrap_or("error")
            .to_owned(),
        platform: document
            .get("platform")
            .and_then(Value::as_str)
            .unwrap_or("other")
            .to_owned(),
        timestamp: timestamp.to_owned(),
        payload,
    }))
}

#[derive(Clone, Copy, Debug)]
pub enum CrashFileKind {
    Minidump,
    AppleCrashReport,
}

impl CrashFileKind {
    fn mechanism(self) -> &'static str {
        match self {
            Self::Minidump => "minidump",
            Self::AppleCrashReport => "applecrashreport",
        }
    }
}

pub fn crash_event(
    event_id: &str,
    response: Value,
    extra: Option<Value>,
    kind: CrashFileKind,
) -> Value {
    let timestamp = response.get("timestamp").cloned();
    let crash_reason = response
        .get("crash_reason")
        .and_then(Value::as_str)
        .unwrap_or("Native crash");
    let crash_details = response
        .get("crash_details")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let threads = response
        .get("stacktraces")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default()
        .into_iter()
        .map(|stacktrace| {
            json!({
                "id": stacktrace.get("thread_id"),
                "name": stacktrace.get("thread_name"),
                "crashed": stacktrace.get("is_requesting"),
                "current": stacktrace.get("is_requesting"),
                "stacktrace": {
                    "frames": stacktrace.get("frames").cloned().unwrap_or_else(|| json!([])),
                    "registers": stacktrace.get("registers")
                }
            })
        })
        .collect::<Vec<_>>();
    let mut event = json!({
        "event_id": event_id,
        "platform": "native",
        "level": "fatal",
        "timestamp": timestamp,
        "exception": { "values": [{
            "type": crash_reason,
            "value": crash_details,
            "mechanism": { "type": kind.mechanism(), "handled": false }
        }]},
        "threads": { "values": threads },
        "debug_meta": { "images": response.get("modules").cloned().unwrap_or_else(|| json!([])) },
        "contexts": {
            "os": response.get("system_info"),
            "symbolicator": {
                "status": response.get("status"),
                "crashed": response.get("crashed"),
                "assertion": response.get("assertion")
            }
        },
        "_platformd_symbolicated": true
    });
    if let (Some(event), Some(extra)) = (
        event.as_object_mut(),
        extra.and_then(|value| value.as_object().cloned()),
    ) {
        for (key, value) in extra {
            if key == "contexts" {
                if let (Some(contexts), Some(extra_contexts)) = (
                    event.get_mut("contexts").and_then(Value::as_object_mut),
                    value.as_object(),
                ) {
                    for (name, context) in extra_contexts {
                        contexts
                            .entry(name.clone())
                            .or_insert_with(|| context.clone());
                    }
                }
                continue;
            }
            if !matches!(
                key.as_str(),
                "event_id"
                    | "platform"
                    | "exception"
                    | "threads"
                    | "debug_meta"
                    | "_platformd_symbolicated"
            ) {
                event.insert(key, value);
            }
        }
    }
    event
}

pub(super) fn stacktraces(event: &Value, include_threads: bool) -> Vec<Value> {
    let mut output = Vec::new();
    if let Some(stacktrace) = event.get("stacktrace").and_then(Value::as_object) {
        output.push(Value::Object(stacktrace.clone()));
    }
    if let Some(exceptions) = event.pointer("/exception/values").and_then(Value::as_array) {
        output.extend(
            exceptions
                .iter()
                .filter_map(|exception| exception.get("stacktrace").cloned()),
        );
    }
    if include_threads
        && let Some(threads) = event.pointer("/threads/values").and_then(Value::as_array)
    {
        output.extend(threads.iter().filter_map(|thread| {
            let mut stacktrace = thread.get("stacktrace")?.as_object()?.clone();
            for name in ["id", "name", "crashed", "current", "registers"] {
                if let Some(value) = thread.get(name) {
                    stacktrace.insert(name.into(), value.clone());
                }
            }
            Some(Value::Object(stacktrace))
        }));
    }
    output
}

pub(super) fn debug_images(event: &Value) -> Vec<Value> {
    event
        .pointer("/debug_meta/images")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default()
}

fn has_native_images(event: &Value) -> bool {
    event
        .pointer("/debug_meta/images")
        .and_then(Value::as_array)
        .is_some_and(|images| !images.is_empty())
}

#[cfg(test)]
mod tests {
    use tempfile::tempdir;

    use super::*;

    #[test]
    fn extracts_exception_and_thread_stacktraces() {
        let event = json!({
            "exception": {"values": [{"stacktrace": {"frames": [{"lineno": 1}]}}]},
            "threads": {"values": [{"id": "main", "crashed": true, "stacktrace": {"frames": []}}]}
        });
        let traces = stacktraces(&event, true);
        assert_eq!(traces.len(), 2);
        assert_eq!(traces[1]["id"], "main");
    }

    #[test]
    fn crash_event_preserves_processing_context_and_file_kind() {
        let event = crash_event(
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            json!({"status": "failed", "stacktraces": [], "modules": []}),
            Some(json!({"contexts": {"runtime": {"name": "test"}}})),
            CrashFileKind::AppleCrashReport,
        );
        assert_eq!(
            event.pointer("/contexts/symbolicator/status"),
            Some(&json!("failed"))
        );
        assert_eq!(
            event.pointer("/contexts/runtime/name"),
            Some(&json!("test"))
        );
        assert_eq!(
            event.pointer("/exception/values/0/mechanism/type"),
            Some(&json!("applecrashreport"))
        );
    }

    #[test]
    fn a_new_artifact_restarts_an_active_application_scan() {
        let first = SymbolicationContext {
            app_id: "app".into(),
            project_id: "1".into(),
        };
        let mut scans = VecDeque::from([ApplicationScan {
            context: first,
            offset: RESCAN_BATCH_SIZE,
            only_missing: true,
        }]);
        let mut scanning_apps = HashSet::from(["app".to_owned()]);

        schedule_application(
            SymbolicationContext {
                app_id: "app".into(),
                project_id: "2".into(),
            },
            &mut scans,
            &mut scanning_apps,
        );

        assert_eq!(scans.len(), 1);
        assert_eq!(scans[0].offset, 0);
        assert!(!scans[0].only_missing);
        assert_eq!(scans[0].context.project_id, "2");
    }

    #[tokio::test]
    async fn restores_unindexed_event_payload_from_raw_content() {
        let directory = tempdir().unwrap();
        let store = Store::open(directory.path().to_owned()).await.unwrap();
        let timestamp = "2026-08-09T10:00:00Z";
        let payload = json!({"platform": "javascript", "message": "large event"});
        let bytes = serde_json::to_vec(&payload).unwrap();
        let mut content = Document::base("content_chunk", "app", "1", timestamp, timestamp);
        content.content_id = Some("event-content".into());
        content.sequence = Some(0);
        content.chunk_count = Some(1);
        content.size_bytes = Some(bytes.len() as u64);
        content.content = Some(bytes);
        store.ingest(vec![content]).await.unwrap();

        let mut document = Document::base("event", "app", "1", timestamp, timestamp);
        document.event_id = Some("event".into());
        document.issue_id = Some("issue".into());
        document.content_id = Some("event-content".into());
        document.title = Some("Large event".into());
        document.platform = Some("javascript".into());
        let document = serde_json::to_value(document).unwrap();

        let restored = stored_event(&store, &document)
            .await
            .unwrap()
            .expect("event metadata is complete");

        assert_eq!(restored.payload, payload);
    }

    #[tokio::test]
    async fn startup_scan_keeps_completed_symbolication() {
        let directory = tempdir().unwrap();
        let store = Store::open(directory.path().to_owned()).await.unwrap();
        let timestamp = "2026-08-09T10:00:00Z";
        let mut event = Document::base("event", "app", "1", timestamp, timestamp);
        event.event_id = Some("event".into());
        event.issue_id = Some("issue".into());
        event.title = Some("Already processed".into());
        event.platform = Some("javascript".into());
        event.payload = Some(json!({ "platform": "javascript" }));
        let mut completed = Document::base("symbolication", "app", "1", timestamp, timestamp);
        completed.event_id = Some("event".into());
        completed.status = Some("preserved".into());
        completed.payload = Some(json!({ "sentinel": true }));
        store.ingest(vec![event, completed]).await.unwrap();

        let scan = ApplicationScan {
            context: SymbolicationContext {
                app_id: "app".into(),
                project_id: "1".into(),
            },
            offset: 0,
            only_missing: true,
        };
        assert!(!process_application_batch(&Symbolicator::new(store.clone()), &store, &scan).await);

        let response = store
            .search(SearchRequest {
                query: all([
                    term("doc_kind", "symbolication"),
                    term("app_id", "app"),
                    term("event_id", "event"),
                ]),
                max_hits: 1,
                start_offset: None,
                sort_by: "received_at".into(),
            })
            .await
            .unwrap();
        assert_eq!(response.hits[0]["status"], "preserved");
        assert_eq!(response.hits[0]["payload"]["sentinel"], true);
    }
}
