use std::collections::HashMap;
use std::io::Cursor;

use base64::Engine;
use base64::engine::general_purpose::STANDARD as BASE64;
use serde_json::{Value, json};
use symbolic_debuginfo::sourcebundle::{
    SourceBundle, SourceBundleDebugSession, SourceFileDescriptor, SourceFileType,
};
use symbolic_sourcemapcache::{
    ScopeLookupResult, SourceMapCache, SourceMapCacheWriter, SourcePosition,
};

use crate::artifact;
use crate::error::{Error, Result};
use crate::storage::Store;

use super::{debug_images, stacktraces};

pub async fn symbolicate(store: &Store, service_id: &str, event: &Value) -> Result<Value> {
    let bundles = artifact::source_bundles(
        store,
        service_id,
        event.get("release").and_then(Value::as_str),
        event.get("dist").and_then(Value::as_str),
    )
    .await?;
    let mut traces = stacktraces(event, false);
    let modules = debug_images(event);
    let (traces, modules, errors) = tokio::task::spawn_blocking(move || {
        process_stacktraces(&mut traces, &modules, &bundles).map(|errors| (traces, modules, errors))
    })
    .await
    .map_err(|error| Error::Storage(format!("JavaScript symbolication task failed: {error}")))??;

    Ok(json!({
        "status": "completed",
        "stacktraces": traces,
        "modules": modules,
        "errors": errors,
    }))
}

fn process_stacktraces(
    stacktraces: &mut [Value],
    modules: &[Value],
    bundles: &[artifact::StoredArtifact],
) -> Result<Vec<Value>> {
    let sessions = bundles
        .iter()
        .map(|artifact| {
            SourceBundle::parse(&artifact.bytes)
                .map_err(|error| format!("parse source bundle: {error}"))?
                .debug_session()
                .map_err(|error| format!("open source bundle: {error}"))
        })
        .collect::<Vec<_>>();
    let mut source_cache =
        HashMap::<SourceCacheKey, std::result::Result<PreparedSource, String>>::new();
    let mut errors = Vec::new();
    for stacktrace in stacktraces {
        let Some(frames) = stacktrace.get_mut("frames").and_then(Value::as_array_mut) else {
            continue;
        };
        for frame in frames {
            let abs_path = frame
                .get("abs_path")
                .or_else(|| frame.get("filename"))
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned();
            let debug_id = modules.iter().find_map(|module| {
                let code_file = module.get("code_file").and_then(Value::as_str)?;
                (code_file == abs_path)
                    .then(|| module.get("debug_id").and_then(Value::as_str))
                    .flatten()
            });
            let mut resolved = false;
            for (bundle_index, bundle) in bundles.iter().enumerate() {
                let key = SourceCacheKey {
                    bundle_index,
                    abs_path: abs_path.clone(),
                    debug_id: debug_id.map(str::to_owned),
                };
                let prepared = source_cache.entry(key).or_insert_with(|| {
                    sessions[bundle_index]
                        .as_ref()
                        .map_err(Clone::clone)
                        .and_then(|session| {
                            prepare_source(session, &abs_path, debug_id)
                                .map_err(|error| error.to_string())
                        })
                });
                match prepared
                    .as_ref()
                    .map_err(|message| Error::Storage(message.clone()))
                    .and_then(|prepared| apply_prepared_source(frame, prepared))
                {
                    Ok(true) => {
                        resolved = true;
                        break;
                    }
                    Ok(false) => {}
                    Err(error) => errors.push(json!({
                        "abs_path": abs_path,
                        "type": "malformed_sourcemap",
                        "message": error.to_string(),
                        "artifact": bundle.id,
                    })),
                }
            }
            if !resolved && !bundles.is_empty() {
                errors.push(json!({
                    "abs_path": abs_path,
                    "type": "missing_source",
                }));
            }
        }
    }
    Ok(errors)
}

#[derive(Eq, Hash, PartialEq)]
struct SourceCacheKey {
    bundle_index: usize,
    abs_path: String,
    debug_id: Option<String>,
}

enum PreparedSource {
    Missing,
    Context(String),
    SourceMap { bytes: Vec<u8>, reference: String },
}

fn prepare_source<'a>(
    session: &'a SourceBundleDebugSession<'a>,
    abs_path: &str,
    debug_id: Option<&str>,
) -> Result<PreparedSource> {
    let Some(source) = find_minified_source(session, abs_path, debug_id)? else {
        return prepare_standalone_sourcemap(session, abs_path, debug_id);
    };
    let source_contents = source
        .contents()
        .ok_or_else(|| Error::Storage("source bundle entry has no contents".into()))?
        .to_owned();
    let Some(sourcemap_reference) = source.source_mapping_url().map(str::to_owned) else {
        return Ok(PreparedSource::Context(source_contents));
    };
    let sourcemap_contents = if let Some(contents) = decode_data_sourcemap(&sourcemap_reference)? {
        contents
    } else {
        let sourcemap_url = resolve_url(abs_path, &sourcemap_reference);
        let descriptor = match find_source_by_url(session, &sourcemap_url)? {
            Some(descriptor) => Some(descriptor),
            None => find_source_by_url(session, &sourcemap_reference)?,
        }
        .ok_or_else(|| Error::Storage(format!("source map {sourcemap_url} is missing")))?;
        descriptor
            .contents()
            .ok_or_else(|| Error::Storage("source map entry has no contents".into()))?
            .to_owned()
    };

    compile_sourcemap(&source_contents, &sourcemap_contents, sourcemap_reference)
}

fn prepare_standalone_sourcemap<'a>(
    session: &'a SourceBundleDebugSession<'a>,
    abs_path: &str,
    debug_id: Option<&str>,
) -> Result<PreparedSource> {
    // Compiled full-stack bundles can expose source maps to CI while keeping the corresponding
    // browser chunks embedded in an executable. Exact URL lookup keeps content-hashed chunk names
    // as the artifact identity without requiring SDK metadata.
    let Some(sourcemap) = find_standalone_sourcemap(session, abs_path, debug_id)? else {
        return Ok(PreparedSource::Missing);
    };
    let sourcemap_reference = sourcemap
        .url()
        .map(str::to_owned)
        .unwrap_or_else(|| standalone_sourcemap_url(abs_path));
    let sourcemap_contents = sourcemap
        .contents()
        .ok_or_else(|| Error::Storage("source map entry has no contents".into()))?;
    compile_sourcemap("", sourcemap_contents, sourcemap_reference)
}

fn compile_sourcemap(
    source_contents: &str,
    sourcemap_contents: &str,
    sourcemap_reference: String,
) -> Result<PreparedSource> {
    let writer = SourceMapCacheWriter::new(source_contents, sourcemap_contents)
        .map_err(|error| Error::Storage(format!("compile source map: {error}")))?;
    let mut bytes = Cursor::new(Vec::new());
    writer
        .serialize(&mut bytes)
        .map_err(|error| Error::Storage(format!("serialize source map cache: {error}")))?;
    Ok(PreparedSource::SourceMap {
        bytes: bytes.into_inner(),
        reference: sourcemap_reference,
    })
}

fn apply_prepared_source(frame: &mut Value, prepared: &PreparedSource) -> Result<bool> {
    let (bytes, sourcemap_reference) = match prepared {
        PreparedSource::Missing => return Ok(false),
        PreparedSource::Context(source) => {
            apply_context(frame, source);
            return Ok(false);
        }
        PreparedSource::SourceMap { bytes, reference } => (bytes, reference),
    };
    let cache = SourceMapCache::parse(bytes)
        .map_err(|error| Error::Storage(format!("read source map cache: {error}")))?;
    let line = frame.get("lineno").and_then(Value::as_u64).unwrap_or(0);
    if line == 0 {
        return Ok(false);
    }
    let column = frame.get("colno").and_then(Value::as_u64).unwrap_or(1);
    let Ok(line) = u32::try_from(line - 1) else {
        return Ok(false);
    };
    let Ok(column) = u32::try_from(column.saturating_sub(1)) else {
        return Ok(false);
    };
    let Some(location) = cache.lookup(SourcePosition::new(line, column)) else {
        return Ok(false);
    };

    let Some(frame) = frame.as_object_mut() else {
        return Ok(false);
    };
    frame.insert("lineno".into(), Value::from(location.line() + 1));
    frame.insert("colno".into(), Value::from(location.column() + 1));
    if let Some(filename) = location.file_name().filter(|name| !name.is_empty()) {
        frame.insert("filename".into(), Value::String(filename.to_owned()));
        frame.insert("abs_path".into(), Value::String(filename.to_owned()));
    }
    let function = match location.scope() {
        ScopeLookupResult::NamedScope(name) => Some(name),
        ScopeLookupResult::AnonymousScope => Some("<anonymous>"),
        ScopeLookupResult::Unknown => location.name(),
    };
    if let Some(function) = function.filter(|name| !name.is_empty()) {
        frame.insert("function".into(), Value::String(function.to_owned()));
    }
    frame.insert(
        "data".into(),
        json!({ "symbolicated": true, "sourcemap": sourcemap_reference }),
    );
    if let Some(source) = location.file_source()
        && let Ok(line) = usize::try_from(location.line())
        && let Some(line) = line.checked_add(1)
    {
        apply_context_to_object(frame, source, line);
    }
    Ok(true)
}

#[cfg(test)]
fn symbolicate_frame(
    frame: &mut Value,
    abs_path: &str,
    debug_id: Option<&str>,
    bundle_bytes: &[u8],
) -> Result<bool> {
    let bundle = SourceBundle::parse(bundle_bytes)
        .map_err(|error| Error::Storage(format!("parse source bundle: {error}")))?;
    let session = bundle
        .debug_session()
        .map_err(|error| Error::Storage(format!("open source bundle: {error}")))?;
    let prepared = prepare_source(&session, abs_path, debug_id)?;
    apply_prepared_source(frame, &prepared)
}

fn find_minified_source<'a>(
    session: &'a SourceBundleDebugSession<'a>,
    abs_path: &str,
    debug_id: Option<&str>,
) -> Result<Option<SourceFileDescriptor<'a>>> {
    if let Some(debug_id) = debug_id.and_then(|value| value.parse().ok()) {
        for ty in [SourceFileType::MinifiedSource, SourceFileType::Source] {
            if let Some(source) = session
                .source_by_debug_id(debug_id, ty)
                .map_err(source_bundle_error)?
            {
                return Ok(Some(source));
            }
        }
    }
    find_source_by_url(session, abs_path)
}

fn find_standalone_sourcemap<'a>(
    session: &'a SourceBundleDebugSession<'a>,
    abs_path: &str,
    debug_id: Option<&str>,
) -> Result<Option<SourceFileDescriptor<'a>>> {
    if let Some(debug_id) = debug_id.and_then(|value| value.parse().ok())
        && let Some(source) = session
            .source_by_debug_id(debug_id, SourceFileType::SourceMap)
            .map_err(source_bundle_error)?
    {
        return Ok(Some(source));
    }
    find_source_by_url(session, &standalone_sourcemap_url(abs_path))
}

fn standalone_sourcemap_url(source_url: &str) -> String {
    let suffix_start = source_url.find(['?', '#']).unwrap_or(source_url.len());
    format!("{}.map", &source_url[..suffix_start])
}

fn find_source_by_url<'a>(
    session: &'a SourceBundleDebugSession<'a>,
    url: &str,
) -> Result<Option<SourceFileDescriptor<'a>>> {
    for candidate in release_file_candidates(url) {
        if let Some(source) = session
            .source_by_url(&candidate)
            .map_err(source_bundle_error)?
        {
            return Ok(Some(source));
        }
    }
    Ok(None)
}

fn release_file_candidates(url: &str) -> Vec<String> {
    let without_fragment = url.split('#').next().unwrap_or(url);
    let without_query = without_fragment
        .split('?')
        .next()
        .unwrap_or(without_fragment);
    let relative = url::Url::parse(without_fragment).ok().map(|url| {
        let query = url
            .query()
            .map(|query| format!("?{query}"))
            .unwrap_or_default();
        format!("~{}{query}", url.path())
    });
    let relative_without_query = relative
        .as_deref()
        .map(|url| url.split('?').next().unwrap_or(url).to_owned());
    [
        Some(without_fragment.to_owned()),
        (without_query != without_fragment).then(|| without_query.to_owned()),
        relative,
        relative_without_query,
    ]
    .into_iter()
    .flatten()
    .collect()
}

fn resolve_url(base: &str, reference: &str) -> String {
    url::Url::parse(base)
        .and_then(|base| base.join(reference))
        .map(|url| url.to_string())
        .unwrap_or_else(|_| {
            base.rsplit_once('/')
                .map(|(prefix, _)| format!("{prefix}/{reference}"))
                .unwrap_or_else(|| reference.to_owned())
        })
}

fn decode_data_sourcemap(reference: &str) -> Result<Option<String>> {
    let Some(data) = reference.strip_prefix("data:") else {
        return Ok(None);
    };
    let Some((metadata, encoded)) = data.split_once(',') else {
        return Err(Error::Storage(
            "inline source map data URL has no payload".into(),
        ));
    };
    let mut parts = metadata.split(';').map(str::trim);
    if !parts
        .next()
        .is_some_and(|mime| mime.eq_ignore_ascii_case("application/json"))
    {
        return Ok(None);
    }
    if !parts.any(|parameter| parameter.eq_ignore_ascii_case("base64")) {
        return Err(Error::Storage(
            "inline source map data URL is not base64 encoded".into(),
        ));
    }
    let bytes = BASE64
        .decode(encoded)
        .map_err(|error| Error::Storage(format!("decode inline source map: {error}")))?;
    String::from_utf8(bytes)
        .map(Some)
        .map_err(|error| Error::Storage(format!("inline source map is not UTF-8: {error}")))
}

fn apply_context(frame: &mut Value, source: &str) {
    let line = frame
        .get("lineno")
        .and_then(Value::as_u64)
        .and_then(|line| usize::try_from(line).ok())
        .unwrap_or(0);
    if let Some(frame) = frame.as_object_mut() {
        apply_context_to_object(frame, source, line);
    }
}

fn apply_context_to_object(frame: &mut serde_json::Map<String, Value>, source: &str, line: usize) {
    if line == 0 {
        return;
    }
    let lines: Vec<&str> = source.lines().collect();
    let Some(context) = lines.get(line - 1) else {
        return;
    };
    let start = line.saturating_sub(4);
    let end = (line + 3).min(lines.len());
    frame.insert("pre_context".into(), json!(lines[start..line - 1]));
    frame.insert("context_line".into(), Value::String((*context).to_owned()));
    frame.insert("post_context".into(), json!(lines[line..end]));
}

fn source_bundle_error(error: symbolic_debuginfo::sourcebundle::SourceBundleError) -> Error {
    Error::Storage(format!("read source bundle: {error}"))
}

#[cfg(test)]
mod tests {
    use std::fs::File;
    use std::io::Cursor;

    use symbolic_debuginfo::sourcebundle::{SourceBundleWriter, SourceFileInfo, SourceFileType};

    use super::*;

    #[test]
    fn decodes_inline_source_maps_with_media_type_parameters() {
        let encoded = BASE64.encode(r#"{"version":3,"sources":[]}"#);
        let reference = format!("data:application/json;charset=utf-8;base64,{encoded}");

        assert_eq!(
            decode_data_sourcemap(&reference).unwrap().as_deref(),
            Some(r#"{"version":3,"sources":[]}"#)
        );
    }

    #[test]
    fn resolves_a_frame_from_a_source_bundle() {
        let directory = tempfile::tempdir().unwrap();
        let bundle_path = directory.path().join("bundle.zip");
        let mut bundle = SourceBundleWriter::start(File::create(&bundle_path).unwrap()).unwrap();
        let mut minified_info = SourceFileInfo::new();
        minified_info.set_ty(SourceFileType::MinifiedSource);
        minified_info.set_url("~/app.min.js".into());
        minified_info.add_header("sourcemap".into(), "app.min.js.map".into());
        bundle
            .add_file(
                "app.min.js",
                Cursor::new("function boom(){throw new Error(\"broken\")}boom();"),
                minified_info,
            )
            .unwrap();
        let mut map_info = SourceFileInfo::new();
        map_info.set_ty(SourceFileType::SourceMap);
        map_info.set_url("~/app.min.js.map".into());
        bundle
            .add_file(
                "app.min.js.map",
                Cursor::new(
                    r#"{"version":3,"file":"app.min.js","sources":["app.js"],"sourcesContent":["export function boom() {\n  throw new Error('broken');\n}\nboom();\n"],"names":["boom","Error"],"mappings":"AAAO,SAASA,OAAO,CAAC,CAAE,MAAM,IAAIC,KAAK,CAAC,QAAQ,CAAC,CAAC,CAACD,IAAI,EAAE"}"#,
                ),
                map_info,
            )
            .unwrap();
        bundle.finish().unwrap();
        let bytes = std::fs::read(bundle_path).unwrap();
        let mut frame = json!({
            "abs_path": "https://example.invalid/app.min.js",
            "filename": "app.min.js",
            "function": "boom",
            "lineno": 1,
            "colno": 23,
        });

        assert!(
            symbolicate_frame(
                &mut frame,
                "https://example.invalid/app.min.js",
                None,
                &bytes,
            )
            .unwrap()
        );
        assert_eq!(frame["filename"], "app.js");
        assert_eq!(frame["data"]["symbolicated"], true);
    }

    #[test]
    fn resolves_a_hashed_chunk_from_a_standalone_source_map() {
        let directory = tempfile::tempdir().unwrap();
        let bundle_path = directory.path().join("bundle.zip");
        let mut bundle = SourceBundleWriter::start(File::create(&bundle_path).unwrap()).unwrap();
        let mut map_info = SourceFileInfo::new();
        map_info.set_ty(SourceFileType::SourceMap);
        map_info.set_url("~/chunk-a1b2c3.js.map".into());
        bundle
            .add_file(
                "chunk-a1b2c3.js.map",
                Cursor::new(
                    r#"{"version":3,"sources":["app.js"],"sourcesContent":["export function boom() {\n  throw new Error('broken');\n}\nboom();\n"],"names":["boom","Error"],"mappings":"AAAO,SAASA,OAAO,CAAC,CAAE,MAAM,IAAIC,KAAK,CAAC,QAAQ,CAAC,CAAC,CAACD,IAAI,EAAE"}"#,
                ),
                map_info,
            )
            .unwrap();
        bundle.finish().unwrap();
        let bytes = std::fs::read(bundle_path).unwrap();
        let mut frame = json!({
            "abs_path": "https://example.invalid/chunk-a1b2c3.js?cache=1",
            "filename": "chunk-a1b2c3.js",
            "function": "boom",
            "lineno": 1,
            "colno": 23,
        });

        assert!(
            symbolicate_frame(
                &mut frame,
                "https://example.invalid/chunk-a1b2c3.js?cache=1",
                None,
                &bytes,
            )
            .unwrap()
        );
        assert_eq!(frame["filename"], "app.js");
        assert_eq!(frame["data"]["symbolicated"], true);
        assert_eq!(frame["data"]["sourcemap"], "~/chunk-a1b2c3.js.map");
    }
}
