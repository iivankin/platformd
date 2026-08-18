use std::borrow::Cow;
use std::collections::{HashMap, HashSet};
use std::ffi::OsStr;
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Component, Path, PathBuf};
#[cfg(test)]
use std::sync::OnceLock;
use std::sync::{Arc, Mutex, RwLock};
use std::time::{SystemTime, UNIX_EPOCH};

use chdb_rust::arg::Arg;
use chdb_rust::format::OutputFormat;
use chdb_rust::session::{Session, SessionBuilder};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use uuid::Uuid;

use crate::error::{Error, Result};
use crate::metric_sql::{MetricSqlQuery, MetricSqlRow, decode_rows, service_filter};
use crate::model::{Document, SearchClause, SearchQuery, SearchRequest, SearchResponse};

const MAX_CONTENT_CHUNKS: usize = 128;
const ANALYTICS_DATABASE: &str = "telemetry";
const RECORDING_DOC_KINDS: &str = "'replay_event', 'replay_recording', 'replay_video'";
const RECORDING_RETENTION: &str = "INTERVAL 14 DAY";

#[derive(Clone, Copy)]
pub(crate) enum SignalTable {
    Logs,
    Metrics,
    Profiles,
    Spans,
}

impl SignalTable {
    const fn name(self) -> &'static str {
        match self {
            Self::Logs => "logs",
            Self::Metrics => "metrics",
            Self::Profiles => "profiles",
            Self::Spans => "spans",
        }
    }
}

#[derive(Clone)]
pub struct Store {
    inner: Arc<StoreInner>,
}

struct StoreInner {
    maintenance: RwLock<()>,
    analytics: Mutex<Session>,
    blob_dir: PathBuf,
    backup_dir: PathBuf,
    #[cfg(test)]
    _test_session: tokio::sync::OwnedSemaphorePermit,
}

#[derive(Serialize)]
struct AnalyticsRow<'a> {
    storage_id: &'a str,
    version: u64,
    doc_kind: &'a str,
    service_id: &'a str,
    timestamp: &'a str,
    received_at: &'a str,
    ingest_id: Option<&'a str>,
    event_id: Option<&'a str>,
    issue_id: Option<&'a str>,
    item_type: Option<&'a str>,
    title: Option<&'a str>,
    message: Option<&'a str>,
    level: Option<&'a str>,
    platform: Option<&'a str>,
    environment: Option<&'a str>,
    release: Option<&'a str>,
    dist: Option<&'a str>,
    transaction: Option<&'a str>,
    sdk_name: Option<&'a str>,
    sdk_version: Option<&'a str>,
    user: Option<&'a str>,
    status: Option<&'a str>,
    filename: Option<&'a str>,
    content_type: Option<&'a str>,
    content_id: Option<&'a str>,
    checksum: Option<&'a str>,
    debug_id: Option<&'a str>,
    code_id: Option<&'a str>,
    symbol_type: Option<&'a str>,
    object_name: Option<&'a str>,
    replay_id: Option<&'a str>,
    blob_id: Option<&'a str>,
    segment_id: Option<u64>,
    sequence: Option<u64>,
    chunk_count: Option<u64>,
    size_bytes: Option<u64>,
    source: String,
    search: String,
}

#[derive(serde::Deserialize)]
struct SourceRow {
    source: String,
}

pub(crate) struct LogQuery {
    pub service_ids: Vec<String>,
    pub deployment_id: Option<String>,
    pub contains: Option<String>,
    pub field_filters: Vec<LogFieldFilter>,
    pub severity_text: Option<String>,
    pub trace_id: Option<String>,
    pub span_id: Option<String>,
    pub from_unix_nano: Option<u64>,
    pub to_unix_nano: Option<u64>,
    pub after_time_unix_nano: Option<u64>,
    pub after_id: Option<String>,
    pub limit: usize,
    pub ascending: bool,
}

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(crate) struct LogFieldFilter {
    pub path: String,
    pub operator: LogFieldOperator,
    #[serde(default)]
    pub value: String,
}

#[derive(Clone, Copy, Deserialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum LogFieldOperator {
    Equals,
    Contains,
    Exists,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct LogRecord {
    pub id: String,
    #[serde(alias = "service_id")]
    pub service_id: String,
    #[serde(alias = "time_unix_nano")]
    pub time_unix_nano: u64,
    pub stream: String,
    pub text: String,
    pub partial: bool,
    #[serde(alias = "deployment_id")]
    pub deployment_id: String,
    #[serde(alias = "attempt_id")]
    pub attempt_id: String,
    #[serde(alias = "trace_id")]
    pub trace_id: String,
    #[serde(alias = "span_id")]
    pub span_id: String,
    #[serde(alias = "severity_number")]
    pub severity_number: i32,
    #[serde(alias = "severity_text")]
    pub severity_text: String,
    #[serde(alias = "body_json")]
    pub body_json: Option<String>,
    #[serde(default)]
    pub phase: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct LogPage {
    pub records: Vec<LogRecord>,
    pub truncated: bool,
    pub next_time_unix_nano: Option<u64>,
    pub next_id: Option<String>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub(crate) struct TraceSummary {
    pub trace_id: String,
    // ClickHouse SELECT aliases collide with WHERE column names if both are
    // `segment_id`; the query emits primary_segment_id instead.
    #[serde(alias = "primary_segment_id")]
    pub segment_id: String,
    #[serde(alias = "root_service_id")]
    pub service_id: String,
    pub name: String,
    #[serde(serialize_with = "serialize_u64_string")]
    pub started_at_unix_nano: u64,
    #[serde(serialize_with = "serialize_u64_string")]
    pub duration_nano: u64,
    pub span_count: u64,
    pub error_span_count: u64,
    pub sources: Vec<String>,
    pub is_ai: bool,
    pub ai_agent: String,
    pub ai_agent_run_count: u64,
    pub ai_model: String,
    pub ai_provider: String,
    pub ai_input_tokens: Option<u64>,
    pub ai_output_tokens: Option<u64>,
    pub ai_cache_read_tokens: Option<u64>,
    pub ai_cache_write_tokens: Option<u64>,
    pub ai_reasoning_tokens: Option<u64>,
    pub ai_cost_usd: Option<f64>,
    pub ai_ttft_seconds: Option<f64>,
    pub ai_tokens_per_second: Option<f64>,
    pub ai_model_call_count: u64,
    pub ai_tool_call_count: u64,
}

#[derive(Clone, Copy)]
pub(crate) enum TraceSummaryOrder {
    Latest,
    Slowest,
    Spans,
}

#[derive(Clone, Copy)]
pub(crate) enum TraceSummaryStatus {
    All,
    Error,
    Ok,
}

pub(crate) struct TraceSummaryQuery {
    pub anchor_service_id: Option<String>,
    pub service_ids: Vec<String>,
    pub from_unix_nano: Option<u64>,
    pub to_unix_nano: Option<u64>,
    pub search: Option<String>,
    pub status: TraceSummaryStatus,
    pub order: TraceSummaryOrder,
    pub limit: usize,
    pub offset: usize,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct TraceDetail {
    pub trace_id: String,
    pub segment_id: String,
    pub spans: Vec<TraceSpan>,
    pub related_segments: Vec<RelatedTraceSegment>,
    pub metrics: Vec<TraceMetricSample>,
    pub profiles: Vec<TraceProfile>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub(crate) struct RelatedTraceSegment {
    pub segment_id: String,
    #[serde(alias = "segment_service_id")]
    pub service_id: String,
    pub name: String,
    #[serde(serialize_with = "serialize_u64_string")]
    pub started_at_unix_nano: u64,
    #[serde(serialize_with = "serialize_u64_string")]
    pub duration_nano: u64,
    pub span_count: u64,
    pub error_span_count: u64,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct TraceProfile {
    pub service_id: String,
    pub profile_id: String,
    pub profiler_id: String,
    pub platform: String,
    #[serde(serialize_with = "serialize_u64_string")]
    pub started_at_unix_nano: u64,
    #[serde(serialize_with = "serialize_u64_string")]
    pub ended_at_unix_nano: u64,
    pub sample_count: u64,
    pub stacks: Vec<TraceProfileStack>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct TraceProfileStack {
    pub thread_id: String,
    pub thread_name: String,
    pub span_id: String,
    pub sample_count: u64,
    #[serde(serialize_with = "serialize_u64_string")]
    pub duration_nano: u64,
    pub frames: Value,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub(crate) struct TraceSpan {
    pub service_id: String,
    pub trace_id: String,
    pub segment_id: String,
    pub is_segment: bool,
    pub span_id: String,
    pub parent_span_id: String,
    pub trace_state: String,
    pub name: String,
    pub kind: i32,
    #[serde(serialize_with = "serialize_u64_string")]
    pub start_time_unix_nano: u64,
    #[serde(serialize_with = "serialize_u64_string")]
    pub end_time_unix_nano: u64,
    #[serde(serialize_with = "serialize_u64_string")]
    pub duration_nano: u64,
    pub status_code: i32,
    pub status_message: String,
    pub flags: u32,
    pub resource: Value,
    pub scope: Value,
    pub span: Value,
    #[serde(serialize_with = "serialize_u64_string")]
    pub received_at_unix_nano: u64,
    pub source: String,
    pub ai_kind: String,
    pub ai_operation: String,
    pub ai_provider: String,
    pub ai_model: String,
    pub ai_agent: String,
    pub ai_input_tokens: Option<u64>,
    pub ai_output_tokens: Option<u64>,
    pub ai_cache_read_tokens: Option<u64>,
    pub ai_cache_write_tokens: Option<u64>,
    pub ai_reasoning_tokens: Option<u64>,
    pub ai_cost_usd: Option<f64>,
    pub ai_ttft_seconds: Option<f64>,
    pub ai_tokens_per_second: Option<f64>,
    #[serde(default)]
    pub baseline_duration_nano: Option<f64>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub(crate) struct TraceMetricSample {
    pub name: String,
    pub unit: String,
    #[serde(serialize_with = "serialize_u64_string")]
    pub time_unix_nano: u64,
    pub value: Option<f64>,
    pub span_id: String,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub(crate) struct IssueInsights {
    pub user_count: u64,
    pub distributions: Vec<IssueDistribution>,
    pub activity: Vec<IssueActivityBin>,
    pub first_event_id: String,
    pub latest_event_id: String,
    pub recommended_event_id: String,
}

#[derive(Deserialize, Serialize)]
pub(crate) struct IssueDistribution {
    pub key: String,
    pub value: String,
    pub count: u64,
}

#[derive(Deserialize, Serialize)]
pub(crate) struct IssueActivityBin {
    pub bin: u64,
    pub count: u64,
}

#[derive(Deserialize)]
struct IssueUserCount {
    user_count: u64,
    first_event_id: String,
    latest_event_id: String,
    recommended_event_id: String,
}

#[derive(Deserialize)]
struct TraceBaselineRow {
    name: String,
    kind: i32,
    baseline_duration_nano: f64,
}

#[derive(Deserialize)]
struct TraceMetricRow {
    name: String,
    unit: String,
    time_unix_nano: u64,
    value: Option<f64>,
    exemplars: String,
}

#[derive(Deserialize)]
struct TraceIdRow {
    trace_id: String,
}

#[derive(Deserialize)]
struct TraceSegmentIdRow {
    segment_id: String,
}

#[derive(Deserialize)]
struct TraceProfileRow {
    service_id: String,
    profile_key: String,
    stored_profile_id: String,
    profiler_id: String,
    platform: String,
    thread_id: String,
    thread_name: String,
    span_id: String,
    sample_count: u64,
    total_duration_nano: u64,
    started_at_unix_nano: u64,
    ended_at_unix_nano: u64,
    stack: String,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub(crate) struct MetricDescriptor {
    pub name: String,
    pub description: String,
    pub unit: String,
    pub kind: String,
    pub attribute_keys: Vec<String>,
    #[serde(serialize_with = "serialize_u64_string")]
    pub last_seen_unix_nano: u64,
}

fn serialize_u64_string<S>(value: &u64, serializer: S) -> std::result::Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(&value.to_string())
}

impl Store {
    pub async fn open(volume: PathBuf) -> Result<Self> {
        #[cfg(test)]
        let test_session = test_session_gate()
            .acquire_owned()
            .await
            .map_err(|_| Error::Storage("test chDB session gate is closed".into()))?;
        tokio::task::spawn_blocking(move || {
            Self::open_blocking(
                &volume,
                #[cfg(test)]
                test_session,
            )
        })
        .await
        .map_err(|error| Error::Storage(format!("open embedded store task: {error}")))?
    }

    fn open_blocking(
        volume: &Path,
        #[cfg(test)] test_session: tokio::sync::OwnedSemaphorePermit,
    ) -> Result<Self> {
        let analytics_dir = volume.join("chdb");
        let blob_dir = volume.join("blobs");
        let backup_dir = volume.join("backups");
        fs::create_dir_all(&analytics_dir)
            .map_err(|error| Error::Storage(format!("create chDB directory: {error}")))?;
        fs::create_dir_all(&blob_dir)
            .map_err(|error| Error::Storage(format!("create blob directory: {error}")))?;
        fs::create_dir_all(&backup_dir)
            .map_err(|error| Error::Storage(format!("create backup directory: {error}")))?;
        cleanup_temporary_files_in(&blob_dir)?;
        cleanup_backup_staging(&backup_dir)?;

        let chdb_config = volume.join("chdb-config.xml");
        write_chdb_config(&chdb_config, &backup_dir)?;

        let analytics = SessionBuilder::new()
            .with_data_path(analytics_dir)
            .with_arg(Arg::ConfigFilePath(Cow::Owned(
                chdb_config.to_string_lossy().into_owned(),
            )))
            .build()
            .map_err(|error| Error::Storage(format!("open chDB: {error}")))?;
        initialize_analytics(&analytics)?;
        remove_unreferenced_blobs(&blob_dir, &referenced_blobs(&analytics)?)?;

        Ok(Self {
            inner: Arc::new(StoreInner {
                maintenance: RwLock::new(()),
                analytics: Mutex::new(analytics),
                blob_dir,
                backup_dir,
                #[cfg(test)]
                _test_session: test_session,
            }),
        })
    }

    pub async fn ingest(&self, documents: Vec<Document>) -> Result<()> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.ingest_blocking(documents))
            .await
            .map_err(|error| Error::Storage(format!("commit embedded store task: {error}")))?
    }

    pub(crate) async fn ingest_signal_rows(
        &self,
        table: SignalTable,
        rows: Vec<Value>,
    ) -> Result<()> {
        if rows.is_empty() {
            return Ok(());
        }
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.ingest_signal_rows_blocking(table, rows))
            .await
            .map_err(|error| Error::Storage(format!("store OTLP batch task: {error}")))?
    }

    fn ingest_signal_rows_blocking(&self, table: SignalTable, rows: Vec<Value>) -> Result<()> {
        let _maintenance = self
            .inner
            .maintenance
            .read()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        let mut body = String::new();
        for mut row in rows {
            if matches!(table, SignalTable::Spans)
                && let Some(row) = row.as_object_mut()
            {
                row.entry("replay_id")
                    .or_insert_with(|| Value::String(String::new()));
                if !row
                    .get("segment_id")
                    .and_then(Value::as_str)
                    .is_some_and(|value| !value.is_empty())
                    || !row.get("is_segment").is_some_and(Value::is_boolean)
                {
                    return Err(Error::InvalidRequest(
                        "telemetry span has no segment identity".into(),
                    ));
                }
            }
            serde_json::to_writer(StringWriter(&mut body), &row)
                .map_err(|error| Error::Storage(format!("encode OTLP row: {error}")))?;
            body.push('\n');
        }
        let query = format!(
            "INSERT INTO {ANALYTICS_DATABASE}.{} FORMAT JSONEachRow\n{body}",
            table.name()
        );
        self.with_analytics(|analytics| {
            analytics.execute(&query, None).map_err(|error| {
                Error::Storage(format!(
                    "insert OTLP {} into embedded chDB: {error}",
                    table.name()
                ))
            })?;
            Ok(())
        })?;
        Ok(())
    }

    fn ingest_blocking(&self, mut documents: Vec<Document>) -> Result<()> {
        if documents.is_empty() {
            return Ok(());
        }
        let _maintenance = self
            .inner
            .maintenance
            .read()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        persist_document_blobs(&self.inner.blob_dir, &mut documents)?;

        self.ingest_analytics(&documents)?;
        Ok(())
    }

    pub async fn create_backup(&self) -> Result<(String, PathBuf)> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.create_backup_blocking())
            .await
            .map_err(|error| Error::Storage(format!("create telemetry backup task: {error}")))?
    }

    pub async fn delete_service(&self, service_id: String) -> Result<()> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.delete_service_blocking(&service_id))
            .await
            .map_err(|error| Error::Storage(format!("delete telemetry service task: {error}")))?
    }

    fn delete_service_blocking(&self, service_id: &str) -> Result<()> {
        let _maintenance = self
            .inner
            .maintenance
            .write()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        let analytics = self
            .inner
            .analytics
            .lock()
            .map_err(|_| Error::Storage("chDB session lock is poisoned".into()))?;
        for table in ["documents", "spans", "logs", "metrics", "profiles"] {
            analytics
                .execute(
                    &format!(
                        "ALTER TABLE {ANALYTICS_DATABASE}.{table} DELETE WHERE service_id = {} SETTINGS mutations_sync=2",
                        chdb_string(service_id)
                    ),
                    None,
                )
                .map_err(|error| {
                    Error::Storage(format!("delete service telemetry from {table}: {error}"))
                })?;
        }
        let referenced = referenced_blobs(&analytics)?;
        drop(analytics);
        remove_unreferenced_blobs(&self.inner.blob_dir, &referenced)
    }

    pub async fn restore_backup(archive: PathBuf, volume: PathBuf) -> Result<()> {
        #[cfg(test)]
        let test_session = test_session_gate()
            .acquire_owned()
            .await
            .map_err(|_| Error::Storage("test chDB session gate is closed".into()))?;
        tokio::task::spawn_blocking(move || {
            restore_backup_blocking(
                &archive,
                &volume,
                #[cfg(test)]
                test_session,
            )
        })
        .await
        .map_err(|error| Error::Storage(format!("restore telemetry backup task: {error}")))?
    }

    fn create_backup_blocking(&self) -> Result<(String, PathBuf)> {
        let _maintenance = self
            .inner
            .maintenance
            .write()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        let id = Uuid::new_v4().simple().to_string();
        let native_name = format!("native-{id}");
        let native_path = self.inner.backup_dir.join(&native_name);
        let archive_path = self.inner.backup_dir.join(format!("export-{id}.tar"));
        let cleanup = |error| {
            let _ = fs::remove_dir_all(&native_path);
            let _ = fs::remove_file(&archive_path);
            Err(error)
        };

        let analytics = match self.inner.analytics.lock() {
            Ok(analytics) => analytics,
            Err(_) => return cleanup(Error::Storage("chDB session lock is poisoned".into())),
        };
        let referenced = match referenced_blobs(&analytics) {
            Ok(referenced) => referenced,
            Err(error) => return cleanup(error),
        };
        if let Err(error) = remove_unreferenced_blobs(&self.inner.blob_dir, &referenced) {
            return cleanup(error);
        }
        if let Err(error) = analytics.execute(
            &format!(
                "BACKUP DATABASE {ANALYTICS_DATABASE} TO Disk('telemetry_backups', {})",
                chdb_string(&native_name)
            ),
            None,
        ) {
            return cleanup(Error::Storage(format!(
                "create native chDB backup: {error}"
            )));
        }
        drop(analytics);

        if let Err(error) = write_backup_archive(&archive_path, &native_path, &self.inner.blob_dir)
        {
            return cleanup(error);
        }
        if let Err(error) = fs::remove_dir_all(&native_path) {
            return cleanup(Error::Storage(format!(
                "remove native chDB backup staging directory: {error}"
            )));
        }
        Ok((id, archive_path))
    }

    pub async fn remove_backup(&self, id: &str) -> Result<()> {
        if !valid_backup_id(id) {
            return Err(Error::InvalidRequest(
                "invalid telemetry backup identifier".into(),
            ));
        }
        let path = self.inner.backup_dir.join(format!("export-{id}.tar"));
        match tokio::fs::remove_file(path).await {
            Ok(()) => Ok(()),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(error) => Err(Error::Storage(format!("remove telemetry backup: {error}"))),
        }
    }

    pub fn backup_path(&self, id: &str) -> Result<PathBuf> {
        if !valid_backup_id(id) {
            return Err(Error::InvalidRequest(
                "invalid telemetry backup identifier".into(),
            ));
        }
        Ok(self.inner.backup_dir.join(format!("export-{id}.tar")))
    }

    fn ingest_analytics(&self, documents: &[Document]) -> Result<()> {
        let base_version = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|error| Error::Storage(format!("read system clock: {error}")))?
            .as_nanos()
            .try_into()
            .unwrap_or(u64::MAX.saturating_sub(documents.len() as u64));
        let wal_id = Uuid::new_v4().simple().to_string();
        let mut body = String::new();
        for (index, document) in documents.iter().enumerate() {
            let storage_id =
                stable_storage_id(document).unwrap_or_else(|| format!("event:{wal_id}:{index}"));
            let version = if document.doc_kind == "issue" {
                document
                    .revision
                    .filter(|revision| *revision > 0)
                    .ok_or_else(|| {
                        Error::Storage("issue document has no positive revision".into())
                    })?
            } else {
                base_version.saturating_add(index as u64)
            };
            let row = AnalyticsRow::new(&storage_id, version, document)?;
            serde_json::to_writer(StringWriter(&mut body), &row)
                .map_err(|error| Error::Storage(format!("encode chDB row: {error}")))?;
            body.push('\n');
        }
        // The event and its derived issue state must stay in one ClickHouse input block.
        // Document batches are small, so parallel parsing adds no useful throughput here.
        let query = format!(
            "INSERT INTO {ANALYTICS_DATABASE}.documents SETTINGS \
             date_time_input_format='best_effort', input_format_parallel_parsing=0 \
             FORMAT JSONEachRow\n{body}"
        );
        self.with_analytics(|analytics| {
            analytics.execute(&query, None).map_err(|error| {
                Error::Storage(format!("insert documents into embedded chDB: {error}"))
            })?;
            Ok(())
        })?;
        Ok(())
    }

    pub async fn search(&self, request: SearchRequest) -> Result<SearchResponse> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.search_blocking(request))
            .await
            .map_err(|error| Error::Storage(format!("search embedded store task: {error}")))?
    }

    pub async fn search_all(&self, query: SearchQuery, sort_by: &str) -> Result<Vec<Value>> {
        let count = self
            .search(SearchRequest {
                query: query.clone(),
                max_hits: 0,
                start_offset: None,
                sort_by: String::new(),
            })
            .await?
            .num_hits;
        if count == 0 {
            return Ok(Vec::new());
        }
        let max_hits = usize::try_from(count)
            .map_err(|_| Error::Storage("search result count exceeds this platform".into()))?;
        self.search(SearchRequest {
            query,
            max_hits,
            start_offset: None,
            sort_by: sort_by.to_owned(),
        })
        .await
        .map(|response| response.hits)
    }

    pub(crate) async fn metric_samples(
        &self,
        scope_kind: String,
        scope_ids: Vec<String>,
        from_unix_nano: u64,
        to_unix_nano: u64,
    ) -> Result<Vec<Value>> {
        if scope_kind.is_empty() || from_unix_nano == 0 || to_unix_nano < from_unix_nano {
            return Err(Error::InvalidRequest(
                "invalid telemetry metric query".into(),
            ));
        }
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut clauses = vec![
                format!("scope_kind = {}", chdb_string(&scope_kind)),
                format!("time_unix_nano BETWEEN {from_unix_nano} AND {to_unix_nano}"),
                "field != ''".into(),
            ];
            if !scope_ids.is_empty() {
                let ids = scope_ids
                    .iter()
                    .map(|scope_id| chdb_string(scope_id))
                    .collect::<Vec<_>>()
                    .join(", ");
                clauses.push(format!("scope_id IN ({ids})"));
            }
            let query = format!(
                "SELECT scope_kind, scope_id, time_unix_nano, \
                 mapFromArrays(groupArray(field), groupArray(multiIf(\
                   arrayExists((key, value) -> key = 'platformd.value.type' AND value = 'bool', attribute_keys, attribute_values), \
                     concat('b:', if(value_int IS NOT NULL, toString(value_int), toString(value_double))), \
                   value_int IS NOT NULL, concat('i:', toString(value_int)), \
                   concat('f:', toString(value_double))))) AS values, \
                 mapFilter((field_name, field_attributes) -> field_attributes != '{{}}', \
                   mapFromArrays(groupArray(field), groupArray(toJSONString(mapFilter(\
                     (key, value) -> startsWith(key, 'platformd.dimension.'), \
                     mapFromArrays(attribute_keys, attribute_values)))))) AS attributes \
                 FROM {ANALYTICS_DATABASE}.metrics WHERE {} \
                 GROUP BY scope_kind, scope_id, time_unix_nano ORDER BY time_unix_nano, scope_id",
                clauses.join(" AND ")
            );
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| Error::Storage(format!("query chDB metrics: {error}")))?;
                output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!("decode chDB metric sample: {error}"))
                        })
                    })
                    .collect()
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query telemetry metrics task: {error}")))?
    }

    pub(crate) async fn log_records(&self, request: LogQuery) -> Result<LogPage> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut clauses = vec![service_filter(&request.service_ids)];
            if let Some(deployment_id) = request.deployment_id.as_deref() {
                clauses.push(format!(
                    "deployment_id = {}",
                    chdb_string(deployment_id)
                ));
            }
            if let Some(contains) = request.contains.as_deref() {
                clauses.push(format!(
                    "(positionCaseInsensitiveUTF8(message, {0}) > 0 OR \
                     positionCaseInsensitiveUTF8(ifNull(body_json, ''), {0}) > 0)",
                    chdb_string(contains)
                ));
            }
            clauses.extend(request.field_filters.iter().map(log_field_clause));
            if let Some(severity_text) = request.severity_text.as_deref() {
                clauses.push(format!(
                    "severity_text = {}",
                    chdb_string(severity_text)
                ));
            }
            if let Some(trace_id) = request.trace_id.as_deref() {
                clauses.push(format!("trace_id = {}", chdb_string(trace_id)));
            }
            if let Some(span_id) = request.span_id.as_deref() {
                clauses.push(format!("span_id = {}", chdb_string(span_id)));
            }
            if let Some(from) = request.from_unix_nano {
                clauses.push(format!("time_unix_nano >= {from}"));
            }
            if let Some(to) = request.to_unix_nano {
                clauses.push(format!("time_unix_nano <= {to}"));
            }
            if let (Some(time), Some(id)) = (
                request.after_time_unix_nano,
                request.after_id.as_deref(),
            ) {
                let comparison = if request.ascending { ">" } else { "<" };
                clauses.push(format!(
                    "(time_unix_nano {comparison} {time} OR (time_unix_nano = {time} AND toString(id) {comparison} {}))",
                    chdb_string(id)
                ));
            }
            let direction = if request.ascending { "ASC" } else { "DESC" };
            let query = format!(
                "SELECT toString(id) AS id, service_id, time_unix_nano, stream, leftUTF8(message, 65536) AS text, partial, \
                 deployment_id, attempt_id, trace_id, span_id, severity_number, severity_text, body_json, \
                 if(JSON_VALUE(scope, '$.name') = 'platformd.before_deploy', 'before_deploy', '') AS phase \
                 FROM {ANALYTICS_DATABASE}.logs WHERE {} \
                 ORDER BY time_unix_nano {direction}, id {direction} LIMIT {}",
                clauses.join(" AND "),
                request.limit + 1
            );
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| Error::Storage(format!("query chDB logs: {error}")))?;
                let mut records = output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str::<LogRecord>(line).map_err(|error| {
                            Error::Storage(format!("decode chDB log record: {error}"))
                        })
                    })
                    .collect::<Result<Vec<_>>>()?;
                let truncated = records.len() > request.limit;
                records.truncate(request.limit);
                let cursor = records.last().cloned();
                if !request.ascending {
                    records.reverse();
                }
                Ok(LogPage {
                    records,
                    truncated,
                    next_time_unix_nano: cursor.as_ref().map(|record| record.time_unix_nano),
                    next_id: cursor.map(|record| record.id),
                })
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query telemetry logs task: {error}")))?
    }

    pub(crate) async fn trace_summaries(
        &self,
        request: TraceSummaryQuery,
    ) -> Result<Vec<TraceSummary>> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut having_clauses = Vec::new();
            if let Some(from) = request.from_unix_nano {
                having_clauses.push(format!("started_at_unix_nano >= {from}"));
            }
            if let Some(to) = request.to_unix_nano {
                having_clauses.push(format!("started_at_unix_nano <= {to}"));
            }
            match request.status {
                TraceSummaryStatus::All => {}
                TraceSummaryStatus::Error => having_clauses.push("error_span_count > 0".into()),
                TraceSummaryStatus::Ok => having_clauses.push("error_span_count = 0".into()),
            }
            let parsed_search = trace_search(request.search.as_deref());
            having_clauses.extend(parsed_search.having_clauses);
            let having = if having_clauses.is_empty() {
                String::new()
            } else {
                format!(" HAVING {}", having_clauses.join(" AND "))
            };
            let services = service_filter(&request.service_ids);
            // Product traces are OTEL-shaped: one list row per distributed trace_id.
            // Sentry transactions stay as segment_id within that trace; primary segment
            // is the earliest root for deep links / summary naming.
            let anchor_filter = request.anchor_service_id.as_deref().map_or_else(
                String::new,
                |anchor| {
                    format!(
                        " AND trace_id IN (SELECT DISTINCT trace_id FROM {ANALYTICS_DATABASE}.spans FINAL WHERE service_id = {} AND notEmpty(segment_id))",
                        chdb_string(anchor)
                    )
                },
            );
            let search_filter =
                trace_search_filter(&request.service_ids, parsed_search.text.as_deref());
            let order = match request.order {
                TraceSummaryOrder::Latest => "started_at_unix_nano DESC, trace_id DESC",
                TraceSummaryOrder::Slowest => {
                    "duration_nano DESC, started_at_unix_nano DESC, trace_id DESC"
                }
                TraceSummaryOrder::Spans => {
                    "span_count DESC, started_at_unix_nano DESC, trace_id DESC"
                }
            };
            let query = format!(
                "SELECT trace_id, \
                 if(empty(argMinIf(segment_id, start_time_unix_nano, is_segment)), \
                    argMin(segment_id, start_time_unix_nano), \
                    argMinIf(segment_id, start_time_unix_nano, is_segment)) AS primary_segment_id, \
                 if(empty(argMinIf(service_id, start_time_unix_nano, is_segment)), \
                    argMin(service_id, start_time_unix_nano), \
                    argMinIf(service_id, start_time_unix_nano, is_segment)) AS root_service_id, \
                 if(empty(argMinIf(name, start_time_unix_nano, is_segment)), \
                    argMin(name, start_time_unix_nano), \
                    argMinIf(name, start_time_unix_nano, is_segment)) AS name, \
                 min(start_time_unix_nano) AS started_at_unix_nano, \
                 greatest(max(end_time_unix_nano), min(start_time_unix_nano)) - min(start_time_unix_nano) AS duration_nano, \
                 count() AS span_count, countIf(status_code = 2) AS error_span_count, \
                 arraySort(groupUniqArray(source)) AS sources, \
                 toBool(countIf(notEmpty(ai_kind)) > 0) AS is_ai, \
                 argMinIf(ai_agent, start_time_unix_nano, notEmpty(ai_agent)) AS ai_agent, \
                 countIf(ai_kind = 'agent') AS ai_agent_run_count, \
                 argMinIf(ai_model, start_time_unix_nano, notEmpty(ai_model)) AS ai_model, \
                 argMinIf(ai_provider, start_time_unix_nano, notEmpty(ai_provider)) AS ai_provider, \
                 if(countIf(ai_kind IN ('model', 'embedding', 'rerank') AND isNotNull(ai_input_tokens)) > 0, \
                    sumIf(ifNull(ai_input_tokens, 0), ai_kind IN ('model', 'embedding', 'rerank')), \
                    if(ai_agent_run_count = 1, maxIf(ai_input_tokens, ai_kind = 'agent'), NULL)) AS ai_input_tokens, \
                 if(countIf(ai_kind IN ('model', 'embedding', 'rerank') AND isNotNull(ai_output_tokens)) > 0, \
                    sumIf(ifNull(ai_output_tokens, 0), ai_kind IN ('model', 'embedding', 'rerank')), \
                    if(ai_agent_run_count = 1, maxIf(ai_output_tokens, ai_kind = 'agent'), NULL)) AS ai_output_tokens, \
                 if(countIf(ai_kind IN ('model', 'embedding', 'rerank') AND isNotNull(ai_cache_read_tokens)) > 0, \
                    sumIf(ifNull(ai_cache_read_tokens, 0), ai_kind IN ('model', 'embedding', 'rerank')), \
                    if(ai_agent_run_count = 1, maxIf(ai_cache_read_tokens, ai_kind = 'agent'), NULL)) AS ai_cache_read_tokens, \
                 if(countIf(ai_kind IN ('model', 'embedding', 'rerank') AND isNotNull(ai_cache_write_tokens)) > 0, \
                    sumIf(ifNull(ai_cache_write_tokens, 0), ai_kind IN ('model', 'embedding', 'rerank')), \
                    if(ai_agent_run_count = 1, maxIf(ai_cache_write_tokens, ai_kind = 'agent'), NULL)) AS ai_cache_write_tokens, \
                 if(countIf(ai_kind = 'model' AND isNotNull(ai_reasoning_tokens)) > 0, \
                    sumIf(ifNull(ai_reasoning_tokens, 0), ai_kind = 'model'), \
                    if(ai_agent_run_count = 1, maxIf(ai_reasoning_tokens, ai_kind = 'agent'), NULL)) AS ai_reasoning_tokens, \
                 if(countIf(ai_kind IN ('model', 'embedding', 'rerank') AND isNotNull(ai_cost_usd)) > 0, \
                    sumIf(ifNull(ai_cost_usd, 0), ai_kind IN ('model', 'embedding', 'rerank')), \
                    if(ai_agent_run_count = 1, maxIf(ai_cost_usd, ai_kind = 'agent'), NULL)) AS ai_cost_usd, \
                 if(countIf(isNotNull(ai_ttft_seconds)) > 0, \
                    minIf(ifNull(ai_ttft_seconds, 0), isNotNull(ai_ttft_seconds)), NULL) AS ai_ttft_seconds, \
                 if(countIf(isNotNull(ai_tokens_per_second)) > 0, \
                    avgIf(ifNull(ai_tokens_per_second, 0), isNotNull(ai_tokens_per_second)), NULL) AS ai_tokens_per_second, \
                 countIf(ai_kind IN ('model', 'embedding', 'rerank')) AS ai_model_call_count, \
                 countIf(ai_kind = 'tool') AS ai_tool_call_count \
                 FROM {ANALYTICS_DATABASE}.spans FINAL \
                 WHERE {services} AND notEmpty(segment_id){anchor_filter}{search_filter} \
                 GROUP BY trace_id{having} ORDER BY {order} LIMIT {limit} OFFSET {offset}",
                limit = request.limit,
                offset = request.offset,
            );
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| Error::Storage(format!("query chDB traces: {error}")))?;
                output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!("decode chDB trace summary: {error}"))
                        })
                    })
                    .collect()
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query telemetry traces task: {error}")))?
    }

    pub(crate) async fn trace(
        &self,
        anchor_service_id: Option<String>,
        service_ids: Vec<String>,
        trace_id: String,
        requested_segment_id: Option<String>,
    ) -> Result<TraceDetail> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let services = service_filter(&service_ids);
            let segment_id = if let Some(segment_id) = requested_segment_id {
                segment_id
            } else {
                let anchor = anchor_service_id
                    .as_deref()
                    .map_or_else(|| services.clone(), |service_id| {
                        format!("service_id = {}", chdb_string(service_id))
                    });
                let segment_query = format!(
                    "SELECT segment_id FROM {ANALYTICS_DATABASE}.spans FINAL \
                     WHERE {anchor} AND trace_id = {} AND notEmpty(segment_id) \
                     ORDER BY is_segment DESC, start_time_unix_nano ASC, span_id ASC LIMIT 1",
                    chdb_string(&trace_id)
                );
                store.with_analytics(|analytics| {
                    let output = analytics
                        .execute(
                            &segment_query,
                            Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                        )
                        .map_err(|error| {
                            Error::Storage(format!("query chDB trace segment: {error}"))
                        })?;
                    output
                        .data_utf8_lossy()
                        .lines()
                        .find(|line| !line.is_empty())
                        .map(serde_json::from_str::<TraceSegmentIdRow>)
                        .transpose()
                        .map_err(|error| {
                            Error::Storage(format!("decode chDB trace segment: {error}"))
                        })?
                        .map(|row| row.segment_id)
                        .ok_or(Error::NotFound)
                })?
            };
            // OTEL-first detail: every Sentry/OTLP segment that shares this
            // distributed trace_id is loaded so parent_span_id forms one waterfall.
            let query = format!(
                "SELECT service_id, trace_id, segment_id, is_segment, span_id, parent_span_id, trace_state, name, kind, \
                 start_time_unix_nano, end_time_unix_nano, duration_nano, status_code, \
                 status_message, flags, resource, scope, span, received_at_unix_nano, source, \
                 ai_kind, ai_operation, ai_provider, ai_model, ai_agent, ai_input_tokens, \
                 ai_output_tokens, ai_cache_read_tokens, ai_cache_write_tokens, ai_reasoning_tokens, \
                 ai_cost_usd, ai_ttft_seconds, ai_tokens_per_second \
                 FROM {ANALYTICS_DATABASE}.spans FINAL \
                 WHERE {services} AND trace_id = {} AND notEmpty(segment_id) \
                 ORDER BY start_time_unix_nano, span_id",
                chdb_string(&trace_id)
            );
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| Error::Storage(format!("query chDB trace: {error}")))?;
                let mut spans: Vec<TraceSpan> = Vec::new();
                for line in output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                {
                    let mut row: Value = serde_json::from_str(line).map_err(|error| {
                        Error::Storage(format!("decode chDB trace span: {error}"))
                    })?;
                    let object = row
                        .as_object_mut()
                        .ok_or_else(|| Error::Storage("chDB trace span is not an object".into()))?;
                    for field in ["resource", "scope", "span"] {
                        let decoded = object
                            .get(field)
                            .and_then(Value::as_str)
                            .filter(|value| !value.is_empty())
                            .map(serde_json::from_str)
                            .transpose()
                            .map_err(|error| {
                                Error::Storage(format!("decode chDB trace {field}: {error}"))
                            })?
                            .unwrap_or(Value::Null);
                        object.insert(field.into(), decoded);
                    }
                    spans.push(serde_json::from_value(row).map_err(|error| {
                        Error::Storage(format!("decode chDB trace span fields: {error}"))
                    })?);
                }
                if spans.is_empty() {
                    return Err(Error::NotFound);
                }
                let trace_start = spans
                    .iter()
                    .map(|span| span.start_time_unix_nano)
                    .min()
                    .unwrap_or_default();
                let trace_end = spans
                    .iter()
                    .map(|span| span.end_time_unix_nano.max(span.start_time_unix_nano))
                    .max()
                    .unwrap_or(trace_start);
                let baseline_query = format!(
                    "SELECT name, kind, avg(duration_nano) AS baseline_duration_nano \
                     FROM {ANALYTICS_DATABASE}.spans FINAL \
                     WHERE {services} AND duration_nano > 0 \
                     AND start_time_unix_nano >= toUnixTimestamp64Nano(now64(9) - INTERVAL 24 HOUR) \
                     GROUP BY name, kind",
                );
                let baseline_output = analytics
                    .execute(
                        &baseline_query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB trace baselines: {error}"))
                    })?;
                let mut baselines = HashMap::new();
                for line in baseline_output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                {
                    let row: TraceBaselineRow = serde_json::from_str(line).map_err(|error| {
                        Error::Storage(format!("decode chDB trace baseline: {error}"))
                    })?;
                    baselines.insert((row.name, row.kind), row.baseline_duration_nano);
                }
                for span in &mut spans {
                    span.baseline_duration_nano = baselines
                        .get(&(span.name.clone(), span.kind))
                        .copied();
                }

                let metric_query = format!(
                    "SELECT name, unit, time_unix_nano, \
                     coalesce(value_double, toFloat64(value_int), sum, toFloat64(count)) AS value, exemplars \
                     FROM {ANALYTICS_DATABASE}.metrics \
                     WHERE {services} AND positionCaseInsensitive(exemplars, {}) > 0 \
                     ORDER BY time_unix_nano LIMIT 100",
                    chdb_string(&trace_id)
                );
                let metric_output = analytics
                    .execute(
                        &metric_query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB trace metric exemplars: {error}"))
                    })?;
                let span_ids = spans
                    .iter()
                    .map(|span| span.span_id.as_str())
                    .collect::<HashSet<_>>();
                let metrics = metric_output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        let row: TraceMetricRow = serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!("decode chDB trace metric exemplar: {error}"))
                        })?;
                        let span_id = exemplar_span_id(&row.exemplars, &trace_id)?;
                        Ok(TraceMetricSample {
                            name: row.name,
                            unit: row.unit,
                            time_unix_nano: row.time_unix_nano,
                            value: row.value,
                            span_id,
                        })
                    })
                    .collect::<Result<Vec<_>>>()?
                    .into_iter()
                    .filter(|metric| span_ids.contains(metric.span_id.as_str()))
                    .collect();
                let related_query = format!(
                    "SELECT segment_id, \
                     if(empty(argMinIf(service_id, start_time_unix_nano, is_segment)), \
                        argMin(service_id, start_time_unix_nano), \
                        argMinIf(service_id, start_time_unix_nano, is_segment)) AS segment_service_id, \
                     if(empty(argMinIf(name, start_time_unix_nano, is_segment)), \
                        argMin(name, start_time_unix_nano), \
                        argMinIf(name, start_time_unix_nano, is_segment)) AS name, \
                     min(start_time_unix_nano) AS started_at_unix_nano, \
                     greatest(max(end_time_unix_nano), min(start_time_unix_nano)) - min(start_time_unix_nano) AS duration_nano, \
                     count() AS span_count, countIf(status_code = 2) AS error_span_count \
                     FROM {ANALYTICS_DATABASE}.spans FINAL \
                     WHERE {services} AND trace_id = {} AND notEmpty(segment_id) AND segment_id != {} \
                     GROUP BY segment_id ORDER BY started_at_unix_nano LIMIT 100",
                    chdb_string(&trace_id),
                    chdb_string(&segment_id)
                );
                let related_output = analytics
                    .execute(
                        &related_query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB related trace segments: {error}"))
                    })?;
                let related_segments = related_output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!("decode chDB related trace segment: {error}"))
                        })
                    })
                    .collect::<Result<Vec<_>>>()?;
                let profile_ids = trace_profile_ids(&spans);
                let profiles = trace_profiles(
                    analytics,
                    &service_ids,
                    &trace_id,
                    &profile_ids,
                    trace_start,
                    trace_end,
                )?;
                Ok(TraceDetail {
                    trace_id,
                    segment_id,
                    spans,
                    related_segments,
                    metrics,
                    profiles,
                })
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query telemetry trace task: {error}")))?
    }

    pub(crate) async fn metric_catalog(
        &self,
        service_ids: Vec<String>,
    ) -> Result<Vec<MetricDescriptor>> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let query = format!(
                "SELECT name, argMax(description, time_unix_nano) AS description, \
                 argMax(unit, time_unix_nano) AS unit, argMax(kind, time_unix_nano) AS kind, \
                 arraySort(arrayDistinct(arrayFlatten(groupArray(attribute_keys)))) AS attribute_keys, \
                 max(time_unix_nano) AS last_seen_unix_nano \
                 FROM {ANALYTICS_DATABASE}.metrics WHERE {} AND notEmpty(name) \
                 GROUP BY name ORDER BY name LIMIT 1000",
                service_filter(&service_ids)
            );
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB metric catalog: {error}"))
                    })?;
                output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!("decode chDB metric descriptor: {error}"))
                        })
                    })
                    .collect()
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query telemetry metric catalog task: {error}")))?
    }

    pub(crate) async fn replay_trace_ids(
        &self,
        service_id: String,
        replay_id: String,
        from_unix_nano: Option<u64>,
        to_unix_nano: Option<u64>,
    ) -> Result<Vec<String>> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let mut clauses = vec![
                format!("service_id = {}", chdb_string(&service_id)),
                format!("replay_id = {}", chdb_string(&replay_id)),
            ];
            if let Some(from) = from_unix_nano {
                clauses.push(format!("end_time_unix_nano >= {from}"));
            }
            if let Some(to) = to_unix_nano {
                clauses.push(format!("start_time_unix_nano <= {to}"));
            }
            let query = format!(
                "SELECT DISTINCT trace_id FROM {ANALYTICS_DATABASE}.spans FINAL \
                 WHERE {} ORDER BY trace_id LIMIT 1000",
                clauses.join(" AND ")
            );
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB replay traces: {error}"))
                    })?;
                output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str::<TraceIdRow>(line)
                            .map(|row| row.trace_id)
                            .map_err(|error| {
                                Error::Storage(format!("decode chDB replay trace: {error}"))
                            })
                    })
                    .collect()
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query replay traces task: {error}")))?
    }

    pub(crate) async fn issue_insights(
        &self,
        service_id: String,
        issue_id: String,
    ) -> Result<IssueInsights> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            let filter = format!(
                "doc_kind = 'event' AND service_id = {} AND issue_id = {}",
                chdb_string(&service_id),
                chdb_string(&issue_id)
            );
            let dimensions = [
                ("release", "ifNull(release, '')"),
                ("environment", "ifNull(environment, '')"),
                ("platform", "ifNull(platform, '')"),
                ("user", "ifNull(user, '')"),
                (
                    "browser",
                    "ifNull(JSON_VALUE(source, '$.payload.contexts.browser.name'), '')",
                ),
                (
                    "device",
                    "coalesce(JSON_VALUE(source, '$.payload.contexts.device.name'), JSON_VALUE(source, '$.payload.contexts.device.model'), '')",
                ),
                (
                    "os",
                    "ifNull(JSON_VALUE(source, '$.payload.contexts.os.name'), '')",
                ),
            ];
            let distribution_query = dimensions
                .iter()
                .map(|(key, expression)| {
                    format!(
                        "SELECT {} AS key, value, count FROM (\
                         SELECT {expression} AS value, count() AS count \
                         FROM {ANALYTICS_DATABASE}.documents FINAL WHERE {filter} \
                         GROUP BY value HAVING notEmpty(value) ORDER BY count DESC LIMIT 5)",
                        chdb_string(key)
                    )
                })
                .collect::<Vec<_>>()
                .join(" UNION ALL ");
            store.with_analytics(|analytics| {
                let user_query = format!(
                    "SELECT uniqExactIf(ifNull(user, ''), notEmpty(ifNull(user, ''))) AS user_count, \
                     argMin(ifNull(event_id, ''), timestamp) AS first_event_id, \
                     argMax(ifNull(event_id, ''), timestamp) AS latest_event_id, \
                     argMax(ifNull(event_id, ''), tuple( \
                       toUInt8(notEmpty(ifNull(replay_id, ''))) * 4 + toUInt8(notEmpty(ifNull(user, ''))) * 2 + \
                       toUInt8(position(source, '\"stacktrace\"') > 0), timestamp)) AS recommended_event_id \
                     FROM {ANALYTICS_DATABASE}.documents FINAL WHERE {filter}"
                );
                let user_count = analytics
                    .execute(
                        &user_query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB issue user count: {error}"))
                    })?;
                let user_count = user_count
                    .data_utf8_lossy()
                    .lines()
                    .find(|line| !line.is_empty())
                    .map(serde_json::from_str::<IssueUserCount>)
                    .transpose()
                    .map_err(|error| {
                        Error::Storage(format!("decode chDB issue user count: {error}"))
                    })?
                    .unwrap_or(IssueUserCount {
                        user_count: 0,
                        first_event_id: String::new(),
                        latest_event_id: String::new(),
                        recommended_event_id: String::new(),
                    });
                let output = analytics
                    .execute(
                        &distribution_query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB issue distributions: {error}"))
                    })?;
                let distributions = output
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!(
                                "decode chDB issue distribution: {error}"
                            ))
                        })
                    })
                    .collect::<Result<Vec<_>>>()?;
                let activity_query = format!(
                    "SELECT bin, count() AS count FROM ( \
                       SELECT least(31, intDiv(timestamp_millis - first_millis, \
                         greatest(1, intDiv(last_millis - first_millis, 32) + 1))) AS bin \
                       FROM ( \
                         SELECT toUInt64(toUnixTimestamp64Milli(timestamp)) AS timestamp_millis, \
                           min(toUInt64(toUnixTimestamp64Milli(timestamp))) OVER () AS first_millis, \
                           max(toUInt64(toUnixTimestamp64Milli(timestamp))) OVER () AS last_millis \
                         FROM {ANALYTICS_DATABASE}.documents FINAL WHERE {filter} \
                       ) \
                     ) GROUP BY bin ORDER BY bin"
                );
                let activity = analytics
                    .execute(
                        &activity_query,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::Storage(format!("query chDB issue activity: {error}"))
                    })?
                    .data_utf8_lossy()
                    .lines()
                    .filter(|line| !line.is_empty())
                    .map(|line| {
                        serde_json::from_str(line).map_err(|error| {
                            Error::Storage(format!("decode chDB issue activity: {error}"))
                        })
                    })
                    .collect::<Result<Vec<_>>>()?;
                Ok(IssueInsights {
                    user_count: user_count.user_count,
                    distributions,
                    activity,
                    first_event_id: user_count.first_event_id,
                    latest_event_id: user_count.latest_event_id,
                    recommended_event_id: user_count.recommended_event_id,
                })
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query issue insights task: {error}")))?
    }

    pub(crate) async fn metric_sql(
        &self,
        service_ids: Vec<String>,
        query: MetricSqlQuery,
    ) -> Result<Vec<MetricSqlRow>> {
        let statement = query.compile(&service_ids)?;
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            store.with_analytics(|analytics| {
                let output = analytics
                    .execute(
                        &statement,
                        Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                    )
                    .map_err(|error| {
                        Error::InvalidRequest(format!("metric SQL failed: {error}"))
                    })?;
                decode_rows(&output.data_utf8_lossy())
            })
        })
        .await
        .map_err(|error| Error::Storage(format!("query telemetry metric SQL task: {error}")))?
    }

    fn search_blocking(&self, request: SearchRequest) -> Result<SearchResponse> {
        self.search_analytics(request)
    }

    fn search_analytics(&self, request: SearchRequest) -> Result<SearchResponse> {
        validate_page(&request)?;
        let where_clause = analytics_where(&request.query)?;
        self.with_analytics(|analytics| {
            let count_query = format!(
                "SELECT count() AS count FROM {ANALYTICS_DATABASE}.documents FINAL{where_clause}"
            );
            let count = analytics
                .execute(&count_query, None)
                .map_err(|error| Error::Storage(format!("count chDB documents: {error}")))?
                .data_utf8()
                .map_err(|error| Error::Storage(format!("decode chDB count: {error}")))?
                .trim()
                .parse::<u64>()
                .map_err(|error| Error::Storage(format!("parse chDB count: {error}")))?;
            let offset = request.start_offset.unwrap_or_default();
            if request.max_hits == 0 || offset as u64 >= count {
                return Ok(SearchResponse { num_hits: count, hits: Vec::new() });
            }
            let order = analytics_order(&request.sort_by)?;
            let query = format!(
                "SELECT source FROM {ANALYTICS_DATABASE}.documents FINAL{where_clause} ORDER BY {order} LIMIT {} OFFSET {}",
                request.max_hits, offset
            );
            let output = analytics
                .execute(
                    &query,
                    Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
                )
                .map_err(|error| Error::Storage(format!("query chDB documents: {error}")))?;
            let mut hits = Vec::with_capacity(request.max_hits);
            for line in output.data_utf8_lossy().lines().filter(|line| !line.is_empty()) {
                let row: SourceRow = serde_json::from_str(line)
                    .map_err(|error| Error::Storage(format!("decode chDB row: {error}")))?;
                hits.push(serde_json::from_str(&row.source).map_err(|error| {
                    Error::Storage(format!("decode stored document: {error}"))
                })?);
            }
            Ok(SearchResponse { num_hits: count, hits })
        })
    }

    pub async fn exists(&self, query: SearchQuery) -> Result<bool> {
        Ok(self
            .search(SearchRequest {
                query,
                max_hits: 0,
                start_offset: None,
                sort_by: String::new(),
            })
            .await?
            .num_hits
            > 0)
    }

    pub async fn read_blob(&self, blob_id: &str) -> Result<Vec<u8>> {
        let path = blob_path(&self.inner.blob_dir, blob_id)?;
        let bytes = tokio::fs::read(path)
            .await
            .map_err(|error| Error::Storage(format!("read blob {blob_id}: {error}")))?;
        let actual = format!("{:x}", Sha256::digest(&bytes));
        if !actual.eq_ignore_ascii_case(blob_id) {
            return Err(Error::Storage(format!(
                "blob {blob_id} failed its SHA-256 integrity check"
            )));
        }
        Ok(bytes)
    }

    pub async fn recording_bytes(&self) -> Result<u64> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.recording_bytes_blocking())
            .await
            .map_err(|error| Error::Storage(format!("measure recording blobs task: {error}")))?
    }

    fn recording_bytes_blocking(&self) -> Result<u64> {
        let _maintenance = self
            .inner
            .maintenance
            .read()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        let blob_ids = {
            let analytics = self
                .inner
                .analytics
                .lock()
                .map_err(|_| Error::Storage("chDB session lock is poisoned".into()))?;
            recording_blob_ids(&analytics)?
        };
        allocated_blob_total(&self.inner.blob_dir, &blob_ids)
    }

    pub(crate) async fn reclaim_expired_recordings(&self) -> Result<()> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.reclaim_expired_recordings_blocking())
            .await
            .map_err(|error| Error::Storage(format!("reclaim expired recordings task: {error}")))?
    }

    fn reclaim_expired_recordings_blocking(&self) -> Result<()> {
        let _maintenance = self
            .inner
            .maintenance
            .write()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        let analytics = self
            .inner
            .analytics
            .lock()
            .map_err(|_| Error::Storage("chDB session lock is poisoned".into()))?;
        mutate_documents(
            &analytics,
            &format!(
                "doc_kind IN ({RECORDING_DOC_KINDS}) AND timestamp < now('UTC') - {RECORDING_RETENTION}"
            ),
            "expire telemetry recordings",
        )?;
        delete_orphaned_content_chunks(&analytics)?;
        let referenced = referenced_blobs(&analytics)?;
        drop(analytics);
        remove_unreferenced_blobs(&self.inner.blob_dir, &referenced)
    }

    pub async fn load_content(
        &self,
        doc_kind: &str,
        service_id: &str,
        content_id: &str,
        maximum_bytes: usize,
    ) -> Result<Vec<u8>> {
        let response = self
            .search(SearchRequest {
                query: all([
                    term("doc_kind", doc_kind),
                    term("service_id", service_id),
                    term("content_id", content_id),
                ]),
                max_hits: MAX_CONTENT_CHUNKS,
                start_offset: None,
                sort_by: "-sequence".into(),
            })
            .await?;
        if response.hits.is_empty() {
            return Err(Error::NotFound);
        }
        let chunk_count = response.hits[0]
            .get("chunk_count")
            .and_then(Value::as_u64)
            .ok_or_else(|| Error::Storage("stored content has no chunk count".into()))?;
        if chunk_count != response.num_hits || response.hits.len() as u64 != response.num_hits {
            return Err(Error::Storage(
                "stored content chunks are incomplete".into(),
            ));
        }

        let mut output = Vec::new();
        for (sequence, chunk) in response.hits.into_iter().enumerate() {
            if chunk.get("sequence").and_then(Value::as_u64) != Some(sequence as u64)
                || chunk.get("chunk_count").and_then(Value::as_u64) != Some(chunk_count)
            {
                return Err(Error::Storage(
                    "stored content chunks are incomplete".into(),
                ));
            }
            let blob_id = chunk
                .get("blob_id")
                .and_then(Value::as_str)
                .ok_or_else(|| Error::Storage("stored content chunk has no blob".into()))?;
            let decoded = self.read_blob(blob_id).await?;
            if chunk.get("size_bytes").and_then(Value::as_u64) != Some(decoded.len() as u64) {
                return Err(Error::Storage(
                    "stored content chunk size does not match its blob".into(),
                ));
            }
            output
                .len()
                .checked_add(decoded.len())
                .filter(|size| *size <= maximum_bytes)
                .ok_or_else(|| Error::Storage("stored content exceeds its size limit".into()))?;
            output.extend_from_slice(&decoded);
        }
        Ok(output)
    }

    fn with_analytics<T>(&self, operation: impl FnOnce(&Session) -> Result<T>) -> Result<T> {
        let analytics = self
            .inner
            .analytics
            .lock()
            .map_err(|_| Error::Storage("chDB session lock is poisoned".into()))?;
        operation(&analytics)
    }

    pub(crate) fn insert_json_rows(&self, table: &str, rows: Vec<Value>) -> Result<()> {
        if rows.is_empty() {
            return Ok(());
        }
        let _maintenance = self
            .inner
            .maintenance
            .read()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        let mut body = String::new();
        for row in rows {
            serde_json::to_writer(StringWriter(&mut body), &row)
                .map_err(|error| Error::Storage(format!("encode analytics row: {error}")))?;
            body.push('\n');
        }
        let query = format!(
            "INSERT INTO {ANALYTICS_DATABASE}.{table} SETTINGS \
             date_time_input_format='best_effort', input_format_parallel_parsing=0 \
             FORMAT JSONEachRow\n{body}"
        );
        self.with_analytics(|analytics| {
            analytics.execute(&query, None).map_err(|error| {
                Error::Storage(format!("insert {table} into embedded chDB: {error}"))
            })?;
            Ok(())
        })
    }

    pub(crate) fn query_json_rows(&self, query: &str) -> Result<Vec<Value>> {
        let _maintenance = self
            .inner
            .maintenance
            .read()
            .map_err(|_| Error::Storage("telemetry maintenance lock is poisoned".into()))?;
        self.with_analytics(|analytics| {
            let output = analytics
                .execute(query, Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]))
                .map_err(|error| Error::Storage(format!("query embedded chDB: {error}")))?;
            output
                .data_utf8_lossy()
                .lines()
                .filter(|line| !line.is_empty())
                .map(|line| {
                    serde_json::from_str(line)
                        .map_err(|error| Error::Storage(format!("decode chDB row: {error}")))
                })
                .collect()
        })
    }
}

fn trace_profile_ids(spans: &[TraceSpan]) -> HashSet<String> {
    let mut ids = HashSet::new();
    for span in spans {
        collect_profile_ids(&span.span, &mut ids);
        collect_profile_ids(&span.resource, &mut ids);
    }
    ids
}

fn collect_profile_ids(value: &Value, ids: &mut HashSet<String>) {
    match value {
        Value::Array(values) => {
            for value in values {
                collect_profile_ids(value, ids);
            }
        }
        Value::Object(object) => {
            if let Some(key) = object.get("key").and_then(Value::as_str)
                && is_profile_id_key(key)
                && let Some(value) = object.get("value").and_then(attribute_string)
            {
                insert_profile_id(ids, value);
            }
            for (key, value) in object {
                if is_profile_id_key(key)
                    && let Some(value) = attribute_string(value)
                {
                    insert_profile_id(ids, value);
                }
                collect_profile_ids(value, ids);
            }
        }
        _ => {}
    }
}

fn is_profile_id_key(key: &str) -> bool {
    matches!(
        key.chars()
            .filter(|character| character.is_ascii_alphanumeric())
            .flat_map(char::to_lowercase)
            .collect::<String>()
            .as_str(),
        "profileid" | "profilerid" | "sentryprofileid" | "sentryprofilerid"
    )
}

fn attribute_string(value: &Value) -> Option<&str> {
    value.as_str().or_else(|| {
        value
            .as_object()
            .and_then(|value| {
                value
                    .get("stringValue")
                    .or_else(|| value.get("string_value"))
            })
            .and_then(Value::as_str)
    })
}

fn insert_profile_id(ids: &mut HashSet<String>, value: &str) {
    let value = value.replace('-', "").to_ascii_lowercase();
    if value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        ids.insert(value);
    }
}

fn trace_profiles(
    analytics: &Session,
    service_ids: &[String],
    trace_id: &str,
    profile_ids: &HashSet<String>,
    trace_start: u64,
    trace_end: u64,
) -> Result<Vec<TraceProfile>> {
    let id_filter = if profile_ids.is_empty() {
        String::new()
    } else {
        let ids = profile_ids
            .iter()
            .map(|value| chdb_string(value))
            .collect::<Vec<_>>()
            .join(", ");
        format!(" OR profiler_id IN ({ids}) OR profile_id IN ({ids})")
    };
    let services = service_filter(service_ids);
    let query = format!(
        "SELECT service_id, if(notEmpty(profiler_id), profiler_id, profile_id) AS profile_key, \
         any(profile_id) AS stored_profile_id, profiler_id, any(platform) AS platform, \
         thread_id, any(thread_name) AS thread_name, \
         span_id, count() AS sample_count, \
         sum(least(sample_time_unix_nano + duration_nano, {trace_end}) - greatest(sample_time_unix_nano, {trace_start})) AS total_duration_nano, \
         min(greatest(sample_time_unix_nano, {trace_start})) AS started_at_unix_nano, \
         max(least(sample_time_unix_nano + duration_nano, {trace_end})) AS ended_at_unix_nano, stack \
         FROM {ANALYTICS_DATABASE}.profiles FINAL \
         WHERE {services} AND sample_time_unix_nano <= {trace_end} \
         AND sample_time_unix_nano + duration_nano >= {trace_start} \
         AND (trace_id = {}{id_filter}) \
         GROUP BY service_id, profile_key, profiler_id, thread_id, span_id, stack \
         ORDER BY started_at_unix_nano, profile_key, total_duration_nano DESC LIMIT 20000",
        chdb_string(trace_id),
    );
    let output = analytics
        .execute(
            &query,
            Some(&[Arg::OutputFormat(OutputFormat::JSONEachRow)]),
        )
        .map_err(|error| Error::Storage(format!("query chDB trace profiles: {error}")))?;
    let mut profiles: HashMap<(String, String), TraceProfile> = HashMap::new();
    for line in output
        .data_utf8_lossy()
        .lines()
        .filter(|line| !line.is_empty())
    {
        let row: TraceProfileRow = serde_json::from_str(line)
            .map_err(|error| Error::Storage(format!("decode chDB trace profile: {error}")))?;
        let frames = serde_json::from_str(&row.stack)
            .map_err(|error| Error::Storage(format!("decode chDB profile stack: {error}")))?;
        let profile = profiles
            .entry((row.service_id.clone(), row.profile_key))
            .or_insert_with(|| TraceProfile {
                service_id: row.service_id,
                profile_id: row.stored_profile_id,
                profiler_id: row.profiler_id,
                platform: row.platform,
                started_at_unix_nano: row.started_at_unix_nano,
                ended_at_unix_nano: row.ended_at_unix_nano,
                sample_count: 0,
                stacks: Vec::new(),
            });
        profile.started_at_unix_nano = profile.started_at_unix_nano.min(row.started_at_unix_nano);
        profile.ended_at_unix_nano = profile.ended_at_unix_nano.max(row.ended_at_unix_nano);
        profile.sample_count += row.sample_count;
        profile.stacks.push(TraceProfileStack {
            thread_id: row.thread_id,
            thread_name: row.thread_name,
            span_id: row.span_id,
            sample_count: row.sample_count,
            duration_nano: row.total_duration_nano,
            frames,
        });
    }
    let mut profiles = profiles.into_values().collect::<Vec<_>>();
    profiles.sort_unstable_by_key(|profile| profile.started_at_unix_nano);
    Ok(profiles)
}

fn log_field_clause(filter: &LogFieldFilter) -> String {
    let path = chdb_string(&format!("$.{}", filter.path));
    let value = chdb_string(&filter.value);
    match filter.operator {
        LogFieldOperator::Equals => format!("JSON_VALUE(body_json, {path}) = {value}"),
        LogFieldOperator::Contains => format!(
            "positionCaseInsensitiveUTF8(ifNull(JSON_VALUE(body_json, {path}), ''), {value}) > 0"
        ),
        LogFieldOperator::Exists => format!("JSON_EXISTS(body_json, {path}) = 1"),
    }
}

#[cfg(test)]
fn test_session_gate() -> Arc<tokio::sync::Semaphore> {
    static GATE: OnceLock<Arc<tokio::sync::Semaphore>> = OnceLock::new();
    GATE.get_or_init(|| Arc::new(tokio::sync::Semaphore::new(1)))
        .clone()
}

impl<'a> AnalyticsRow<'a> {
    fn new(storage_id: &'a str, version: u64, source: &'a Document) -> Result<Self> {
        let source_json = serde_json::to_string(source)
            .map_err(|error| Error::Storage(format!("encode stored document: {error}")))?;
        Ok(Self {
            storage_id,
            version,
            doc_kind: &source.doc_kind,
            service_id: &source.service_id,
            timestamp: &source.timestamp,
            received_at: &source.received_at,
            ingest_id: source.ingest_id.as_deref(),
            event_id: source.event_id.as_deref(),
            issue_id: source.issue_id.as_deref(),
            item_type: source.item_type.as_deref(),
            title: source.title.as_deref(),
            message: source.message.as_deref(),
            level: source.level.as_deref(),
            platform: source.platform.as_deref(),
            environment: source.environment.as_deref(),
            release: source.release.as_deref(),
            dist: source.dist.as_deref(),
            transaction: source.transaction.as_deref(),
            sdk_name: source.sdk_name.as_deref(),
            sdk_version: source.sdk_version.as_deref(),
            user: source.user.as_deref(),
            status: source.status.as_deref(),
            filename: source.filename.as_deref(),
            content_type: source.content_type.as_deref(),
            content_id: source.content_id.as_deref(),
            checksum: source.checksum.as_deref(),
            debug_id: source.debug_id.as_deref(),
            code_id: source.code_id.as_deref(),
            symbol_type: source.symbol_type.as_deref(),
            object_name: source.object_name.as_deref(),
            replay_id: source.replay_id.as_deref(),
            blob_id: source.blob_id.as_deref(),
            segment_id: source.segment_id,
            sequence: source.sequence,
            chunk_count: source.chunk_count,
            size_bytes: source.size_bytes,
            source: source_json,
            search: searchable(source),
        })
    }
}

struct StringWriter<'a>(&'a mut String);

fn exemplar_span_id(exemplars: &str, trace_id: &str) -> Result<String> {
    let values: Value = serde_json::from_str(exemplars)
        .map_err(|error| Error::Storage(format!("decode metric exemplars: {error}")))?;
    Ok(values
        .as_array()
        .into_iter()
        .flatten()
        .find_map(|value| {
            (value.get("traceId").and_then(Value::as_str) == Some(trace_id))
                .then(|| value.get("spanId").and_then(Value::as_str))
                .flatten()
                .map(str::to_owned)
        })
        .unwrap_or_default())
}

impl std::io::Write for StringWriter<'_> {
    fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
        let text = std::str::from_utf8(bytes)
            .map_err(|error| std::io::Error::new(std::io::ErrorKind::InvalidData, error))?;
        self.0.push_str(text);
        Ok(bytes.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

fn initialize_analytics(session: &Session) -> Result<()> {
    for statement in [
        format!("CREATE DATABASE IF NOT EXISTS {ANALYTICS_DATABASE}"),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.documents (\n\
             storage_id String, version UInt64, doc_kind LowCardinality(String), service_id String,\n\
             timestamp DateTime64(6, 'UTC'), received_at DateTime64(6, 'UTC'),\n\
             ingest_id Nullable(String), event_id Nullable(String), issue_id Nullable(String), item_type Nullable(String),\n\
             title Nullable(String), message Nullable(String), level Nullable(String), platform Nullable(String),\n\
             environment Nullable(String), release Nullable(String), dist Nullable(String), transaction Nullable(String),\n\
             sdk_name Nullable(String), sdk_version Nullable(String), user Nullable(String), status Nullable(String),\n\
             filename Nullable(String), content_type Nullable(String), content_id Nullable(String), checksum Nullable(String),\n\
             debug_id Nullable(String), code_id Nullable(String), symbol_type Nullable(String), object_name Nullable(String),\n\
             replay_id Nullable(String), blob_id Nullable(String), segment_id Nullable(UInt64), sequence Nullable(UInt64),\n\
             chunk_count Nullable(UInt64), size_bytes Nullable(UInt64), source String, search String\n\
             ) ENGINE=ReplacingMergeTree(version) ORDER BY (service_id, doc_kind, storage_id)"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.documents DROP CONSTRAINT IF EXISTS document_identity"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.documents ADD CONSTRAINT IF NOT EXISTS document_identity \
             CHECK notEmpty(doc_kind) AND notEmpty(service_id)"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.documents ADD CONSTRAINT IF NOT EXISTS event_identity \
             CHECK doc_kind != 'event' OR isNotNull(event_id)"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.documents ADD CONSTRAINT IF NOT EXISTS issue_identity \
             CHECK doc_kind != 'issue' OR (isNotNull(issue_id) AND version > 0)"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.documents MODIFY TTL timestamp + {RECORDING_RETENTION} \
             DELETE WHERE doc_kind IN ({RECORDING_DOC_KINDS})"
        ),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.spans (\n\
             service_id String, trace_id FixedString(32), span_id FixedString(16), parent_span_id String,\n\
             segment_id String, is_segment Bool,\n\
             trace_state String, name String, kind Int32, start_time_unix_nano UInt64, end_time_unix_nano UInt64,\n\
             duration_nano UInt64, status_code Int32, status_message String, flags UInt32,\n\
             resource String, scope String, span String, received_at_unix_nano UInt64, version UInt64, source LowCardinality(String), replay_id String,\n\
             ai_kind LowCardinality(String), ai_operation String, ai_provider LowCardinality(String), ai_model String, ai_agent String,\n\
             ai_input_tokens Nullable(UInt64), ai_output_tokens Nullable(UInt64), ai_cache_read_tokens Nullable(UInt64),\n\
             ai_cache_write_tokens Nullable(UInt64), ai_reasoning_tokens Nullable(UInt64), ai_cost_usd Nullable(Float64),\n\
             ai_ttft_seconds Nullable(Float64), ai_tokens_per_second Nullable(Float64), search_text String,\n\
             INDEX spans_search_text_idx search_text TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 1\n\
             ) ENGINE=ReplacingMergeTree(version) ORDER BY (service_id, trace_id, span_id) \
             TTL toDateTime(start_time_unix_nano / 1000000000) + INTERVAL 30 DAY DELETE"
        ),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.profiles (\n\
             service_id String, profile_id String, profiler_id String, trace_id String, span_id String,\n\
             platform LowCardinality(String), thread_id String, thread_name String,\n\
             sample_time_unix_nano UInt64, duration_nano UInt64, stack String,\n\
             received_at_unix_nano UInt64, sample_id String, version UInt64\n\
             ) ENGINE=ReplacingMergeTree(version) ORDER BY (service_id, profile_id, sample_id) \
             TTL toDateTime(sample_time_unix_nano / 1000000000) + INTERVAL 30 DAY DELETE"
        ),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.logs (\n\
             id UUID, service_id String, deployment_id String, attempt_id String, stream LowCardinality(String), partial Bool,\n\
             time_unix_nano UInt64, observed_time_unix_nano UInt64,\n\
             trace_id String, span_id String, severity_number Int32, severity_text String, flags UInt32,\n\
             message String, body String, body_json Nullable(String), attributes String,\n\
             resource String, scope String, record String, received_at_unix_nano UInt64\n\
             ) ENGINE=MergeTree ORDER BY (service_id, time_unix_nano, id) \
             TTL toDateTime(time_unix_nano / 1000000000) + INTERVAL 7 DAY DELETE"
        ),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.metrics (\n\
             id UUID, service_id String, scope_kind LowCardinality(String), scope_id String, field String,\n\
             name String, description String, unit String, kind LowCardinality(String),\n\
             start_time_unix_nano UInt64, time_unix_nano UInt64, value_int Nullable(Int64), value_double Nullable(Float64),\n\
             count Nullable(UInt64), sum Nullable(Float64), min Nullable(Float64), max Nullable(Float64),\n\
             explicit_bounds Array(Float64), bucket_counts Array(UInt64), exponential_scale Int32,\n\
             positive_offset Int64, positive_counts Array(UInt64), negative_offset Int64,\n\
             negative_counts Array(UInt64), zero_count UInt64,\n\
             aggregation_temporality Int32, is_monotonic Bool, flags UInt32,\n\
             attribute_keys Array(String), attribute_values Array(String), attributes String, exemplars String, point String,\n\
             resource String, scope String, metric String, received_at_unix_nano UInt64\n\
             ) ENGINE=MergeTree ORDER BY (service_id, name, time_unix_nano, id) \
             TTL toDateTime(time_unix_nano / 1000000000) + INTERVAL 30 DAY DELETE"
        ),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.analytics_events (\n\
             event_id String, tracker_id String, service_id String, distinct_id String, session_id String,\n\
             event_name LowCardinality(String), timestamp DateTime64(3, 'UTC'),\n\
             hostname String, pathname String, page_title String,\n\
             referrer String, referrer_domain String, referrer_source String, channel LowCardinality(String),\n\
             utm_source String, utm_medium String, utm_campaign String, utm_content String, utm_term String,\n\
             gclid String, fbclid String, msclkid String, ttclid String, li_fat_id String, twclid String,\n\
             browser LowCardinality(String), browser_version String, os LowCardinality(String), os_version String,\n\
             device LowCardinality(String), screen String, language String, country LowCardinality(String),\n\
             region String, city String, interactive UInt8, props_keys Array(String), props_values Array(String),\n\
             revenue Nullable(Float64), currency LowCardinality(String),\n\
             bot_kind LowCardinality(String), bot_name String,\n\
             lcp Nullable(Float64), inp Nullable(Float64), cls Nullable(Float64), fcp Nullable(Float64), ttfb Nullable(Float64)\n\
             ) ENGINE=MergeTree PARTITION BY toYYYYMM(timestamp)\n\
             ORDER BY (tracker_id, toDate(timestamp), event_name, distinct_id, timestamp)\n\
             TTL timestamp + INTERVAL 400 DAY DELETE"
        ),
        format!(
            "CREATE TABLE IF NOT EXISTS {ANALYTICS_DATABASE}.analytics_heatmaps (\n\
             tracker_id String, distinct_id String, timestamp DateTime64(3, 'UTC'), pathname String, hostname String,\n\
             x Int16, y Int16, scale_factor Float32, viewport_w UInt16, viewport_h UInt16,\n\
             page_h UInt16, scroll_pct UInt8, event_type LowCardinality(String)\n\
             ) ENGINE=MergeTree PARTITION BY toYYYYMM(timestamp)\n\
             ORDER BY (tracker_id, pathname, timestamp)\n\
             TTL timestamp + INTERVAL 90 DAY DELETE"
        ),
    ] {
        session
            .execute(&statement, None)
            .map_err(|error| Error::Storage(format!("initialize embedded chDB schema: {error}")))?;
    }
    for statement in [
        format!("DROP TABLE IF EXISTS {ANALYTICS_DATABASE}.analytics_aliases"),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS segment_id String AFTER parent_span_id"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS is_segment Bool AFTER segment_id"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS replay_id String AFTER source"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_kind LowCardinality(String) AFTER source"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_operation String AFTER ai_kind"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_provider LowCardinality(String) AFTER ai_operation"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_model String AFTER ai_provider"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_agent String AFTER ai_model"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_input_tokens Nullable(UInt64) AFTER ai_agent"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_output_tokens Nullable(UInt64) AFTER ai_input_tokens"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_cache_read_tokens Nullable(UInt64) AFTER ai_output_tokens"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_cache_write_tokens Nullable(UInt64) AFTER ai_cache_read_tokens"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_reasoning_tokens Nullable(UInt64) AFTER ai_cache_write_tokens"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_cost_usd Nullable(Float64) AFTER ai_reasoning_tokens"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_ttft_seconds Nullable(Float64) AFTER ai_cost_usd"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS ai_tokens_per_second Nullable(Float64) AFTER ai_ttft_seconds"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD COLUMN IF NOT EXISTS search_text String AFTER ai_tokens_per_second"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans ADD INDEX IF NOT EXISTS spans_search_text_idx search_text TYPE text(tokenizer = 'splitByNonAlpha') GRANULARITY 1"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.spans MODIFY TTL toDateTime(start_time_unix_nano / 1000000000) + INTERVAL 30 DAY DELETE"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS attribute_keys Array(String) AFTER flags"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS attribute_values Array(String) AFTER attribute_keys"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS explicit_bounds Array(Float64) AFTER max"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS bucket_counts Array(UInt64) AFTER explicit_bounds"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS exponential_scale Int32 AFTER bucket_counts"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS positive_offset Int64 AFTER exponential_scale"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS positive_counts Array(UInt64) AFTER positive_offset"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS negative_offset Int64 AFTER positive_counts"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS negative_counts Array(UInt64) AFTER negative_offset"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.metrics ADD COLUMN IF NOT EXISTS zero_count UInt64 AFTER negative_counts"
        ),
        format!(
            "ALTER TABLE {ANALYTICS_DATABASE}.analytics_heatmaps ADD COLUMN IF NOT EXISTS hostname String AFTER pathname"
        ),
    ] {
        session.execute(&statement, None).map_err(|error| {
            Error::Storage(format!("migrate embedded chDB metrics schema: {error}"))
        })?;
    }
    Ok(())
}

struct TraceSearch {
    text: Option<String>,
    having_clauses: Vec<String>,
}

fn trace_search(search: Option<&str>) -> TraceSearch {
    let mut text = Vec::new();
    let mut having_clauses = Vec::new();
    for token in search.unwrap_or_default().split_whitespace() {
        let normalized = token.to_ascii_lowercase();
        if let Some(status) = normalized.strip_prefix("status:") {
            having_clauses.push(match status {
                "error" => "error_span_count > 0".into(),
                "ok" => "error_span_count = 0".into(),
                _ => "0".into(),
            });
            continue;
        }
        if let Some(duration) = normalized.strip_prefix("duration:") {
            having_clauses.push(trace_duration_clause(duration).unwrap_or_else(|| "0".into()));
            continue;
        }
        text.push(token);
    }
    TraceSearch {
        text: (!text.is_empty()).then(|| text.join(" ")),
        having_clauses,
    }
}

fn trace_duration_clause(value: &str) -> Option<String> {
    let (operator, value) = if let Some(value) = value.strip_prefix(">=") {
        (">=", value)
    } else if let Some(value) = value.strip_prefix("<=") {
        ("<=", value)
    } else if let Some(value) = value.strip_prefix('>') {
        (">", value)
    } else if let Some(value) = value.strip_prefix('<') {
        ("<", value)
    } else {
        ("=", value)
    };
    let (amount, multiplier) = if let Some(amount) = value.strip_suffix("ms") {
        (amount, 1_000_000_f64)
    } else {
        (value.strip_suffix('s')?, 1_000_000_000_f64)
    };
    let amount = amount.parse::<f64>().ok()?;
    let nanos = amount * multiplier;
    if !nanos.is_finite() || nanos < 0.0 || nanos > u64::MAX as f64 {
        return None;
    }
    Some(format!("duration_nano {operator} {:.0}", nanos.round()))
}

fn trace_search_filter(service_ids: &[String], search: Option<&str>) -> String {
    let Some(search) = search.map(str::trim).filter(|search| !search.is_empty()) else {
        return String::new();
    };
    let tokens = search
        .split(|character: char| {
            !character.is_alphanumeric() && character != '_' && character != '-'
        })
        .filter(|token| !token.is_empty())
        .take(12)
        .map(|token| {
            let pattern = chdb_string(&format!("%{token}%"));
            format!("(name ILIKE {pattern} OR search_text ILIKE {pattern})")
        })
        .collect::<Vec<_>>();
    let trace_id = (search.len() == 32 && search.bytes().all(|byte| byte.is_ascii_hexdigit()))
        .then(|| format!("trace_id = {}", chdb_string(&search.to_ascii_lowercase())));
    let matches = match (trace_id, tokens.is_empty()) {
        (Some(trace_id), false) => format!("({trace_id} OR {})", tokens.join(" AND ")),
        (Some(trace_id), true) => trace_id,
        (None, false) => tokens.join(" AND "),
        (None, true) => return String::new(),
    };
    format!(
        " AND trace_id IN (SELECT trace_id FROM {ANALYTICS_DATABASE}.spans FINAL WHERE {} AND {matches})",
        service_filter(service_ids)
    )
}

fn validate_page(request: &SearchRequest) -> Result<()> {
    request
        .start_offset
        .unwrap_or_default()
        .checked_add(request.max_hits)
        .ok_or_else(|| Error::InvalidRequest("search offset and limit are too large".into()))?;
    Ok(())
}

fn analytics_where(query: &SearchQuery) -> Result<String> {
    let mut clauses = query
        .clauses
        .iter()
        .map(analytics_clause)
        .collect::<Result<Vec<_>>>()?;
    if let Some(text) = query.text.as_deref().filter(|value| !value.is_empty()) {
        clauses.push(format!(
            "positionCaseInsensitiveUTF8(search, {}) > 0",
            chdb_string(text)
        ));
    }
    Ok(if clauses.is_empty() {
        String::new()
    } else {
        format!(" WHERE {}", clauses.join(" AND "))
    })
}

fn analytics_clause(clause: &SearchClause) -> Result<String> {
    match clause {
        SearchClause::Term { field, value } => {
            validate_field(field)?;
            Ok(format!("{field} = {}", chdb_string(value)))
        }
        SearchClause::Any(clauses) if !clauses.is_empty() => Ok(format!(
            "({})",
            clauses
                .iter()
                .map(analytics_clause)
                .collect::<Result<Vec<_>>>()?
                .join(" OR ")
        )),
        SearchClause::Any(_) => Err(Error::InvalidRequest("empty any search clause".into())),
    }
}

fn validate_field(field: &str) -> Result<()> {
    if [
        "doc_kind",
        "service_id",
        "ingest_id",
        "event_id",
        "issue_id",
        "item_type",
        "level",
        "platform",
        "environment",
        "release",
        "dist",
        "sdk_name",
        "sdk_version",
        "status",
        "content_type",
        "content_id",
        "checksum",
        "debug_id",
        "code_id",
        "symbol_type",
        "replay_id",
        "blob_id",
    ]
    .contains(&field)
    {
        Ok(())
    } else {
        Err(Error::InvalidRequest(format!(
            "unknown embedded search field {field}"
        )))
    }
}

fn analytics_order(sort_by: &str) -> Result<&'static str> {
    match sort_by {
        "-sequence" => Ok("sequence ASC, storage_id ASC"),
        "timestamp" => Ok("timestamp DESC, storage_id DESC"),
        "received_at" => Ok("received_at DESC, storage_id DESC"),
        "" => Ok("timestamp DESC, storage_id DESC"),
        value => Err(Error::InvalidRequest(format!(
            "unsupported search sort field {value}"
        ))),
    }
}

pub(crate) fn chdb_string(value: &str) -> String {
    format!("'{}'", value.replace('\\', "\\\\").replace('\'', "\\'"))
}

fn searchable(source: &Document) -> String {
    let payload = source.payload.as_ref().map(Value::to_string);
    let mut searchable = String::new();
    for value in [
        source.event_id.as_deref(),
        source.issue_id.as_deref(),
        source.title.as_deref(),
        source.message.as_deref(),
        source.transaction.as_deref(),
        source.user.as_deref(),
        source.filename.as_deref(),
        source.object_name.as_deref(),
        payload.as_deref(),
    ]
    .into_iter()
    .flatten()
    {
        searchable.push_str(value);
        searchable.push('\n');
    }
    searchable
}

fn persist_document_blobs(blob_dir: &Path, documents: &mut [Document]) -> Result<()> {
    for document in documents {
        if let Some(content) = document.content.take() {
            document.blob_id = Some(write_blob(blob_dir, &content)?);
        }
    }
    Ok(())
}

fn stable_storage_id(document: &Document) -> Option<String> {
    let service_id = &document.service_id;
    match document.doc_kind.as_str() {
        "issue" => Some(format!(
            "issue:{service_id}:{}",
            document.issue_id.as_deref()?
        )),
        "symbolication" => Some(format!(
            "symbolication:{service_id}:{}",
            document.event_id.as_deref()?
        )),
        "replay_event" | "replay_recording" | "replay_video" => Some(format!(
            "replay:{}:{service_id}:{}:{}",
            document.doc_kind,
            document.replay_id.as_deref()?,
            document.segment_id.unwrap_or_default()
        )),
        "content_chunk" => Some(format!(
            "content:{service_id}:{}:{}",
            document.content_id.as_deref()?,
            document.sequence?
        )),
        "upload_chunk" => Some(format!(
            "upload-chunk:{service_id}:{}",
            document.content_id.as_deref()?
        )),
        "artifact" => Some(format!(
            "artifact:{service_id}:{}",
            document.ingest_id.as_deref()?
        )),
        "artifact_content_chunk" => Some(format!(
            "artifact-content:{service_id}:{}:{}",
            document.content_id.as_deref()?,
            document.sequence?
        )),
        _ => None,
    }
}

fn write_blob(blob_dir: &Path, bytes: &[u8]) -> Result<String> {
    let blob_id = format!("{:x}", Sha256::digest(bytes));
    let path = blob_path(blob_dir, &blob_id)?;
    if path.exists() {
        let existing = fs::read(&path)
            .map_err(|error| Error::Storage(format!("read existing blob {blob_id}: {error}")))?;
        if existing == bytes {
            return Ok(blob_id);
        }
        return Err(Error::Storage(format!(
            "blob {blob_id} exists with different content"
        )));
    }
    let temporary = path.with_extension(format!("tmp-{}", Uuid::new_v4().simple()));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(&temporary)
        .map_err(|error| Error::Storage(format!("create blob {blob_id}: {error}")))?;
    file.write_all(bytes)
        .and_then(|_| file.sync_all())
        .map_err(|error| Error::Storage(format!("write blob {blob_id}: {error}")))?;
    match fs::rename(&temporary, &path) {
        Ok(()) => sync_directory(blob_dir)?,
        Err(_error) if path.exists() => {
            let _ = fs::remove_file(&temporary);
            let existing = fs::read(&path).map_err(|read_error| {
                Error::Storage(format!("read raced blob {blob_id}: {read_error}"))
            })?;
            if existing != bytes {
                return Err(Error::Storage(format!(
                    "blob {blob_id} exists with different content"
                )));
            }
        }
        Err(error) => {
            let _ = fs::remove_file(&temporary);
            return Err(Error::Storage(format!("publish blob {blob_id}: {error}")));
        }
    }
    Ok(blob_id)
}

fn blob_path(blob_dir: &Path, blob_id: &str) -> Result<PathBuf> {
    if blob_id.len() != 64 || !blob_id.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(Error::InvalidRequest("invalid blob identifier".into()));
    }
    Ok(blob_dir.join(blob_id.to_ascii_lowercase()))
}

fn remove_unreferenced_blobs(blob_dir: &Path, referenced: &HashSet<String>) -> Result<()> {
    let mut removed = false;
    for entry in fs::read_dir(blob_dir)
        .map_err(|error| Error::Storage(format!("read telemetry blob directory: {error}")))?
    {
        let entry =
            entry.map_err(|error| Error::Storage(format!("read telemetry blob entry: {error}")))?;
        let name = entry.file_name();
        let Some(blob_id) = name.to_str() else {
            continue;
        };
        if blob_id.len() != 64
            || !blob_id.bytes().all(|byte| byte.is_ascii_hexdigit())
            || referenced.contains(blob_id)
        {
            continue;
        }
        let file_type = entry
            .file_type()
            .map_err(|error| Error::Storage(format!("inspect telemetry blob: {error}")))?;
        if !file_type.is_file() {
            continue;
        }
        fs::remove_file(entry.path()).map_err(|error| {
            Error::Storage(format!("remove unreferenced telemetry blob: {error}"))
        })?;
        removed = true;
    }
    if removed {
        sync_directory(blob_dir)?;
    }
    Ok(())
}

fn referenced_blobs(analytics: &Session) -> Result<HashSet<String>> {
    query_string_set(
        analytics,
        &format!(
            "SELECT DISTINCT assumeNotNull(blob_id) FROM {ANALYTICS_DATABASE}.documents FINAL WHERE isNotNull(blob_id)"
        ),
        "list referenced telemetry blobs",
    )
}

fn recording_blob_ids(analytics: &Session) -> Result<HashSet<String>> {
    let mut blob_ids = query_string_set(
        analytics,
        &format!(
            "SELECT DISTINCT assumeNotNull(blob_id) FROM {ANALYTICS_DATABASE}.documents FINAL \
             WHERE isNotNull(blob_id) AND doc_kind IN ({RECORDING_DOC_KINDS})"
        ),
        "list recording telemetry blobs",
    )?;
    let content_ids = query_string_set(
        analytics,
        &format!(
            "SELECT DISTINCT assumeNotNull(content_id) FROM {ANALYTICS_DATABASE}.documents FINAL \
             WHERE isNotNull(content_id) AND doc_kind IN ({RECORDING_DOC_KINDS})"
        ),
        "list recording content identifiers",
    )?;
    if let Some(content_filter) = sql_in_list(&content_ids) {
        blob_ids.extend(query_string_set(
            analytics,
            &format!(
                "SELECT DISTINCT assumeNotNull(blob_id) FROM {ANALYTICS_DATABASE}.documents FINAL \
                 WHERE isNotNull(blob_id) AND doc_kind = 'content_chunk' AND content_id IN ({content_filter})"
            ),
            "list recording content blobs",
        )?);
    }
    Ok(blob_ids)
}

fn delete_orphaned_content_chunks(analytics: &Session) -> Result<()> {
    let referenced = query_string_set(
        analytics,
        &format!(
            "SELECT DISTINCT assumeNotNull(content_id) FROM {ANALYTICS_DATABASE}.documents FINAL \
             WHERE isNotNull(content_id) AND doc_kind NOT IN ('content_chunk', 'artifact_content_chunk', 'upload_chunk')"
        ),
        "list referenced telemetry content",
    )?;
    let chunks = query_string_set(
        analytics,
        &format!(
            "SELECT DISTINCT assumeNotNull(content_id) FROM {ANALYTICS_DATABASE}.documents FINAL \
             WHERE doc_kind = 'content_chunk' AND isNotNull(content_id)"
        ),
        "list telemetry content chunks",
    )?;
    let orphans = chunks
        .into_iter()
        .filter(|content_id| !referenced.contains(content_id))
        .collect::<HashSet<_>>();
    let Some(content_filter) = sql_in_list(&orphans) else {
        return Ok(());
    };
    mutate_documents(
        analytics,
        &format!("doc_kind = 'content_chunk' AND content_id IN ({content_filter})"),
        "expire orphaned recording content",
    )
}

fn mutate_documents(analytics: &Session, predicate: &str, label: &str) -> Result<()> {
    analytics
        .execute(
            &format!(
                "ALTER TABLE {ANALYTICS_DATABASE}.documents DELETE WHERE {predicate} SETTINGS mutations_sync=2"
            ),
            None,
        )
        .map_err(|error| Error::Storage(format!("{label}: {error}")))?;
    Ok(())
}

fn sql_in_list(values: &HashSet<String>) -> Option<String> {
    if values.is_empty() {
        return None;
    }
    Some(
        values
            .iter()
            .map(|value| chdb_string(value))
            .collect::<Vec<_>>()
            .join(", "),
    )
}

fn query_string_set(analytics: &Session, statement: &str, label: &str) -> Result<HashSet<String>> {
    analytics
        .execute(statement, None)
        .map_err(|error| Error::Storage(format!("{label}: {error}")))?
        .data_utf8()
        .map_err(|error| Error::Storage(format!("decode {label}: {error}")))
        .map(|output| {
            output
                .lines()
                .filter(|line| !line.is_empty())
                .map(str::to_owned)
                .collect()
        })
}

fn allocated_blob_total(blob_dir: &Path, blob_ids: &HashSet<String>) -> Result<u64> {
    let mut total = 0_u64;
    for blob_id in blob_ids {
        let path = blob_path(blob_dir, blob_id)?;
        match fs::metadata(&path) {
            Ok(metadata) => total = total.saturating_add(allocated_blob_bytes(&metadata)),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => {
                return Err(Error::Storage(format!(
                    "stat recording blob {blob_id}: {error}"
                )));
            }
        }
    }
    Ok(total)
}

fn allocated_blob_bytes(metadata: &fs::Metadata) -> u64 {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        let blocks = metadata.blocks();
        if blocks > 0 {
            return blocks.saturating_mul(512);
        }
    }
    metadata.len()
}

fn write_chdb_config(path: &Path, backup_dir: &Path) -> Result<()> {
    let backup_dir = backup_dir
        .to_str()
        .ok_or_else(|| Error::Storage("chDB backup path is not valid UTF-8".into()))?;
    let content = format!(
        "<clickhouse><storage_configuration><disks><telemetry_backups><type>local</type><path>{}/</path></telemetry_backups></disks></storage_configuration><backups><allowed_disk>telemetry_backups</allowed_disk></backups></clickhouse>\n",
        xml_text(backup_dir.trim_end_matches('/'))
    );
    let temporary = path.with_extension(format!("tmp-{}", Uuid::new_v4().simple()));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(&temporary)
        .map_err(|error| Error::Storage(format!("create chDB config: {error}")))?;
    file.write_all(content.as_bytes())
        .and_then(|_| file.sync_all())
        .map_err(|error| Error::Storage(format!("write chDB config: {error}")))?;
    fs::rename(&temporary, path).map_err(|error| {
        let _ = fs::remove_file(&temporary);
        Error::Storage(format!("publish chDB config: {error}"))
    })?;
    if let Some(parent) = path.parent() {
        sync_directory(parent)?;
    }
    Ok(())
}

fn write_backup_archive(path: &Path, native_path: &Path, blob_dir: &Path) -> Result<()> {
    let file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(path)
        .map_err(|error| Error::Storage(format!("create telemetry backup archive: {error}")))?;
    let mut archive = tar::Builder::new(file);
    archive
        .append_dir_all("chdb-backup", native_path)
        .map_err(|error| Error::Storage(format!("archive native chDB backup: {error}")))?;
    archive
        .append_dir_all("blobs", blob_dir)
        .map_err(|error| Error::Storage(format!("archive telemetry blobs: {error}")))?;
    let file = archive
        .into_inner()
        .map_err(|error| Error::Storage(format!("finish telemetry backup archive: {error}")))?;
    file.sync_all()
        .map_err(|error| Error::Storage(format!("sync telemetry backup archive: {error}")))?;
    if let Some(parent) = path.parent() {
        sync_directory(parent)?;
    }
    Ok(())
}

fn restore_backup_blocking(
    archive_path: &Path,
    volume: &Path,
    #[cfg(test)] _test_session: tokio::sync::OwnedSemaphorePermit,
) -> Result<()> {
    if !archive_path.is_absolute() || !volume.is_absolute() || volume.parent().is_none() {
        return Err(Error::InvalidRequest(
            "telemetry restore paths must be absolute".into(),
        ));
    }
    if volume.exists() {
        return Err(Error::Storage(format!(
            "telemetry restore destination {} already exists",
            volume.display()
        )));
    }
    let parent = volume
        .parent()
        .ok_or_else(|| Error::Storage("telemetry restore destination has no parent".into()))?;
    fs::create_dir_all(parent)
        .map_err(|error| Error::Storage(format!("create telemetry restore parent: {error}")))?;
    let stage = parent.join(format!(".telemetry-restore-{}", Uuid::new_v4().simple()));
    fs::create_dir(&stage)
        .map_err(|error| Error::Storage(format!("create telemetry restore staging: {error}")))?;
    let restore = || -> Result<()> {
        extract_backup_archive(archive_path, &stage)?;
        validate_restored_blobs(&stage.join("blobs"))?;

        let backup_dir = stage.join("backups");
        let chdb_dir = stage.join("chdb");
        fs::create_dir(&backup_dir).map_err(|error| {
            Error::Storage(format!("create restored chDB backup disk: {error}"))
        })?;
        fs::create_dir(&chdb_dir).map_err(|error| {
            Error::Storage(format!("create restored chDB data directory: {error}"))
        })?;
        let native_name = format!("restore-{}", Uuid::new_v4().simple());
        fs::rename(stage.join("chdb-backup"), backup_dir.join(&native_name))
            .map_err(|error| Error::Storage(format!("stage native chDB restore: {error}")))?;
        let chdb_config = stage.join("chdb-config.xml");
        write_chdb_config(&chdb_config, &backup_dir)?;
        let analytics = SessionBuilder::new()
            .with_data_path(chdb_dir)
            .with_arg(Arg::ConfigFilePath(Cow::Owned(
                chdb_config.to_string_lossy().into_owned(),
            )))
            .build()
            .map_err(|error| Error::Storage(format!("open restored chDB: {error}")))?;
        initialize_analytics(&analytics)?;
        analytics
            .execute(
                &format!(
                    "RESTORE DATABASE {ANALYTICS_DATABASE} FROM Disk('telemetry_backups', {}) SETTINGS allow_non_empty_tables=true",
                    chdb_string(&native_name)
                ),
                None,
            )
            .map_err(|error| Error::Storage(format!("restore native chDB backup: {error}")))?;
        drop(analytics);
        fs::remove_dir_all(backup_dir.join(native_name)).map_err(|error| {
            Error::Storage(format!("remove restored chDB backup staging: {error}"))
        })?;
        fs::rename(&stage, volume)
            .map_err(|error| Error::Storage(format!("publish telemetry restore: {error}")))?;
        sync_directory(parent)?;
        Ok(())
    };
    let result = restore();
    if result.is_err() {
        let _ = fs::remove_dir_all(&stage);
    }
    result
}

fn extract_backup_archive(archive_path: &Path, destination: &Path) -> Result<()> {
    let file = File::open(archive_path)
        .map_err(|error| Error::Storage(format!("open telemetry backup archive: {error}")))?;
    let mut archive = tar::Archive::new(file);
    let mut paths = HashSet::new();
    let entries = archive
        .entries()
        .map_err(|error| Error::Storage(format!("read telemetry backup archive: {error}")))?;
    for entry in entries {
        let mut entry = entry
            .map_err(|error| Error::Storage(format!("read telemetry backup entry: {error}")))?;
        let path = entry
            .path()
            .map_err(|error| Error::Storage(format!("decode telemetry backup path: {error}")))?
            .into_owned();
        if path.as_os_str().is_empty()
            || path
                .components()
                .any(|component| !matches!(component, Component::Normal(_)))
            || !allowed_backup_path(&path)
            || !paths.insert(path.clone())
        {
            return Err(Error::Storage(format!(
                "telemetry backup entry {} is unsafe",
                path.display()
            )));
        }
        let entry_type = entry.header().entry_type();
        if !entry_type.is_file() && !entry_type.is_dir() {
            return Err(Error::Storage(format!(
                "telemetry backup entry {} has an unsupported type",
                path.display()
            )));
        }
        if !entry
            .unpack_in(destination)
            .map_err(|error| Error::Storage(format!("extract telemetry backup: {error}")))?
        {
            return Err(Error::Storage(format!(
                "telemetry backup entry {} escaped its destination",
                path.display()
            )));
        }
    }
    for required in ["chdb-backup", "blobs"] {
        if !destination.join(required).exists() {
            return Err(Error::Storage(format!(
                "telemetry backup is missing {required}"
            )));
        }
    }
    Ok(())
}

fn allowed_backup_path(path: &Path) -> bool {
    path.starts_with("chdb-backup") || path.starts_with("blobs")
}

fn validate_restored_blobs(directory: &Path) -> Result<()> {
    for entry in fs::read_dir(directory)
        .map_err(|error| Error::Storage(format!("read restored blobs: {error}")))?
    {
        let entry =
            entry.map_err(|error| Error::Storage(format!("read restored blob entry: {error}")))?;
        let name = entry
            .file_name()
            .to_str()
            .filter(|name| name.len() == 64 && name.bytes().all(|byte| byte.is_ascii_hexdigit()))
            .map(str::to_ascii_lowercase)
            .ok_or_else(|| Error::Storage("restored blob has an invalid name".into()))?;
        let file_type = entry
            .file_type()
            .map_err(|error| Error::Storage(format!("inspect restored blob: {error}")))?;
        if !file_type.is_file() {
            return Err(Error::Storage("restored blob is not a regular file".into()));
        }
        let bytes = fs::read(entry.path())
            .map_err(|error| Error::Storage(format!("read restored blob {name}: {error}")))?;
        if format!("{:x}", Sha256::digest(bytes)) != name {
            return Err(Error::Storage(format!(
                "restored blob {name} failed its SHA-256 integrity check"
            )));
        }
    }
    Ok(())
}

fn valid_backup_id(id: &str) -> bool {
    id.len() == 32
        && id
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() && !byte.is_ascii_uppercase())
}

fn cleanup_backup_staging(directory: &Path) -> Result<()> {
    for entry in fs::read_dir(directory)
        .map_err(|error| Error::Storage(format!("read {}: {error}", directory.display())))?
    {
        let entry = entry
            .map_err(|error| Error::Storage(format!("read {}: {error}", directory.display())))?;
        let name = entry.file_name();
        let Some(name) = name.to_str() else {
            continue;
        };
        if !["native-", "export-"]
            .iter()
            .any(|prefix| name.starts_with(prefix))
        {
            continue;
        }
        let file_type = entry.file_type().map_err(|error| {
            Error::Storage(format!("inspect {}: {error}", entry.path().display()))
        })?;
        let result = if file_type.is_dir() {
            fs::remove_dir_all(entry.path())
        } else {
            fs::remove_file(entry.path())
        };
        result.map_err(|error| {
            Error::Storage(format!(
                "remove stale telemetry backup {}: {error}",
                entry.path().display()
            ))
        })?;
    }
    Ok(())
}

fn xml_text(value: &str) -> String {
    value
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
        .replace('\'', "&apos;")
}

fn cleanup_temporary_files_in(directory: &Path) -> Result<()> {
    for entry in fs::read_dir(directory)
        .map_err(|error| Error::Storage(format!("read {}: {error}", directory.display())))?
    {
        let entry = entry
            .map_err(|error| Error::Storage(format!("read {}: {error}", directory.display())))?;
        if is_storage_temporary_file(&entry.file_name()) {
            fs::remove_file(entry.path()).map_err(|error| {
                Error::Storage(format!(
                    "remove temporary {}: {error}",
                    entry.path().display()
                ))
            })?;
        }
    }
    Ok(())
}

fn is_storage_temporary_file(name: &OsStr) -> bool {
    name.to_str().is_some_and(|name| name.contains(".tmp-"))
}

fn sync_directory(path: &Path) -> Result<()> {
    File::open(path)
        .and_then(|file| file.sync_all())
        .map_err(|error| Error::Storage(format!("sync directory {}: {error}", path.display())))
}

pub fn term(field: &'static str, value: &str) -> SearchClause {
    SearchClause::Term {
        field,
        value: value.to_owned(),
    }
}

pub fn any(clauses: impl IntoIterator<Item = SearchClause>) -> SearchClause {
    SearchClause::Any(clauses.into_iter().collect())
}

pub fn all(clauses: impl IntoIterator<Item = SearchClause>) -> SearchQuery {
    SearchQuery {
        clauses: clauses.into_iter().collect(),
        text: None,
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;
    use tempfile::TempDir;

    use super::*;

    fn document(kind: &str, app: &str, timestamp: &str) -> Document {
        Document::base(kind, app, timestamp, timestamp)
    }

    fn recording_documents(
        kind: &str,
        timestamp: &str,
        replay_id: &str,
        segment_id: u64,
        payload: Vec<u8>,
    ) -> Vec<Document> {
        let content_id = format!("{:x}", Sha256::digest(&payload));
        let mut recording = document(kind, "app", timestamp);
        recording.replay_id = Some(replay_id.to_owned());
        recording.segment_id = Some(segment_id);
        recording.content_id = Some(content_id.clone());
        recording.chunk_count = Some(1);
        recording.size_bytes = Some(payload.len() as u64);
        let mut chunk = document("content_chunk", "app", timestamp);
        chunk.content_id = Some(content_id);
        chunk.sequence = Some(0);
        chunk.chunk_count = Some(1);
        chunk.size_bytes = Some(payload.len() as u64);
        chunk.content = Some(payload);
        vec![recording, chunk]
    }

    fn now_unix_nanos() -> u64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
            .try_into()
            .unwrap()
    }

    #[tokio::test]
    async fn stores_content_by_digest_and_reassembles_chunks() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut first = document("content_chunk", "app", "2026-08-09T10:00:00Z");
        first.content_id = Some("upload".into());
        first.sequence = Some(0);
        first.chunk_count = Some(2);
        first.size_bytes = Some(3);
        first.content = Some(b"abc".to_vec());
        let mut second = document("content_chunk", "app", "2026-08-09T10:00:01Z");
        second.content_id = Some("upload".into());
        second.sequence = Some(1);
        second.chunk_count = Some(2);
        second.size_bytes = Some(3);
        second.content = Some(b"def".to_vec());
        store.ingest(vec![first, second]).await.unwrap();

        assert_eq!(
            store
                .load_content("content_chunk", "app", "upload", 6)
                .await
                .unwrap(),
            b"abcdef"
        );
    }

    #[tokio::test]
    async fn replaces_issue_by_explicit_revision() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut first = document("issue", "app", "2026-08-09T10:00:00Z");
        first.issue_id = Some("issue-1".into());
        first.status = Some("open".into());
        first.event_count = Some(1);
        first.revision = Some(1);
        store.ingest(vec![first]).await.unwrap();
        let mut replacement = document("issue", "app", "2026-08-09T11:00:00Z");
        replacement.issue_id = Some("issue-1".into());
        replacement.status = Some("resolved".into());
        replacement.event_count = Some(2);
        replacement.revision = Some(2);
        store.ingest(vec![replacement]).await.unwrap();
        let mut stale = document("issue", "app", "2026-08-09T12:00:00Z");
        stale.issue_id = Some("issue-1".into());
        stale.status = Some("ignored".into());
        stale.event_count = Some(3);
        stale.revision = Some(1);
        store.ingest(vec![stale]).await.unwrap();

        let result = store
            .search(SearchRequest {
                query: all([term("doc_kind", "issue"), term("service_id", "app")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(result.num_hits, 1);
        assert_eq!(result.hits[0]["status"], "resolved");
        assert_eq!(result.hits[0]["event_count"], 2);
    }

    #[tokio::test]
    async fn aggregates_issue_users_and_runtime_distributions() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut events = Vec::new();
        for (index, user) in ["alex@example.com", "alex@example.com", "sam@example.com"]
            .into_iter()
            .enumerate()
        {
            let mut event = document("event", "app", &format!("2026-08-09T10:00:0{index}Z"));
            event.event_id = Some(format!("event-{index}"));
            event.issue_id = Some("issue-1".into());
            event.user = Some(user.into());
            event.release = Some(if index < 2 { "1.2.0" } else { "1.3.0" }.into());
            event.environment = Some("production".into());
            event.platform = Some("javascript".into());
            event.payload = Some(json!({
                "contexts": {
                    "browser": {"name": if index < 2 { "Chrome" } else { "Safari" }},
                    "device": {"model": "Desktop"},
                    "os": {"name": "macOS"}
                }
            }));
            events.push(event);
        }
        store.ingest(events).await.unwrap();

        let insights = store
            .issue_insights("app".into(), "issue-1".into())
            .await
            .unwrap();
        assert_eq!(insights.user_count, 2);
        assert!(insights.distributions.iter().any(|distribution| {
            distribution.key == "release"
                && distribution.value == "1.2.0"
                && distribution.count == 2
        }));
        assert!(insights.distributions.iter().any(|distribution| {
            distribution.key == "browser"
                && distribution.value == "Chrome"
                && distribution.count == 2
        }));
    }

    #[tokio::test]
    async fn queries_container_logs_with_stable_pagination_metadata() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let first_timestamp = now_unix_nanos();
        let second_timestamp = first_timestamp + 1;
        store
            .ingest_signal_rows(
                SignalTable::Logs,
                vec![
                    json!({
                        "id": "00000000-0000-4000-8000-000000000001",
                        "service_id": "service",
                        "deployment_id": "deployment", "attempt_id": "attempt",
                        "stream": "stdout", "partial": false,
                        "time_unix_nano": first_timestamp, "message": "starting",
                    }),
                    json!({
                        "id": "00000000-0000-4000-8000-000000000002",
                        "service_id": "service",
                        "deployment_id": "deployment", "attempt_id": "attempt",
                        "stream": "stderr", "partial": true,
                        "time_unix_nano": second_timestamp, "message": "failed readiness",
                        "trace_id": "0123456789abcdef0123456789abcdef",
                        "span_id": "0123456789abcdef",
                        "severity_number": 17, "severity_text": "ERROR",
                        "body_json": "{\"caller\":\"server.go:42\",\"http\":{\"route\":\"/checkout\"},\"request_id\":\"req-1\"}",
                        "scope": "{\"name\":\"platformd.before_deploy\"}",
                    }),
                ],
            )
            .await
            .unwrap();

        let page = store
            .log_records(LogQuery {
                service_ids: vec!["service".into()],
                deployment_id: Some("deployment".into()),
                contains: None,
                field_filters: vec![],
                severity_text: None,
                trace_id: None,
                span_id: None,
                from_unix_nano: None,
                to_unix_nano: None,
                after_time_unix_nano: None,
                after_id: None,
                limit: 1,
                ascending: false,
            })
            .await
            .unwrap();
        assert!(page.truncated);
        assert_eq!(page.records[0].text, "failed readiness");
        assert!(page.records[0].partial);
        assert_eq!(page.records[0].stream, "stderr");
        assert_eq!(page.records[0].severity_number, 17);
        assert_eq!(page.records[0].phase, "before_deploy");
        assert_eq!(page.next_time_unix_nano, Some(second_timestamp));

        let older = store
            .log_records(LogQuery {
                service_ids: vec!["service".into()],
                deployment_id: Some("deployment".into()),
                contains: None,
                field_filters: vec![],
                severity_text: None,
                trace_id: None,
                span_id: None,
                from_unix_nano: None,
                to_unix_nano: None,
                after_time_unix_nano: page.next_time_unix_nano,
                after_id: page.next_id,
                limit: 1,
                ascending: false,
            })
            .await
            .unwrap();
        assert!(!older.truncated);
        assert_eq!(older.records.len(), 1);
        assert_eq!(older.records[0].text, "starting");

        let filtered = store
            .log_records(LogQuery {
                service_ids: vec!["service".into()],
                deployment_id: None,
                contains: Some("SERVER.GO:42".into()),
                field_filters: vec![
                    LogFieldFilter {
                        path: "caller".into(),
                        operator: LogFieldOperator::Equals,
                        value: "server.go:42".into(),
                    },
                    LogFieldFilter {
                        path: "http.route".into(),
                        operator: LogFieldOperator::Contains,
                        value: "CHECKOUT".into(),
                    },
                    LogFieldFilter {
                        path: "request_id".into(),
                        operator: LogFieldOperator::Exists,
                        value: String::new(),
                    },
                ],
                severity_text: Some("ERROR".into()),
                trace_id: Some("0123456789abcdef0123456789abcdef".into()),
                span_id: Some("0123456789abcdef".into()),
                from_unix_nano: Some(second_timestamp),
                to_unix_nano: Some(second_timestamp),
                after_time_unix_nano: None,
                after_id: None,
                limit: 10,
                ascending: true,
            })
            .await
            .unwrap();
        assert_eq!(filtered.records.len(), 1);
        assert_eq!(filtered.records[0].severity_text, "ERROR");
        assert!(
            filtered.records[0]
                .body_json
                .as_deref()
                .is_some_and(|body| body.contains("server.go:42"))
        );
        assert_eq!(
            filtered.records[0].trace_id,
            "0123456789abcdef0123456789abcdef"
        );
    }

    #[tokio::test]
    async fn telemetry_bundle_restores_chdb_and_blobs() {
        let root = TempDir::new().unwrap();
        let source = root.path().join("source");
        let store = Store::open(source).await.unwrap();
        let mut event = document("event", "app", "2026-08-09T10:00:00Z");
        event.event_id = Some("event-1".into());
        event.content = Some(b"payload".to_vec());
        let mut issue = document("issue", "app", "2026-08-09T10:00:00Z");
        issue.issue_id = Some("issue-1".into());
        issue.status = Some("open".into());
        issue.revision = Some(1);
        store.ingest(vec![event, issue]).await.unwrap();
        let orphan_blob = format!("{:x}", Sha256::digest(b"orphan payload"));
        fs::write(store.inner.blob_dir.join(&orphan_blob), b"orphan payload").unwrap();
        let log_timestamp = now_unix_nanos();
        store
            .ingest_signal_rows(
                SignalTable::Logs,
                vec![json!({
                    "id": Uuid::new_v4().to_string(), "service_id": "app",
                    "deployment_id": "deployment", "attempt_id": "attempt",
                    "stream": "stderr", "partial": false,
                    "time_unix_nano": log_timestamp, "message": "retained log"
                })],
            )
            .await
            .unwrap();
        let (_, archive) = store.create_backup().await.unwrap();
        let archived_paths = tar::Archive::new(File::open(&archive).unwrap())
            .entries()
            .unwrap()
            .map(|entry| entry.unwrap().path().unwrap().into_owned())
            .collect::<Vec<_>>();
        let orphan_archive_path = Path::new("blobs").join(&orphan_blob);
        assert!(
            !archived_paths
                .iter()
                .any(|path| path == &orphan_archive_path)
        );
        let retained_archive = root.path().join("retained.tar");
        fs::copy(&archive, &retained_archive).unwrap();
        drop(store);

        let restored = root.path().join("restored");
        Store::restore_backup(retained_archive, restored.clone())
            .await
            .unwrap();
        assert!(!restored.join("telemetry.sqlite").exists());
        let restored_store = Store::open(restored).await.unwrap();
        let events = restored_store
            .search(SearchRequest {
                query: all([term("doc_kind", "event"), term("service_id", "app")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        let issues = restored_store
            .search(SearchRequest {
                query: all([term("doc_kind", "issue"), term("service_id", "app")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(events.num_hits, 1);
        assert_eq!(issues.num_hits, 1);
        let logs = restored_store
            .log_records(LogQuery {
                service_ids: vec!["app".into()],
                deployment_id: None,
                contains: None,
                field_filters: vec![],
                severity_text: None,
                trace_id: None,
                span_id: None,
                from_unix_nano: None,
                to_unix_nano: None,
                after_time_unix_nano: None,
                after_id: None,
                limit: 10,
                ascending: true,
            })
            .await
            .unwrap();
        assert_eq!(logs.records.len(), 1);
        assert_eq!(logs.records[0].text, "retained log");
        assert_eq!(
            fs::read_dir(restored_store.inner.blob_dir.clone())
                .unwrap()
                .count(),
            1
        );
    }

    #[tokio::test]
    async fn deleting_a_service_removes_its_documents_signals_and_unshared_blobs() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut removed = document("event", "removed", "2026-08-09T10:00:00Z");
        removed.event_id = Some("removed-event".into());
        removed.content = Some(b"removed payload".to_vec());
        let removed_blob = format!("{:x}", Sha256::digest(b"removed payload"));
        let mut retained = document("event", "retained", "2026-08-09T10:00:00Z");
        retained.event_id = Some("retained-event".into());
        retained.content = Some(b"retained payload".to_vec());
        let retained_blob = format!("{:x}", Sha256::digest(b"retained payload"));
        store.ingest(vec![removed, retained]).await.unwrap();
        let timestamp = now_unix_nanos();
        store
            .ingest_signal_rows(
                SignalTable::Logs,
                ["removed", "retained"]
                    .into_iter()
                    .map(|service_id| {
                        json!({
                            "id": Uuid::new_v4().to_string(), "service_id": service_id,
                            "time_unix_nano": timestamp, "message": service_id
                        })
                    })
                    .collect(),
            )
            .await
            .unwrap();

        store.delete_service("removed".into()).await.unwrap();

        assert!(
            !store
                .exists(all([
                    term("doc_kind", "event"),
                    term("service_id", "removed")
                ]))
                .await
                .unwrap()
        );
        assert!(
            store
                .exists(all([
                    term("doc_kind", "event"),
                    term("service_id", "retained")
                ]))
                .await
                .unwrap()
        );
        assert!(
            store
                .log_records(LogQuery {
                    service_ids: vec!["removed".into()],
                    deployment_id: None,
                    contains: None,
                    field_filters: vec![],
                    severity_text: None,
                    trace_id: None,
                    span_id: None,
                    from_unix_nano: None,
                    to_unix_nano: None,
                    after_time_unix_nano: None,
                    after_id: None,
                    limit: 10,
                    ascending: true,
                })
                .await
                .unwrap()
                .records
                .is_empty()
        );
        assert!(!store.inner.blob_dir.join(removed_blob).exists());
        assert!(store.inner.blob_dir.join(retained_blob).exists());
    }

    #[tokio::test]
    async fn recording_bytes_count_unique_replay_blobs() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let recording_payload = vec![7; 4096];
        let mut event = document("event", "app", "2026-08-09T10:00:01Z");
        event.event_id = Some("cccccccccccccccccccccccccccccccc".into());
        event.content_id = Some("event-content".into());
        event.chunk_count = Some(1);
        event.size_bytes = Some(4096);
        let mut event_chunk = document("content_chunk", "app", "2026-08-09T10:00:01Z");
        event_chunk.content_id = Some("event-content".into());
        event_chunk.sequence = Some(0);
        event_chunk.chunk_count = Some(1);
        event_chunk.size_bytes = Some(4096);
        event_chunk.content = Some(vec![9; 4096]);
        let mut documents = recording_documents(
            "replay_recording",
            "2026-08-09T10:00:00Z",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            0,
            recording_payload.clone(),
        );
        documents.extend([event, event_chunk]);
        store.ingest(documents).await.unwrap();

        let recording_blob = format!("{:x}", Sha256::digest(recording_payload));
        let want =
            allocated_blob_bytes(&fs::metadata(store.inner.blob_dir.join(recording_blob)).unwrap());
        assert_eq!(store.recording_bytes().await.unwrap(), want);
    }

    #[tokio::test]
    async fn expired_recordings_are_reclaimed_after_fourteen_days() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let expired_payload = vec![7; 4096];
        let kept_payload = vec![8; 4096];
        let video_payload = vec![6; 4096];
        let event_payload = vec![9; 4096];
        let mut event = document("event", "app", "2020-01-01T00:00:00Z");
        event.event_id = Some("cccccccccccccccccccccccccccccccc".into());
        event.content_id = Some("event-content".into());
        event.chunk_count = Some(1);
        event.size_bytes = Some(event_payload.len() as u64);
        let mut event_chunk = document("content_chunk", "app", "2020-01-01T00:00:00Z");
        event_chunk.content_id = Some("event-content".into());
        event_chunk.sequence = Some(0);
        event_chunk.chunk_count = Some(1);
        event_chunk.size_bytes = Some(event_payload.len() as u64);
        event_chunk.content = Some(event_payload.clone());
        let mut documents = recording_documents(
            "replay_recording",
            "2020-01-01T00:00:00Z",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            0,
            expired_payload.clone(),
        );
        documents.extend(recording_documents(
            "replay_recording",
            "2099-01-01T00:00:00Z",
            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
            0,
            kept_payload.clone(),
        ));
        documents.extend(recording_documents(
            "replay_video",
            "2020-01-01T00:00:00Z",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            0,
            video_payload.clone(),
        ));
        documents.extend([event, event_chunk]);
        store.ingest(documents).await.unwrap();

        let expired_blob = format!("{:x}", Sha256::digest(expired_payload));
        let kept_blob = format!("{:x}", Sha256::digest(kept_payload));
        let video_blob = format!("{:x}", Sha256::digest(video_payload));
        let event_blob = format!("{:x}", Sha256::digest(event_payload));
        store.reclaim_expired_recordings().await.unwrap();
        assert!(!store.inner.blob_dir.join(expired_blob).exists());
        assert!(!store.inner.blob_dir.join(video_blob).exists());
        assert!(store.inner.blob_dir.join(kept_blob).exists());
        assert!(store.inner.blob_dir.join(event_blob).exists());
        let leftover = store
            .search(SearchRequest {
                query: all([term("doc_kind", "replay_recording")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(leftover.num_hits, 1);
        assert_eq!(
            leftover.hits[0]["replay_id"],
            "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
        );
    }

    #[tokio::test]
    async fn replay_video_reuses_stable_storage_id() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let payload = vec![4; 2048];
        let first = recording_documents(
            "replay_video",
            "2026-08-09T10:00:00Z",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            7,
            payload.clone(),
        );
        let second = recording_documents(
            "replay_video",
            "2026-08-09T10:00:01Z",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            7,
            payload,
        );
        store.ingest(first).await.unwrap();
        store.ingest(second).await.unwrap();
        let result = store
            .search(SearchRequest {
                query: all([term("doc_kind", "replay_video")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(result.num_hits, 1);
    }

    #[tokio::test]
    async fn startup_removes_blobs_left_by_failed_ingestion() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let orphan = format!("{:x}", Sha256::digest(b"orphan"));
        fs::write(store.inner.blob_dir.join(&orphan), b"orphan").unwrap();
        drop(store);

        let reopened = Store::open(volume.path().to_owned()).await.unwrap();
        assert!(!reopened.inner.blob_dir.join(orphan).exists());
    }

    #[tokio::test]
    async fn queries_platformd_metric_fields_as_samples() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let timestamp = now_unix_nanos();
        store
            .ingest_signal_rows(
                SignalTable::Metrics,
                vec![json!({
                    "id": Uuid::new_v4().to_string(), "service_id": "service-1",
                    "scope_kind": "resource_service", "scope_id": "service-1",
                    "field": "MemoryBytes",
                    "name": "platformd.resource.memory_bytes", "description": "", "unit": "By",
                    "kind": "gauge", "start_time_unix_nano": 0, "time_unix_nano": timestamp,
                    "value_int": 1, "value_double": null, "count": null, "sum": null,
                    "min": null, "max": null, "aggregation_temporality": 0,
                    "is_monotonic": false, "flags": 0,
                    "attribute_keys": ["platformd.field", "platformd.value.type", "platformd.dimension.name", "region"],
                    "attribute_values": ["MemoryBytes", "bool", "worker-1", "eu-west"],
                    "attributes": "[{\"key\":\"platformd.field\",\"value\":{\"stringValue\":\"MemoryBytes\"}}]",
                    "exemplars": "[]", "point": "{}", "resource": "{}", "scope": "{}",
                    "metric": "{}", "received_at_unix_nano": timestamp
                })],
            )
            .await
            .unwrap();
        let samples = store
            .metric_samples(
                "resource_service".into(),
                vec!["service-1".into()],
                timestamp - 1,
                timestamp + 1,
            )
            .await
            .unwrap();
        assert_eq!(samples.len(), 1);
        assert_eq!(samples[0]["values"]["MemoryBytes"], "b:1");
        let field_attributes: Value =
            serde_json::from_str(samples[0]["attributes"]["MemoryBytes"].as_str().unwrap())
                .unwrap();
        assert_eq!(field_attributes["platformd.dimension.name"], "worker-1");
        assert_eq!(field_attributes.as_object().unwrap().len(), 1);
    }

    #[tokio::test]
    async fn queries_trace_waterfall_and_service_metric_sql() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let started = now_unix_nanos();
        let trace_id = "0123456789abcdef0123456789abcdef";
        store
            .ingest_signal_rows(
                SignalTable::Spans,
                vec![
                    json!({
                        "service_id": "service-1", "trace_id": trace_id,
                        "span_id": "0123456789abcdef", "parent_span_id": "",
                        "segment_id": "0123456789abcdef", "is_segment": true, "trace_state": "",
                        "name": "GET /checkout", "kind": 2,
                        "start_time_unix_nano": started, "end_time_unix_nano": started + 10_000_000,
                        "duration_nano": 10_000_000, "status_code": 0, "status_message": "", "flags": 1,
                        "resource": "{\"attributes\":[]}", "scope": "{}", "span": "{\"attributes\":[{\"key\":\"profiler_id\",\"value\":{\"stringValue\":\"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\"}}]}",
                        "received_at_unix_nano": started + 10_000_000, "version": 1, "source": "otlp"
                    }),
                    json!({
                        "service_id": "service-1", "trace_id": trace_id,
                        "span_id": "fedcba9876543210", "parent_span_id": "0123456789abcdef",
                        "segment_id": "0123456789abcdef", "is_segment": false, "trace_state": "",
                        "name": "SELECT cart", "kind": 3,
                        "start_time_unix_nano": started + 1_000_000, "end_time_unix_nano": started + 8_000_000,
                        "duration_nano": 7_000_000, "status_code": 0, "status_message": "", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 10_000_000, "version": 1, "source": "sentry"
                    }),
                    json!({
                        "service_id": "service-1", "trace_id": trace_id,
                        "span_id": "1111222233334444", "parent_span_id": "0123456789abcdef",
                        "segment_id": "0123456789abcdef", "is_segment": false, "trace_state": "",
                        "name": "invoke_agent checkout-agent", "kind": 1,
                        "start_time_unix_nano": started + 1_500_000, "end_time_unix_nano": started + 6_500_000,
                        "duration_nano": 5_000_000, "status_code": 1, "status_message": "", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 10_000_000, "version": 1, "source": "otlp",
                        "ai_kind": "agent", "ai_operation": "invoke_agent", "ai_agent": "checkout-agent",
                        "ai_input_tokens": 999, "ai_output_tokens": 999, "ai_cost_usd": 9.99
                    }),
                    json!({
                        "service_id": "service-1", "trace_id": trace_id,
                        "span_id": "aabbccdd11223344", "parent_span_id": "1111222233334444",
                        "segment_id": "0123456789abcdef", "is_segment": false, "trace_state": "",
                        "name": "chat gpt-5-mini", "kind": 3,
                        "start_time_unix_nano": started + 2_000_000, "end_time_unix_nano": started + 6_000_000,
                        "duration_nano": 4_000_000, "status_code": 1, "status_message": "", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 10_000_000, "version": 1, "source": "otlp",
                        "ai_kind": "model", "ai_operation": "chat", "ai_provider": "openai", "ai_model": "gpt-5-mini",
                        "ai_input_tokens": 120, "ai_output_tokens": 30, "ai_cache_read_tokens": 80,
                        "ai_cache_write_tokens": 0, "ai_reasoning_tokens": 0,
                        "ai_cost_usd": 0.002, "ai_ttft_seconds": 0.2, "ai_tokens_per_second": 75.0,
                        "search_text": "find invoice 42 awaiting bank confirmation"
                    }),
                    json!({
                        "service_id": "service-2", "trace_id": trace_id,
                        "span_id": "9999aaaa5555bbbb", "parent_span_id": "0123456789abcdef",
                        "segment_id": "0123456789abcdef", "is_segment": false, "trace_state": "",
                        "name": "POST inventory", "kind": 2,
                        "start_time_unix_nano": started + 3_000_000, "end_time_unix_nano": started + 20_000_000,
                        "duration_nano": 17_000_000, "status_code": 2, "status_message": "out of stock", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 20_000_000, "version": 1, "source": "otlp",
                        "search_text": "warehouse inventory reservation"
                    }),
                    json!({
                        "service_id": "service-1", "trace_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                        "span_id": "aaaaaaaaaaaaaaaa", "parent_span_id": "",
                        "segment_id": "aaaaaaaaaaaaaaaa", "is_segment": true, "trace_state": "",
                        "name": "GET /slow", "kind": 2,
                        "start_time_unix_nano": started - 1_000_000, "end_time_unix_nano": started + 4_000_000,
                        "duration_nano": 5_000_000, "status_code": 1, "status_message": "", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 100_000_000, "version": 1, "source": "otlp"
                    }),
                    json!({
                        "service_id": "service-2", "trace_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                        "span_id": "bbbbbbbbbbbbbbbb", "parent_span_id": "aaaaaaaaaaaaaaaa",
                        "segment_id": "aaaaaaaaaaaaaaaa", "is_segment": false, "trace_state": "",
                        "name": "slow worker", "kind": 2,
                        "start_time_unix_nano": started, "end_time_unix_nano": started + 99_000_000,
                        "duration_nano": 99_000_000, "status_code": 1, "status_message": "", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 100_000_000, "version": 1, "source": "otlp"
                    }),
                    json!({
                        "service_id": "service-2", "trace_id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                        "span_id": "cccccccccccccccc", "parent_span_id": "",
                        "segment_id": "cccccccccccccccc", "is_segment": true, "trace_state": "",
                        "name": "foreign only", "kind": 2,
                        "start_time_unix_nano": started + 1_000_000, "end_time_unix_nano": started + 500_000_000,
                        "duration_nano": 499_000_000, "status_code": 2, "status_message": "", "flags": 1,
                        "resource": "{}", "scope": "{}", "span": "{}",
                        "received_at_unix_nano": started + 500_000_000, "version": 1, "source": "otlp"
                    }),
                ],
            )
            .await
            .unwrap();
        let summary_query = |search: Option<&str>, status, order, limit, from| TraceSummaryQuery {
            anchor_service_id: Some("service-1".into()),
            service_ids: vec!["service-1".into(), "service-2".into()],
            from_unix_nano: from,
            to_unix_nano: None,
            search: search.map(str::to_owned),
            status,
            order,
            limit,
            offset: 0,
        };
        let summaries = store
            .trace_summaries(summary_query(
                None,
                TraceSummaryStatus::All,
                TraceSummaryOrder::Latest,
                10,
                None,
            ))
            .await
            .unwrap();
        assert_eq!(summaries.len(), 2);
        assert_eq!(summaries[0].name, "GET /checkout");
        assert_eq!(summaries[0].duration_nano, 20_000_000);
        assert_eq!(summaries[0].span_count, 5);
        assert_eq!(summaries[0].error_span_count, 1);
        assert!(summaries[0].is_ai);
        assert_eq!(summaries[0].ai_agent, "checkout-agent");
        assert_eq!(summaries[0].ai_agent_run_count, 1);
        assert_eq!(summaries[0].ai_model, "gpt-5-mini");
        assert_eq!(summaries[0].ai_input_tokens, Some(120));
        assert_eq!(summaries[0].ai_cache_read_tokens, Some(80));
        assert_eq!(summaries[0].ai_cache_write_tokens, Some(0));
        assert_eq!(summaries[0].ai_reasoning_tokens, Some(0));
        assert_eq!(summaries[0].ai_tool_call_count, 0);
        assert_eq!(summaries[0].ai_cost_usd, Some(0.002));
        assert_eq!(
            store
                .trace_summaries(summary_query(
                    Some("invoice bank"),
                    TraceSummaryStatus::All,
                    TraceSummaryOrder::Latest,
                    10,
                    None,
                ))
                .await
                .unwrap()
                .len(),
            1
        );
        assert!(
            store
                .trace_summaries(summary_query(
                    Some("weather"),
                    TraceSummaryStatus::All,
                    TraceSummaryOrder::Latest,
                    10,
                    None,
                ))
                .await
                .unwrap()
                .is_empty()
        );
        let remote_search = store
            .trace_summaries(summary_query(
                Some("warehouse status:error duration:>15ms"),
                TraceSummaryStatus::All,
                TraceSummaryOrder::Latest,
                10,
                None,
            ))
            .await
            .unwrap();
        assert_eq!(remote_search.len(), 1);
        assert_eq!(remote_search[0].trace_id, trace_id);
        let slowest = store
            .trace_summaries(summary_query(
                None,
                TraceSummaryStatus::Ok,
                TraceSummaryOrder::Slowest,
                1,
                None,
            ))
            .await
            .unwrap();
        assert_eq!(slowest.len(), 1);
        assert_eq!(slowest[0].trace_id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
        assert_eq!(slowest[0].duration_nano, 100_000_000);
        assert!(
            store
                .trace_summaries(summary_query(
                    None,
                    TraceSummaryStatus::All,
                    TraceSummaryOrder::Latest,
                    10,
                    Some(started + 1),
                ))
                .await
                .unwrap()
                .is_empty()
        );
        store
            .ingest_signal_rows(
                SignalTable::Profiles,
                vec![
                    json!({
                        "service_id": "service-1", "profile_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                        "profiler_id": "", "trace_id": trace_id, "span_id": "0123456789abcdef",
                        "platform": "node", "thread_id": "main", "thread_name": "MainThread",
                        "sample_time_unix_nano": started, "duration_nano": 10_000_000,
                        "stack": "[{\"function\":\"main\"},{\"function\":\"checkout\"}]",
                        "received_at_unix_nano": started, "sample_id": "sample-1", "version": started
                    }),
                    json!({
                        "service_id": "service-1", "profile_id": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                        "profiler_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "trace_id": "", "span_id": "",
                        "platform": "node", "thread_id": "main", "thread_name": "MainThread",
                        "sample_time_unix_nano": started + 4_000_000, "duration_nano": 1_000_000,
                        "stack": "[{\"function\":\"continuous-inside\"}]",
                        "received_at_unix_nano": started, "sample_id": "sample-2", "version": started
                    }),
                    json!({
                        "service_id": "service-1", "profile_id": "cccccccccccccccccccccccccccccccc",
                        "profiler_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "trace_id": "", "span_id": "",
                        "platform": "node", "thread_id": "main", "thread_name": "MainThread",
                        "sample_time_unix_nano": started + 100_000_000_000, "duration_nano": 1_000_000,
                        "stack": "[{\"function\":\"continuous-outside\"}]",
                        "received_at_unix_nano": started, "sample_id": "sample-3", "version": started
                    }),
                ],
            )
            .await
            .unwrap();
        let detail = store
            .trace(
                Some("service-1".into()),
                vec!["service-1".into()],
                trace_id.into(),
                None,
            )
            .await
            .unwrap();
        assert_eq!(detail.spans.len(), 4);
        assert!(detail.spans[0].resource.is_object());
        assert_eq!(detail.profiles.len(), 2);
        assert_eq!(detail.profiles[0].sample_count, 1);
        assert_eq!(
            detail.profiles[0].stacks[0].frames[1]["function"],
            "checkout"
        );
        assert_eq!(
            detail
                .profiles
                .iter()
                .map(|profile| profile.sample_count)
                .sum::<u64>(),
            2
        );
        assert!(detail.profiles.iter().all(|profile| {
            profile.stacks.iter().all(|stack| {
                stack
                    .frames
                    .as_array()
                    .into_iter()
                    .flatten()
                    .all(|frame| frame["function"] != "continuous-outside")
            })
        }));
        let project_detail = store
            .trace(
                Some("service-1".into()),
                vec!["service-1".into(), "service-2".into()],
                trace_id.into(),
                None,
            )
            .await
            .unwrap();
        assert_eq!(project_detail.spans.len(), 5);
        assert!(
            project_detail
                .spans
                .iter()
                .any(|span| span.service_id == "service-2")
        );

        store
            .ingest_signal_rows(
                SignalTable::Metrics,
                vec![
                    json!({
                        "id": Uuid::new_v4().to_string(), "service_id": "service-1",
                        "name": "checkout.queue.depth", "description": "Pending checkouts", "unit": "{item}", "kind": "gauge",
                        "time_unix_nano": started, "value_int": 2,
                        "attribute_keys": ["region"], "attribute_values": ["eu-west"],
                        "exemplars": serde_json::to_string(&json!([{
                            "traceId": trace_id,
                            "spanId": "0123456789abcdef"
                        }])).unwrap()
                    }),
                    json!({
                        "id": Uuid::new_v4().to_string(), "service_id": "service-1",
                        "name": "checkout.queue.depth", "description": "Pending checkouts", "unit": "{item}", "kind": "gauge",
                        "time_unix_nano": started + 1_000_000_000, "value_int": 6,
                        "attribute_keys": ["region"], "attribute_values": ["eu-west"]
                    }),
                ],
            )
            .await
            .unwrap();
        let detail_with_metric = store
            .trace(
                Some("service-1".into()),
                vec!["service-1".into()],
                trace_id.into(),
                None,
            )
            .await
            .unwrap();
        assert_eq!(detail_with_metric.metrics.len(), 1);
        assert_eq!(detail_with_metric.metrics[0].name, "checkout.queue.depth");
        assert_eq!(detail_with_metric.metrics[0].span_id, "0123456789abcdef");
        let catalog = store
            .metric_catalog(vec!["service-1".into()])
            .await
            .unwrap();
        assert_eq!(catalog[0].name, "checkout.queue.depth");
        let points = store
            .metric_sql(
                vec!["service-1".into()],
                MetricSqlQuery {
                    sql: "SELECT bucket AS time, avg(value) AS value, attributes['region'] AS series \
                          FROM metrics WHERE name = 'checkout.queue.depth' AND attributes['region'] = 'eu-west' \
                          GROUP BY bucket, series ORDER BY time"
                        .into(),
                    from_unix_nano: started,
                    to_unix_nano: started + 2_000_000_000,
                    step_nano: 2_000_000_000,
                },
            )
            .await
            .unwrap();
        assert_eq!(points.len(), 1);
        assert_eq!(points[0].series.as_deref(), Some("eu-west"));
        assert_eq!(points[0].value, 4.0);

        store
            .ingest_signal_rows(
                SignalTable::Metrics,
                vec![json!({
                    "id": Uuid::new_v4().to_string(), "service_id": "service-2",
                    "name": "checkout.queue.depth", "kind": "gauge",
                    "time_unix_nano": started, "value_int": 10
                })],
            )
            .await
            .unwrap();
        let scoped = store
            .metric_sql(
                vec!["service-1".into(), "service-2".into()],
                MetricSqlQuery {
                    sql: "SELECT bucket AS time, avg(value) AS value, service_id AS series \
                          FROM metrics WHERE name = 'checkout.queue.depth' \
                          GROUP BY bucket, series ORDER BY series"
                        .into(),
                    from_unix_nano: started,
                    to_unix_nano: started + 2_000_000_000,
                    step_nano: 2_000_000_000,
                },
            )
            .await
            .unwrap();
        assert_eq!(scoped.len(), 2);
        assert_eq!(scoped[0].series.as_deref(), Some("service-1"));
        assert_eq!(scoped[1].series.as_deref(), Some("service-2"));

        store
            .ingest_signal_rows(
                SignalTable::Metrics,
                vec![
                    json!({
                        "id": Uuid::new_v4().to_string(), "service_id": "service-1",
                        "name": "checkout.completed", "kind": "sum", "time_unix_nano": started,
                        "value_int": 100, "aggregation_temporality": 2,
                        "attribute_keys": ["region"], "attribute_values": ["eu-west"]
                    }),
                    json!({
                        "id": Uuid::new_v4().to_string(), "service_id": "service-1",
                        "name": "checkout.completed", "kind": "sum", "time_unix_nano": started + 1_000_000_000,
                        "value_int": 130, "aggregation_temporality": 2,
                        "attribute_keys": ["region"], "attribute_values": ["eu-west"]
                    }),
                ],
            )
            .await
            .unwrap();
        let rates = store
            .metric_sql(
                vec!["service-1".into()],
                MetricSqlQuery {
                    sql: "SELECT bucket AS time, sum(delta) / min(step_seconds) AS value \
                          FROM metrics WHERE name = 'checkout.completed' GROUP BY bucket ORDER BY time"
                        .into(),
                    from_unix_nano: started,
                    to_unix_nano: started + 2_000_000_000,
                    step_nano: 2_000_000_000,
                },
            )
            .await
            .unwrap();
        assert_eq!(rates[0].value, 15.0);
        let ratios = store
            .metric_sql(
                vec!["service-1".into()],
                MetricSqlQuery {
                    sql: "WITH totals AS (SELECT bucket, \
                            sumIf(delta, name = 'checkout.completed') AS completed, \
                            sumIf(delta, name = 'checkout.completed') AS attempted \
                          FROM metrics GROUP BY bucket) \
                          SELECT bucket AS time, 100 * completed / nullIf(attempted, 0) AS value \
                          FROM totals ORDER BY time"
                        .into(),
                    from_unix_nano: started,
                    to_unix_nano: started + 2_000_000_000,
                    step_nano: 2_000_000_000,
                },
            )
            .await
            .unwrap();
        assert_eq!(ratios[0].value, 100.0);

        store
            .ingest_signal_rows(
                SignalTable::Metrics,
                vec![json!({
                    "id": Uuid::new_v4().to_string(), "service_id": "service-1",
                    "name": "checkout.duration", "kind": "histogram", "time_unix_nano": started,
                    "aggregation_temporality": 1, "count": 5, "sum": 60.0, "min": 2.0, "max": 30.0,
                    "bucket_counts": [2,2,1], "explicit_bounds": [10.0,20.0],
                    "point": "{\"bucketCounts\":[2,2,1],\"explicitBounds\":[10.0,20.0]}",
                    "attribute_keys": ["region"], "attribute_values": ["eu-west"]
                })],
            )
            .await
            .unwrap();
        let percentiles = store
            .metric_sql(
                vec!["service-1".into()],
                MetricSqlQuery {
                    sql: "SELECT bucket AS time, quantileExactWeighted(0.9)(tupleElement(sample, 1), tupleElement(sample, 2)) AS value \
                          FROM (SELECT bucket, arrayJoin(arrayZip(histogram_values, histogram_delta_counts)) AS sample \
                            FROM metrics WHERE name = 'checkout.duration') \
                          GROUP BY bucket ORDER BY time"
                        .into(),
                    from_unix_nano: started,
                    to_unix_nano: started + 2_000_000_000,
                    step_nano: 2_000_000_000,
                },
            )
            .await
            .unwrap();
        assert_eq!(percentiles[0].value, 20.0);
    }

    #[tokio::test]
    async fn sentry_transactions_sharing_a_trace_id_merge_like_otlp() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let started = now_unix_nanos();
        let trace_id = "0123456789abcdef0123456789abcdef";
        let span = |segment_id: &str, parent: &str, name: &str, offset: u64| {
            json!({
                "service_id": "service-1", "trace_id": trace_id,
                "span_id": segment_id, "parent_span_id": parent,
                "segment_id": segment_id, "is_segment": true, "trace_state": "",
                "name": name, "kind": 2,
                "start_time_unix_nano": started + offset,
                "end_time_unix_nano": started + offset + 1_000_000_000,
                "duration_nano": 1_000_000_000, "status_code": 1,
                "status_message": "", "flags": 0, "resource": "{}", "scope": "{}",
                "span": "{}", "received_at_unix_nano": started + offset,
                "version": started + offset, "source": "sentry"
            })
        };
        store
            .ingest_signal_rows(
                SignalTable::Spans,
                vec![
                    span("1111111111111111", "", "GET /checkout", 0),
                    span(
                        "2222222222222222",
                        "1111111111111111",
                        "POST /api/pay",
                        500_000_000,
                    ),
                ],
            )
            .await
            .unwrap();

        let summaries = store
            .trace_summaries(TraceSummaryQuery {
                anchor_service_id: Some("service-1".into()),
                service_ids: vec!["service-1".into()],
                from_unix_nano: None,
                to_unix_nano: None,
                search: None,
                status: TraceSummaryStatus::All,
                order: TraceSummaryOrder::Latest,
                limit: 10,
                offset: 0,
            })
            .await
            .unwrap();
        assert_eq!(summaries.len(), 1);
        assert_eq!(summaries[0].trace_id, trace_id);
        assert_eq!(summaries[0].segment_id, "1111111111111111");
        assert_eq!(summaries[0].name, "GET /checkout");
        assert_eq!(summaries[0].span_count, 2);
        assert_eq!(summaries[0].duration_nano, 1_500_000_000);

        let detail = store
            .trace(
                Some("service-1".into()),
                vec!["service-1".into()],
                trace_id.into(),
                Some("1111111111111111".into()),
            )
            .await
            .unwrap();
        assert_eq!(detail.segment_id, "1111111111111111");
        assert_eq!(detail.spans.len(), 2);
        assert_eq!(detail.related_segments.len(), 1);
        assert_eq!(detail.related_segments[0].segment_id, "2222222222222222");
        assert_eq!(detail.related_segments[0].name, "POST /api/pay");
    }

    #[tokio::test]
    async fn rejects_incomplete_issue_state_at_the_database_boundary() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let result = store.with_analytics(|analytics| {
            analytics
                .execute(
                    "INSERT INTO telemetry.documents \
                     (storage_id, version, doc_kind, service_id, timestamp, received_at, source, search) \
                     VALUES ('invalid-issue', 0, 'issue', 'app', now64(6), now64(6), '{}', '')",
                    None,
                )
                .map_err(|error| Error::Storage(format!("insert invalid issue state: {error}")))?;
            Ok(())
        });

        assert!(result.is_err());
    }

    #[test]
    fn quotes_chdb_string_literals() {
        assert_eq!(chdb_string("a'b\\c"), "'a\\'b\\\\c'");
    }
}
