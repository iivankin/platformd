mod call;
mod tools;

use axum::Json;
use axum::body::Bytes;
use axum::extract::{Extension, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use serde::Deserialize;
use serde_json::{Value, json};
use std::sync::Arc;
use url::Url;

use crate::access::AccessIdentity;
use crate::server::ServerState;

const PROTOCOL_VERSION: &str = "2025-11-25";
const PREVIOUS_PROTOCOL_VERSION: &str = "2025-06-18";
pub(crate) const MAX_BODY_BYTES: usize = 1 << 20;

#[derive(Deserialize)]
struct RpcRequest {
    jsonrpc: String,
    id: Option<Value>,
    method: String,
    #[serde(default)]
    params: Option<Value>,
}

pub async fn handle(
    State(state): State<Arc<ServerState>>,
    Extension(identity): Extension<AccessIdentity>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if !valid_origin(&headers, &state).await {
        return StatusCode::FORBIDDEN.into_response();
    }
    if !accepts_mcp(&headers) {
        return StatusCode::NOT_ACCEPTABLE.into_response();
    }
    if !is_json(&headers) {
        return StatusCode::UNSUPPORTED_MEDIA_TYPE.into_response();
    }
    if body.len() > MAX_BODY_BYTES {
        return StatusCode::PAYLOAD_TOO_LARGE.into_response();
    }
    let Ok(raw) = serde_json::from_slice::<Value>(&body) else {
        return rpc_error(Value::Null, -32600, "Invalid Request");
    };
    let Some(object) = raw.as_object() else {
        return rpc_error(Value::Null, -32600, "Invalid Request");
    };
    let response_id = object
        .get("id")
        .filter(|id| valid_request_id(id))
        .cloned()
        .unwrap_or(Value::Null);
    if object.get("id").is_some_and(|id| !valid_request_id(id))
        || object
            .get("params")
            .is_some_and(|params| !params.is_object())
    {
        return rpc_error(response_id, -32600, "Invalid Request");
    }
    let Ok(message) = serde_json::from_value::<RpcRequest>(raw) else {
        return rpc_error(response_id, -32600, "Invalid Request");
    };
    if message.jsonrpc != "2.0" || message.method.is_empty() {
        return rpc_error(message.id.unwrap_or(Value::Null), -32600, "Invalid Request");
    }
    if !valid_protocol_header(&headers, message.method == "initialize") {
        return (
            StatusCode::BAD_REQUEST,
            "Missing or unsupported MCP-Protocol-Version",
        )
            .into_response();
    }

    match message.method.as_str() {
        "initialize" => initialize(message),
        "notifications/initialized" => {
            if message.id.is_some() {
                rpc_error(Value::Null, -32600, "Initialized must be a notification")
            } else {
                StatusCode::ACCEPTED.into_response()
            }
        }
        "ping" => match message.id {
            Some(id) => rpc_result(id, json!({})),
            None => invalid_notification(),
        },
        "tools/list" => match message.id {
            Some(id) if valid_list_params(message.params.as_ref()) => {
                rpc_result(id, json!({ "tools": tools::list(&identity) }))
            }
            Some(id) => rpc_error(id, -32602, "tools/list cursor is not supported"),
            None => invalid_notification(),
        },
        "tools/call" => match message.id {
            Some(id) => match call::execute(state, identity, message.params).await {
                Ok(output) => tool_result(id, output, false),
                Err(error) => tool_result(id, json!({ "error": error.to_string() }), true),
            },
            None => invalid_notification(),
        },
        _ if message.id.is_some() => rpc_error(
            message.id.unwrap_or(Value::Null),
            -32601,
            "Method not found",
        ),
        _ => invalid_notification(),
    }
}

fn initialize(message: RpcRequest) -> Response {
    let Some(id) = message.id else {
        return invalid_notification();
    };
    let Some(params) = message.params else {
        return rpc_error(id, -32602, "Invalid initialize params");
    };
    let protocol = params.get("protocolVersion").and_then(Value::as_str);
    let client_name = params.pointer("/clientInfo/name").and_then(Value::as_str);
    let client_version = params
        .pointer("/clientInfo/version")
        .and_then(Value::as_str);
    if protocol.is_none()
        || client_name.is_none()
        || client_version.is_none()
        || !params.get("capabilities").is_some_and(Value::is_object)
    {
        return rpc_error(id, -32602, "Invalid initialize params");
    }
    let negotiated = protocol
        .filter(|version| supported_protocol(version))
        .unwrap_or(PROTOCOL_VERSION);
    rpc_result(
        id,
        json!({
            "protocolVersion": negotiated,
            "capabilities": { "tools": {} },
            "serverInfo": {
                "name": "error-tracker",
                "version": env!("CARGO_PKG_VERSION")
            }
        }),
    )
}

fn invalid_notification() -> Response {
    StatusCode::BAD_REQUEST.into_response()
}

fn rpc_result(id: Value, result: Value) -> Response {
    (
        StatusCode::OK,
        Json(json!({ "jsonrpc": "2.0", "id": id, "result": result })),
    )
        .into_response()
}

fn rpc_error(id: Value, code: i64, message: &str) -> Response {
    (
        StatusCode::OK,
        Json(json!({
            "jsonrpc": "2.0",
            "id": id,
            "error": { "code": code, "message": message }
        })),
    )
        .into_response()
}

fn tool_result(id: Value, output: Value, is_error: bool) -> Response {
    let text = serde_json::to_string_pretty(&output).unwrap_or_else(|_| "null".into());
    rpc_result(
        id,
        json!({
            "content": [{ "type": "text", "text": text }],
            "structuredContent": output,
            "isError": is_error
        }),
    )
}

fn valid_list_params(params: Option<&Value>) -> bool {
    params.is_none_or(|params| {
        params.as_object().is_some_and(|object| {
            object
                .iter()
                .all(|(key, value)| key == "_meta" && value.is_object())
        })
    })
}

fn valid_request_id(id: &Value) -> bool {
    id.is_string() || id.as_i64().is_some() || id.as_u64().is_some()
}

fn valid_protocol_header(headers: &HeaderMap, initialize: bool) -> bool {
    let values = headers.get_all("mcp-protocol-version");
    let mut values = values.iter();
    let first = values.next().and_then(|value| value.to_str().ok());
    if values.next().is_some() {
        return false;
    }
    match first {
        Some(version) => supported_protocol(version),
        None => initialize,
    }
}

fn supported_protocol(version: &str) -> bool {
    matches!(version, PROTOCOL_VERSION | PREVIOUS_PROTOCOL_VERSION)
}

fn accepts_mcp(headers: &HeaderMap) -> bool {
    let values = headers.get_all(header::ACCEPT);
    let mut json = false;
    let mut event_stream = false;
    for value in values.iter().filter_map(|value| value.to_str().ok()) {
        for part in value.split(',') {
            json |= accepts_media_type(part, "application/json");
            event_stream |= accepts_media_type(part, "text/event-stream");
        }
    }
    json && event_stream
}

fn accepts_media_type(value: &str, expected: &str) -> bool {
    let mut parts = value.split(';');
    if !parts
        .next()
        .is_some_and(|value| value.trim().eq_ignore_ascii_case(expected))
    {
        return false;
    }
    parts
        .filter_map(|parameter| parameter.split_once('='))
        .find(|(name, _)| name.trim().eq_ignore_ascii_case("q"))
        .map(|(_, quality)| {
            quality
                .trim()
                .parse::<f32>()
                .is_ok_and(|quality| quality > 0.0 && quality <= 1.0)
        })
        .unwrap_or(true)
}

fn is_json(headers: &HeaderMap) -> bool {
    headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .is_some_and(|value| value.trim().eq_ignore_ascii_case("application/json"))
}

async fn valid_origin(headers: &HeaderMap, state: &ServerState) -> bool {
    let origins = headers.get_all(header::ORIGIN);
    let mut origins = origins.iter();
    let Some(origin) = origins.next() else {
        return true;
    };
    if origins.next().is_some() {
        return false;
    }
    let Ok(origin) = origin.to_str() else {
        return false;
    };
    Url::parse(state.config.public_url())
        .map(|url| url.origin().ascii_serialization() == origin)
        .unwrap_or(false)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn validates_mcp_request_ids_and_metadata() {
        assert!(valid_request_id(&json!("request-1")));
        assert!(valid_request_id(&json!(1)));
        assert!(!valid_request_id(&Value::Null));
        assert!(!valid_request_id(&json!(1.5)));
        assert!(!valid_request_id(&json!([1])));

        assert!(valid_list_params(Some(&json!({ "_meta": {} }))));
        assert!(!valid_list_params(Some(&json!({ "cursor": null }))));
        assert!(!valid_list_params(Some(&json!({ "_meta": "invalid" }))));
    }

    #[test]
    fn accept_header_requires_both_usable_mcp_media_types() {
        let mut headers = HeaderMap::new();
        headers.insert(
            header::ACCEPT,
            "Application/JSON, Text/Event-Stream; q=0.5"
                .parse()
                .unwrap(),
        );
        assert!(accepts_mcp(&headers));

        headers.insert(
            header::ACCEPT,
            "application/json, text/event-stream; q=0".parse().unwrap(),
        );
        assert!(!accepts_mcp(&headers));
    }
}
