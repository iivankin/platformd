use proguard::{ProguardMapper, ProguardMapping, StackFrame};
use serde_json::{Value, json};

use crate::artifact;
use crate::error::{Error, Result};
use crate::storage::Store;

use super::{debug_images, stacktraces};

pub async fn symbolicate(store: &Store, service_id: &str, event: &Value) -> Result<Value> {
    let debug_id = proguard_debug_id(event);
    let mappings = artifact::proguard_mappings(store, service_id, debug_id.as_deref())
        .await?
        .into_iter()
        .map(|artifact| artifact.bytes)
        .collect::<Vec<_>>();
    let mut traces = stacktraces(event, true);
    let mut exceptions = event
        .pointer("/exception/values")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let (traces, exceptions, errors) = tokio::task::spawn_blocking(move || {
        let errors = apply_mappings(&mut traces, &mut exceptions, &mappings);
        Ok::<_, Error>((traces, exceptions, errors))
    })
    .await
    .map_err(|error| Error::Storage(format!("JVM symbolication task failed: {error}")))??;

    Ok(json!({
        "status": "completed",
        "stacktraces": traces,
        "exceptions": exceptions,
        "modules": debug_images(event),
        "errors": errors,
    }))
}

fn apply_mappings(
    stacktraces: &mut [Value],
    exceptions: &mut [Value],
    mappings: &[Vec<u8>],
) -> Vec<Value> {
    let mut errors = Vec::new();
    for bytes in mappings {
        match apply_mapping(stacktraces, exceptions, bytes) {
            Ok(true) => break,
            Ok(false) => {}
            Err(error) => errors.push(json!({
                "type": "malformed_proguard_mapping",
                "message": error.to_string(),
            })),
        }
    }
    errors
}

fn apply_mapping(
    stacktraces: &mut [Value],
    exceptions: &mut [Value],
    bytes: &[u8],
) -> Result<bool> {
    let mapping = ProguardMapping::new(bytes);
    if !mapping.is_valid() {
        return Err(Error::Storage("invalid ProGuard mapping".into()));
    }
    let mapper = ProguardMapper::new(mapping);
    let mut changed = false;
    for stacktrace in stacktraces {
        let Some(frames) = stacktrace.get_mut("frames").and_then(Value::as_array_mut) else {
            continue;
        };
        let mut remapped_frames = Vec::with_capacity(frames.len());
        for frame in frames.iter() {
            let Some(class) = frame.get("module").and_then(Value::as_str) else {
                remapped_frames.push(frame.clone());
                continue;
            };
            let method = frame
                .get("function")
                .and_then(Value::as_str)
                .unwrap_or("<unknown>");
            let line = frame
                .get("lineno")
                .and_then(Value::as_u64)
                .and_then(|line| usize::try_from(line).ok())
                .unwrap_or(0);
            let mapped = mapper.remap_frame(&StackFrame::new(class, method, line));
            let mut any = false;
            for mapped in mapped {
                any = true;
                changed = true;
                let mut output = frame.clone();
                if let Some(output) = output.as_object_mut() {
                    output.insert("module".into(), Value::String(mapped.class().to_owned()));
                    output.insert("function".into(), Value::String(mapped.method().to_owned()));
                    output.insert(
                        "lineno".into(),
                        mapped.line().map_or(Value::Null, Value::from),
                    );
                    output.insert(
                        "filename".into(),
                        mapped
                            .file()
                            .map_or(Value::Null, |file| Value::String(file.to_owned())),
                    );
                    output.insert(
                        "method_synthesized".into(),
                        mapped.method_synthesized().into(),
                    );
                    output.insert("data".into(), json!({ "symbolicated": true }));
                }
                remapped_frames.push(output);
            }
            if !any {
                remapped_frames.push(frame.clone());
            }
        }
        *frames = remapped_frames;
    }
    for exception in exceptions {
        let full_type = match (
            exception.get("module").and_then(Value::as_str),
            exception.get("type").and_then(Value::as_str),
        ) {
            (Some(module), Some(kind)) if !module.is_empty() => format!("{module}.{kind}"),
            (_, Some(kind)) => kind.to_owned(),
            _ => continue,
        };
        let Some(mapped) = mapper.remap_class(&full_type) else {
            continue;
        };
        changed = true;
        let (module, kind) = mapped.rsplit_once('.').unwrap_or(("", mapped));
        if let Some(exception) = exception.as_object_mut() {
            exception.insert("module".into(), Value::String(module.to_owned()));
            exception.insert("type".into(), Value::String(kind.to_owned()));
        }
    }
    Ok(changed)
}

fn proguard_debug_id(event: &Value) -> Option<String> {
    debug_images(event).into_iter().find_map(|image| {
        matches!(image.get("type").and_then(Value::as_str), Some("proguard"))
            .then(|| {
                image
                    .get("debug_id")
                    .or_else(|| image.get("uuid"))
                    .and_then(Value::as_str)
                    .map(str::to_owned)
            })
            .flatten()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn remaps_jvm_frame_and_exception() {
        let mut traces = vec![json!({"frames": [{
            "module": "a.a.a.b.c",
            "function": "a",
            "lineno": 13
        }]})];
        let mut exceptions = vec![json!({"module": "a.a.a.b", "type": "c"})];
        let mapping = br#"android.arch.core.internal.SafeIterableMap -> a.a.a.b.c:
    13:13:java.util.Map$Entry eldest():168:168 -> a
"#;
        assert!(apply_mapping(&mut traces, &mut exceptions, mapping).unwrap());
        assert_eq!(traces[0]["frames"][0]["function"], "eldest");
        assert_eq!(traces[0]["frames"][0]["lineno"], 168);
        assert_eq!(exceptions[0]["type"], "SafeIterableMap");
    }

    #[test]
    fn skips_valid_but_unrelated_mappings() {
        let mut traces = vec![json!({"frames": [{
            "module": "a",
            "function": "b",
            "lineno": 13
        }]})];
        let mut exceptions = Vec::new();
        let mappings = vec![
            br#"com.example.Unrelated -> x:
    1:1:void nope():10:10 -> y
"#
            .to_vec(),
            br#"com.example.Real -> a:
    13:13:void original():42:42 -> b
"#
            .to_vec(),
        ];

        let errors = apply_mappings(&mut traces, &mut exceptions, &mappings);

        assert!(errors.is_empty());
        assert_eq!(traces[0]["frames"][0]["module"], "com.example.Real");
        assert_eq!(traces[0]["frames"][0]["function"], "original");
        assert_eq!(traces[0]["frames"][0]["lineno"], 42);
    }
}
