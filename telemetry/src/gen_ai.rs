use opentelemetry_proto::tonic::common::v1::any_value::Value as AttributeValue;
use opentelemetry_proto::tonic::common::v1::{AnyValue, KeyValue};
use opentelemetry_proto::tonic::trace::v1::Span;
use serde_json::Value;

const MAX_SEARCH_BYTES: usize = 64 << 10;

#[derive(Default)]
pub(crate) struct SpanIndex {
    pub kind: String,
    pub operation: String,
    pub provider: String,
    pub model: String,
    pub agent: String,
    pub tool: String,
    pub user_id: String,
    pub session_id: String,
    pub input_tokens: Option<u64>,
    pub output_tokens: Option<u64>,
    pub cache_read_tokens: Option<u64>,
    pub cache_write_tokens: Option<u64>,
    pub reasoning_tokens: Option<u64>,
    pub cost_usd: Option<f64>,
    pub ttft_seconds: Option<f64>,
    pub tokens_per_second: Option<f64>,
    pub search_text: String,
}

pub(crate) fn index_span(span: &Span) -> SpanIndex {
    let attributes = &span.attributes;
    let operation =
        text(attributes, &["gen_ai.operation.name", "ai.operationId"]).unwrap_or_default();
    let provider = text(
        attributes,
        &["gen_ai.provider.name", "gen_ai.system", "ai.model.provider"],
    )
    .unwrap_or_default();
    let model = text(
        attributes,
        &[
            "gen_ai.response.model",
            "gen_ai.request.model",
            "ai.response.model",
            "ai.model.id",
        ],
    )
    .unwrap_or_default();
    let agent = text(
        attributes,
        &["gen_ai.agent.name", "ai.telemetry.functionId"],
    )
    .unwrap_or_default();
    let tool = text(attributes, &["gen_ai.tool.name", "ai.toolCall.name"]).unwrap_or_default();
    let user_id = text(
        attributes,
        &[
            "ai.settings.context.userId",
            "ai.settings.runtimeContext.userId",
            "user.id",
            "enduser.id",
        ],
    )
    .unwrap_or_default();
    let session_id = text(
        attributes,
        &[
            "ai.settings.context.sessionId",
            "ai.settings.context.chatId",
            "ai.settings.runtimeContext.sessionId",
            "ai.settings.runtimeContext.chatId",
            "gen_ai.conversation.id",
            "chat.id",
        ],
    )
    .unwrap_or_default();
    let input_tokens = unsigned(
        attributes,
        &[
            "gen_ai.usage.input_tokens",
            "ai.usage.tokens",
            "ai.usage.inputTokens",
            "ai.usage.promptTokens",
        ],
    );
    let output_tokens = unsigned(
        attributes,
        &[
            "gen_ai.usage.output_tokens",
            "ai.usage.outputTokens",
            "ai.usage.completionTokens",
        ],
    );
    let cache_read_tokens = unsigned(
        attributes,
        &[
            "gen_ai.usage.cache_read.input_tokens",
            "ai.usage.cachedInputTokens",
            "ai.usage.inputTokenDetails.cacheReadTokens",
        ],
    );
    let cache_write_tokens = unsigned(
        attributes,
        &[
            "gen_ai.usage.cache_creation.input_tokens",
            "ai.usage.inputTokenDetails.cacheWriteTokens",
        ],
    );
    let reasoning_tokens = unsigned(
        attributes,
        &[
            "gen_ai.usage.reasoning_tokens",
            "ai.usage.outputTokenDetails.reasoningTokens",
        ],
    );
    let cost_usd = number(
        attributes,
        &[
            "operation.cost",
            "gen_ai.usage.cost",
            "ai.usage.cost",
            "llm.usage.total_cost",
        ],
    )
    .filter(|value| *value >= 0.0);
    let ttft_seconds = number(attributes, &["gen_ai.client.operation.time_to_first_chunk"])
        .or_else(|| {
            number(attributes, &["ai.response.msToFirstChunk"]).map(|value| value / 1_000.0)
        })
        .filter(|value| *value >= 0.0);
    let duration_seconds = number(attributes, &["gen_ai.client.operation.duration"])
        .filter(|value| *value > 0.0)
        .or_else(|| {
            number(attributes, &["ai.response.msToFinish"])
                .map(|value| value / 1_000.0)
                .filter(|value| *value > 0.0)
        })
        .or_else(|| {
            (span.end_time_unix_nano >= span.start_time_unix_nano).then_some(
                span.end_time_unix_nano
                    .saturating_sub(span.start_time_unix_nano) as f64
                    / 1_000_000_000.0,
            )
        });
    let tokens_per_second = number(attributes, &["ai.response.avgOutputTokensPerSecond"])
        .filter(|value| *value >= 0.0)
        .or_else(|| {
            output_tokens
                .zip(duration_seconds)
                .filter(|(_, duration)| *duration > 0.0)
                .map(|(tokens, duration)| tokens as f64 / duration)
        });
    let kind = classify(&operation, &span.name, attributes).to_owned();

    SpanIndex {
        kind,
        operation,
        provider,
        model,
        agent,
        tool,
        user_id,
        session_id,
        input_tokens,
        output_tokens,
        cache_read_tokens,
        cache_write_tokens,
        reasoning_tokens,
        cost_usd,
        ttft_seconds,
        tokens_per_second,
        search_text: search_text(span),
    }
}

fn classify<'a>(operation: &'a str, name: &'a str, attributes: &[KeyValue]) -> &'a str {
    match operation {
        "invoke_agent" => "agent",
        "agent_step" => "step",
        "chat" | "text_completion" => "model",
        "embeddings" => "embedding",
        "rerank" => "rerank",
        "execute_tool" => "tool",
        _ if name == "ai.generateText"
            || name == "ai.streamText"
            || name == "ai.generateObject"
            || name == "ai.streamObject" =>
        {
            "agent"
        }
        _ if name.ends_with(".doEmbed") => "embedding",
        _ if name.ends_with(".doRerank") => "rerank",
        _ if name.ends_with(".doGenerate") || name.ends_with(".doStream") => "model",
        _ if name == "ai.toolCall" || has(attributes, "gen_ai.tool.name") => "tool",
        _ if attributes.iter().any(|attribute| {
            attribute.key.starts_with("gen_ai.") || attribute.key.starts_with("ai.")
        }) =>
        {
            "ai"
        }
        _ => "",
    }
}

fn search_text(span: &Span) -> String {
    let mut output = SearchText::default();
    output.push(&span.name);
    for attribute in &span.attributes {
        if is_searchable(&attribute.key) {
            output.push(&attribute.key);
            if let Some(value) = attribute.value.as_ref() {
                output.push_value(value, None);
            }
        }
        if output.full() {
            break;
        }
    }
    output.value
}

fn is_searchable(key: &str) -> bool {
    let is_context =
        key.starts_with("ai.settings.context.") || key.starts_with("ai.settings.runtimeContext.");
    if is_context && is_sensitive_key(key) {
        return false;
    }
    matches!(
        key,
        "gen_ai.operation.name"
            | "gen_ai.provider.name"
            | "gen_ai.request.model"
            | "gen_ai.response.model"
            | "gen_ai.agent.name"
            | "gen_ai.tool.name"
            | "gen_ai.tool.call.id"
            | "gen_ai.tool.call.arguments"
            | "gen_ai.tool.call.result"
            | "gen_ai.system_instructions"
            | "gen_ai.input.messages"
            | "gen_ai.output.messages"
            | "gen_ai.tool.definitions"
            | "pydantic_ai.all_messages"
            | "ai.operationId"
            | "ai.model.id"
            | "ai.model.provider"
            | "ai.telemetry.functionId"
            | "ai.prompt"
            | "ai.prompt.messages"
            | "ai.prompt.tools"
            | "ai.prompt.toolChoice"
            | "ai.response.text"
            | "ai.response.reasoning"
            | "ai.response.toolCalls"
            | "ai.toolCall.name"
            | "ai.toolCall.id"
            | "ai.toolCall.args"
            | "ai.toolCall.result"
    ) || is_context
}

fn is_sensitive_key(key: &str) -> bool {
    let key = key
        .chars()
        .filter(|character| character.is_ascii_alphanumeric())
        .flat_map(char::to_lowercase)
        .collect::<String>();
    key.contains("secret")
        || key.contains("password")
        || key.contains("passphrase")
        || key.contains("authorization")
        || key.contains("token")
        || key.contains("jwt")
        || key.contains("apikey")
        || key.contains("privatekey")
        || key.contains("credential")
        || key.contains("cookie")
}

#[derive(Default)]
struct SearchText {
    value: String,
}

impl SearchText {
    fn full(&self) -> bool {
        self.value.len() >= MAX_SEARCH_BYTES
    }

    fn push(&mut self, value: &str) {
        if value.is_empty() || self.full() {
            return;
        }
        if !self.value.is_empty() {
            self.value.push(' ');
        }
        let remaining = MAX_SEARCH_BYTES.saturating_sub(self.value.len());
        let end = value
            .char_indices()
            .map(|(index, _)| index)
            .take_while(|index| *index <= remaining)
            .last()
            .unwrap_or_default();
        if value.len() <= remaining {
            self.value.push_str(value);
        } else if end > 0 {
            self.value.push_str(&value[..end]);
        }
    }

    fn push_value(&mut self, value: &AnyValue, key: Option<&str>) {
        if self.full() || key.is_some_and(|key| is_binary_field(key) || is_sensitive_key(key)) {
            return;
        }
        match value.value.as_ref() {
            Some(AttributeValue::StringValue(value)) => {
                if let Ok(json) = serde_json::from_str::<Value>(value) {
                    self.push_json(&json, key);
                } else {
                    self.push(value);
                }
            }
            Some(AttributeValue::BoolValue(value)) => self.push(&value.to_string()),
            Some(AttributeValue::IntValue(value)) => self.push(&value.to_string()),
            Some(AttributeValue::DoubleValue(value)) => self.push(&value.to_string()),
            Some(AttributeValue::ArrayValue(value)) => {
                for value in &value.values {
                    self.push_value(value, key);
                }
            }
            Some(AttributeValue::KvlistValue(value)) => {
                for entry in &value.values {
                    if is_binary_field(&entry.key) || is_sensitive_key(&entry.key) {
                        continue;
                    }
                    self.push(&entry.key);
                    if let Some(value) = entry.value.as_ref() {
                        self.push_value(value, Some(&entry.key));
                    }
                }
            }
            Some(AttributeValue::BytesValue(_))
            | Some(AttributeValue::StringValueStrindex(_))
            | None => {}
        }
    }

    fn push_json(&mut self, value: &Value, key: Option<&str>) {
        if self.full() || key.is_some_and(|key| is_binary_field(key) || is_sensitive_key(key)) {
            return;
        }
        match value {
            Value::Null => {}
            Value::Bool(value) => self.push(&value.to_string()),
            Value::Number(value) => self.push(&value.to_string()),
            Value::String(value) => self.push(value),
            Value::Array(values) => {
                for value in values {
                    self.push_json(value, key);
                }
            }
            Value::Object(values) => {
                let is_blob = values
                    .get("type")
                    .and_then(Value::as_str)
                    .is_some_and(|value| matches!(value, "blob" | "image" | "file"));
                for (key, value) in values {
                    if is_binary_field(key) || is_sensitive_key(key) {
                        continue;
                    }
                    self.push(key);
                    if !(is_blob && matches!(key.as_str(), "content" | "data" | "blob")) {
                        self.push_json(value, Some(key));
                    }
                }
            }
        }
    }
}

fn is_binary_field(key: &str) -> bool {
    matches!(
        key.to_ascii_lowercase().as_str(),
        "embedding" | "embeddings" | "bytes"
    )
}

fn has(attributes: &[KeyValue], key: &str) -> bool {
    attributes.iter().any(|attribute| attribute.key == key)
}

fn text(attributes: &[KeyValue], keys: &[&str]) -> Option<String> {
    keys.iter().find_map(|key| {
        let value = value(attributes, key)?;
        match value.value.as_ref()? {
            AttributeValue::StringValue(value) if !value.trim().is_empty() => Some(value.clone()),
            AttributeValue::IntValue(value) => Some(value.to_string()),
            AttributeValue::BoolValue(value) => Some(value.to_string()),
            AttributeValue::DoubleValue(value) => Some(value.to_string()),
            _ => None,
        }
    })
}

fn unsigned(attributes: &[KeyValue], keys: &[&str]) -> Option<u64> {
    keys.iter().find_map(|key| {
        let value = value(attributes, key)?;
        match value.value.as_ref()? {
            AttributeValue::IntValue(value) => u64::try_from(*value).ok(),
            AttributeValue::DoubleValue(value) if value.is_finite() && *value >= 0.0 => {
                Some(*value as u64)
            }
            AttributeValue::StringValue(value) => value.parse().ok(),
            _ => None,
        }
    })
}

fn number(attributes: &[KeyValue], keys: &[&str]) -> Option<f64> {
    keys.iter().find_map(|key| {
        let value = value(attributes, key)?;
        let value = match value.value.as_ref()? {
            AttributeValue::IntValue(value) => *value as f64,
            AttributeValue::DoubleValue(value) => *value,
            AttributeValue::StringValue(value) => value.parse().ok()?,
            _ => return None,
        };
        value.is_finite().then_some(value)
    })
}

fn value<'a>(attributes: &'a [KeyValue], key: &str) -> Option<&'a AnyValue> {
    attributes
        .iter()
        .find(|attribute| attribute.key == key)?
        .value
        .as_ref()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn attribute(key: &str, value: AttributeValue) -> KeyValue {
        KeyValue {
            key: key.into(),
            value: Some(AnyValue { value: Some(value) }),
            ..Default::default()
        }
    }

    #[test]
    fn indexes_standard_and_ai_sdk_attributes() {
        let span = Span {
            name: "chat gpt-5".into(),
            start_time_unix_nano: 1_000_000_000,
            end_time_unix_nano: 3_000_000_000,
            attributes: vec![
                attribute("gen_ai.operation.name", AttributeValue::StringValue("chat".into())),
                attribute(
                    "gen_ai.provider.name",
                    AttributeValue::StringValue("openai".into()),
                ),
                attribute(
                    "gen_ai.response.model",
                    AttributeValue::StringValue("gpt-5".into()),
                ),
                attribute("gen_ai.usage.input_tokens", AttributeValue::IntValue(240)),
                attribute("gen_ai.usage.output_tokens", AttributeValue::IntValue(60)),
                attribute(
                    "gen_ai.usage.cache_read.input_tokens",
                    AttributeValue::IntValue(180),
                ),
                attribute("operation.cost", AttributeValue::DoubleValue(0.0042)),
                attribute(
                    "gen_ai.input.messages",
                    AttributeValue::StringValue(
                        r#"[{"role":"user","parts":[{"type":"text","content":"find invoice 42"},{"type":"blob","content":"do-not-index-base64"}]}]"#.into(),
                    ),
                ),
            ],
            ..Default::default()
        };

        let index = index_span(&span);
        assert_eq!(index.kind, "model");
        assert_eq!(index.provider, "openai");
        assert_eq!(index.model, "gpt-5");
        assert_eq!(index.input_tokens, Some(240));
        assert_eq!(index.output_tokens, Some(60));
        assert_eq!(index.cache_read_tokens, Some(180));
        assert_eq!(index.cost_usd, Some(0.0042));
        assert_eq!(index.tokens_per_second, Some(30.0));
        assert!(index.search_text.contains("find invoice 42"));
        assert!(!index.search_text.contains("do-not-index-base64"));
    }

    #[test]
    fn empty_text_attributes_do_not_block_fallbacks() {
        let span = Span {
            attributes: vec![
                attribute(
                    "gen_ai.provider.name",
                    AttributeValue::StringValue(" ".into()),
                ),
                attribute(
                    "gen_ai.system",
                    AttributeValue::StringValue("openai".into()),
                ),
                attribute(
                    "gen_ai.response.model",
                    AttributeValue::StringValue("".into()),
                ),
                attribute(
                    "gen_ai.request.model",
                    AttributeValue::StringValue("gpt-fallback".into()),
                ),
                attribute("gen_ai.tool.name", AttributeValue::StringValue("".into())),
                attribute(
                    "ai.toolCall.name",
                    AttributeValue::StringValue("lookup_invoice".into()),
                ),
                attribute(
                    "ai.settings.context.userId",
                    AttributeValue::StringValue("".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.userId",
                    AttributeValue::StringValue("user-42".into()),
                ),
                attribute(
                    "ai.settings.context.sessionId",
                    AttributeValue::StringValue("".into()),
                ),
                attribute(
                    "ai.settings.context.chatId",
                    AttributeValue::StringValue("session-17".into()),
                ),
            ],
            ..Default::default()
        };

        let index = index_span(&span);
        assert_eq!(index.provider, "openai");
        assert_eq!(index.model, "gpt-fallback");
        assert_eq!(index.tool, "lookup_invoice");
        assert_eq!(index.user_id, "user-42");
        assert_eq!(index.session_id, "session-17");
    }

    #[test]
    fn recognizes_legacy_ai_sdk_tool_spans() {
        let span = Span {
            name: "ai.toolCall".into(),
            attributes: vec![
                attribute(
                    "ai.toolCall.name",
                    AttributeValue::StringValue("lookupWeather".into()),
                ),
                attribute(
                    "ai.toolCall.args",
                    AttributeValue::StringValue(r#"{"city":"Belgrade"}"#.into()),
                ),
            ],
            ..Default::default()
        };

        let index = index_span(&span);
        assert_eq!(index.kind, "tool");
        assert_eq!(index.tool, "lookupWeather");
        assert!(index.search_text.contains("lookupWeather"));
        assert!(index.search_text.contains("Belgrade"));
    }

    #[test]
    fn indexes_standard_tool_name_separately_from_operation() {
        let span = Span {
            name: "execute_tool run_sql".into(),
            attributes: vec![
                attribute(
                    "gen_ai.operation.name",
                    AttributeValue::StringValue("execute_tool".into()),
                ),
                attribute(
                    "gen_ai.tool.name",
                    AttributeValue::StringValue("run_sql".into()),
                ),
            ],
            ..Default::default()
        };

        let index = index_span(&span);
        assert_eq!(index.kind, "tool");
        assert_eq!(index.operation, "execute_tool");
        assert_eq!(index.tool, "run_sql");
    }

    #[test]
    fn indexes_ai_sdk_runtime_context_as_user_and_session() {
        let span = Span {
            name: "ai.streamText".into(),
            attributes: vec![
                attribute(
                    "ai.settings.context.userId",
                    AttributeValue::StringValue("user_42".into()),
                ),
                attribute(
                    "ai.settings.context.chatId",
                    AttributeValue::StringValue("chat_17".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.apiKey",
                    AttributeValue::StringValue("must-not-be-indexed".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.authToken",
                    AttributeValue::StringValue("hidden-auth-token".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.bearerToken",
                    AttributeValue::StringValue("hidden-bearer-token".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.refreshToken",
                    AttributeValue::StringValue("hidden-refresh-token".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.sessionToken",
                    AttributeValue::StringValue("hidden-session-token".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.idToken",
                    AttributeValue::StringValue("hidden-id-token".into()),
                ),
                attribute(
                    "ai.settings.runtimeContext.jwt",
                    AttributeValue::StringValue("hidden-jwt".into()),
                ),
                attribute(
                    "ai.settings.context.profile",
                    AttributeValue::StringValue(
                        r#"{"organization":"acme","token":"hidden-nested-token","auth":{"passphrase":"hidden-passphrase"}}"#.into(),
                    ),
                ),
            ],
            ..Default::default()
        };

        let index = index_span(&span);
        assert_eq!(index.user_id, "user_42");
        assert_eq!(index.session_id, "chat_17");
        assert!(index.search_text.contains("user_42"));
        assert!(index.search_text.contains("chat_17"));
        assert!(!index.search_text.contains("must-not-be-indexed"));
        assert!(!index.search_text.contains("hidden-auth-token"));
        assert!(!index.search_text.contains("hidden-bearer-token"));
        assert!(!index.search_text.contains("hidden-refresh-token"));
        assert!(!index.search_text.contains("hidden-session-token"));
        assert!(!index.search_text.contains("hidden-id-token"));
        assert!(!index.search_text.contains("hidden-jwt"));
        assert!(!index.search_text.contains("hidden-nested-token"));
        assert!(!index.search_text.contains("hidden-passphrase"));
        assert!(index.search_text.contains("acme"));
    }

    #[test]
    fn indexes_sentry_chat_attribute_as_session() {
        let span = Span {
            attributes: vec![attribute(
                "chat.id",
                AttributeValue::StringValue("chat_17".into()),
            )],
            ..Default::default()
        };

        assert_eq!(index_span(&span).session_id, "chat_17");
    }

    #[test]
    fn ignores_negative_latency_and_uses_derived_throughput() {
        let span = Span {
            start_time_unix_nano: 1_000_000_000,
            end_time_unix_nano: 3_000_000_000,
            attributes: vec![
                attribute("gen_ai.usage.output_tokens", AttributeValue::IntValue(20)),
                attribute(
                    "gen_ai.client.operation.time_to_first_chunk",
                    AttributeValue::DoubleValue(-1.0),
                ),
                attribute(
                    "ai.response.avgOutputTokensPerSecond",
                    AttributeValue::DoubleValue(-10.0),
                ),
                attribute(
                    "gen_ai.client.operation.duration",
                    AttributeValue::DoubleValue(-2.0),
                ),
            ],
            ..Default::default()
        };

        let index = index_span(&span);
        assert_eq!(index.ttft_seconds, None);
        assert_eq!(index.tokens_per_second, Some(10.0));
    }

    #[test]
    fn indexes_legacy_ai_sdk_embedding_and_rerank_provider_spans() {
        let embedding = Span {
            name: "ai.embedMany.doEmbed".into(),
            attributes: vec![attribute("ai.usage.tokens", AttributeValue::IntValue(42))],
            ..Default::default()
        };
        let embedding_operation = Span {
            name: "ai.embedMany".into(),
            attributes: vec![attribute("ai.usage.tokens", AttributeValue::IntValue(42))],
            ..Default::default()
        };
        let rerank = Span {
            name: "ai.rerank.doRerank".into(),
            ..Default::default()
        };

        let embedding = index_span(&embedding);
        assert_eq!(embedding.kind, "embedding");
        assert_eq!(embedding.input_tokens, Some(42));
        assert_eq!(index_span(&embedding_operation).kind, "ai");
        assert_eq!(index_span(&rerank).kind, "rerank");
    }

    #[test]
    fn prefers_ai_sdk_reported_output_throughput() {
        let span = Span {
            start_time_unix_nano: 1_000_000_000,
            end_time_unix_nano: 3_000_000_000,
            attributes: vec![
                attribute("gen_ai.usage.output_tokens", AttributeValue::IntValue(20)),
                attribute(
                    "ai.response.avgOutputTokensPerSecond",
                    AttributeValue::DoubleValue(37.5),
                ),
            ],
            ..Default::default()
        };

        assert_eq!(index_span(&span).tokens_per_second, Some(37.5));
    }
}
