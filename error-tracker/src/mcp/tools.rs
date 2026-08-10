use serde::Serialize;
use serde_json::{Value, json};

use crate::access::AccessIdentity;

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Tool {
    name: &'static str,
    description: &'static str,
    input_schema: Value,
}

pub fn list(identity: &AccessIdentity) -> Vec<Tool> {
    let app = json!({
        "appId": { "type": "string", "description": "Exact application ID" }
    });
    let mut tools = vec![
        tool(
            "list_apps",
            "List applications visible to this token.",
            object(json!({}), &[]),
        ),
        tool(
            "get_app",
            "Read one visible application and its webhook metadata.",
            object(app.clone(), &["appId"]),
        ),
        tool(
            "list_issues",
            "Search and list issues for one application.",
            list_schema(),
        ),
        tool(
            "get_issue",
            "Read an issue and its recent events.",
            object(
                json!({
                    "appId": { "type": "string" }, "issueId": { "type": "string" }
                }),
                &["appId", "issueId"],
            ),
        ),
        tool(
            "list_events",
            "Search and list error events for one application.",
            list_schema(),
        ),
        tool(
            "get_event",
            "Read one event with symbolication output.",
            object(
                json!({
                    "appId": { "type": "string" }, "eventId": { "type": "string" }
                }),
                &["appId", "eventId"],
            ),
        ),
        tool(
            "list_replays",
            "List replay events for one application.",
            list_schema(),
        ),
        tool(
            "get_replay",
            "Read all stored items for one replay.",
            object(
                json!({
                    "appId": { "type": "string" }, "replayId": { "type": "string" }
                }),
                &["appId", "replayId"],
            ),
        ),
        tool(
            "list_artifacts",
            "Search uploaded source maps and debug artifacts.",
            list_schema(),
        ),
    ];
    if identity.is_admin() {
        tools.extend([
            tool(
                "update_issue_status",
                "Set an issue to open, resolved, or ignored.",
                object(
                    json!({
                        "appId": { "type": "string" }, "issueId": { "type": "string" },
                        "status": { "type": "string", "enum": ["open", "resolved", "ignored"] }
                    }),
                    &["appId", "issueId", "status"],
                ),
            ),
            tool(
                "create_webhook",
                "Create a signed webhook for an application.",
                webhook_create_schema(),
            ),
            tool(
                "update_webhook",
                "Update or disable an application webhook.",
                webhook_update_schema(),
            ),
            tool(
                "delete_webhook",
                "Delete an application webhook.",
                object(
                    json!({
                        "appId": { "type": "string" }, "webhookId": { "type": "string" }
                    }),
                    &["appId", "webhookId"],
                ),
            ),
        ]);
        if identity.app_id().is_none() {
            tools.push(tool(
                "create_app",
                "Create an application and return its one-time upload token.",
                object(
                    json!({
                        "name": { "type": "string", "maxLength": 80 },
                        "slug": { "type": "string", "maxLength": 48 }
                    }),
                    &["name", "slug"],
                ),
            ));
        }
    }
    tools
}

fn tool(name: &'static str, description: &'static str, input_schema: Value) -> Tool {
    Tool {
        name,
        description,
        input_schema,
    }
}

fn object(properties: Value, required: &[&str]) -> Value {
    let mut schema = json!({
        "type": "object",
        "properties": properties,
        "additionalProperties": false
    });
    if !required.is_empty() {
        schema["required"] = json!(required);
    }
    schema
}

fn list_schema() -> Value {
    object(
        json!({
            "appId": { "type": "string" },
            "query": { "type": "string", "maxLength": 256 },
            "limit": { "type": "integer", "minimum": 1, "maximum": 100 },
            "offset": { "type": "integer", "minimum": 0 }
        }),
        &["appId"],
    )
}

fn webhook_events() -> Value {
    json!({
        "type": "array",
        "minItems": 1,
        "uniqueItems": true,
        "items": {
            "type": "string",
            "enum": ["event_received", "issue_created", "issue_regressed", "issue_resolved"]
        }
    })
}

fn webhook_create_schema() -> Value {
    object(
        json!({
            "appId": { "type": "string" },
            "url": { "type": "string", "format": "uri" },
            "events": webhook_events()
        }),
        &["appId", "url", "events"],
    )
}

fn webhook_update_schema() -> Value {
    object(
        json!({
            "appId": { "type": "string" },
            "webhookId": { "type": "string" },
            "url": { "type": "string", "format": "uri" },
            "events": webhook_events(),
            "enabled": { "type": "boolean" }
        }),
        &["appId", "webhookId", "url", "events", "enabled"],
    )
}
