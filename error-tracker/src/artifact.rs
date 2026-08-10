use std::cmp::Reverse;
use std::collections::HashSet;

use jiff::Timestamp;
use serde_json::Value;
use sha1::{Digest, Sha1};
use symbolic_common::DebugId;

use crate::config::AppConfig;
use crate::error::{Error, Result};
use crate::model::Document;
use crate::storage::{Store, all, any, term};

pub const CHUNK_BYTES: usize = 3 << 20;
pub const MAX_FILE_BYTES: usize = 256 << 20;
const MAX_CHUNKS: usize = MAX_FILE_BYTES.div_ceil(CHUNK_BYTES);
const MAX_METADATA_BYTES: usize = 2048;

#[derive(Clone, Debug)]
pub struct ArtifactMetadata {
    pub checksum: String,
    pub kind: String,
    pub name: String,
    pub debug_id: Option<String>,
    pub code_id: Option<String>,
    pub symbol_type: String,
    pub release: Option<String>,
    pub dist: Option<String>,
}

#[derive(Clone, Debug)]
pub struct StoredArtifact {
    pub id: String,
    pub name: String,
    pub debug_id: Option<String>,
    pub symbol_type: String,
    pub bytes: Vec<u8>,
}

pub fn sha1_hex(bytes: &[u8]) -> String {
    format!("{:x}", Sha1::digest(bytes))
}

pub fn valid_sha1(value: &str) -> bool {
    value.len() == 40 && value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

pub async fn store_upload_chunk(
    store: &Store,
    app: &AppConfig,
    checksum: &str,
    bytes: &[u8],
) -> Result<()> {
    if bytes.len() > CHUNK_BYTES || sha1_hex(bytes) != checksum.to_ascii_lowercase() {
        return Err(Error::InvalidRequest(
            "uploaded chunk does not match its SHA1 filename or size limit".into(),
        ));
    }
    if content_exists(store, "upload_chunk", &app.id, checksum).await? {
        return Ok(());
    }
    let now = Timestamp::now().to_string();
    let mut document = Document::base("upload_chunk", &app.id, &app.project_id, &now, &now);
    document.content_id = Some(checksum.to_ascii_lowercase());
    document.checksum = Some(checksum.to_ascii_lowercase());
    document.sequence = Some(0);
    document.chunk_count = Some(1);
    document.size_bytes = Some(bytes.len() as u64);
    document.content = Some(bytes.to_vec());
    store.ingest(vec![document]).await
}

pub async fn missing_upload_chunks(
    store: &Store,
    app_id: &str,
    checksums: &[String],
) -> Result<Vec<String>> {
    if checksums.len() > MAX_CHUNKS {
        return Err(Error::InvalidRequest(
            "artifact contains too many chunks".into(),
        ));
    }
    let mut missing = Vec::new();
    for checksum in checksums {
        if !valid_sha1(checksum) || !content_exists(store, "upload_chunk", app_id, checksum).await?
        {
            missing.push(checksum.clone());
        }
    }
    Ok(missing)
}

pub async fn assemble(
    store: &Store,
    app: &AppConfig,
    metadata: &ArtifactMetadata,
    chunks: &[String],
) -> Result<bool> {
    if !valid_sha1(&metadata.checksum) || chunks.is_empty() {
        return Err(Error::InvalidRequest(
            "artifact checksum and chunks are required".into(),
        ));
    }
    validate_metadata(metadata)?;
    let debug_id = metadata.debug_id.as_deref().map(normalize_debug_id);
    let code_id = metadata.code_id.as_deref().map(str::to_ascii_lowercase);
    let association_id = artifact_association_id(metadata, debug_id.as_deref(), code_id.as_deref());
    if store
        .exists(all([
            term("doc_kind", "artifact"),
            term("app_id", &app.id),
            term("ingest_id", &association_id),
        ]))
        .await?
    {
        return Ok(false);
    }
    let mut assembled_chunks = Vec::with_capacity(chunks.len());
    let mut size_bytes = 0usize;
    let mut digest = Sha1::new();
    for checksum in chunks {
        let chunk = load_content(store, "upload_chunk", &app.id, checksum).await?;
        size_bytes = size_bytes.saturating_add(chunk.len());
        if size_bytes > MAX_FILE_BYTES {
            return Err(Error::InvalidRequest(
                "artifact exceeds the file size limit".into(),
            ));
        }
        digest.update(&chunk);
        assembled_chunks.push(chunk);
    }
    if format!("{:x}", digest.finalize()) != metadata.checksum.to_ascii_lowercase() {
        return Err(Error::InvalidRequest(
            "assembled artifact does not match its SHA1 checksum".into(),
        ));
    }
    let now = Timestamp::now().to_string();
    let mut documents = Vec::new();
    let mut artifact = Document::base("artifact", &app.id, &app.project_id, &now, &now);
    artifact.item_type = Some(metadata.kind.clone());
    artifact.ingest_id = Some(association_id);
    artifact.content_id = Some(metadata.checksum.to_ascii_lowercase());
    artifact.checksum = Some(metadata.checksum.to_ascii_lowercase());
    artifact.title = Some(metadata.name.clone());
    artifact.object_name = Some(metadata.name.clone());
    artifact.filename = Some(metadata.name.clone());
    artifact.debug_id = debug_id;
    artifact.code_id = code_id;
    artifact.symbol_type = Some(metadata.symbol_type.clone());
    artifact.release = metadata.release.clone();
    artifact.dist = metadata.dist.clone();
    artifact.size_bytes = Some(size_bytes as u64);
    artifact.chunk_count = Some(assembled_chunks.len() as u64);
    documents.push(artifact);
    if !content_exists(store, "artifact_content_chunk", &app.id, &metadata.checksum).await? {
        let chunk_count = assembled_chunks.len() as u64;
        for (sequence, chunk) in assembled_chunks.into_iter().enumerate() {
            let mut document = Document::base(
                "artifact_content_chunk",
                &app.id,
                &app.project_id,
                &now,
                &now,
            );
            document.item_type = Some(metadata.kind.clone());
            document.content_id = Some(metadata.checksum.to_ascii_lowercase());
            document.checksum = Some(metadata.checksum.to_ascii_lowercase());
            document.sequence = Some(sequence as u64);
            document.chunk_count = Some(chunk_count);
            document.size_bytes = Some(chunk.len() as u64);
            document.content = Some(chunk);
            documents.push(document);
        }
    }
    store.ingest(documents).await?;
    Ok(true)
}

fn validate_metadata(metadata: &ArtifactMetadata) -> Result<()> {
    for (name, value) in [
        ("kind", Some(metadata.kind.as_str())),
        ("name", Some(metadata.name.as_str())),
        ("debug ID", metadata.debug_id.as_deref()),
        ("code ID", metadata.code_id.as_deref()),
        ("symbol type", Some(metadata.symbol_type.as_str())),
        ("release", metadata.release.as_deref()),
        ("dist", metadata.dist.as_deref()),
    ] {
        if value.is_some_and(|value| value.len() > MAX_METADATA_BYTES) {
            return Err(Error::InvalidRequest(format!(
                "artifact {name} exceeds {MAX_METADATA_BYTES} bytes"
            )));
        }
    }
    Ok(())
}

pub async fn load_artifact(store: &Store, app_id: &str, id: &str) -> Result<Vec<u8>> {
    load_content(store, "artifact_content_chunk", app_id, id).await
}

pub async fn source_bundles(
    store: &Store,
    app_id: &str,
    release: Option<&str>,
    dist: Option<&str>,
) -> Result<Vec<StoredArtifact>> {
    let terms = vec![
        term("doc_kind", "artifact"),
        term("app_id", app_id),
        term("item_type", "artifact_bundle"),
    ];
    let mut hits = store
        .search_all(all(terms), "timestamp")
        .await?
        .into_iter()
        .filter(|hit| source_bundle_matches(hit, release, dist))
        .collect::<Vec<_>>();
    // Keep newest-first order among equally specific bundles, but never let a
    // generic upload shadow one explicitly associated with this release/dist.
    prioritize_source_bundles(&mut hits, release, dist);
    load_artifacts_from_hits(store, app_id, hits).await
}

pub async fn debug_files(
    store: &Store,
    app_id: &str,
    debug_id: Option<&str>,
    code_id: Option<&str>,
) -> Result<Vec<StoredArtifact>> {
    let mut terms = vec![
        term("doc_kind", "artifact"),
        term("app_id", app_id),
        term("item_type", "debug_file"),
    ];
    match (debug_id, code_id) {
        (Some(debug_id), Some(code_id)) => terms.push(any([
            term("debug_id", &normalize_debug_id(debug_id)),
            term("code_id", &code_id.to_ascii_lowercase()),
        ])),
        (Some(debug_id), None) => terms.push(term("debug_id", &normalize_debug_id(debug_id))),
        (None, Some(code_id)) => terms.push(term("code_id", &code_id.to_ascii_lowercase())),
        (None, None) => {}
    }
    load_artifacts(store, app_id, all(terms)).await
}

pub async fn proguard_mappings(
    store: &Store,
    app_id: &str,
    debug_id: Option<&str>,
) -> Result<Vec<StoredArtifact>> {
    let mut terms = vec![
        term("doc_kind", "artifact"),
        term("app_id", app_id),
        term("item_type", "debug_file"),
        term("symbol_type", "proguard"),
    ];
    if let Some(debug_id) = debug_id {
        terms.push(term("debug_id", &normalize_debug_id(debug_id)));
    }
    load_artifacts(store, app_id, all(terms)).await
}

async fn load_artifacts(
    store: &Store,
    app_id: &str,
    query: crate::model::SearchQuery,
) -> Result<Vec<StoredArtifact>> {
    let hits = store.search_all(query, "timestamp").await?;
    load_artifacts_from_hits(store, app_id, hits).await
}

async fn load_artifacts_from_hits(
    store: &Store,
    app_id: &str,
    hits: Vec<Value>,
) -> Result<Vec<StoredArtifact>> {
    let mut artifacts = Vec::with_capacity(hits.len());
    let mut seen = HashSet::new();
    for hit in hits {
        let id = hit
            .get("content_id")
            .and_then(Value::as_str)
            .ok_or_else(|| Error::Storage("artifact has no content identifier".into()))?
            .to_owned();
        let debug_id = hit
            .get("debug_id")
            .and_then(Value::as_str)
            .map(str::to_owned);
        let symbol_type = hit
            .get("symbol_type")
            .and_then(Value::as_str)
            .unwrap_or("unknown")
            .to_owned();
        if !seen.insert((id.clone(), debug_id.clone(), symbol_type.clone())) {
            continue;
        }
        artifacts.push(StoredArtifact {
            bytes: load_artifact(store, app_id, &id).await?,
            id,
            name: hit
                .get("object_name")
                .and_then(Value::as_str)
                .unwrap_or("artifact")
                .to_owned(),
            debug_id,
            symbol_type,
        });
    }
    Ok(artifacts)
}

fn source_bundle_matches(hit: &Value, release: Option<&str>, dist: Option<&str>) -> bool {
    [("release", release), ("dist", dist)]
        .into_iter()
        .all(|(field, expected)| {
            expected.is_none_or(|expected| {
                hit.get(field)
                    .and_then(Value::as_str)
                    .is_none_or(|actual| actual == expected)
            })
        })
}

fn source_bundle_specificity(hit: &Value, release: Option<&str>, dist: Option<&str>) -> u8 {
    [("release", release), ("dist", dist)]
        .into_iter()
        .filter(|(field, expected)| {
            expected
                .is_some_and(|expected| hit.get(*field).and_then(Value::as_str) == Some(expected))
        })
        .count() as u8
}

fn prioritize_source_bundles(hits: &mut [Value], release: Option<&str>, dist: Option<&str>) {
    hits.sort_by_key(|hit| Reverse(source_bundle_specificity(hit, release, dist)));
}

fn normalize_debug_id(value: &str) -> String {
    value
        .parse::<DebugId>()
        .map_or_else(|_| value.to_ascii_lowercase(), |id| id.to_string())
}

fn artifact_association_id(
    metadata: &ArtifactMetadata,
    debug_id: Option<&str>,
    code_id: Option<&str>,
) -> String {
    let mut digest = Sha1::new();
    digest.update(metadata.checksum.to_ascii_lowercase().as_bytes());
    digest.update([0]);
    for value in [
        Some(metadata.kind.as_str()),
        Some(metadata.name.as_str()),
        debug_id,
        code_id,
        Some(metadata.symbol_type.as_str()),
        metadata.release.as_deref(),
        metadata.dist.as_deref(),
    ] {
        if let Some(value) = value {
            digest.update(value.as_bytes());
        }
        digest.update([0]);
    }
    format!("{:x}", digest.finalize())
}

async fn content_exists(
    store: &Store,
    doc_kind: &str,
    app_id: &str,
    content_id: &str,
) -> Result<bool> {
    store
        .exists(all([
            term("doc_kind", doc_kind),
            term("app_id", app_id),
            term("content_id", &content_id.to_ascii_lowercase()),
        ]))
        .await
}

async fn load_content(
    store: &Store,
    doc_kind: &str,
    app_id: &str,
    content_id: &str,
) -> Result<Vec<u8>> {
    store
        .load_content(
            doc_kind,
            app_id,
            &content_id.to_ascii_lowercase(),
            MAX_FILE_BYTES,
        )
        .await
}

#[cfg(test)]
mod tests {
    use tempfile::tempdir;

    use super::*;

    #[test]
    fn validates_sha1_identifiers() {
        assert!(valid_sha1("0123456789abcdef0123456789abcdef01234567"));
        assert!(!valid_sha1("not-a-checksum"));
        assert_eq!(
            sha1_hex(b"hello"),
            "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"
        );
    }

    #[test]
    fn canonicalizes_equivalent_debug_id_spellings() {
        assert_eq!(
            normalize_debug_id("DFB8E43AF2423D73A453AEB6A777EF750"),
            normalize_debug_id("dfb8e43a-f242-3d73-a453-aeb6a777ef75-0")
        );
    }

    #[test]
    fn artifact_association_includes_release_and_dist() {
        let mut metadata = ArtifactMetadata {
            checksum: "0123456789abcdef0123456789abcdef01234567".into(),
            kind: "artifact_bundle".into(),
            name: "bundle.zip".into(),
            debug_id: None,
            code_id: None,
            symbol_type: "sourcebundle".into(),
            release: Some("web@1".into()),
            dist: None,
        };
        let first = artifact_association_id(&metadata, None, None);
        assert_eq!(first, artifact_association_id(&metadata, None, None));
        metadata.release = Some("web@2".into());
        assert_ne!(first, artifact_association_id(&metadata, None, None));
    }

    #[test]
    fn unversioned_source_bundles_match_versioned_events() {
        assert!(source_bundle_matches(
            &serde_json::json!({}),
            Some("web@1"),
            Some("production")
        ));
        assert!(source_bundle_matches(
            &serde_json::json!({ "release": "web@1", "dist": "production" }),
            Some("web@1"),
            Some("production")
        ));
        assert!(!source_bundle_matches(
            &serde_json::json!({ "release": "web@2" }),
            Some("web@1"),
            None
        ));
    }

    #[test]
    fn exact_source_bundle_associations_are_more_specific_than_fallbacks() {
        let mut hits = vec![
            serde_json::json!({ "id": "newest-generic" }),
            serde_json::json!({ "id": "release", "release": "web@1" }),
            serde_json::json!({
                "id": "release-and-dist",
                "release": "web@1",
                "dist": "production"
            }),
            serde_json::json!({ "id": "older-generic" }),
        ];

        prioritize_source_bundles(&mut hits, Some("web@1"), Some("production"));

        assert_eq!(hits[0]["id"], "release-and-dist");
        assert_eq!(hits[1]["id"], "release");
        assert_eq!(hits[2]["id"], "newest-generic");
        assert_eq!(hits[3]["id"], "older-generic");
    }

    #[test]
    fn rejects_oversized_artifact_metadata() {
        let metadata = ArtifactMetadata {
            checksum: "0123456789abcdef0123456789abcdef01234567".into(),
            kind: "debug_file".into(),
            name: "x".repeat(MAX_METADATA_BYTES + 1),
            debug_id: None,
            code_id: None,
            symbol_type: "elf".into(),
            release: None,
            dist: None,
        };

        assert!(validate_metadata(&metadata).is_err());
    }

    #[tokio::test]
    async fn assembles_uploaded_chunks_without_changing_their_bytes() {
        let directory = tempdir().unwrap();
        let store = Store::open(directory.path().to_owned()).await.unwrap();
        let app = AppConfig {
            id: "app".into(),
            project_id: "1".into(),
            name: "App".into(),
            slug: "app".into(),
            public_key: "key".into(),
            auth_token_hash: "hash".into(),
            webhooks: Vec::new(),
            created_at: "2026-08-09T00:00:00Z".into(),
            updated_at: "2026-08-09T00:00:00Z".into(),
        };
        let chunks = [b"first".as_slice(), b"second".as_slice()];
        let checksums = chunks
            .iter()
            .map(|chunk| sha1_hex(chunk))
            .collect::<Vec<_>>();
        for (checksum, chunk) in checksums.iter().zip(chunks) {
            store_upload_chunk(&store, &app, checksum, chunk)
                .await
                .unwrap();
        }
        let expected = b"firstsecond";
        let artifact_checksum = sha1_hex(expected);
        assert!(
            assemble(
                &store,
                &app,
                &ArtifactMetadata {
                    checksum: artifact_checksum.clone(),
                    kind: "debug_file".into(),
                    name: "app.sym".into(),
                    debug_id: None,
                    code_id: None,
                    symbol_type: "breakpad".into(),
                    release: None,
                    dist: None,
                },
                &checksums,
            )
            .await
            .unwrap()
        );
        assert_eq!(
            load_artifact(&store, &app.id, &artifact_checksum)
                .await
                .unwrap(),
            expected
        );

        assert!(
            assemble(
                &store,
                &app,
                &ArtifactMetadata {
                    checksum: artifact_checksum,
                    kind: "debug_file".into(),
                    name: "mapping.txt".into(),
                    debug_id: Some("AABBCCDD".into()),
                    code_id: None,
                    symbol_type: "proguard".into(),
                    release: None,
                    dist: None,
                },
                &checksums,
            )
            .await
            .unwrap()
        );
        assert!(
            proguard_mappings(&store, &app.id, Some("different"))
                .await
                .unwrap()
                .is_empty()
        );
        let mappings = proguard_mappings(&store, &app.id, Some("aabbccdd"))
            .await
            .unwrap();
        assert_eq!(mappings.len(), 1);
        assert_eq!(mappings[0].bytes, expected);
    }
}
