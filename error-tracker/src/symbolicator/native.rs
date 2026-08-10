use serde_json::{Value, json};
use symbolic_debuginfo::Archive;
use symbolic_symcache::{SymCache, SymCacheConverter};

use crate::artifact;
use crate::error::{Error, Result};
use crate::storage::Store;

use super::{debug_images, stacktraces};

struct NativeCache {
    debug_id: String,
    bytes: Vec<u8>,
}

pub async fn symbolicate(store: &Store, app_id: &str, event: &Value) -> Result<Value> {
    let mut traces = stacktraces(event, true);
    let mut modules = debug_images(event);
    let mut artifacts = Vec::new();
    for module in &modules {
        let Some(debug_id) = image_debug_id(module) else {
            continue;
        };
        let code_id = module.get("code_id").and_then(Value::as_str);
        for artifact in artifact::debug_files(store, app_id, Some(debug_id), code_id).await? {
            if !artifacts.iter().any(|stored: &artifact::StoredArtifact| {
                stored.id == artifact.id
                    && stored.debug_id == artifact.debug_id
                    && stored.symbol_type == artifact.symbol_type
            }) {
                artifacts.push(artifact);
            }
        }
    }
    let (traces, modules, errors) = tokio::task::spawn_blocking(move || {
        let (caches, errors) = build_caches(&artifacts);
        apply_caches(&mut traces, &mut modules, &caches);
        (traces, modules, errors)
    })
    .await
    .map_err(|error| Error::Storage(format!("native symbolication task failed: {error}")))?;
    Ok(json!({
        "status": "completed",
        "stacktraces": traces,
        "modules": modules,
        "errors": errors,
    }))
}

fn build_caches(artifacts: &[artifact::StoredArtifact]) -> (Vec<NativeCache>, Vec<Value>) {
    let mut caches = Vec::new();
    let mut errors = Vec::new();
    for artifact in artifacts {
        if matches!(
            artifact.symbol_type.as_str(),
            "proguard" | "sourcebundle" | "bcsymbolmap" | "uuidmap" | "dart_symbol_map"
        ) {
            continue;
        }
        match build_cache(artifact) {
            Ok(mut built) => caches.append(&mut built),
            Err(error) => errors.push(json!({
                "type": "malformed_debug_file",
                "message": error.to_string(),
                "artifact": artifact.id,
                "name": artifact.name,
            })),
        }
    }
    (caches, errors)
}

fn build_cache(artifact: &artifact::StoredArtifact) -> Result<Vec<NativeCache>> {
    let archive = Archive::parse(&artifact.bytes)
        .map_err(|error| Error::Storage(format!("parse {}: {error}", artifact.name)))?;
    let mut caches = Vec::new();
    for object in archive.objects() {
        let object =
            object.map_err(|error| Error::Storage(format!("read {}: {error}", artifact.name)))?;
        let object_debug_id = object.debug_id().to_string().to_ascii_lowercase();
        if artifact
            .debug_id
            .as_ref()
            .is_some_and(|expected| !same_debug_id(expected, &object_debug_id))
        {
            continue;
        }
        let mut converter = SymCacheConverter::new();
        converter
            .process_object(&object)
            .map_err(|error| Error::Storage(format!("convert {}: {error}", artifact.name)))?;
        let mut bytes = Vec::new();
        converter
            .serialize(&mut bytes)
            .map_err(|error| Error::Storage(format!("serialize {}: {error}", artifact.name)))?;
        caches.push(NativeCache {
            debug_id: artifact
                .debug_id
                .clone()
                .unwrap_or(object_debug_id)
                .to_ascii_lowercase(),
            bytes,
        });
    }
    if caches.is_empty() {
        return Err(Error::Storage(format!(
            "{} contains no matching debug object",
            artifact.name
        )));
    }
    Ok(caches)
}

fn apply_caches(stacktraces: &mut [Value], modules: &mut [Value], caches: &[NativeCache]) {
    for module in modules.iter_mut() {
        let debug_id = image_debug_id(module);
        let status = debug_id
            .and_then(|debug_id| find_cache(caches, debug_id))
            .map_or("missing", |_| "found");
        if let Some(module) = module.as_object_mut() {
            module.insert("debug_status".into(), Value::String(status.into()));
        }
    }

    for stacktrace in stacktraces {
        let Some(frames) = stacktrace.get_mut("frames").and_then(Value::as_array_mut) else {
            continue;
        };
        let mut output = Vec::with_capacity(frames.len());
        for frame in frames.iter() {
            let Some(instruction) = frame
                .get("instruction_addr")
                .or_else(|| frame.get("addr"))
                .and_then(parse_address)
            else {
                output.push(frame.clone());
                continue;
            };
            let Some(image) = find_image(modules, frame, instruction) else {
                output.push(frame.clone());
                continue;
            };
            let Some(debug_id) = image_debug_id(image) else {
                output.push(frame.clone());
                continue;
            };
            let Some(cache) = find_cache(caches, debug_id) else {
                output.push(frame.clone());
                continue;
            };
            let image_addr = image.get("image_addr").and_then(parse_address).unwrap_or(0);
            let relative = if image_addr == 0 {
                instruction
            } else {
                instruction.saturating_sub(image_addr)
            };
            let Ok(symcache) = SymCache::parse(&cache.bytes) else {
                output.push(frame.clone());
                continue;
            };
            let mut locations = symcache.lookup(relative).peekable();
            if locations.peek().is_none() {
                output.push(frame.clone());
                continue;
            }
            for location in locations {
                let mut symbolicated = frame.clone();
                if let Some(symbolicated) = symbolicated.as_object_mut() {
                    let function = location.function();
                    symbolicated
                        .insert("function".into(), Value::String(function.name().to_owned()));
                    symbolicated.insert("symbol".into(), Value::String(function.name().to_owned()));
                    symbolicated.insert("lineno".into(), Value::from(location.line()));
                    if let Some(file) = location.file() {
                        let path = file.full_path();
                        let filename = path.rsplit('/').next().unwrap_or(&path).to_owned();
                        symbolicated.insert("filename".into(), Value::String(filename));
                        symbolicated.insert("abs_path".into(), Value::String(path));
                    }
                    symbolicated.insert(
                        "data".into(),
                        json!({ "symbolicated": true, "debug_id": debug_id }),
                    );
                }
                output.push(symbolicated);
            }
        }
        *frames = output;
    }
}

fn find_image<'a>(modules: &'a [Value], frame: &Value, instruction: u64) -> Option<&'a Value> {
    let package = frame.get("package").and_then(Value::as_str);
    modules.iter().find(|image| {
        if package.is_some_and(|package| {
            image
                .get("code_file")
                .or_else(|| image.get("debug_file"))
                .and_then(Value::as_str)
                .is_some_and(|file| file == package || file.ends_with(package))
        }) {
            return true;
        }
        let start = image.get("image_addr").and_then(parse_address).unwrap_or(0);
        let size = image.get("image_size").and_then(parse_address).unwrap_or(0);
        start != 0
            && instruction >= start
            && (size == 0 || instruction < start.saturating_add(size))
    })
}

fn find_cache<'a>(caches: &'a [NativeCache], debug_id: &str) -> Option<&'a NativeCache> {
    caches
        .iter()
        .find(|cache| same_debug_id(&cache.debug_id, debug_id))
}

fn image_debug_id(image: &Value) -> Option<&str> {
    image
        .get("debug_id")
        .or_else(|| image.get("uuid"))
        .and_then(Value::as_str)
}

fn same_debug_id(left: &str, right: &str) -> bool {
    match (
        left.parse::<symbolic_common::DebugId>(),
        right.parse::<symbolic_common::DebugId>(),
    ) {
        (Ok(left), Ok(right)) => left == right,
        _ => false,
    }
}

fn parse_address(value: &Value) -> Option<u64> {
    match value {
        Value::Number(number) => number.as_u64(),
        Value::String(value) => u64::from_str_radix(value.trim_start_matches("0x"), 16)
            .ok()
            .or_else(|| value.parse().ok()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn compares_formatted_and_compact_debug_ids() {
        assert!(same_debug_id(
            "dfb8e43a-f242-3d73-a453-aeb6a777ef75-0",
            "DFB8E43AF2423D73A453AEB6A777EF750"
        ));
    }

    #[test]
    fn converts_and_resolves_a_breakpad_debug_file() {
        let artifact = artifact::StoredArtifact {
            id: "artifact".into(),
            name: "app.sym".into(),
            debug_id: Some("dfb8e43a-f242-3d73-a453-aeb6a777ef75-0".into()),
            symbol_type: "breakpad".into(),
            bytes: b"MODULE Linux x86_64 DFB8E43AF2423D73A453AEB6A777EF750 app\n\
FILE 0 /src/main.c\n\
FUNC 1000 30 0 crash_now\n\
1000 10 42 0\n"
                .to_vec(),
        };
        let caches = build_cache(&artifact).unwrap();
        let symcache = SymCache::parse(&caches[0].bytes).unwrap();
        let location = symcache.lookup(0x1000).next().unwrap();
        assert_eq!(location.function().name(), "crash_now");
        assert_eq!(location.line(), 42);
        assert_eq!(location.file().unwrap().full_path(), "/src/main.c");
    }
}
