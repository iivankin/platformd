use std::collections::HashMap;

use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::Value;

use crate::error::{Error, Result};
use crate::storage::{AiOverview, AiOverviewSummary, AiUserOverview};

const MAX_AGENTS: usize = 12;
const MAX_LATENCY_ROWS: usize = 20;
const MAX_MODELS: usize = 256;
const MAX_USERS: usize = 12;
const AI_OVERVIEW_QUERY_SETTINGS: &str = "SETTINGS max_execution_time=10, max_memory_usage=536870912, max_threads=2, max_result_rows=250000, result_overflow_mode='throw', output_format_json_named_tuples_as_objects=1";
const GENERATION_FILTER: &str = "ai_kind IN ('model', 'embedding', 'rerank')";

pub(crate) fn ai_overview_query(
    aggregate_services: &str,
    identity_services: &str,
    from_unix_nano: u64,
    to_unix_nano: u64,
    step_nano: u64,
) -> String {
    // Keep raw spans inside chDB: the result grows with dashboard groups rather than
    // trace size. A trace-level identity is inherited only when it is unambiguous.
    format!(
        "WITH \
         buckets AS (\
           SELECT toUInt64(least({to_unix_nano}, {from_unix_nano} + (number + 1) * {step_nano})) AS time_unix_nano \
           FROM numbers(toUInt64(intDiv({to_unix_nano} - {from_unix_nano} - 1, {step_nano}) + 1))\
         ), \
         selected_traces AS (\
           SELECT DISTINCT trace_id \
           FROM telemetry.spans FINAL \
           WHERE {aggregate_services} \
             AND start_time_unix_nano >= {from_unix_nano} \
             AND start_time_unix_nano <= {to_unix_nano} \
             AND notEmpty(ai_kind)\
         ), \
         trace_identity AS (\
           SELECT trace_id, \
             if(uniqExactIf(ai_user_id, notEmpty(ai_user_id)) = 1, \
               anyIf(ai_user_id, notEmpty(ai_user_id)), '') AS trace_user_id, \
             if(uniqExactIf(ai_session_id, notEmpty(ai_session_id)) = 1, \
               anyIf(ai_session_id, notEmpty(ai_session_id)), '') AS trace_session_id \
           FROM telemetry.spans FINAL \
           WHERE {identity_services} AND trace_id IN (SELECT trace_id FROM selected_traces) \
           GROUP BY trace_id\
         ), \
         ai_spans AS (\
           SELECT source_span.*, \
             if(notEmpty(source_span.ai_user_id), source_span.ai_user_id, identity.trace_user_id) AS effective_user_id, \
             if(notEmpty(source_span.ai_session_id), source_span.ai_session_id, identity.trace_session_id) AS effective_session_id, \
             toUInt64(least({to_unix_nano}, {from_unix_nano} + \
               (intDiv(start_time_unix_nano - {from_unix_nano}, {step_nano}) + 1) * {step_nano})) AS time_unix_nano \
           FROM telemetry.spans AS source_span FINAL \
           LEFT JOIN trace_identity AS identity USING (trace_id) \
           WHERE {aggregate_services} \
             AND start_time_unix_nano >= {from_unix_nano} \
             AND start_time_unix_nano <= {to_unix_nano} \
             AND notEmpty(ai_kind)\
         ), \
         top_users AS (\
           SELECT effective_user_id AS user_id, \
             countIf(ai_kind = 'agent') AS run_count, \
             uniqExactIf(effective_session_id, notEmpty(effective_session_id)) AS session_count, \
             sumIf(ifNull(ai_input_tokens, 0) + ifNull(ai_output_tokens, 0), {generation_filter}) AS token_count \
           FROM ai_spans \
           WHERE notEmpty(effective_user_id) \
           GROUP BY effective_user_id \
           ORDER BY token_count DESC, run_count DESC, user_id \
           LIMIT {MAX_USERS}\
         ), \
         agent_rows AS (\
           SELECT multiIf(notEmpty(ai_agent), ai_agent, notEmpty(ai_operation), ai_operation, name) AS agent, \
             count() AS run_count, countIf(status_code = 2) AS error_count, \
             uniqExactIf(effective_user_id, notEmpty(effective_user_id)) AS user_count, \
             quantile(0.50)(duration_nano / 1000000000.) AS p50_latency_seconds, \
             quantile(0.95)(duration_nano / 1000000000.) AS p95_latency_seconds, \
             quantile(0.99)(duration_nano / 1000000000.) AS p99_latency_seconds \
           FROM ai_spans WHERE ai_kind = 'agent' \
           GROUP BY agent ORDER BY run_count DESC, agent LIMIT {MAX_AGENTS}\
         ), \
         model_rows AS (\
           SELECT ai_provider AS provider, ai_model AS model, count() AS generation_count, \
             quantile(0.50)(duration_nano / 1000000000.) AS p50_latency_seconds, \
             quantile(0.95)(duration_nano / 1000000000.) AS p95_latency_seconds, \
             quantile(0.99)(duration_nano / 1000000000.) AS p99_latency_seconds \
           FROM ai_spans WHERE {generation_filter} \
           GROUP BY provider, model \
           ORDER BY generation_count DESC, provider, model LIMIT {MAX_MODELS}\
         ), \
         latency_rows AS (\
           SELECT ai_kind AS kind, \
             multiIf(ai_kind = 'tool', if(empty(ai_tool), name, ai_tool), \
               empty(ai_model), name, empty(ai_provider), ai_model, concat(ai_provider, ' / ', ai_model)) AS name, \
             count() AS count, \
             quantile(0.50)(duration_nano / 1000000000.) AS p50_latency_seconds, \
             quantile(0.90)(duration_nano / 1000000000.) AS p90_latency_seconds, \
             quantile(0.95)(duration_nano / 1000000000.) AS p95_latency_seconds, \
             quantile(0.99)(duration_nano / 1000000000.) AS p99_latency_seconds \
           FROM ai_spans WHERE {generation_filter} OR ai_kind = 'tool' \
           GROUP BY kind, name \
           ORDER BY p95_latency_seconds DESC, kind, name LIMIT {MAX_LATENCY_ROWS}\
         ) \
         SELECT section, payload FROM (\
	           SELECT 'summary' AS section, toJSONString(tuple(\
	             countIf(ai_kind = 'agent') AS agent_run_count, \
	             uniqExactIf(multiIf(notEmpty(ai_agent), ai_agent, notEmpty(ai_operation), ai_operation, name), \
	               ai_kind = 'agent') AS agent_count, \
	             countIf(ai_kind = 'agent' AND notEmpty(effective_user_id)) AS identified_agent_run_count, \
             countIf({generation_filter}) AS generation_count, \
             countIf(ai_kind = 'tool') AS tool_call_count, \
             countIf(status_code = 2) AS error_count, \
             uniqExactIf(effective_user_id, notEmpty(effective_user_id)) AS user_count, \
             uniqExactIf(effective_session_id, notEmpty(effective_session_id)) AS session_count, \
             uniqExactIf(tuple(ai_provider, ai_model), {generation_filter}) AS model_count\
           )) AS payload FROM ai_spans \
           UNION ALL \
           SELECT 'activity', toJSONString(tuple(\
             time_unix_nano, \
             countIf(ai_kind = 'agent') AS agent_run_count, \
             countIf({generation_filter}) AS generation_count, \
             countIf(ai_kind = 'tool') AS tool_call_count, \
             countIf(status_code = 2) AS error_count\
           )) FROM buckets LEFT JOIN ai_spans USING (time_unix_nano) GROUP BY time_unix_nano \
           UNION ALL \
           SELECT 'usage', toJSONString(tuple(\
             time_unix_nano, \
             sum(ifNull(ai_input_tokens, 0)) AS input_tokens, \
             sum(ifNull(ai_output_tokens, 0)) AS output_tokens, \
             sum(ifNull(ai_cache_read_tokens, 0)) AS cache_read_tokens, \
             sum(ifNull(ai_cache_write_tokens, 0)) AS cache_write_tokens, \
             sum(ifNull(ai_reasoning_tokens, 0)) AS reasoning_tokens, \
             if(countIf(isNull(ai_cost_usd)) > 0, NULL, sum(ifNull(ai_cost_usd, 0.))) AS reported_cost_usd, \
             if(countIf(isNull(ai_estimated_cost_usd)) > 0, NULL, \
               sum(ifNull(ai_estimated_cost_usd, 0.))) AS estimated_cost_usd\
           )) FROM ai_spans WHERE {generation_filter} \
           GROUP BY time_unix_nano, isNull(ai_cost_usd), isNull(ai_estimated_cost_usd) \
           UNION ALL \
           SELECT 'model_usage', toJSONString(tuple(\
             ai_spans.ai_provider AS provider, ai_spans.ai_model AS model, \
             sum(ifNull(ai_input_tokens, 0)) AS input_tokens, \
             sum(ifNull(ai_output_tokens, 0)) AS output_tokens, \
             sum(ifNull(ai_cache_read_tokens, 0)) AS cache_read_tokens, \
             sum(ifNull(ai_cache_write_tokens, 0)) AS cache_write_tokens, \
             sum(ifNull(ai_reasoning_tokens, 0)) AS reasoning_tokens, \
             if(countIf(isNull(ai_cost_usd)) > 0, NULL, sum(ifNull(ai_cost_usd, 0.))) AS reported_cost_usd, \
             if(countIf(isNull(ai_estimated_cost_usd)) > 0, NULL, \
               sum(ifNull(ai_estimated_cost_usd, 0.))) AS estimated_cost_usd\
           )) FROM ai_spans \
           INNER JOIN model_rows \
             ON model_rows.provider = ai_spans.ai_provider AND model_rows.model = ai_spans.ai_model \
           WHERE {generation_filter} \
           GROUP BY provider, model, isNull(ai_cost_usd), isNull(ai_estimated_cost_usd) \
           UNION ALL \
           SELECT 'model', toJSONString(tuple(\
             provider, model, generation_count, p50_latency_seconds, \
             p95_latency_seconds, p99_latency_seconds\
           )) FROM model_rows \
           UNION ALL \
           SELECT 'agent', toJSONString(tuple(\
             agent, run_count, error_count, user_count, p50_latency_seconds, \
             p95_latency_seconds, p99_latency_seconds\
           )) FROM agent_rows \
           UNION ALL \
           SELECT 'user_summary', toJSONString(tuple(\
             user_id, run_count, session_count, token_count\
           )) FROM top_users \
           UNION ALL \
           SELECT 'user_usage', toJSONString(tuple(\
             effective_user_id AS user_id, \
             count() AS generation_count, \
             sum(ifNull(ai_input_tokens, 0)) AS input_tokens, \
             sum(ifNull(ai_output_tokens, 0)) AS output_tokens, \
             sum(ifNull(ai_cache_read_tokens, 0)) AS cache_read_tokens, \
             sum(ifNull(ai_cache_write_tokens, 0)) AS cache_write_tokens, \
             if(countIf(isNull(ai_cost_usd)) > 0, NULL, sum(ifNull(ai_cost_usd, 0.))) AS reported_cost_usd, \
             if(countIf(isNull(ai_estimated_cost_usd)) > 0, NULL, \
               sum(ifNull(ai_estimated_cost_usd, 0.))) AS estimated_cost_usd\
           )) FROM ai_spans \
           INNER JOIN top_users ON top_users.user_id = ai_spans.effective_user_id \
           WHERE {generation_filter} \
           GROUP BY user_id, isNull(ai_cost_usd), isNull(ai_estimated_cost_usd) \
           UNION ALL \
           SELECT 'latency', toJSONString(tuple(\
             kind, name, count, p50_latency_seconds, p90_latency_seconds, \
             p95_latency_seconds, p99_latency_seconds\
           )) FROM latency_rows\
         ) {AI_OVERVIEW_QUERY_SETTINGS}",
        generation_filter = GENERATION_FILTER,
    )
}

#[derive(Deserialize)]
struct AggregateRow {
    section: String,
    payload: String,
}

#[derive(Deserialize)]
struct UserSummaryRow {
    user_id: String,
    run_count: u64,
    session_count: u64,
    token_count: u64,
}

#[derive(Deserialize)]
struct UserUsageRow {
    user_id: String,
    generation_count: u64,
    input_tokens: u64,
    output_tokens: u64,
    cache_read_tokens: u64,
    cache_write_tokens: u64,
    reported_cost_usd: Option<f64>,
    estimated_cost_usd: Option<f64>,
}

fn payload<T: DeserializeOwned>(row: &AggregateRow) -> Result<T> {
    serde_json::from_str(&row.payload).map_err(|error| {
        Error::Storage(format!(
            "decode AI overview {} aggregate: {error}",
            row.section
        ))
    })
}

pub(crate) fn decode_ai_overview(rows: Vec<Value>) -> Result<AiOverview> {
    let mut overview = AiOverview {
        summary: AiOverviewSummary::default(),
        activity: Vec::new(),
        usage: Vec::new(),
        model_usage: Vec::new(),
        models: Vec::new(),
        agents: Vec::new(),
        users: Vec::new(),
        latency: Vec::new(),
    };
    let mut user_summaries = Vec::<UserSummaryRow>::new();
    let mut user_usage = HashMap::<String, Vec<UserUsageRow>>::new();

    for value in rows {
        let row = serde_json::from_value::<AggregateRow>(value)
            .map_err(|error| Error::Storage(format!("decode AI overview aggregate: {error}")))?;
        match row.section.as_str() {
            "summary" => overview.summary = payload(&row)?,
            "activity" => overview.activity.push(payload(&row)?),
            "usage" => overview.usage.push(payload(&row)?),
            "model_usage" => overview.model_usage.push(payload(&row)?),
            "model" => overview.models.push(payload(&row)?),
            "agent" => overview.agents.push(payload(&row)?),
            "user_summary" => user_summaries.push(payload(&row)?),
            "user_usage" => {
                let usage = payload::<UserUsageRow>(&row)?;
                user_usage
                    .entry(usage.user_id.clone())
                    .or_default()
                    .push(usage);
            }
            "latency" => overview.latency.push(payload(&row)?),
            section => {
                return Err(Error::Storage(format!(
                    "unknown AI overview aggregate section {section}"
                )));
            }
        }
    }

    overview.activity.sort_by_key(|point| point.time_unix_nano);
    overview.usage.sort_by_key(|point| point.time_unix_nano);
    overview.model_usage.sort_by(|left, right| {
        left.provider
            .cmp(&right.provider)
            .then_with(|| left.model.cmp(&right.model))
    });
    overview.models.sort_by(|left, right| {
        right
            .generation_count
            .cmp(&left.generation_count)
            .then_with(|| left.provider.cmp(&right.provider))
            .then_with(|| left.model.cmp(&right.model))
    });
    overview.agents.sort_by(|left, right| {
        right
            .run_count
            .cmp(&left.run_count)
            .then_with(|| left.agent.cmp(&right.agent))
    });
    overview.agents.truncate(MAX_AGENTS);
    overview.latency.sort_by(|left, right| {
        right
            .p95_latency_seconds
            .total_cmp(&left.p95_latency_seconds)
            .then_with(|| left.kind.cmp(&right.kind))
            .then_with(|| left.name.cmp(&right.name))
    });
    overview.latency.truncate(MAX_LATENCY_ROWS);

    user_summaries.sort_by(|left, right| {
        right
            .token_count
            .cmp(&left.token_count)
            .then_with(|| right.run_count.cmp(&left.run_count))
            .then_with(|| left.user_id.cmp(&right.user_id))
    });
    user_summaries.truncate(MAX_USERS);
    for summary in user_summaries {
        let usage = user_usage.remove(&summary.user_id).unwrap_or_default();
        if usage.is_empty() {
            overview.users.push(AiUserOverview {
                user_id: summary.user_id,
                run_count: summary.run_count,
                session_count: summary.session_count,
                generation_count: 0,
                input_tokens: 0,
                output_tokens: 0,
                cache_read_tokens: 0,
                cache_write_tokens: 0,
                reported_cost_usd: None,
                estimated_cost_usd: None,
            });
            continue;
        }
        overview
            .users
            .extend(usage.into_iter().map(|usage| AiUserOverview {
                user_id: summary.user_id.clone(),
                run_count: summary.run_count,
                session_count: summary.session_count,
                generation_count: usage.generation_count,
                input_tokens: usage.input_tokens,
                output_tokens: usage.output_tokens,
                cache_read_tokens: usage.cache_read_tokens,
                cache_write_tokens: usage.cache_write_tokens,
                reported_cost_usd: usage.reported_cost_usd,
                estimated_cost_usd: usage.estimated_cost_usd,
            }));
    }
    overview.users.sort_by(|left, right| {
        right
            .input_tokens
            .saturating_add(right.output_tokens)
            .cmp(&left.input_tokens.saturating_add(left.output_tokens))
            .then_with(|| left.user_id.cmp(&right.user_id))
    });

    Ok(overview)
}
