use std::ffi::OsStr;
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use tantivy::collector::{Count, TopDocs};
use tantivy::query::{AllQuery, BooleanQuery, Occur, Query, QueryParser, TermQuery};
use tantivy::schema::{
    DateOptions, DateTimePrecision, FAST, Field, IndexRecordOption, STORED, STRING, Schema, TEXT,
    Value as _,
};
use tantivy::{
    DateTime, DocAddress, Index, IndexReader, IndexWriter, Order, ReloadPolicy, TantivyDocument,
    Term,
};
use uuid::Uuid;

use crate::error::{Error, Result};
use crate::model::{Document, SearchClause, SearchQuery, SearchRequest, SearchResponse};

const INDEX_MEMORY_BYTES: usize = 64 << 20;
const MAX_CONTENT_CHUNKS: usize = 128;

#[derive(Clone)]
pub struct Store {
    inner: Arc<StoreInner>,
}

struct StoreInner {
    index: Index,
    reader: IndexReader,
    writer: Mutex<IndexWriter>,
    fields: Fields,
    blob_dir: PathBuf,
    wal_dir: PathBuf,
}

#[derive(Clone, Copy)]
struct Fields {
    source: Field,
    storage_id: Field,
    search: Field,
    doc_kind: Field,
    app_id: Field,
    project_id: Field,
    timestamp: Field,
    received_at: Field,
    ingest_id: Field,
    event_id: Field,
    issue_id: Field,
    item_type: Field,
    title: Field,
    message: Field,
    level: Field,
    platform: Field,
    environment: Field,
    release: Field,
    dist: Field,
    transaction: Field,
    sdk_name: Field,
    sdk_version: Field,
    user: Field,
    status: Field,
    filename: Field,
    content_type: Field,
    content_id: Field,
    checksum: Field,
    debug_id: Field,
    code_id: Field,
    symbol_type: Field,
    object_name: Field,
    replay_id: Field,
    blob_id: Field,
    payload: Field,
    segment_id: Field,
    sequence: Field,
    chunk_count: Field,
    size_bytes: Field,
}

#[derive(Deserialize, Serialize)]
struct WalRecord {
    documents: Vec<Document>,
}

#[derive(Serialize)]
struct PendingWalRecord<'a> {
    documents: &'a [Document],
}

struct TemporaryFile {
    path: PathBuf,
    active: bool,
}

impl TemporaryFile {
    fn new(path: PathBuf) -> Self {
        Self { path, active: true }
    }

    fn path(&self) -> &Path {
        &self.path
    }

    fn persist(mut self, destination: &Path) -> std::io::Result<()> {
        fs::rename(&self.path, destination)?;
        self.active = false;
        Ok(())
    }
}

impl Drop for TemporaryFile {
    fn drop(&mut self) {
        if self.active {
            let _ = fs::remove_file(&self.path);
        }
    }
}

impl Store {
    pub async fn open(volume: PathBuf) -> Result<Self> {
        tokio::task::spawn_blocking(move || Self::open_blocking(&volume))
            .await
            .map_err(|error| Error::Storage(format!("open embedded store task: {error}")))?
    }

    fn open_blocking(volume: &Path) -> Result<Self> {
        let index_dir = volume.join("index");
        let blob_dir = volume.join("blobs");
        let wal_dir = volume.join("wal");
        fs::create_dir_all(&index_dir)
            .map_err(|error| Error::Storage(format!("create index directory: {error}")))?;
        fs::create_dir_all(&blob_dir)
            .map_err(|error| Error::Storage(format!("create blob directory: {error}")))?;
        fs::create_dir_all(&wal_dir)
            .map_err(|error| Error::Storage(format!("create WAL directory: {error}")))?;

        let index = if index_dir.join("meta.json").exists() {
            Index::open_in_dir(&index_dir)
                .map_err(|error| Error::Storage(format!("open Tantivy index: {error}")))?
        } else {
            Index::create_in_dir(&index_dir, schema())
                .map_err(|error| Error::Storage(format!("create Tantivy index: {error}")))?
        };
        let fields = Fields::from_schema(&index.schema())?;
        let mut writer = index
            .writer_with_num_threads(1, INDEX_MEMORY_BYTES)
            .map_err(|error| Error::Storage(format!("open Tantivy writer: {error}")))?;
        cleanup_temporary_files(&wal_dir, &blob_dir)?;
        recover_wal(&mut writer, fields, &wal_dir)?;
        let reader = index
            .reader_builder()
            .reload_policy(ReloadPolicy::Manual)
            .try_into()
            .map_err(|error| Error::Storage(format!("open Tantivy reader: {error}")))?;

        Ok(Self {
            inner: Arc::new(StoreInner {
                index,
                reader,
                writer: Mutex::new(writer),
                fields,
                blob_dir,
                wal_dir,
            }),
        })
    }

    pub async fn ingest(&self, documents: Vec<Document>) -> Result<()> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.ingest_blocking(documents))
            .await
            .map_err(|error| Error::Storage(format!("commit embedded store task: {error}")))?
    }

    fn ingest_blocking(&self, mut documents: Vec<Document>) -> Result<()> {
        if documents.is_empty() {
            return Ok(());
        }
        persist_document_blobs(&self.inner.blob_dir, &mut documents)?;
        let wal_id = Uuid::new_v4().simple().to_string();
        // Finish fallible document conversion before publishing the WAL or mutating
        // Tantivy. A later writer commit must never pick up half of a rejected batch.
        let prepared = prepare_documents(self.inner.fields, &wal_id, &documents)?;
        write_wal(&self.inner.wal_dir, &wal_id, &documents)?;
        let mut writer = self
            .inner
            .writer
            .lock()
            .map_err(|_| Error::Storage("Tantivy writer lock is poisoned".into()))?;
        if let Err(error) = apply_documents(&mut writer, prepared) {
            return Err(rollback_writer(&mut writer, error));
        }
        if let Err(error) = writer.commit() {
            return Err(rollback_writer(
                &mut writer,
                Error::Storage(format!("commit Tantivy index: {error}")),
            ));
        }
        self.inner
            .reader
            .reload()
            .map_err(|error| Error::Storage(format!("reload Tantivy reader: {error}")))?;
        remove_wal(&self.inner.wal_dir, &wal_id)?;
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

    fn search_blocking(&self, request: SearchRequest) -> Result<SearchResponse> {
        let query = build_query(&self.inner.index, &request.query)?;
        let searcher = self.inner.reader.searcher();
        let num_hits = searcher
            .search(query.as_ref(), &Count)
            .map_err(|error| Error::Storage(format!("count Tantivy results: {error}")))?
            as u64;
        let offset = request.start_offset.unwrap_or_default();
        if request.max_hits == 0 || offset >= num_hits as usize {
            return Ok(SearchResponse {
                num_hits,
                hits: Vec::new(),
            });
        }

        offset
            .checked_add(request.max_hits)
            .ok_or_else(|| Error::InvalidRequest("search offset and limit are too large".into()))?;
        let top_docs = TopDocs::with_limit(request.max_hits).and_offset(offset);
        let addresses: Vec<DocAddress> = match request.sort_by.strip_prefix('-') {
            Some("sequence") => searcher
                .search(
                    query.as_ref(),
                    &top_docs.order_by_fast_field::<u64>("sequence", Order::Asc),
                )
                .map_err(|error| Error::Storage(format!("sort Tantivy results: {error}")))?
                .into_iter()
                .map(|(_, address)| address)
                .collect(),
            Some(field) => {
                return Err(Error::Storage(format!(
                    "unsupported ascending sort field: {field}"
                )));
            }
            None if request.sort_by == "timestamp" || request.sort_by == "received_at" => searcher
                .search(
                    query.as_ref(),
                    &top_docs.order_by_fast_field::<DateTime>(&request.sort_by, Order::Desc),
                )
                .map_err(|error| Error::Storage(format!("sort Tantivy results: {error}")))?
                .into_iter()
                .map(|(_, address)| address)
                .collect(),
            None if request.sort_by.is_empty() => searcher
                .search(query.as_ref(), &top_docs.order_by_score())
                .map_err(|error| Error::Storage(format!("rank Tantivy results: {error}")))?
                .into_iter()
                .map(|(_, address)| address)
                .collect(),
            None => {
                return Err(Error::Storage(format!(
                    "unsupported descending sort field: {}",
                    request.sort_by
                )));
            }
        };

        let mut hits = Vec::with_capacity(addresses.len());
        for address in addresses {
            let document: TantivyDocument = searcher
                .doc(address)
                .map_err(|error| Error::Storage(format!("load Tantivy document: {error}")))?;
            let source = document
                .get_first(self.inner.fields.source)
                .and_then(|value| value.as_str())
                .ok_or_else(|| Error::Storage("Tantivy document has no stored source".into()))?;
            hits.push(
                serde_json::from_str(source)
                    .map_err(|error| Error::Storage(format!("decode stored document: {error}")))?,
            );
        }
        Ok(SearchResponse { num_hits, hits })
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

    pub async fn load_content(
        &self,
        doc_kind: &str,
        app_id: &str,
        content_id: &str,
        maximum_bytes: usize,
    ) -> Result<Vec<u8>> {
        let response = self
            .search(SearchRequest {
                query: all([
                    term("doc_kind", doc_kind),
                    term("app_id", app_id),
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
            let size = output
                .len()
                .checked_add(decoded.len())
                .filter(|size| *size <= maximum_bytes)
                .ok_or_else(|| Error::Storage("stored content exceeds its size limit".into()))?;
            output.reserve(size - output.len());
            output.extend_from_slice(&decoded);
        }
        Ok(output)
    }
}

impl Fields {
    fn from_schema(schema: &Schema) -> Result<Self> {
        let field = |name| {
            schema
                .get_field(name)
                .map_err(|error| Error::Storage(format!("Tantivy schema field {name}: {error}")))
        };
        Ok(Self {
            source: field("_source")?,
            storage_id: field("_storage_id")?,
            search: field("_search")?,
            doc_kind: field("doc_kind")?,
            app_id: field("app_id")?,
            project_id: field("project_id")?,
            timestamp: field("timestamp")?,
            received_at: field("received_at")?,
            ingest_id: field("ingest_id")?,
            event_id: field("event_id")?,
            issue_id: field("issue_id")?,
            item_type: field("item_type")?,
            title: field("title")?,
            message: field("message")?,
            level: field("level")?,
            platform: field("platform")?,
            environment: field("environment")?,
            release: field("release")?,
            dist: field("dist")?,
            transaction: field("transaction")?,
            sdk_name: field("sdk_name")?,
            sdk_version: field("sdk_version")?,
            user: field("user")?,
            status: field("status")?,
            filename: field("filename")?,
            content_type: field("content_type")?,
            content_id: field("content_id")?,
            checksum: field("checksum")?,
            debug_id: field("debug_id")?,
            code_id: field("code_id")?,
            symbol_type: field("symbol_type")?,
            object_name: field("object_name")?,
            replay_id: field("replay_id")?,
            blob_id: field("blob_id")?,
            payload: field("payload")?,
            segment_id: field("segment_id")?,
            sequence: field("sequence")?,
            chunk_count: field("chunk_count")?,
            size_bytes: field("size_bytes")?,
        })
    }
}

fn schema() -> Schema {
    let mut builder = Schema::builder();
    builder.add_text_field("_source", STORED);
    builder.add_text_field("_storage_id", STRING);
    builder.add_text_field("_search", TEXT);
    for name in [
        "doc_kind",
        "app_id",
        "project_id",
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
    ] {
        builder.add_text_field(name, STRING);
    }
    let date = DateOptions::default()
        .set_fast()
        .set_precision(DateTimePrecision::Microseconds);
    builder.add_date_field("timestamp", date.clone());
    builder.add_date_field("received_at", date);
    for name in [
        "title",
        "message",
        "transaction",
        "user",
        "filename",
        "object_name",
        "payload",
    ] {
        builder.add_text_field(name, TEXT);
    }
    for name in ["segment_id", "sequence", "chunk_count", "size_bytes"] {
        builder.add_u64_field(name, FAST);
    }
    builder.build()
}

fn cleanup_temporary_files(wal_dir: &Path, blob_dir: &Path) -> Result<()> {
    cleanup_temporary_files_in(wal_dir)?;
    let shards = fs::read_dir(blob_dir)
        .map_err(|error| Error::Storage(format!("read blob directory: {error}")))?;
    for shard in shards {
        let shard =
            shard.map_err(|error| Error::Storage(format!("read blob directory entry: {error}")))?;
        let file_type = shard
            .file_type()
            .map_err(|error| Error::Storage(format!("read blob entry type: {error}")))?;
        if file_type.is_dir() {
            cleanup_temporary_files_in(&shard.path())?;
        }
    }
    Ok(())
}

fn cleanup_temporary_files_in(directory: &Path) -> Result<()> {
    let entries = fs::read_dir(directory).map_err(|error| {
        Error::Storage(format!(
            "read temporary file directory {}: {error}",
            directory.display()
        ))
    })?;
    let mut removed = false;
    for entry in entries {
        let entry = entry.map_err(|error| {
            Error::Storage(format!(
                "read temporary file entry in {}: {error}",
                directory.display()
            ))
        })?;
        if is_storage_temporary_file(&entry.file_name()) {
            fs::remove_file(entry.path())
                .map_err(|error| Error::Storage(format!("remove stale temporary file: {error}")))?;
            removed = true;
        }
    }
    if removed {
        sync_directory(directory)?;
    }
    Ok(())
}

fn is_storage_temporary_file(name: &OsStr) -> bool {
    name.to_str()
        .and_then(|name| name.strip_prefix('.'))
        .and_then(|name| name.strip_suffix(".tmp"))
        .is_some_and(|id| id.len() == 32 && id.bytes().all(|byte| byte.is_ascii_hexdigit()))
}

fn recover_wal(writer: &mut IndexWriter, fields: Fields, wal_dir: &Path) -> Result<()> {
    let directory = fs::read_dir(wal_dir)
        .map_err(|error| Error::Storage(format!("read WAL directory: {error}")))?;
    let mut entries = Vec::new();
    for entry in directory {
        let entry =
            entry.map_err(|error| Error::Storage(format!("read WAL directory entry: {error}")))?;
        if entry.path().extension().and_then(|value| value.to_str()) == Some("json") {
            entries.push(entry);
        }
    }
    entries.sort_by_key(|entry| entry.file_name());
    for entry in entries {
        let path = entry.path();
        let wal_id = path
            .file_stem()
            .and_then(|value| value.to_str())
            .ok_or_else(|| Error::Storage("invalid WAL filename".into()))?;
        let bytes =
            fs::read(&path).map_err(|error| Error::Storage(format!("read WAL record: {error}")))?;
        let record: WalRecord = serde_json::from_slice(&bytes)
            .map_err(|error| Error::Storage(format!("decode WAL record: {error}")))?;
        let prepared = prepare_documents(fields, wal_id, &record.documents)?;
        apply_documents(writer, prepared)?;
        writer
            .commit()
            .map_err(|error| Error::Storage(format!("commit recovered WAL: {error}")))?;
        fs::remove_file(&path)
            .map_err(|error| Error::Storage(format!("remove recovered WAL: {error}")))?;
    }
    sync_directory(wal_dir)
}

fn write_wal(wal_dir: &Path, wal_id: &str, documents: &[Document]) -> Result<()> {
    let temporary = TemporaryFile::new(wal_dir.join(format!(".{wal_id}.tmp")));
    let committed = wal_dir.join(format!("{wal_id}.json"));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(temporary.path())
        .map_err(|error| Error::Storage(format!("create WAL record: {error}")))?;
    serde_json::to_writer(&mut file, &PendingWalRecord { documents })
        .map_err(|error| Error::Storage(format!("write WAL record: {error}")))?;
    file.sync_all()
        .map_err(|error| Error::Storage(format!("sync WAL record: {error}")))?;
    temporary
        .persist(&committed)
        .map_err(|error| Error::Storage(format!("commit WAL record: {error}")))?;
    sync_directory(wal_dir)
}

fn remove_wal(wal_dir: &Path, wal_id: &str) -> Result<()> {
    fs::remove_file(wal_dir.join(format!("{wal_id}.json")))
        .map_err(|error| Error::Storage(format!("remove WAL record: {error}")))?;
    sync_directory(wal_dir)
}

fn prepare_documents(
    fields: Fields,
    wal_id: &str,
    documents: &[Document],
) -> Result<Vec<(Term, TantivyDocument)>> {
    documents
        .iter()
        .enumerate()
        .map(|(index, document)| {
            if document.content.is_some() {
                return Err(Error::Storage(
                    "document content reached the index before blob persistence".into(),
                ));
            }
            let storage_id =
                stable_storage_id(document).unwrap_or_else(|| format!("wal:{wal_id}:{index}"));
            let document = to_tantivy_document(fields, &storage_id, document)?;
            let storage_id = Term::from_field_text(fields.storage_id, &storage_id);
            Ok((storage_id, document))
        })
        .collect()
}

fn apply_documents(
    writer: &mut IndexWriter,
    documents: Vec<(Term, TantivyDocument)>,
) -> Result<()> {
    for (storage_id, document) in documents {
        writer.delete_term(storage_id);
        writer
            .add_document(document)
            .map_err(|error| Error::Storage(format!("add Tantivy document: {error}")))?;
    }
    Ok(())
}

fn rollback_writer(writer: &mut IndexWriter, cause: Error) -> Error {
    match writer.rollback() {
        Ok(_) => cause,
        Err(error) => Error::Storage(format!("{cause}; rollback Tantivy writer: {error}")),
    }
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
    let app_id = &document.app_id;
    match document.doc_kind.as_str() {
        "issue" => Some(format!("issue:{app_id}:{}", document.issue_id.as_deref()?)),
        "symbolication" => Some(format!(
            "symbolication:{app_id}:{}",
            document.event_id.as_deref()?
        )),
        "replay_event" | "replay_recording" => Some(format!(
            "replay:{}:{app_id}:{}:{}",
            document.doc_kind,
            document.replay_id.as_deref()?,
            document.segment_id.unwrap_or_default()
        )),
        "content_chunk" => Some(format!(
            "content:{app_id}:{}:{}",
            document.content_id.as_deref()?,
            document.sequence?
        )),
        "upload_chunk" => Some(format!(
            "upload-chunk:{app_id}:{}",
            document.content_id.as_deref()?
        )),
        "artifact" => Some(format!(
            "artifact:{app_id}:{}",
            document.ingest_id.as_deref()?
        )),
        "artifact_content_chunk" => Some(format!(
            "artifact-content:{app_id}:{}:{}",
            document.content_id.as_deref()?,
            document.sequence?
        )),
        _ => None,
    }
}

fn to_tantivy_document(
    fields: Fields,
    storage_id: &str,
    source: &Document,
) -> Result<TantivyDocument> {
    let mut document = TantivyDocument::default();
    let source_json = serde_json::to_string(source)
        .map_err(|error| Error::Storage(format!("encode stored document: {error}")))?;
    document.add_text(fields.source, &source_json);
    document.add_text(fields.storage_id, storage_id);
    document.add_text(fields.doc_kind, &source.doc_kind);
    document.add_text(fields.app_id, &source.app_id);
    document.add_text(fields.project_id, &source.project_id);
    add_timestamp(&mut document, fields.timestamp, &source.timestamp)?;
    add_timestamp(&mut document, fields.received_at, &source.received_at)?;

    add_optional(&mut document, fields.ingest_id, source.ingest_id.as_deref());
    add_optional(&mut document, fields.event_id, source.event_id.as_deref());
    add_optional(&mut document, fields.issue_id, source.issue_id.as_deref());
    add_optional(&mut document, fields.item_type, source.item_type.as_deref());
    add_optional(&mut document, fields.title, source.title.as_deref());
    add_optional(&mut document, fields.message, source.message.as_deref());
    add_optional(&mut document, fields.level, source.level.as_deref());
    add_optional(&mut document, fields.platform, source.platform.as_deref());
    add_optional(
        &mut document,
        fields.environment,
        source.environment.as_deref(),
    );
    add_optional(&mut document, fields.release, source.release.as_deref());
    add_optional(&mut document, fields.dist, source.dist.as_deref());
    add_optional(
        &mut document,
        fields.transaction,
        source.transaction.as_deref(),
    );
    add_optional(&mut document, fields.sdk_name, source.sdk_name.as_deref());
    add_optional(
        &mut document,
        fields.sdk_version,
        source.sdk_version.as_deref(),
    );
    add_optional(&mut document, fields.user, source.user.as_deref());
    add_optional(&mut document, fields.status, source.status.as_deref());
    add_optional(&mut document, fields.filename, source.filename.as_deref());
    add_optional(
        &mut document,
        fields.content_type,
        source.content_type.as_deref(),
    );
    add_optional(
        &mut document,
        fields.content_id,
        source.content_id.as_deref(),
    );
    add_optional(&mut document, fields.checksum, source.checksum.as_deref());
    add_optional(&mut document, fields.debug_id, source.debug_id.as_deref());
    add_optional(&mut document, fields.code_id, source.code_id.as_deref());
    add_optional(
        &mut document,
        fields.symbol_type,
        source.symbol_type.as_deref(),
    );
    add_optional(
        &mut document,
        fields.object_name,
        source.object_name.as_deref(),
    );
    add_optional(&mut document, fields.replay_id, source.replay_id.as_deref());
    add_optional(&mut document, fields.blob_id, source.blob_id.as_deref());
    add_u64(&mut document, fields.segment_id, source.segment_id);
    add_u64(&mut document, fields.sequence, source.sequence);
    add_u64(&mut document, fields.chunk_count, source.chunk_count);
    add_u64(&mut document, fields.size_bytes, source.size_bytes);

    let payload = source.payload.as_ref().map(Value::to_string);
    add_optional(&mut document, fields.payload, payload.as_deref());
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
    document.add_text(fields.search, &searchable);
    Ok(document)
}

fn add_optional(document: &mut TantivyDocument, field: Field, value: Option<&str>) {
    if let Some(value) = value {
        document.add_text(field, value);
    }
}

fn add_u64(document: &mut TantivyDocument, field: Field, value: Option<u64>) {
    if let Some(value) = value {
        document.add_u64(field, value);
    }
}

fn add_timestamp(document: &mut TantivyDocument, field: Field, value: &str) -> Result<()> {
    let timestamp = value
        .parse::<jiff::Timestamp>()
        .map_err(|error| Error::Storage(format!("invalid document timestamp {value}: {error}")))?;
    document.add_date(
        field,
        DateTime::from_timestamp_micros(timestamp.as_microsecond()),
    );
    Ok(())
}

fn write_blob(blob_dir: &Path, bytes: &[u8]) -> Result<String> {
    let blob_id = format!("{:x}", Sha256::digest(bytes));
    let path = blob_path(blob_dir, &blob_id)?;
    if path.exists() {
        let existing = fs::read(&path)
            .map_err(|error| Error::Storage(format!("verify existing blob {blob_id}: {error}")))?;
        if existing != bytes {
            return Err(Error::Storage(format!(
                "existing blob {blob_id} failed its content-addressed integrity check"
            )));
        }
        return Ok(blob_id);
    }
    let parent = path
        .parent()
        .ok_or_else(|| Error::Storage("blob path has no parent".into()))?;
    let shard_created = !parent.exists();
    fs::create_dir_all(parent)
        .map_err(|error| Error::Storage(format!("create blob shard: {error}")))?;
    if shard_created {
        sync_directory(blob_dir)?;
    }
    let temporary = TemporaryFile::new(parent.join(format!(".{}.tmp", Uuid::new_v4().simple())));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .open(temporary.path())
        .map_err(|error| Error::Storage(format!("create blob: {error}")))?;
    file.write_all(bytes)
        .map_err(|error| Error::Storage(format!("write blob: {error}")))?;
    file.sync_all()
        .map_err(|error| Error::Storage(format!("sync blob: {error}")))?;
    temporary
        .persist(&path)
        .map_err(|error| Error::Storage(format!("commit blob: {error}")))?;
    sync_directory(parent)?;
    Ok(blob_id)
}

fn blob_path(blob_dir: &Path, blob_id: &str) -> Result<PathBuf> {
    if blob_id.len() != 64 || !blob_id.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return Err(Error::InvalidRequest("invalid blob identifier".into()));
    }
    Ok(blob_dir
        .join(blob_id[..2].to_ascii_lowercase())
        .join(blob_id[2..].to_ascii_lowercase()))
}

fn sync_directory(path: &Path) -> Result<()> {
    File::open(path)
        .and_then(|file| file.sync_all())
        .map_err(|error| Error::Storage(format!("sync directory {}: {error}", path.display())))
}

fn build_query(index: &Index, query: &SearchQuery) -> Result<Box<dyn Query>> {
    let mut clauses = query
        .clauses
        .iter()
        .map(|clause| clause_query(&index.schema(), clause).map(|query| (Occur::Must, query)))
        .collect::<Result<Vec<_>>>()?;
    if let Some(text) = query.text.as_deref() {
        let parser = QueryParser::for_index(
            index,
            vec![
                index
                    .schema()
                    .get_field("_search")
                    .map_err(|error| Error::Storage(format!("find search field: {error}")))?,
            ],
        );
        let parsed = parser
            .parse_query(text)
            .map_err(|error| Error::InvalidRequest(format!("invalid search query: {error}")))?;
        clauses.push((Occur::Must, parsed));
    }
    if clauses.is_empty() {
        Ok(Box::new(AllQuery))
    } else {
        Ok(Box::new(BooleanQuery::new(clauses)))
    }
}

fn clause_query(schema: &Schema, clause: &SearchClause) -> Result<Box<dyn Query>> {
    match clause {
        SearchClause::Term { field, value } => {
            let field = schema.get_field(field).map_err(|error| {
                Error::Storage(format!("unknown embedded search field {field}: {error}"))
            })?;
            Ok(Box::new(TermQuery::new(
                Term::from_field_text(field, value),
                IndexRecordOption::Basic,
            )))
        }
        SearchClause::Any(clauses) => {
            let queries = clauses
                .iter()
                .map(|clause| clause_query(schema, clause).map(|query| (Occur::Should, query)))
                .collect::<Result<Vec<_>>>()?;
            Ok(Box::new(BooleanQuery::new(queries)))
        }
    }
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
    use tempfile::TempDir;

    use super::*;

    fn document(kind: &str, app: &str, timestamp: &str) -> Document {
        Document::base(kind, app, "1", timestamp, timestamp)
    }

    #[tokio::test]
    async fn commits_searchable_documents_and_blobs() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut event = document("event", "app", "2026-08-09T10:00:00Z");
        event.title = Some("Embedded failure".into());
        event.content = Some(b"raw envelope item".to_vec());
        store.ingest(vec![event]).await.unwrap();

        let response = store
            .search(SearchRequest {
                query: all([term("doc_kind", "event"), term("app_id", "app")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(response.num_hits, 1);
        let blob_id = response.hits[0]["blob_id"].as_str().unwrap();
        assert_eq!(
            store.read_blob(blob_id).await.unwrap(),
            b"raw envelope item"
        );
    }

    #[tokio::test]
    async fn rejects_corrupted_content_addressed_blobs() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut event = document("event", "app", "2026-08-09T10:00:00Z");
        event.content = Some(b"original bytes".to_vec());
        store.ingest(vec![event]).await.unwrap();
        let response = store
            .search(SearchRequest {
                query: all([term("doc_kind", "event"), term("app_id", "app")]),
                max_hits: 1,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        let blob_id = response.hits[0]["blob_id"].as_str().unwrap();
        std::fs::write(
            blob_path(&volume.path().join("blobs"), blob_id).unwrap(),
            b"corrupt",
        )
        .unwrap();

        let error = store.read_blob(blob_id).await.unwrap_err();
        assert!(error.to_string().contains("integrity check"));

        let mut duplicate = document("event", "app", "2026-08-09T10:01:00Z");
        duplicate.content = Some(b"original bytes".to_vec());
        let error = store.ingest(vec![duplicate]).await.unwrap_err();
        assert!(error.to_string().contains("integrity check"));
        assert!(
            std::fs::read_dir(volume.path().join("wal"))
                .unwrap()
                .next()
                .is_none()
        );
    }

    #[tokio::test]
    async fn removes_only_owned_stale_temporary_files_on_open() {
        let volume = TempDir::new().unwrap();
        let wal_dir = volume.path().join("wal");
        let blob_shard = volume.path().join("blobs/ab");
        std::fs::create_dir_all(&wal_dir).unwrap();
        std::fs::create_dir_all(&blob_shard).unwrap();
        let stale = format!(".{}.tmp", "a".repeat(32));
        std::fs::write(wal_dir.join(&stale), b"partial WAL").unwrap();
        std::fs::write(blob_shard.join(&stale), b"partial blob").unwrap();
        std::fs::write(wal_dir.join("keep.tmp"), b"unrelated").unwrap();

        let _store = Store::open(volume.path().to_owned()).await.unwrap();

        assert!(!wal_dir.join(&stale).exists());
        assert!(!blob_shard.join(&stale).exists());
        assert!(wal_dir.join("keep.tmp").exists());
    }

    #[tokio::test]
    async fn invalid_batch_cannot_leak_into_a_later_commit() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut first = document("event", "app", "2026-08-09T10:00:00Z");
        first.event_id = Some("must-not-commit".into());
        let invalid = document("event", "app", "not-a-timestamp");

        assert!(store.ingest(vec![first, invalid]).await.is_err());
        assert!(
            std::fs::read_dir(volume.path().join("wal"))
                .unwrap()
                .next()
                .is_none()
        );

        let mut later = document("event", "app", "2026-08-09T11:00:00Z");
        later.event_id = Some("later-event".into());
        store.ingest(vec![later]).await.unwrap();
        let leaked = store
            .search(SearchRequest {
                query: all([term("event_id", "must-not-commit")]),
                max_hits: 1,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(leaked.num_hits, 0);
    }

    #[tokio::test]
    async fn rejects_incomplete_content_chunks() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut chunk = document("content_chunk", "app", "2026-08-09T10:00:00Z");
        chunk.content_id = Some("content".into());
        chunk.sequence = Some(0);
        chunk.chunk_count = Some(2);
        chunk.size_bytes = Some(5);
        chunk.content = Some(b"first".to_vec());
        store.ingest(vec![chunk]).await.unwrap();

        let error = store
            .load_content("content_chunk", "app", "content", 1024)
            .await
            .unwrap_err();

        assert!(error.to_string().contains("incomplete"));
    }

    #[tokio::test]
    async fn supports_any_terms_and_ascending_sequence() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut first = document("replay_event", "app", "2026-08-09T10:00:00Z");
        first.sequence = Some(1);
        let mut second = document("replay_recording", "app", "2026-08-09T10:00:01Z");
        second.sequence = Some(0);
        store.ingest(vec![first, second]).await.unwrap();
        let response = store
            .search(SearchRequest {
                query: all([
                    term("app_id", "app"),
                    any([
                        term("doc_kind", "replay_event"),
                        term("doc_kind", "replay_recording"),
                    ]),
                ]),
                max_hits: 10,
                start_offset: None,
                sort_by: "-sequence".into(),
            })
            .await
            .unwrap();
        assert_eq!(response.hits[0]["sequence"], 0);
        assert_eq!(response.hits[1]["sequence"], 1);
    }

    #[tokio::test]
    async fn sorts_fractional_timestamps_chronologically() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let whole = document("event", "app", "2026-08-09T10:00:00Z");
        let fractional = document("event", "app", "2026-08-09T10:00:00.5Z");
        store.ingest(vec![whole, fractional]).await.unwrap();

        let response = store
            .search(SearchRequest {
                query: all([term("doc_kind", "event"), term("app_id", "app")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(response.hits[0]["timestamp"], "2026-08-09T10:00:00.5Z");
        assert_eq!(response.hits[1]["timestamp"], "2026-08-09T10:00:00Z");
    }

    #[tokio::test]
    async fn search_all_is_not_truncated_to_a_page() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let documents = (0..150)
            .map(|sequence| {
                let mut document = document("artifact", "app", "2026-08-09T10:00:00Z");
                document.sequence = Some(sequence);
                document
            })
            .collect::<Vec<_>>();
        let expected_count = documents.len();
        store.ingest(documents).await.unwrap();

        let hits = store
            .search_all(
                all([term("doc_kind", "artifact"), term("app_id", "app")]),
                "timestamp",
            )
            .await
            .unwrap();
        assert_eq!(hits.len(), expected_count);
    }

    #[tokio::test]
    async fn replaces_aggregate_documents_by_their_stable_identity() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut first = document("issue", "app", "2026-08-09T10:00:00Z");
        first.issue_id = Some("issue-1".into());
        first.status = Some("open".into());
        first.event_count = Some(1);
        store.ingest(vec![first]).await.unwrap();

        let mut replacement = document("issue", "app", "2026-08-09T11:00:00Z");
        replacement.issue_id = Some("issue-1".into());
        replacement.status = Some("resolved".into());
        replacement.event_count = Some(2);
        store.ingest(vec![replacement]).await.unwrap();

        let response = store
            .search(SearchRequest {
                query: all([term("doc_kind", "issue"), term("app_id", "app")]),
                max_hits: 10,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(response.num_hits, 1);
        assert_eq!(response.hits[0]["status"], "resolved");
        assert_eq!(response.hits[0]["event_count"], 2);
    }

    #[tokio::test]
    async fn replays_a_durable_wal_record_on_startup() {
        let volume = TempDir::new().unwrap();
        let wal_dir = volume.path().join("wal");
        let blob_dir = volume.path().join("blobs");
        std::fs::create_dir_all(&wal_dir).unwrap();
        std::fs::create_dir_all(&blob_dir).unwrap();
        let mut pending = document("event", "app", "2026-08-09T10:00:00Z");
        pending.event_id = Some("recovered-event".into());
        pending.content = Some(b"recovered bytes".to_vec());
        persist_document_blobs(&blob_dir, std::slice::from_mut(&mut pending)).unwrap();
        write_wal(&wal_dir, "interrupted-ingest", &[pending]).unwrap();

        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let response = store
            .search(SearchRequest {
                query: all([term("event_id", "recovered-event")]),
                max_hits: 1,
                start_offset: None,
                sort_by: "timestamp".into(),
            })
            .await
            .unwrap();
        assert_eq!(response.num_hits, 1);
        let blob_id = response.hits[0]["blob_id"].as_str().unwrap();
        assert_eq!(store.read_blob(blob_id).await.unwrap(), b"recovered bytes");
        assert!(std::fs::read_dir(&wal_dir).unwrap().next().is_none());
    }
}
