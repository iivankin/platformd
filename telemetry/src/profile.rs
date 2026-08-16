use std::collections::{HashMap, HashSet};
use std::str::FromStr;

use android_trace_log::{Action, Time};
use base64::Engine;
use base64::engine::general_purpose::{STANDARD, STANDARD_NO_PAD};
use jiff::Timestamp;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};

const DEFAULT_SAMPLE_DURATION_NANO: u64 = 10_000_000;
const MAX_LEGACY_DURATION_NANO: u64 = 30_000_000_000;
const MAX_CHUNK_DURATION_NANO: u64 = 66_000_000_000;
const COCOA_POINTER_AUTH_MASK: u64 = 0x0000000FFFFFFFFF;

pub(crate) fn sentry_profile_rows(
    service_id: &str,
    payload: &Value,
    received_at_unix_nano: u64,
) -> Vec<Value> {
    let mut rows = Vec::new();
    if let Some(profile) = payload.get("profile").and_then(Value::as_object) {
        rows.extend(standard_profile_rows(
            service_id,
            payload,
            profile,
            received_at_unix_nano,
        ));
    }
    if let Some(profile) = payload.get("js_profile").and_then(Value::as_object) {
        rows.extend(standard_profile_rows(
            service_id,
            payload,
            profile,
            received_at_unix_nano,
        ));
    }
    if let Some(sampled_profile) = text(payload.get("sampled_profile")) {
        rows.extend(android_profile_rows(
            service_id,
            payload,
            sampled_profile,
            received_at_unix_nano,
        ));
    }
    rows
}

fn standard_profile_rows(
    service_id: &str,
    payload: &Value,
    profile: &serde_json::Map<String, Value>,
    received_at_unix_nano: u64,
) -> Vec<Value> {
    let Some(samples) = profile.get("samples").and_then(Value::as_array) else {
        return Vec::new();
    };
    let Some(stacks) = profile.get("stacks").and_then(Value::as_array) else {
        return Vec::new();
    };
    let Some(frames) = profile.get("frames").and_then(Value::as_array) else {
        return Vec::new();
    };
    let version = text(payload.get("version")).unwrap_or("1");
    let profile_id = if version.starts_with('2') {
        text(payload.get("chunk_id"))
    } else {
        text(payload.get("profile_id")).or_else(|| text(payload.get("event_id")))
    }
    .map(normalize_id)
    .filter(|value| !value.is_empty());
    let Some(profile_id) = profile_id else {
        return Vec::new();
    };
    let profiler_id = text(payload.get("profiler_id"))
        .map(normalize_id)
        .unwrap_or_default();
    let platform = text(payload.get("platform")).unwrap_or("unknown");
    let base_time = timestamp_nano(payload.get("timestamp"));
    let transactions = transactions(payload);
    let transaction_trace = transaction_trace(payload);
    let thread_names = thread_names(profile);

    if !valid_references(samples, stacks, frames) {
        return Vec::new();
    }
    let mut sample_count_by_thread = HashMap::<&str, usize>::new();
    for sample in samples.iter().filter_map(Value::as_object) {
        let Some(thread_id) = text(sample.get("thread_id")) else {
            continue;
        };
        let Some(stack_id) = integer(sample.get("stack_id")).map(|value| value as usize) else {
            continue;
        };
        if stacks
            .get(stack_id)
            .and_then(Value::as_array)
            .is_some_and(|stack| !stack.is_empty())
        {
            *sample_count_by_thread.entry(thread_id).or_default() += 1;
        }
    }
    let retained_threads = sample_count_by_thread
        .into_iter()
        .filter_map(|(thread_id, count)| (count > 1).then_some(thread_id))
        .collect::<HashSet<_>>();
    let mut normalized = samples
        .iter()
        .enumerate()
        .filter_map(|(index, sample)| {
            let sample = sample.as_object()?;
            let stack_id = integer(sample.get("stack_id"))? as usize;
            let stack = stacks.get(stack_id)?.as_array()?;
            let thread_id = text(sample.get("thread_id"))?.to_owned();
            if !retained_threads.contains(thread_id.as_str()) {
                return None;
            }
            let relative_nano = integer(sample.get("elapsed_since_start_ns"));
            let sample_time_unix_nano = if version.starts_with('2') {
                timestamp_nano(sample.get("timestamp"))?
            } else {
                base_time?.checked_add(relative_nano?)?
            };
            let stack = stack
                .iter()
                .filter_map(|value| integer(Some(value)))
                .filter_map(|frame_index| frames.get(frame_index as usize))
                // Sentry profile stacks are leaf-first. Persist root-first so the
                // UI can build a flamegraph without SDK-specific ordering rules.
                .rev()
                .map(|frame| normalized_frame(frame, platform))
                .collect::<Vec<_>>();
            if stack.is_empty() {
                return None;
            }
            let (trace_id, span_id) = relative_nano
                .and_then(|elapsed| transaction_at(&transactions, elapsed))
                .map(|transaction| (transaction.trace_id.clone(), transaction.span_id.clone()))
                .or_else(|| transaction_trace.clone())
                .unwrap_or_default();
            Some(ProfileSample {
                index,
                profile_id: profile_id.clone(),
                profiler_id: profiler_id.clone(),
                trace_id,
                span_id,
                platform: platform.to_owned(),
                thread_id: thread_id.clone(),
                thread_name: thread_names.get(&thread_id).cloned().unwrap_or_default(),
                sample_time_unix_nano,
                stack,
            })
        })
        .collect::<Vec<_>>();
    normalized.sort_unstable_by_key(|sample| sample.sample_time_unix_nano);
    let max_duration = if version.starts_with('2') {
        MAX_CHUNK_DURATION_NANO
    } else {
        MAX_LEGACY_DURATION_NANO
    };
    if profile_duration(&normalized) > max_duration {
        return Vec::new();
    }
    serialize_samples(service_id, normalized, received_at_unix_nano)
}

fn serialize_samples(
    service_id: &str,
    mut normalized: Vec<ProfileSample>,
    received_at_unix_nano: u64,
) -> Vec<Value> {
    let durations = sample_durations(&normalized);
    normalized
        .drain(..)
        .zip(durations)
        .map(|(sample, duration_nano)| {
            let sample_id = format!(
                "{:x}",
                Sha256::digest(
                    format!(
                        "{}:{}:{}:{}:{}",
                        service_id,
                        sample.profile_id,
                        sample.thread_id,
                        sample.sample_time_unix_nano,
                        sample.index
                    )
                    .as_bytes()
                )
            );
            json!({
                "service_id": service_id,
                "profile_id": sample.profile_id,
                "profiler_id": sample.profiler_id,
                "trace_id": sample.trace_id,
                "span_id": sample.span_id,
                "platform": sample.platform,
                "thread_id": sample.thread_id,
                "thread_name": sample.thread_name,
                "sample_time_unix_nano": sample.sample_time_unix_nano,
                "duration_nano": duration_nano,
                "stack": Value::Array(sample.stack).to_string(),
                "received_at_unix_nano": received_at_unix_nano,
                "sample_id": sample_id,
                "version": received_at_unix_nano,
            })
        })
        .collect()
}

fn android_profile_rows(
    service_id: &str,
    payload: &Value,
    sampled_profile: &str,
    received_at_unix_nano: u64,
) -> Vec<Value> {
    let decoded = STANDARD_NO_PAD
        .decode(sampled_profile.as_bytes())
        .or_else(|_| STANDARD.decode(sampled_profile.as_bytes()));
    let Ok(decoded) = decoded else {
        return Vec::new();
    };
    let Ok(profile) = android_trace_log::parse(&decoded) else {
        return Vec::new();
    };
    if profile.events.is_empty()
        || profile.elapsed_time.is_zero()
        || u64::try_from(profile.elapsed_time.as_nanos()).unwrap_or(u64::MAX)
            > if text(payload.get("version")).is_some_and(|version| version.starts_with('2')) {
                MAX_CHUNK_DURATION_NANO
            } else {
                MAX_LEGACY_DURATION_NANO
            }
    {
        return Vec::new();
    }
    let Some(profile_id) = text(payload.get("chunk_id"))
        .or_else(|| text(payload.get("profile_id")))
        .or_else(|| text(payload.get("event_id")))
        .map(normalize_id)
        .filter(|value| !value.is_empty())
    else {
        return Vec::new();
    };
    let profiler_id = text(payload.get("profiler_id"))
        .map(normalize_id)
        .unwrap_or_default();
    let start_time_unix_nano = profile
        .start_time
        .timestamp_nanos_opt()
        .and_then(|value| u64::try_from(value).ok())
        .or_else(|| timestamp_nano(payload.get("timestamp")));
    let Some(start_time_unix_nano) = start_time_unix_nano else {
        return Vec::new();
    };
    let methods = profile
        .methods
        .iter()
        .map(|method| (method.id, method))
        .collect::<HashMap<_, _>>();
    let thread_names = profile
        .threads
        .iter()
        .map(|thread| (thread.id, thread.name.as_str()))
        .collect::<HashMap<_, _>>();
    let trace = transaction_trace(payload).unwrap_or_default();
    let mut stacks = HashMap::<u16, Vec<u32>>::new();
    let mut normalized = Vec::new();
    for (index, event) in profile.events.iter().enumerate() {
        let Some(offset_nano) = android_event_offset_nano(event.time) else {
            continue;
        };
        let stack = stacks.entry(event.thread_id).or_default();
        match event.action {
            Action::Enter => stack.push(event.method_id),
            Action::Exit | Action::Unwind => {
                if let Some(position) = stack
                    .iter()
                    .rposition(|method_id| *method_id == event.method_id)
                {
                    stack.truncate(position);
                }
            }
        }
        if stack.is_empty() {
            continue;
        }
        let frames = stack
            .iter()
            .filter_map(|method_id| methods.get(method_id))
            .map(|method| {
                json!({
                    "function": method.name,
                    "module": method.class_name,
                    "signature": method.signature,
                    "filename": method.source_file,
                    "lineno": method.source_line,
                    "in_app": true,
                })
            })
            .collect::<Vec<_>>();
        if frames.is_empty() {
            continue;
        }
        normalized.push(ProfileSample {
            index,
            profile_id: profile_id.clone(),
            profiler_id: profiler_id.clone(),
            trace_id: trace.0.clone(),
            span_id: trace.1.clone(),
            platform: text(payload.get("platform"))
                .unwrap_or("android")
                .to_owned(),
            thread_id: event.thread_id.to_string(),
            thread_name: thread_names
                .get(&event.thread_id)
                .copied()
                .unwrap_or_default()
                .to_owned(),
            sample_time_unix_nano: start_time_unix_nano.saturating_add(offset_nano),
            stack: frames,
        });
    }
    retain_sampled_threads(&mut normalized);
    normalized.sort_unstable_by_key(|sample| sample.sample_time_unix_nano);
    serialize_samples(service_id, normalized, received_at_unix_nano)
}

fn android_event_offset_nano(time: Time) -> Option<u64> {
    let duration = match time {
        Time::Global(duration) => duration,
        Time::Monotonic {
            wall: Some(wall), ..
        } => wall,
        Time::Monotonic { cpu: Some(cpu), .. } => cpu,
        Time::Monotonic { .. } => return None,
    };
    u64::try_from(duration.as_nanos()).ok()
}

fn retain_sampled_threads(samples: &mut Vec<ProfileSample>) {
    let mut counts = HashMap::<&str, usize>::new();
    for sample in samples.iter() {
        *counts.entry(&sample.thread_id).or_default() += 1;
    }
    let retained = counts
        .into_iter()
        .filter_map(|(thread, count)| (count > 1).then_some(thread.to_owned()))
        .collect::<HashSet<_>>();
    samples.retain(|sample| retained.contains(&sample.thread_id));
}

fn valid_references(samples: &[Value], stacks: &[Value], frames: &[Value]) -> bool {
    samples.iter().all(|sample| {
        sample
            .as_object()
            .and_then(|sample| integer(sample.get("stack_id")))
            .and_then(|stack_id| stacks.get(stack_id as usize))
            .is_some()
    }) && stacks.iter().all(|stack| {
        stack.as_array().is_some_and(|stack| {
            stack.iter().all(|frame_id| {
                integer(Some(frame_id))
                    .and_then(|frame_id| frames.get(frame_id as usize))
                    .is_some()
            })
        })
    })
}

fn normalized_frame(frame: &Value, platform: &str) -> Value {
    let mut frame = frame.clone();
    if platform != "cocoa" {
        return frame;
    }
    let Some(frame) = frame.as_object_mut() else {
        return frame;
    };
    for field in ["instruction_addr", "addr"] {
        let Some(address) = text(frame.get(field)) else {
            continue;
        };
        let Some(address) = address
            .strip_prefix("0x")
            .and_then(|address| u64::from_str_radix(address, 16).ok())
        else {
            continue;
        };
        frame.insert(
            field.to_owned(),
            json!(format!("0x{:x}", address & COCOA_POINTER_AUTH_MASK)),
        );
    }
    Value::Object(frame.clone())
}

fn profile_duration(samples: &[ProfileSample]) -> u64 {
    let Some(first) = samples.first() else {
        return 0;
    };
    samples
        .last()
        .map(|last| {
            last.sample_time_unix_nano
                .saturating_sub(first.sample_time_unix_nano)
        })
        .unwrap_or_default()
}

struct ProfileSample {
    index: usize,
    profile_id: String,
    profiler_id: String,
    trace_id: String,
    span_id: String,
    platform: String,
    thread_id: String,
    thread_name: String,
    sample_time_unix_nano: u64,
    stack: Vec<Value>,
}

struct ProfileTransaction {
    start_nano: u64,
    end_nano: u64,
    trace_id: String,
    span_id: String,
}

fn transactions(payload: &Value) -> Vec<ProfileTransaction> {
    payload
        .get("transactions")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|transaction| {
            let transaction = transaction.as_object()?;
            let trace_id = text(transaction.get("trace_id")).map(normalize_id)?;
            if trace_id.len() != 32 {
                return None;
            }
            Some(ProfileTransaction {
                start_nano: integer(transaction.get("relative_start_ns"))?,
                end_nano: integer(transaction.get("relative_end_ns"))?,
                trace_id,
                span_id: text(transaction.get("id"))
                    .map(normalize_id)
                    .unwrap_or_default(),
            })
        })
        .collect()
}

fn transaction_trace(payload: &Value) -> Option<(String, String)> {
    if let Some(trace_id) = text(payload.get("trace_id")).map(normalize_id)
        && trace_id.len() == 32
        && trace_id.bytes().all(|byte| byte.is_ascii_hexdigit())
    {
        return Some((trace_id, String::new()));
    }
    let transaction = payload.get("transaction")?.as_object()?;
    let trace_id = text(transaction.get("trace_id")).map(normalize_id)?;
    if trace_id.len() != 32 || !trace_id.bytes().all(|byte| byte.is_ascii_hexdigit()) {
        return None;
    }
    // Sentry's v1 `transaction.id` is the transaction event identifier, not a
    // span identifier. Keep the span empty and associate the profile by trace.
    Some((trace_id, String::new()))
}

fn transaction_at(
    transactions: &[ProfileTransaction],
    elapsed: u64,
) -> Option<&ProfileTransaction> {
    transactions
        .iter()
        .find(|transaction| elapsed >= transaction.start_nano && elapsed <= transaction.end_nano)
}

fn thread_names(profile: &serde_json::Map<String, Value>) -> HashMap<String, String> {
    profile
        .get("thread_metadata")
        .and_then(Value::as_object)
        .into_iter()
        .flat_map(|metadata| metadata.iter())
        .map(|(thread_id, metadata)| {
            let name = metadata
                .as_object()
                .and_then(|metadata| text(metadata.get("name")))
                .unwrap_or_default();
            (thread_id.clone(), name.to_owned())
        })
        .collect()
}

fn sample_durations(samples: &[ProfileSample]) -> Vec<u64> {
    let mut by_thread: HashMap<&str, Vec<(usize, u64)>> = HashMap::new();
    for (index, sample) in samples.iter().enumerate() {
        by_thread
            .entry(&sample.thread_id)
            .or_default()
            .push((index, sample.sample_time_unix_nano));
    }
    let mut durations = vec![DEFAULT_SAMPLE_DURATION_NANO; samples.len()];
    for thread in by_thread.values_mut() {
        thread.sort_unstable_by_key(|(_, timestamp)| *timestamp);
        let mut previous_duration = DEFAULT_SAMPLE_DURATION_NANO;
        for pair in thread.windows(2) {
            let [(index, timestamp), (_, next_timestamp)] = pair else {
                continue;
            };
            let duration = next_timestamp.saturating_sub(*timestamp).max(1);
            durations[*index] = duration;
            previous_duration = duration;
        }
        if let Some((last_index, _)) = thread.last() {
            durations[*last_index] = previous_duration;
        }
    }
    durations
}

fn timestamp_nano(value: Option<&Value>) -> Option<u64> {
    match value? {
        Value::Number(value) => {
            let seconds = value.as_f64()?;
            if !seconds.is_finite() || seconds < 0.0 {
                return None;
            }
            Some((seconds * 1_000_000_000.0).round() as u64)
        }
        Value::String(value) => Timestamp::from_str(value)
            .ok()
            .and_then(|timestamp| u64::try_from(timestamp.as_nanosecond()).ok()),
        _ => None,
    }
}

fn integer(value: Option<&Value>) -> Option<u64> {
    match value? {
        Value::Number(value) => value.as_u64(),
        Value::String(value) => value.parse().ok(),
        _ => None,
    }
}

fn text(value: Option<&Value>) -> Option<&str> {
    value
        .and_then(Value::as_str)
        .filter(|value| !value.is_empty())
}

fn normalize_id(value: &str) -> String {
    value.replace('-', "").to_ascii_lowercase()
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use android_trace_log::chrono::{TimeZone, Utc};
    use android_trace_log::{Action, AndroidTraceLog, Clock, Event, Method, Thread, Time, Vm};
    use base64::Engine;
    use base64::engine::general_purpose::STANDARD_NO_PAD;
    use serde_json::json;

    use super::sentry_profile_rows;

    #[test]
    fn indexes_v1_samples_and_associates_transaction_trace() {
        let rows = sentry_profile_rows(
            "service-1",
            &json!({
                "event_id": "41fed0925670468bb0457f61a74688ec",
                "platform": "python",
                "timestamp": "2026-08-15T10:00:00Z",
                "transactions": [{
                    "id": "30976f2ddbe04ac9",
                    "trace_id": "4b25bc58f14243d8b208d1e22a054164",
                    "relative_start_ns": "0",
                    "relative_end_ns": "30000000"
                }],
                "version": "1",
                "profile": {
                    "frames": [{"function": "leaf"}, {"function": "root"}],
                    "samples": [
                        {"elapsed_since_start_ns": "10000000", "stack_id": 0, "thread_id": "main"},
                        {"elapsed_since_start_ns": "20000000", "stack_id": 0, "thread_id": "main"}
                    ],
                    "stacks": [[0, 1]],
                    "thread_metadata": {"main": {"name": "MainThread"}}
                }
            }),
            99,
        );

        assert_eq!(rows.len(), 2);
        assert_eq!(rows[0]["trace_id"], "4b25bc58f14243d8b208d1e22a054164");
        assert_eq!(rows[0]["span_id"], "30976f2ddbe04ac9");
        assert_eq!(rows[0]["thread_name"], "MainThread");
        assert_eq!(rows[0]["duration_nano"], 10_000_000);
        assert_eq!(
            rows[0]["stack"],
            r#"[{"function":"root"},{"function":"leaf"}]"#
        );
    }

    #[test]
    fn indexes_v2_continuous_profile_by_profiler_id() {
        let rows = sentry_profile_rows(
            "service-1",
            &json!({
                "chunk_id": "0432a0a4c25f4697bf9f0a2fcbe6a814",
                "platform": "cocoa",
                "profiler_id": "4d229f1d3807421ba62a5f8bc295d836",
                "timestamp": 1_710_805_688.237,
                "version": "2",
                "profile": {
                    "frames": [{"instruction_addr": "0x1"}],
                    "samples": [
                        {"stack_id": 0, "thread_id": "main", "timestamp": 1_710_805_688.5},
                        {"stack_id": 0, "thread_id": "main", "timestamp": 1_710_805_688.51}
                    ],
                    "stacks": [[0]]
                }
            }),
            100,
        );

        assert_eq!(rows.len(), 2);
        assert_eq!(rows[0]["profiler_id"], "4d229f1d3807421ba62a5f8bc295d836");
        assert_eq!(rows[0]["trace_id"], "");
        assert_eq!(
            rows[0]["sample_time_unix_nano"],
            1_710_805_688_500_000_000_u64
        );
    }

    #[test]
    fn associates_node_v1_profile_with_its_single_transaction() {
        let rows = sentry_profile_rows(
            "service-1",
            &json!({
                "event_id": "41fed0925670468bb0457f61a74688ec",
                "platform": "node",
                "timestamp": "2026-08-15T10:00:00Z",
                "transaction": {
                    "id": "50f1e97837214d3c9c1cdbdaf2fbbbc5",
                    "trace_id": "4b25bc58f14243d8b208d1e22a054164"
                },
                "version": "1",
                "profile": {
                    "frames": [{"function": "handler"}],
                    "samples": [
                        {
                            "elapsed_since_start_ns": "10000000",
                            "stack_id": 0,
                            "thread_id": "0"
                        },
                        {
                            "elapsed_since_start_ns": "20000000",
                            "stack_id": 0,
                            "thread_id": "0"
                        }
                    ],
                    "stacks": [[0]]
                }
            }),
            99,
        );

        assert_eq!(rows.len(), 2);
        assert_eq!(rows[0]["trace_id"], "4b25bc58f14243d8b208d1e22a054164");
        assert_eq!(rows[0]["span_id"], "");
    }

    #[test]
    fn indexes_android_method_trace_and_react_native_javascript_profile() {
        let start = Utc.timestamp_opt(1_800_000_000, 0).unwrap();
        let mut trace = AndroidTraceLog {
            data_file_overflow: false,
            clock: Clock::Global,
            elapsed_time: Duration::from_millis(20),
            total_method_calls: 2,
            clock_call_overhead: Duration::ZERO,
            vm: Vm::Art,
            start_time: start,
            pid: Some(42),
            gc_trace: None,
            threads: vec![Thread {
                id: 1,
                name: "main".into(),
            }],
            methods: vec![Method {
                id: 1,
                name: "render".into(),
                class_name: "com.example.Screen".into(),
                signature: "()V".into(),
                source_file: "Screen.kt".into(),
                source_line: Some(42),
            }],
            events: vec![
                Event {
                    action: Action::Enter,
                    thread_id: 1,
                    method_id: 1,
                    time: Time::Global(Duration::from_millis(1)),
                },
                Event {
                    action: Action::Enter,
                    thread_id: 1,
                    method_id: 1,
                    time: Time::Global(Duration::from_millis(2)),
                },
                Event {
                    action: Action::Exit,
                    thread_id: 1,
                    method_id: 1,
                    time: Time::Global(Duration::from_millis(3)),
                },
            ],
        };
        let mut bytes = Vec::new();
        trace.serialize_into(&mut bytes).unwrap();
        trace.events.clear();
        let rows = sentry_profile_rows(
            "service-1",
            &json!({
                "platform": "android",
                "profile_id": "41fed0925670468bb0457f61a74688ec",
                "sampled_profile": STANDARD_NO_PAD.encode(bytes),
                "trace_id": "4b25bc58f14243d8b208d1e22a054164",
                "version": "1",
                "js_profile": {
                    "frames": [{"function": "renderJS"}],
                    "samples": [
                        {"elapsed_since_start_ns": "1000000", "stack_id": 0, "thread_id": "js"},
                        {"elapsed_since_start_ns": "2000000", "stack_id": 0, "thread_id": "js"}
                    ],
                    "stacks": [[0]]
                },
                "timestamp": 1_800_000_000.0
            }),
            99,
        );

        assert!(rows.iter().any(|row| row["thread_name"] == "main"));
        assert!(rows.iter().any(|row| row["thread_id"] == "js"));
        assert!(
            rows.iter()
                .all(|row| row["trace_id"] == "4b25bc58f14243d8b208d1e22a054164")
        );
    }
}
