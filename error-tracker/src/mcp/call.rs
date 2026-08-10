use std::sync::Arc;

use axum::Json;
use axum::extract::{Extension, Path, Query, State};
use serde::Deserialize;
use serde_json::Value;

use crate::access::AccessIdentity;
use crate::config::{CreateApp, CreateWebhook, UpdateWebhook, WebhookEvent};
use crate::error::{Error, Result};
use crate::server::{self, IssueStatusInput, ListQuery, ServerState};

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ToolCall {
    name: String,
    #[serde(default)]
    arguments: Value,
    #[serde(default, rename = "_meta")]
    _meta: Option<serde_json::Map<String, Value>>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct AppArgs {
    app_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ListArgs {
    app_id: String,
    #[serde(default)]
    query: Option<String>,
    #[serde(default = "default_limit")]
    limit: usize,
    #[serde(default)]
    offset: usize,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct IssueArgs {
    app_id: String,
    issue_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct EventArgs {
    app_id: String,
    event_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct ReplayArgs {
    app_id: String,
    replay_id: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct IssueStatusArgs {
    app_id: String,
    issue_id: String,
    status: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct WebhookCreateArgs {
    app_id: String,
    url: String,
    events: Vec<WebhookEvent>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct WebhookUpdateArgs {
    app_id: String,
    webhook_id: String,
    url: String,
    events: Vec<WebhookEvent>,
    enabled: bool,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct WebhookDeleteArgs {
    app_id: String,
    webhook_id: String,
}

pub async fn execute(
    state: Arc<ServerState>,
    identity: AccessIdentity,
    params: Option<Value>,
) -> Result<Value> {
    let call: ToolCall = serde_json::from_value(params.unwrap_or(Value::Null))
        .map_err(|error| Error::InvalidRequest(format!("invalid MCP tool call: {error}")))?;
    match call.name.as_str() {
        "list_apps" => value(server::apps(State(state), Extension(identity)).await?.0),
        "get_app" => {
            let args: AppArgs = arguments(call.arguments)?;
            value(
                server::app(State(state), Path(args.app_id), Extension(identity))
                    .await?
                    .0,
            )
        }
        "list_issues" => {
            let args: ListArgs = list_arguments(call.arguments)?;
            let query = list_query(&args);
            value(
                server::issues(
                    State(state),
                    Path(args.app_id),
                    Extension(identity),
                    Query(query),
                )
                .await?
                .0,
            )
        }
        "get_issue" => {
            let args: IssueArgs = arguments(call.arguments)?;
            value(
                server::issue(
                    State(state),
                    Path((args.app_id, args.issue_id)),
                    Extension(identity),
                )
                .await?
                .0,
            )
        }
        "list_events" => {
            let args: ListArgs = list_arguments(call.arguments)?;
            let query = list_query(&args);
            value(
                server::events(
                    State(state),
                    Path(args.app_id),
                    Extension(identity),
                    Query(query),
                )
                .await?
                .0,
            )
        }
        "get_event" => {
            let args: EventArgs = arguments(call.arguments)?;
            value(
                server::event(
                    State(state),
                    Path((args.app_id, args.event_id)),
                    Extension(identity),
                )
                .await?
                .0,
            )
        }
        "list_replays" => {
            let args: ListArgs = list_arguments(call.arguments)?;
            let query = list_query(&args);
            value(
                server::replays(
                    State(state),
                    Path(args.app_id),
                    Extension(identity),
                    Query(query),
                )
                .await?
                .0,
            )
        }
        "get_replay" => {
            let args: ReplayArgs = arguments(call.arguments)?;
            value(
                server::replay(
                    State(state),
                    Path((args.app_id, args.replay_id)),
                    Extension(identity),
                )
                .await?
                .0,
            )
        }
        "list_artifacts" => {
            let args: ListArgs = list_arguments(call.arguments)?;
            let query = list_query(&args);
            value(
                server::artifacts(
                    State(state),
                    Path(args.app_id),
                    Extension(identity),
                    Query(query),
                )
                .await?
                .0,
            )
        }
        "update_issue_status" => {
            let args: IssueStatusArgs = arguments(call.arguments)?;
            value(
                server::update_issue(
                    State(state),
                    Path((args.app_id, args.issue_id)),
                    Extension(identity),
                    Json(IssueStatusInput {
                        status: args.status,
                    }),
                )
                .await?
                .0,
            )
        }
        "create_webhook" => {
            let args: WebhookCreateArgs = arguments(call.arguments)?;
            value(
                server::create_webhook(
                    State(state),
                    Path(args.app_id),
                    Extension(identity),
                    Json(CreateWebhook {
                        url: args.url,
                        events: args.events,
                    }),
                )
                .await?
                .1
                .0,
            )
        }
        "update_webhook" => {
            let args: WebhookUpdateArgs = arguments(call.arguments)?;
            value(
                server::update_webhook(
                    State(state),
                    Path((args.app_id, args.webhook_id)),
                    Extension(identity),
                    Json(UpdateWebhook {
                        url: args.url,
                        events: args.events,
                        enabled: args.enabled,
                    }),
                )
                .await?
                .0,
            )
        }
        "delete_webhook" => {
            let args: WebhookDeleteArgs = arguments(call.arguments)?;
            let webhook_id = args.webhook_id.clone();
            server::delete_webhook(
                State(state),
                Path((args.app_id, args.webhook_id)),
                Extension(identity),
            )
            .await?;
            Ok(serde_json::json!({ "deleted": webhook_id }))
        }
        "create_app" => {
            let input: CreateApp = arguments(call.arguments)?;
            value(
                server::create_app(State(state), Extension(identity), Json(input))
                    .await?
                    .1
                    .0,
            )
        }
        _ => Err(Error::InvalidRequest("unknown MCP tool".into())),
    }
}

fn arguments<T: for<'de> Deserialize<'de>>(value: Value) -> Result<T> {
    serde_json::from_value(value)
        .map_err(|error| Error::InvalidRequest(format!("invalid MCP tool arguments: {error}")))
}

fn list_arguments(value: Value) -> Result<ListArgs> {
    let args: ListArgs = arguments(value)?;
    if args.limit == 0 || args.limit > 100 {
        return Err(Error::InvalidRequest(
            "MCP list limit must be between 1 and 100".into(),
        ));
    }
    Ok(args)
}

fn list_query(args: &ListArgs) -> ListQuery {
    ListQuery {
        limit: args.limit,
        offset: args.offset,
        query: args.query.clone(),
    }
}

fn value<T: serde::Serialize>(value: T) -> Result<Value> {
    serde_json::to_value(value)
        .map_err(|error| Error::Storage(format!("serialize MCP tool result: {error}")))
}

const fn default_limit() -> usize {
    100
}

#[cfg(test)]
mod tests {
    use serde_json::json;

    use super::*;

    #[test]
    fn accepts_standard_tool_call_metadata() {
        let call: ToolCall = serde_json::from_value(json!({
            "name": "list_apps",
            "arguments": {},
            "_meta": { "progressToken": "progress-1" }
        }))
        .unwrap();

        assert_eq!(call.name, "list_apps");
        assert!(call._meta.is_some());
    }
}
