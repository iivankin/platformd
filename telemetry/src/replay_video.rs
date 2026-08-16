use serde::Deserialize;
use serde_json::Value;

use crate::error::{Error, Result};

const MAX_HEADER_BYTES: usize = 64 << 10;

#[derive(Deserialize)]
struct WireReplayVideo {
    #[serde(with = "serde_bytes")]
    replay_event: Vec<u8>,
    #[serde(with = "serde_bytes")]
    replay_recording: Vec<u8>,
    #[serde(with = "serde_bytes")]
    replay_video: Vec<u8>,
}

pub(crate) struct ReplayVideo {
    pub(crate) event: Value,
    pub(crate) recording: Vec<u8>,
    pub(crate) replay_id: Option<String>,
    pub(crate) segment_id: Option<u64>,
    pub(crate) video: Vec<u8>,
}

pub(crate) fn decode(input: &[u8]) -> Result<ReplayVideo> {
    let wire: WireReplayVideo = rmp_serde::from_slice(input)
        .map_err(|error| Error::InvalidRequest(format!("invalid replay video: {error}")))?;
    let event: Value = serde_json::from_slice(&wire.replay_event)
        .map_err(|error| Error::InvalidRequest(format!("invalid replay video event: {error}")))?;
    let replay_id = event
        .get("replay_id")
        .or_else(|| event.get("event_id"))
        .and_then(Value::as_str)
        .and_then(normalize_id);
    let segment_id = recording_header(&wire.replay_recording)
        .and_then(|header| header.get("segment_id").cloned())
        .and_then(|value| match value {
            Value::Number(value) => value.as_u64(),
            Value::String(value) => value.parse().ok(),
            _ => None,
        });
    Ok(ReplayVideo {
        event,
        recording: wire.replay_recording,
        replay_id,
        segment_id,
        video: wire.replay_video,
    })
}

fn recording_header(input: &[u8]) -> Option<Value> {
    let newline = input.iter().position(|byte| *byte == b'\n')?;
    if newline > MAX_HEADER_BYTES {
        return None;
    }
    serde_json::from_slice(&input[..newline]).ok()
}

fn normalize_id(value: &str) -> Option<String> {
    let value = value.replace('-', "").to_ascii_lowercase();
    (value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit())).then_some(value)
}

#[cfg(test)]
mod tests {
    use serde::Serialize;

    use super::*;

    #[derive(Serialize)]
    struct TestWire<'a> {
        #[serde(with = "serde_bytes")]
        replay_event: &'a [u8],
        #[serde(with = "serde_bytes")]
        replay_recording: &'a [u8],
        #[serde(with = "serde_bytes")]
        replay_video: &'a [u8],
    }

    #[test]
    fn decodes_relay_mobile_replay_container() {
        let input = rmp_serde::to_vec_named(&TestWire {
            replay_event: br#"{"replay_id":"515539018c9b4260a6f999572f1661ee"}"#,
            replay_recording: b"{\"segment_id\":7}\nrecording",
            replay_video: b"video",
        })
        .unwrap();
        let replay = decode(&input).unwrap();
        assert_eq!(
            replay.replay_id.as_deref(),
            Some("515539018c9b4260a6f999572f1661ee")
        );
        assert_eq!(replay.segment_id, Some(7));
        assert_eq!(replay.video, b"video");
    }
}
