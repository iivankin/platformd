use serde_json::{Map, Value};

use crate::error::{Error, Result};

const MAX_HEADER_BYTES: usize = 64 << 10;
const MAX_ITEMS: usize = 256;

#[derive(Clone, Debug)]
pub struct Envelope {
    pub headers: Map<String, Value>,
    pub items: Vec<Item>,
}

#[derive(Clone, Debug)]
pub struct Item {
    pub headers: Map<String, Value>,
    pub item_type: String,
    pub payload: Vec<u8>,
}

pub fn parse(input: &[u8]) -> Result<Envelope> {
    let (header_line, mut cursor) = line(input, 0, true)?;
    let headers = header(header_line, "envelope")?;
    let mut items = Vec::new();

    while cursor < input.len() {
        if items.len() == MAX_ITEMS {
            return Err(Error::InvalidRequest("envelope has too many items".into()));
        }
        let (item_header_line, after_header) = line(input, cursor, false)?;
        cursor = after_header;
        let item_headers = header(item_header_line, "item")?;
        let item_type = item_headers
            .get("type")
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty())
            .ok_or_else(|| Error::InvalidRequest("envelope item type is missing".into()))?
            .to_owned();

        let payload_end = match item_headers.get("length") {
            Some(Value::Number(length)) => {
                let length = length
                    .as_u64()
                    .and_then(|value| usize::try_from(value).ok())
                    .ok_or_else(|| {
                        Error::InvalidRequest("envelope item length is invalid".into())
                    })?;
                cursor
                    .checked_add(length)
                    .filter(|end| *end <= input.len())
                    .ok_or_else(|| Error::InvalidRequest("envelope item is truncated".into()))?
            }
            Some(_) => {
                return Err(Error::InvalidRequest(
                    "envelope item length must be an integer".into(),
                ));
            }
            None => input[cursor..]
                .iter()
                .position(|byte| *byte == b'\n')
                .map_or(input.len(), |offset| cursor + offset),
        };
        let payload = input[cursor..payload_end].to_vec();
        cursor = payload_end;
        if cursor < input.len() {
            if input[cursor] != b'\n' {
                return Err(Error::InvalidRequest(
                    "envelope item payload is not newline terminated".into(),
                ));
            }
            cursor += 1;
        }
        items.push(Item {
            headers: item_headers,
            item_type,
            payload,
        });
    }

    Ok(Envelope { headers, items })
}

fn line(input: &[u8], start: usize, allow_eof: bool) -> Result<(&[u8], usize)> {
    let remainder = input
        .get(start..)
        .ok_or_else(|| Error::InvalidRequest("envelope cursor is invalid".into()))?;
    if let Some(offset) = remainder.iter().position(|byte| *byte == b'\n') {
        if offset > MAX_HEADER_BYTES {
            return Err(Error::InvalidRequest("envelope header is too large".into()));
        }
        return Ok((&remainder[..offset], start + offset + 1));
    }
    if allow_eof && remainder.len() <= MAX_HEADER_BYTES {
        return Ok((remainder, input.len()));
    }
    Err(Error::InvalidRequest(
        "envelope header is not newline terminated".into(),
    ))
}

fn header(input: &[u8], kind: &str) -> Result<Map<String, Value>> {
    if input.contains(&b'\r') {
        return Err(Error::InvalidRequest(format!(
            "{kind} header contains a carriage return"
        )));
    }
    let value: Value = serde_json::from_slice(input)
        .map_err(|error| Error::InvalidRequest(format!("invalid {kind} header: {error}")))?;
    value
        .as_object()
        .cloned()
        .ok_or_else(|| Error::InvalidRequest(format!("{kind} header must be an object")))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_binary_and_unknown_items_without_losing_bytes() {
        let input = b"{\"event_id\":\"0123456789abcdef0123456789abcdef\"}\n{\"type\":\"replay_recording\",\"length\":5}\na\n\0bc\n{\"type\":\"future_type\"}\nvalue";
        let envelope = parse(input).unwrap();
        assert_eq!(envelope.items.len(), 2);
        assert_eq!(envelope.items[0].item_type, "replay_recording");
        assert_eq!(envelope.items[0].payload, b"a\n\0bc");
        assert_eq!(envelope.items[1].item_type, "future_type");
        assert_eq!(envelope.items[1].payload, b"value");
    }

    #[test]
    fn rejects_truncated_length_prefixed_item() {
        let error = parse(b"{}\n{\"type\":\"event\",\"length\":5}\n{}")
            .unwrap_err()
            .to_string();
        assert!(error.contains("truncated"));
    }
}
