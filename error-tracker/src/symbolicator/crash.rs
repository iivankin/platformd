use std::io::Cursor;
use std::path::PathBuf;

use apple_crash_report_parser::AppleCrashReport;
use minidump::Minidump;
use minidump_unwind::{Symbolizer, simple_symbol_supplier};
use serde_json::{Map, Value, json};

use crate::error::{Error, Result};
use crate::storage::Store;

use super::native;

pub async fn process_minidump(store: &Store, app_id: &str, bytes: Vec<u8>) -> Result<Value> {
    let dump = Minidump::read(bytes)
        .map_err(|error| Error::InvalidRequest(format!("parse minidump: {error}")))?;
    // Native DIF symbolication is applied from our own artifact store below. The empty
    // supplier still lets rust-minidump unwind using context, frame pointers and scanning.
    let symbolizer = Symbolizer::new(simple_symbol_supplier(Vec::<PathBuf>::new()));
    let state = minidump_processor::process_minidump(&dump, &symbolizer)
        .await
        .map_err(|error| Error::InvalidRequest(format!("process minidump: {error}")))?;
    let crashed_thread = state.requesting_thread;
    let mut raw = Vec::new();
    state
        .print_json(&mut raw, false)
        .map_err(|error| Error::Storage(format!("encode minidump result: {error}")))?;
    let raw: Value = serde_json::from_slice(&raw)
        .map_err(|error| Error::Storage(format!("decode minidump result: {error}")))?;
    let event = minidump_event(&raw, crashed_thread);
    let mut response = native::symbolicate(store, app_id, &event).await?;
    let crash_info = raw.get("crash_info").cloned().unwrap_or(Value::Null);
    merge_response(
        &mut response,
        [
            ("crashed", Value::Bool(crashed_thread.is_some())),
            (
                "crash_reason",
                crash_info.get("type").cloned().unwrap_or(Value::Null),
            ),
            (
                "crash_details",
                crash_info
                    .get("address")
                    .map(|address| Value::String(format!("at {address}")))
                    .unwrap_or(Value::Null),
            ),
            (
                "assertion",
                crash_info.get("assertion").cloned().unwrap_or(Value::Null),
            ),
            (
                "system_info",
                raw.get("system_info").cloned().unwrap_or(Value::Null),
            ),
        ],
    );
    Ok(response)
}

pub async fn process_apple_crash_report(
    store: &Store,
    app_id: &str,
    bytes: Vec<u8>,
) -> Result<Value> {
    let report = AppleCrashReport::from_reader(Cursor::new(bytes))
        .map_err(|error| Error::InvalidRequest(format!("parse Apple crash report: {error}")))?;
    let mut metadata = report.metadata;
    let modules = report
        .binary_images
        .into_iter()
        .map(|image| {
            json!({
                "type": "macho",
                "code_file": image.path,
                "debug_file": image.name,
                "debug_id": image.uuid.to_string(),
                "image_addr": format!("{:#x}", image.addr.0),
                "image_size": image.size,
                "arch": image.arch,
                "version": image.version,
            })
        })
        .collect::<Vec<_>>();
    let stacktraces = report
        .threads
        .into_iter()
        .map(|thread| {
            let frames = thread
                .frames
                .into_iter()
                .map(|frame| {
                    json!({
                        "package": frame.module,
                        "symbol": frame.symbol,
                        "filename": frame.filename,
                        "lineno": frame.lineno,
                        "instruction_addr": format!("{:#x}", frame.instruction_addr.0),
                    })
                })
                .collect::<Vec<_>>();
            let registers = thread
                .registers
                .unwrap_or_default()
                .into_iter()
                .map(|(name, address)| (name, Value::String(format!("{:#x}", address.0))));
            json!({
                "id": thread.id,
                "name": thread.name,
                "crashed": thread.crashed,
                "current": thread.crashed,
                "stacktrace": {
                    "frames": frames,
                    "registers": Map::from_iter(registers),
                }
            })
        })
        .collect::<Vec<_>>();
    let event = json!({
        "platform": "native",
        "debug_meta": { "images": modules },
        "threads": { "values": stacktraces },
    });
    let mut response = native::symbolicate(store, app_id, &event).await?;
    let os = metadata.remove("OS Version").unwrap_or_default();
    let crash_reason = metadata.remove("Exception Type");
    let crash_details = report
        .application_specific_information
        .or_else(|| metadata.remove("Exception Message"))
        .or_else(|| metadata.remove("Exception Subtype"))
        .or_else(|| metadata.remove("Exception Codes"));
    merge_response(
        &mut response,
        [
            ("crashed", Value::Bool(true)),
            (
                "timestamp",
                report
                    .timestamp
                    .map(|timestamp| Value::String(timestamp.to_rfc3339()))
                    .unwrap_or(Value::Null),
            ),
            (
                "crash_reason",
                crash_reason.map(Value::String).unwrap_or(Value::Null),
            ),
            (
                "crash_details",
                crash_details.map(Value::String).unwrap_or(Value::Null),
            ),
            (
                "system_info",
                json!({
                    "os_name": os,
                    "device_model": metadata.remove("Hardware Model").unwrap_or_default(),
                    "cpu_arch": report.code_type.unwrap_or_default(),
                }),
            ),
        ],
    );
    Ok(response)
}

fn minidump_event(raw: &Value, crashed_thread: Option<usize>) -> Value {
    let modules = raw
        .get("modules")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .map(|module| {
            let base = module.get("base_addr").and_then(parse_address).unwrap_or(0);
            let end = module
                .get("end_addr")
                .and_then(parse_address)
                .unwrap_or(base);
            json!({
                "type": "unknown",
                "code_file": module.get("filename"),
                "debug_file": module.get("debug_file"),
                "debug_id": module.get("debug_id"),
                "code_id": module.get("code_id"),
                "image_addr": format!("{base:#x}"),
                "image_size": end.saturating_sub(base),
            })
        })
        .collect::<Vec<_>>();
    let threads = raw
        .get("threads")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .enumerate()
        .map(|(index, thread)| {
            let mut frames = thread
                .get("frames")
                .and_then(Value::as_array)
                .into_iter()
                .flatten()
                .map(|frame| {
                    json!({
                        "package": frame.get("module"),
                        "function": frame.get("function"),
                        "filename": frame.get("file"),
                        "lineno": frame.get("line"),
                        "instruction_addr": frame.get("offset"),
                        "trust": frame.get("trust"),
                        "registers": frame.get("registers"),
                    })
                })
                .collect::<Vec<_>>();
            frames.reverse();
            json!({
                "id": thread.get("thread_id"),
                "name": thread.get("thread_name"),
                "crashed": crashed_thread == Some(index),
                "current": crashed_thread == Some(index),
                "stacktrace": { "frames": frames },
            })
        })
        .collect::<Vec<_>>();
    json!({
        "platform": "native",
        "debug_meta": { "images": modules },
        "threads": { "values": threads },
    })
}

fn merge_response<const N: usize>(response: &mut Value, values: [(&str, Value); N]) {
    if let Some(response) = response.as_object_mut() {
        for (key, value) in values {
            response.insert(key.into(), value);
        }
    }
}

fn parse_address(value: &Value) -> Option<u64> {
    match value {
        Value::Number(number) => number.as_u64(),
        Value::String(value) => u64::from_str_radix(value.trim_start_matches("0x"), 16).ok(),
        _ => None,
    }
}
