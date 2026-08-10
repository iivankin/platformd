use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct Document {
    pub doc_kind: String,
    pub app_id: String,
    pub project_id: String,
    pub timestamp: String,
    pub received_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub ingest_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub event_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub issue_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub item_type: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub level: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub platform: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub environment: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub release: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub dist: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub transaction: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sdk_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sdk_version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub user: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub status: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub first_seen: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub event_count: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub filename: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub content_type: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub content_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub checksum: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub debug_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub code_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub symbol_type: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub object_name: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub replay_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub segment_id: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub sequence: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub chunk_count: Option<u64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub size_bytes: Option<u64>,
    #[serde(skip)]
    pub content: Option<Vec<u8>>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub blob_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub payload: Option<Value>,
}

impl Document {
    pub fn base(
        doc_kind: impl Into<String>,
        app_id: &str,
        project_id: &str,
        timestamp: &str,
        received_at: &str,
    ) -> Self {
        Self {
            doc_kind: doc_kind.into(),
            app_id: app_id.to_owned(),
            project_id: project_id.to_owned(),
            timestamp: timestamp.to_owned(),
            received_at: received_at.to_owned(),
            ingest_id: None,
            event_id: None,
            issue_id: None,
            item_type: None,
            title: None,
            message: None,
            level: None,
            platform: None,
            environment: None,
            release: None,
            dist: None,
            transaction: None,
            sdk_name: None,
            sdk_version: None,
            user: None,
            status: None,
            first_seen: None,
            event_count: None,
            filename: None,
            content_type: None,
            content_id: None,
            checksum: None,
            debug_id: None,
            code_id: None,
            symbol_type: None,
            object_name: None,
            replay_id: None,
            segment_id: None,
            sequence: None,
            chunk_count: None,
            size_bytes: None,
            content: None,
            blob_id: None,
            payload: None,
        }
    }
}

#[derive(Debug, Deserialize)]
pub struct SearchResponse {
    pub num_hits: u64,
    #[serde(default)]
    pub hits: Vec<Value>,
}

#[derive(Clone, Debug)]
pub enum SearchClause {
    Term { field: &'static str, value: String },
    Any(Vec<SearchClause>),
}

#[derive(Clone, Debug, Default)]
pub struct SearchQuery {
    pub clauses: Vec<SearchClause>,
    pub text: Option<String>,
}

#[derive(Clone, Debug)]
pub struct SearchRequest {
    pub query: SearchQuery,
    pub max_hits: usize,
    pub start_offset: Option<usize>,
    pub sort_by: String,
}
