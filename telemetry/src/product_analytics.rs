use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

use crate::error::{Error, Result};
use crate::metric_sql::{MAX_METRIC_QUERY_BYTES, MAX_METRIC_QUERY_ROWS};
use crate::product_analytics_sql::{ANALYTICS_EVENTS, INTERNAL_EVENTS, VISIT_EVENTS};
use crate::storage::{Store, chdb_string};

const SETTINGS: &str = "SETTINGS max_execution_time=5, max_memory_usage=268435456, max_threads=2";
const EVENTS: &str = ANALYTICS_EVENTS;
const PAGE_LABEL: &str = "if(hostname = '', pathname, concat(hostname, pathname))";

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProductAnalyticsQuery {
    pub report: String,
    pub from: Option<i64>,
    pub to: Option<i64>,
    #[serde(default)]
    pub filters: Vec<AnalyticsFilter>,
    pub dimension: Option<String>,
    pub pathname: Option<String>,
    pub viewport: Option<i32>,
    pub event_type: Option<String>,
    pub steps: Option<Vec<FunnelStep>>,
    pub window_seconds: Option<i64>,
    pub sql: Option<String>,
    pub experiment_id: Option<String>,
    pub flag: Option<String>,
    pub metric_event: Option<String>,
    pub metric_path: Option<String>,
    pub metric_hostname: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct AnalyticsFilter {
    pub dimension: String,
    pub operator: String,
    pub value: FilterValue,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(untagged)]
pub enum FilterValue {
    One(String),
    Many(Vec<String>),
}

#[derive(Clone, Debug, Deserialize)]
pub struct FunnelStep {
    #[serde(rename = "type")]
    pub kind: String,
    pub value: String,
    pub hostname: Option<String>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ProductAnalyticsEvent {
    pub event_id: String,
    pub tracker_id: String,
    #[serde(default)]
    pub service_id: String,
    pub distinct_id: String,
    #[serde(default)]
    pub session_id: String,
    pub event_name: String,
    pub timestamp: String,
    #[serde(default)]
    pub hostname: String,
    #[serde(default)]
    pub pathname: String,
    #[serde(default)]
    pub page_title: String,
    #[serde(default)]
    pub referrer: String,
    #[serde(default)]
    pub referrer_domain: String,
    #[serde(default)]
    pub referrer_source: String,
    #[serde(default)]
    pub channel: String,
    #[serde(default)]
    pub utm_source: String,
    #[serde(default)]
    pub utm_medium: String,
    #[serde(default)]
    pub utm_campaign: String,
    #[serde(default)]
    pub utm_content: String,
    #[serde(default)]
    pub utm_term: String,
    #[serde(default)]
    pub gclid: String,
    #[serde(default)]
    pub fbclid: String,
    #[serde(default)]
    pub msclkid: String,
    #[serde(default)]
    pub ttclid: String,
    #[serde(default)]
    pub li_fat_id: String,
    #[serde(default)]
    pub twclid: String,
    #[serde(default)]
    pub browser: String,
    #[serde(default)]
    pub browser_version: String,
    #[serde(default)]
    pub os: String,
    #[serde(default)]
    pub os_version: String,
    #[serde(default)]
    pub device: String,
    #[serde(default)]
    pub screen: String,
    #[serde(default)]
    pub language: String,
    #[serde(default)]
    pub country: String,
    #[serde(default)]
    pub region: String,
    #[serde(default)]
    pub city: String,
    #[serde(default)]
    pub interactive: u8,
    #[serde(default)]
    pub props_keys: Vec<String>,
    #[serde(default)]
    pub props_values: Vec<String>,
    pub revenue: Option<f64>,
    #[serde(default)]
    pub currency: String,
    #[serde(default)]
    pub bot_kind: String,
    #[serde(default)]
    pub bot_name: String,
    pub lcp: Option<f64>,
    pub inp: Option<f64>,
    pub cls: Option<f64>,
    pub fcp: Option<f64>,
    pub ttfb: Option<f64>,
}

impl Store {
    pub async fn ingest_product_analytics(&self, event: ProductAnalyticsEvent) -> Result<()> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || store.ingest_product_analytics_blocking(event))
            .await
            .map_err(|error| Error::Storage(format!("product analytics ingest task: {error}")))?
    }

    fn ingest_product_analytics_blocking(&self, event: ProductAnalyticsEvent) -> Result<()> {
        if event.event_name == "$heatmap" {
            let x = prop_i64(&event, "x").unwrap_or(0).clamp(0, 100) as i16;
            let y = prop_i64(&event, "y").unwrap_or(0).clamp(0, 100) as i16;
            self.insert_json_rows(
                "analytics_heatmaps",
                vec![json!({
                    "tracker_id": event.tracker_id,
                    "distinct_id": event.distinct_id,
                    "timestamp": event.timestamp,
                    "pathname": event.pathname,
                    "hostname": event.hostname,
                    "x": x,
                    "y": y,
                    "scale_factor": 1.0,
                    "viewport_w": prop_i64(&event, "viewport_w").unwrap_or(0).clamp(0, 65535),
                    "viewport_h": prop_i64(&event, "viewport_h").unwrap_or(0).clamp(0, 65535),
                    "page_h": prop_i64(&event, "page_h").unwrap_or(0).clamp(0, 65535),
                    "scroll_pct": prop_i64(&event, "scroll_pct").unwrap_or(0).clamp(0, 255),
                    "event_type": prop_str(&event, "event_type"),
                })],
            )?;
            return Ok(());
        }
        let row = serde_json::to_value(&event)
            .map_err(|error| Error::Storage(format!("encode product analytics event: {error}")))?;
        self.insert_json_rows("analytics_events", vec![row])
    }

    pub async fn query_product_analytics(
        &self,
        tracker_id: String,
        query: ProductAnalyticsQuery,
    ) -> Result<Value> {
        let store = self.clone();
        tokio::task::spawn_blocking(move || {
            store.query_product_analytics_blocking(&tracker_id, query)
        })
        .await
        .map_err(|error| Error::Storage(format!("product analytics query task: {error}")))?
    }

    fn query_product_analytics_blocking(
        &self,
        tracker_id: &str,
        query: ProductAnalyticsQuery,
    ) -> Result<Value> {
        if let (Some(from), Some(to)) = (query.from, query.to)
            && to <= from
        {
            return Err(Error::InvalidRequest("analytics range is inverted".into()));
        }
        match query.report.as_str() {
            "overview" => self.overview(tracker_id, &query),
            "breakdown" => self.breakdown(tracker_id, &query),
            "realtime" => self.realtime(tracker_id),
            "funnel" => self.funnel(tracker_id, &query),
            "retention" => self.retention(tracker_id, &query),
            "paths" => self.paths(tracker_id, &query),
            "heatmap" => self.heatmap(tracker_id, &query),
            "bots" => self.bots(tracker_id, &query),
            "sessions" => self.sessions(tracker_id, &query),
            "experiment" => self.experiment(tracker_id, &query),
            "sql" => self.analytics_sql(tracker_id, &query),
            "lookup" => self.lookup(tracker_id, &query),
            other => Err(Error::InvalidRequest(format!(
                "unknown analytics report {other}"
            ))),
        }
    }

    fn overview(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let where_sql = event_where(tracker_id, query, true)?;
        let previous = previous_where(tracker_id, query, true)?;
        let current = self.query_json_rows(&format!(
            "SELECT uniqExact(distinct_id) AS visitors, count() AS visits,\n\
             sum(pageviews) AS pageviews, avg(duration) AS duration,\n\
             countIf(interactive_events <= 1) / nullIf(count(), 0) AS bounce_rate\n\
             FROM (\n\
               SELECT distinct_id, if(session_id = '', distinct_id, session_id) AS visit_id,\n\
                 countIf(event_name = '$pageview') AS pageviews,\n\
                 countIf(interactive = 1) AS interactive_events,\n\
                 dateDiff('second', min(timestamp), max(timestamp)) AS duration\n\
               {EVENTS} WHERE {where_sql} AND {INTERNAL_EVENTS}\n\
               GROUP BY distinct_id, visit_id\n\
             ) {SETTINGS}"
        ))?;
        let previous_rows = self.query_json_rows(&format!(
            "SELECT uniqExact(distinct_id) AS visitors, count() AS visits,\n\
             sum(pageviews) AS pageviews, avg(duration) AS duration,\n\
             countIf(interactive_events <= 1) / nullIf(count(), 0) AS bounce_rate\n\
             FROM (\n\
               SELECT distinct_id, if(session_id = '', distinct_id, session_id) AS visit_id,\n\
                 countIf(event_name = '$pageview') AS pageviews,\n\
                 countIf(interactive = 1) AS interactive_events,\n\
                 dateDiff('second', min(timestamp), max(timestamp)) AS duration\n\
               {EVENTS} WHERE {previous} AND {INTERNAL_EVENTS}\n\
               GROUP BY distinct_id, visit_id\n\
             ) {SETTINGS}"
        ))?;
        let timeseries = self.query_json_rows(&format!(
            "SELECT toUnixTimestamp(toStartOfInterval(first_seen, INTERVAL {interval} SECOND)) * 1000 AS time,\n\
             uniqExact(distinct_id) AS visitors, count() AS visits,\n\
             sum(pageviews) AS pageviews, avg(duration) AS duration,\n\
             countIf(interactive_events <= 1) / nullIf(count(), 0) AS bounce_rate\n\
             FROM (\n\
               SELECT distinct_id, if(session_id = '', distinct_id, session_id) AS visit_id,\n\
                 min(timestamp) AS first_seen,\n\
                 countIf(event_name = '$pageview') AS pageviews,\n\
                 countIf(interactive = 1) AS interactive_events,\n\
                 dateDiff('second', min(timestamp), max(timestamp)) AS duration\n\
               {EVENTS} WHERE {where_sql} AND {INTERNAL_EVENTS}\n\
               GROUP BY distinct_id, visit_id\n\
             )\n\
             GROUP BY time ORDER BY time {SETTINGS}",
            interval = graph_interval(query)
        ))?;
        Ok(json!({ "current": current, "previous": previous_rows, "timeseries": timeseries }))
    }

    fn breakdown(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let requested = query.dimension.as_deref().unwrap_or("referrer_source");
        let visit_label = visit_breakdown_label(requested);
        let where_sql = event_where(
            tracker_id,
            query,
            !matches!(requested, "bot_kind" | "bot_name"),
        )?;
        let sql = if let Some(label) = visit_label {
            format!(
                "SELECT label, uniqExact(distinct_id) AS visitors, count() AS events FROM (\n\
                   SELECT {label} AS label, distinct_id\n\
                   {EVENTS} WHERE {where_sql} AND event_name = '$pageview'\n\
                   GROUP BY if(session_id = '', distinct_id, session_id), distinct_id\n\
                 ) GROUP BY label ORDER BY visitors DESC LIMIT 9 {SETTINGS}"
            )
        } else {
            let dimension = match requested {
                "page" | "pathname" => PAGE_LABEL,
                "hostname" => "hostname",
                "country" => "country",
                "region" => "region",
                "city" => "city",
                "device" => "device",
                "browser" => "browser",
                "os" => "os",
                "event" => "event_name",
                "bot_kind" => "bot_kind",
                "bot_name" => "bot_name",
                other => {
                    return Err(Error::InvalidRequest(format!(
                        "unknown analytics dimension {other}"
                    )));
                }
            };
            format!(
                "SELECT {dimension} AS label, uniqExact(distinct_id) AS visitors, count() AS events\n\
                 {EVENTS} WHERE {where_sql} AND {VISIT_EVENTS}\n\
                 GROUP BY label ORDER BY visitors DESC LIMIT 9 {SETTINGS}"
            )
        };
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn realtime(&self, tracker_id: &str) -> Result<Value> {
        let sql = format!(
            "SELECT uniqExact(distinct_id) AS visitors {EVENTS}\n\
             WHERE tracker_id = {} AND timestamp >= now() - INTERVAL 5 MINUTE AND bot_kind = '' AND {VISIT_EVENTS} {SETTINGS}",
            chdb_string(tracker_id)
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn funnel(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let steps = query.steps.as_deref().unwrap_or(&[]);
        if steps.len() < 2 || steps.len() > 8 {
            return Err(Error::InvalidRequest("funnel requires 2–8 steps".into()));
        }
        let window = query.window_seconds.unwrap_or(7 * 24 * 3600).max(60);
        let conditions: Vec<String> = steps.iter().map(step_condition).collect();
        let where_sql = event_where(tracker_id, query, true)?;
        let sql = format!(
            "SELECT level, count() AS visitors FROM (\n\
               SELECT windowFunnel({window})(toUnixTimestamp(timestamp), {}) AS level\n\
               {EVENTS} WHERE {where_sql} AND {VISIT_EVENTS}\n\
               GROUP BY distinct_id\n\
             ) GROUP BY level ORDER BY level {SETTINGS}",
            conditions.join(", ")
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn retention(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let cohort_where = event_where(tracker_id, query, true)?;
        let mut activity_query = query.clone();
        if let Some(to) = query.to {
            const RETENTION_HORIZON_MS: i64 = 28 * 24 * 60 * 60 * 1000;
            activity_query.to = Some(to.saturating_add(RETENTION_HORIZON_MS));
        }
        let activity_where = event_where(tracker_id, &activity_query, true)?;
        let sql = format!(
            "WITH first_seen AS (\n\
               SELECT distinct_id, min(toDate(timestamp)) AS start {EVENTS}\n\
               WHERE {cohort_where} AND {VISIT_EVENTS} GROUP BY distinct_id\n\
             ), activity AS (\n\
               SELECT distinct_id, toDate(timestamp) AS day {EVENTS}\n\
               WHERE {activity_where} AND {VISIT_EVENTS} GROUP BY distinct_id, day\n\
             )\n\
             SELECT toUnixTimestamp(start) * 1000 AS cohort, uniqExact(f.distinct_id) AS size,\n\
               {retention_days}\n\
             FROM first_seen f LEFT JOIN activity a ON a.distinct_id = f.distinct_id\n\
             GROUP BY start ORDER BY start {SETTINGS}",
            retention_days = [0, 1, 2, 3, 4, 5, 6, 7, 14, 21, 28]
                .into_iter()
                .map(|day| format!(
                    "uniqExactIf(f.distinct_id, a.day = f.start + {day}) / uniqExact(f.distinct_id) AS d{day}"
                ))
                .collect::<Vec<_>>()
                .join(", ")
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn paths(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let where_sql = event_where(tracker_id, query, true)?;
        let sql = format!(
            "SELECT from_path, to_path, uniqExact(distinct_id) AS visitors FROM (\n\
               SELECT {PAGE_LABEL} AS from_path,\n\
                 leadInFrame({PAGE_LABEL}) OVER (\n\
                   PARTITION BY distinct_id, if(session_id = '', distinct_id, session_id) ORDER BY timestamp\n\
                   ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING\n\
                 ) AS to_path,\n\
                 distinct_id\n\
               {EVENTS} WHERE {where_sql} AND event_name = '$pageview'\n\
             ) WHERE to_path != ''\n\
             GROUP BY from_path, to_path ORDER BY visitors DESC LIMIT 40 {SETTINGS}"
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn heatmap(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let mut clauses = vec![
            format!("tracker_id = {}", chdb_string(tracker_id)),
            time_clause(query),
        ];
        if let Some(path) = query.pathname.as_deref() {
            clauses.push(format!("pathname = {}", chdb_string(path)));
        }
        if let Some(kind) = query.event_type.as_deref() {
            clauses.push(format!("event_type = {}", chdb_string(kind)));
        }
        if let Some(viewport) = query.viewport {
            clauses.push(viewport_clause(viewport));
        }
        let mut remaining = Vec::new();
        for filter in &query.filters {
            if filter.dimension == "hostname" {
                if let Some(clause) = filter_clause(tracker_id, query, filter)? {
                    clauses.push(clause);
                }
            } else {
                remaining.push(filter.clone());
            }
        }
        if !remaining.is_empty() {
            let mut scoped = query.clone();
            scoped.filters = remaining;
            clauses.push(format!(
                "distinct_id IN (SELECT distinct_id {EVENTS} WHERE {})",
                event_where(tracker_id, &scoped, true)?
            ));
        }
        let sql = format!(
            "SELECT x, y, count() AS hits FROM telemetry.analytics_heatmaps\n\
             WHERE {} GROUP BY x, y ORDER BY hits DESC LIMIT 5000 {SETTINGS}",
            clauses
                .into_iter()
                .filter(|clause| !clause.is_empty())
                .collect::<Vec<_>>()
                .join(" AND ")
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn bots(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let where_sql = event_where(tracker_id, query, false)?;
        let sql = format!(
            "SELECT bot_kind, bot_name, pathname, count() AS hits, uniqExact(distinct_id) AS visitors\n\
             {EVENTS} WHERE {where_sql} AND bot_kind != ''\n\
             GROUP BY bot_kind, bot_name, pathname ORDER BY hits DESC LIMIT 100 {SETTINGS}"
        );
        let hits = self.query_json_rows(&sql)?;
        let referrals = self.query_json_rows(&format!(
            "SELECT referrer_source AS label, uniqExact(distinct_id) AS visitors\n\
             {EVENTS} WHERE {} AND channel = 'AI Assistants' AND bot_kind = ''\n\
             GROUP BY label ORDER BY visitors DESC LIMIT 20 {SETTINGS}",
            event_where(tracker_id, query, true)?
        ))?;
        Ok(json!({ "hits": hits, "referrals": referrals }))
    }

    fn sessions(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let where_sql = event_where(tracker_id, query, true)?;
        let sql = format!(
            "SELECT if(session_id = '', distinct_id, session_id) AS visit_id, distinct_id,\n\
             countIf(event_name = '$pageview') AS views, countIf(event_name != '$pageleave') AS events,\n\
             anyLast(country) AS country, anyLast(city) AS city, anyLast(browser) AS browser,\n\
             anyLast(os) AS os, anyLast(device) AS device,\n\
             toUnixTimestamp64Milli(max(timestamp)) AS last_seen,\n\
             dateDiff('second', min(timestamp), max(timestamp)) AS duration\n\
             {EVENTS} WHERE {where_sql} AND {INTERNAL_EVENTS}\n\
             GROUP BY visit_id, distinct_id ORDER BY last_seen DESC LIMIT 100 {SETTINGS}"
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn experiment(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let flag = query
            .flag
            .as_deref()
            .ok_or_else(|| Error::InvalidRequest("experiment flag is required".into()))?;
        let where_sql = event_where(tracker_id, query, true)?;
        let conversion = if let Some(event) = query.metric_event.as_deref() {
            format!("event_name = {}", chdb_string(event))
        } else if let Some(path) = query.metric_path.as_deref() {
            format!(
                "event_name = '$pageview' AND pathname = {}",
                chdb_string(path)
            )
        } else {
            return Err(Error::InvalidRequest(
                "experiment metric is required".into(),
            ));
        };
        let conversion = if let Some(hostname) = query
            .metric_hostname
            .as_deref()
            .filter(|value| !value.is_empty())
        {
            format!("{conversion} AND hostname = {}", chdb_string(hostname))
        } else {
            conversion
        };
        let window = query.window_seconds.unwrap_or(14 * 24 * 3600).max(60);
        let mut conversion_query = query.clone();
        if let Some(to) = query.to {
            conversion_query.to = Some(to.saturating_add(window.saturating_mul(1000)));
        }
        let conversion_where = event_where(tracker_id, &conversion_query, true)?;
        let exposure_match = match query
            .experiment_id
            .as_deref()
            .filter(|value| !value.is_empty())
        {
            Some(id) => format!(
                "props_values[indexOf(props_keys, 'experiment_id')] = {}",
                chdb_string(id)
            ),
            None => format!(
                "props_values[indexOf(props_keys, 'flag')] = {}",
                chdb_string(flag)
            ),
        };
        let sql = format!(
            "WITH exposures AS (\n\
               SELECT distinct_id, min(timestamp) AS exposed_at,\n\
                 argMin(props_values[indexOf(props_keys, 'variant')], timestamp) AS variant\n\
               {EVENTS}\n\
               WHERE {where_sql} AND event_name = '$flag_called'\n\
                 AND {exposure_match}\n\
               GROUP BY distinct_id\n\
             )\n\
             SELECT e.variant AS variant, count() AS exposed,\n\
               countIf(c.distinct_id != '') AS converted\n\
             FROM exposures e\n\
             LEFT JOIN (\n\
               SELECT distinct_id, min(timestamp) AS converted_at {EVENTS}\n\
               WHERE {conversion_where} AND {conversion}\n\
               GROUP BY distinct_id\n\
             ) c ON c.distinct_id = e.distinct_id AND c.converted_at >= e.exposed_at\n\
               AND c.converted_at <= e.exposed_at + INTERVAL {window} SECOND\n\
             GROUP BY variant {SETTINGS}"
        );
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }

    fn analytics_sql(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let sql = query
            .sql
            .as_deref()
            .ok_or_else(|| Error::InvalidRequest("analytics SQL is required".into()))?;
        if sql.len() > MAX_METRIC_QUERY_BYTES {
            return Err(Error::InvalidRequest("analytics SQL is too large".into()));
        }
        let extra = event_filters(tracker_id, query)?.join(" AND ");
        let compiled =
            crate::product_analytics_sql::compile(sql, tracker_id, query.from, query.to, &extra)?;
        let rows = self.query_json_rows(&compiled)?;
        if rows.len() > MAX_METRIC_QUERY_ROWS {
            return Err(Error::InvalidRequest(
                "analytics SQL returned too many rows".into(),
            ));
        }
        Ok(Value::Array(rows))
    }

    fn lookup(&self, tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Value> {
        let dimension = query.dimension.as_deref().unwrap_or("pathname");
        let where_sql = event_where(tracker_id, query, true)?;
        let sql = if let Some(column) = acquisition_entry_expr(dimension) {
            format!(
                "SELECT label, count() AS events FROM (\n\
                   SELECT argMin({column}, timestamp) AS label\n\
                   {EVENTS} WHERE {where_sql} AND event_name = '$pageview'\n\
                   GROUP BY if(session_id = '', distinct_id, session_id)\n\
                 ) WHERE label != ''\n\
                 GROUP BY label ORDER BY events DESC LIMIT 50 {SETTINGS}"
            )
        } else {
            let column = match dimension {
                "page" => PAGE_LABEL,
                "pathname" => "pathname",
                "event" => "event_name",
                "hostname" => "hostname",
                "country" => "country",
                "region" => "region",
                "city" => "city",
                "browser" => "browser",
                "os" => "os",
                "device" => "device",
                other => {
                    return Err(Error::InvalidRequest(format!("unknown lookup {other}")));
                }
            };
            format!(
                "SELECT {column} AS label, count() AS events {EVENTS}\n\
                 WHERE {where_sql} AND {VISIT_EVENTS} AND {column} != ''\n\
                 GROUP BY label ORDER BY events DESC LIMIT 50 {SETTINGS}"
            )
        };
        Ok(Value::Array(self.query_json_rows(&sql)?))
    }
}

fn event_where(
    tracker_id: &str,
    query: &ProductAnalyticsQuery,
    humans_only: bool,
) -> Result<String> {
    let mut clauses = vec![format!("tracker_id = {}", chdb_string(tracker_id))];
    let time = time_clause(query);
    if !time.is_empty() {
        clauses.push(time);
    }
    if humans_only {
        clauses.push("bot_kind = ''".into());
    }
    if let Some(event_type) = query
        .event_type
        .as_deref()
        .filter(|value| !value.is_empty())
        && query.report != "heatmap"
    {
        clauses.push(format!("event_name = {}", chdb_string(event_type)));
    }
    clauses.extend(event_filters(tracker_id, query)?);
    Ok(clauses.join(" AND "))
}

fn event_filters(tracker_id: &str, query: &ProductAnalyticsQuery) -> Result<Vec<String>> {
    let mut clauses = Vec::new();
    for filter in &query.filters {
        if let Some(clause) = filter_clause(tracker_id, query, filter)? {
            clauses.push(clause);
        }
    }
    Ok(clauses)
}

fn previous_where(
    tracker_id: &str,
    query: &ProductAnalyticsQuery,
    humans_only: bool,
) -> Result<String> {
    let mut previous = query.clone();
    if let (Some(from), Some(to)) = (query.from, query.to) {
        let span = to.saturating_sub(from);
        previous.from = Some(from.saturating_sub(span));
        previous.to = Some(from.saturating_sub(1));
    }
    event_where(tracker_id, &previous, humans_only)
}

fn time_clause(query: &ProductAnalyticsQuery) -> String {
    match (query.from, query.to) {
        (Some(from), Some(to)) if to > from => format!(
            "timestamp >= fromUnixTimestamp64Milli({from}) AND timestamp <= fromUnixTimestamp64Milli({to})"
        ),
        (Some(from), None) => format!("timestamp >= fromUnixTimestamp64Milli({from})"),
        (None, Some(to)) => format!("timestamp <= fromUnixTimestamp64Milli({to})"),
        _ => String::new(),
    }
}

fn viewport_clause(viewport: i32) -> String {
    const BUCKETS: [i32; 7] = [320, 375, 425, 768, 1024, 1440, 1920];
    if let Some(index) = BUCKETS.iter().position(|&bucket| bucket == viewport) {
        if let Some(next) = BUCKETS.get(index + 1) {
            return format!("viewport_w >= {viewport} AND viewport_w < {next}");
        }
        return format!("viewport_w >= {viewport}");
    }
    format!("viewport_w = {viewport}")
}

fn graph_interval(query: &ProductAnalyticsQuery) -> i64 {
    match (query.from, query.to) {
        (Some(from), Some(to)) if to > from => {
            let span = (to - from) / 1000;
            if span <= 24 * 3600 {
                3600
            } else if span <= 7 * 24 * 3600 {
                6 * 3600
            } else {
                24 * 3600
            }
        }
        _ => 24 * 3600,
    }
}

fn filter_clause(
    tracker_id: &str,
    query: &ProductAnalyticsQuery,
    filter: &AnalyticsFilter,
) -> Result<Option<String>> {
    let values = match &filter.value {
        FilterValue::One(value) => vec![value.as_str()],
        FilterValue::Many(values) => values.iter().map(String::as_str).collect(),
    };
    if values.is_empty() {
        return Ok(None);
    }
    if matches!(
        filter.dimension.as_str(),
        "entry_page" | "entry" | "exit_page" | "exit"
    ) {
        let label = if filter.dimension.starts_with("exit") {
            format!("argMax({PAGE_LABEL}, timestamp)")
        } else {
            format!("argMin({PAGE_LABEL}, timestamp)")
        };
        return visit_entry_filter(tracker_id, query, &label, filter.operator.as_str(), &values);
    }
    if let Some(column) = acquisition_entry_expr(filter.dimension.as_str()) {
        return visit_entry_filter(
            tracker_id,
            query,
            &format!("argMin({column}, timestamp)"),
            filter.operator.as_str(),
            &values,
        );
    }
    if matches!(filter.dimension.as_str(), "page") {
        return page_filter_clause(filter.operator.as_str(), &values);
    }
    let column = match filter.dimension.as_str() {
        "pathname" => "pathname",
        "hostname" => "hostname",
        "country" => "country",
        "region" => "region",
        "city" => "city",
        "browser" => "browser",
        "os" => "os",
        "device" => "device",
        "event" => "event_name",
        other => {
            return Err(Error::InvalidRequest(format!(
                "unknown analytics filter {other}"
            )));
        }
    };
    Ok(Some(match filter.operator.as_str() {
        "is" => format!(
            "{column} IN ({})",
            values
                .iter()
                .map(|value| chdb_string(value))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        "is_not" => format!(
            "{column} NOT IN ({})",
            values
                .iter()
                .map(|value| chdb_string(value))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        "contains" => format!(
            "positionCaseInsensitiveUTF8({column}, {}) > 0",
            chdb_string(values[0])
        ),
        "does_not_contain" => format!(
            "positionCaseInsensitiveUTF8({column}, {}) = 0",
            chdb_string(values[0])
        ),
        other => {
            return Err(Error::InvalidRequest(format!(
                "unknown analytics filter operator {other}"
            )));
        }
    }))
}

fn visit_breakdown_label(dimension: &str) -> Option<String> {
    if matches!(dimension, "entry" | "entry_page") {
        return Some(format!("argMin({PAGE_LABEL}, timestamp)"));
    }
    if matches!(dimension, "exit" | "exit_page") {
        return Some(format!("argMax({PAGE_LABEL}, timestamp)"));
    }
    acquisition_entry_expr(dimension).map(|column| format!("argMin({column}, timestamp)"))
}

fn acquisition_entry_expr(dimension: &str) -> Option<&'static str> {
    Some(match dimension {
        "source" | "referrer_source" => "if(referrer_source = '', 'Direct', referrer_source)",
        "channel" => "if(channel = '', 'Direct', channel)",
        "referrer" => "referrer_domain",
        "utm_medium" => "utm_medium",
        "utm_source" => "utm_source",
        "utm_campaign" => "utm_campaign",
        "utm_content" => "utm_content",
        "utm_term" => "utm_term",
        _ => return None,
    })
}

fn visit_entry_filter(
    tracker_id: &str,
    query: &ProductAnalyticsQuery,
    label_expr: &str,
    operator: &str,
    values: &[&str],
) -> Result<Option<String>> {
    let compare = match operator {
        "is" => format!(
            "label IN ({})",
            values
                .iter()
                .map(|value| chdb_string(value))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        "is_not" => format!(
            "label NOT IN ({})",
            values
                .iter()
                .map(|value| chdb_string(value))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        "contains" => format!(
            "positionCaseInsensitiveUTF8(label, {}) > 0",
            chdb_string(values[0])
        ),
        "does_not_contain" => format!(
            "positionCaseInsensitiveUTF8(label, {}) = 0",
            chdb_string(values[0])
        ),
        other => {
            return Err(Error::InvalidRequest(format!(
                "unknown analytics filter operator {other}"
            )));
        }
    };
    let mut inner = vec![
        format!("tracker_id = {}", chdb_string(tracker_id)),
        "event_name = '$pageview'".into(),
        "bot_kind = ''".into(),
    ];
    let time = time_clause(query);
    if !time.is_empty() {
        inner.push(time);
    }
    Ok(Some(format!(
        "if(session_id = '', distinct_id, session_id) IN (\n\
           SELECT visit_id FROM (\n\
             SELECT if(session_id = '', distinct_id, session_id) AS visit_id, {label_expr} AS label\n\
             {EVENTS} WHERE {} GROUP BY visit_id\n\
           ) WHERE {compare}\n\
         )",
        inner.join(" AND ")
    )))
}

fn page_filter_clause(operator: &str, values: &[&str]) -> Result<Option<String>> {
    if matches!(operator, "contains" | "does_not_contain") {
        let needle = chdb_string(values[0]);
        return Ok(Some(if operator == "contains" {
            format!("positionCaseInsensitiveUTF8({PAGE_LABEL}, {needle}) > 0")
        } else {
            format!("positionCaseInsensitiveUTF8({PAGE_LABEL}, {needle}) = 0")
        }));
    }
    let mut hosted = Vec::new();
    let mut paths = Vec::new();
    for value in values {
        if value.starts_with('/') {
            paths.push(*value);
        } else {
            hosted.push(*value);
        }
    }
    let negate = match operator {
        "is" => false,
        "is_not" => true,
        other => {
            return Err(Error::InvalidRequest(format!(
                "unknown analytics filter operator {other}"
            )));
        }
    };
    let mut parts = Vec::new();
    if !hosted.is_empty() {
        parts.push(in_list(PAGE_LABEL, &hosted, negate));
    }
    if !paths.is_empty() {
        parts.push(in_list("pathname", &paths, negate));
    }
    if parts.is_empty() {
        return Ok(None);
    }
    let joiner = if negate { " AND " } else { " OR " };
    if parts.len() == 1 {
        Ok(Some(parts.remove(0)))
    } else {
        Ok(Some(format!("({})", parts.join(joiner))))
    }
}

fn in_list(column: &str, values: &[&str], negate: bool) -> String {
    let list = values
        .iter()
        .map(|value| chdb_string(value))
        .collect::<Vec<_>>()
        .join(", ");
    if negate {
        format!("{column} NOT IN ({list})")
    } else {
        format!("{column} IN ({list})")
    }
}

fn step_condition(step: &FunnelStep) -> String {
    let mut clauses = Vec::new();
    if step.kind == "path" {
        clauses.push(format!(
            "event_name = '$pageview' AND pathname = {}",
            chdb_string(&step.value)
        ));
    } else {
        clauses.push(format!("event_name = {}", chdb_string(&step.value)));
    }
    if let Some(hostname) = step.hostname.as_deref().filter(|value| !value.is_empty()) {
        clauses.push(format!("hostname = {}", chdb_string(hostname)));
    }
    clauses.join(" AND ")
}

fn prop_str(event: &ProductAnalyticsEvent, key: &str) -> String {
    event
        .props_keys
        .iter()
        .position(|item| item == key)
        .and_then(|index| event.props_values.get(index))
        .cloned()
        .unwrap_or_default()
}

fn prop_i64(event: &ProductAnalyticsEvent, key: &str) -> Option<i64> {
    prop_str(event, key).parse().ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tempfile::TempDir;

    fn event(name: &str, distinct: &str, path: &str, timestamp: &str) -> ProductAnalyticsEvent {
        serde_json::from_value(json!({
            "event_id": format!("{name}-{distinct}-{path}-{timestamp}"),
            "tracker_id": "tracker",
            "distinct_id": distinct,
            "session_id": "sid-1",
            "event_name": name,
            "timestamp": timestamp,
            "hostname": "shop.example",
            "pathname": path,
        }))
        .unwrap()
    }

    fn query(report: &str, from: i64, to: i64) -> ProductAnalyticsQuery {
        ProductAnalyticsQuery {
            report: report.into(),
            from: Some(from),
            to: Some(to),
            filters: vec![],
            dimension: None,
            pathname: None,
            viewport: None,
            event_type: None,
            steps: None,
            window_seconds: None,
            sql: None,
            experiment_id: None,
            flag: None,
            metric_event: None,
            metric_path: None,
            metric_hostname: None,
        }
    }

    #[tokio::test]
    async fn ingests_rfc3339_browser_events_and_reports() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        store
            .ingest_product_analytics(event("$pageview", "aid-1", "/", "2026-08-01T12:00:00.000Z"))
            .await
            .unwrap();
        store
            .ingest_product_analytics(event(
                "$pageview",
                "aid-1",
                "/pricing",
                "2026-08-01T12:01:00.000Z",
            ))
            .await
            .unwrap();
        let mut dash = event("$pageview", "aid-2", "/", "2026-08-01T12:00:10.000Z");
        dash.session_id = "sid-2".into();
        dash.hostname = "dash.example".into();
        store.ingest_product_analytics(dash).await.unwrap();
        let mut heatmap = event("$heatmap", "aid-1", "/", "2026-08-01T12:02:00.000Z");
        heatmap.props_keys = vec![
            "x".into(),
            "y".into(),
            "event_type".into(),
            "viewport_w".into(),
        ];
        heatmap.props_values = vec!["48".into(), "32".into(), "click".into(), "1500".into()];
        store.ingest_product_analytics(heatmap).await.unwrap();
        store
            .ingest_product_analytics(event(
                "$pageleave",
                "aid-1",
                "/pricing",
                "2026-08-01T12:04:00.000Z",
            ))
            .await
            .unwrap();

        let from = 1_785_542_400_000; // 2026-08-01T00:00:00Z
        let to = 1_785_715_200_000; // 2026-08-03T00:00:00Z
        let overview = store
            .query_product_analytics("tracker".into(), query("overview", from, to))
            .await
            .unwrap();
        let current = overview["current"][0]["visitors"].as_u64().unwrap_or(0);
        assert_eq!(current, 2, "heatmap must not inflate visitors: {overview}");

        let mut events_lookup = query("lookup", from, to);
        events_lookup.dimension = Some("event".into());
        let event_labels = store
            .query_product_analytics("tracker".into(), events_lookup)
            .await
            .unwrap();
        assert_eq!(
            event_labels[0]["label"], "$pageview",
            "lookup must hide $heatmap: {event_labels}"
        );
        assert_eq!(event_labels.as_array().map(Vec::len).unwrap_or(0), 1);

        let mut entry = query("breakdown", from, to);
        entry.dimension = Some("entry".into());
        let entry_rows = store
            .query_product_analytics("tracker".into(), entry)
            .await
            .unwrap();
        let entry_labels: Vec<&str> = entry_rows
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|row| row["label"].as_str())
            .collect();
        assert!(
            entry_labels.contains(&"shop.example/") && entry_labels.contains(&"dash.example/"),
            "entry = {entry_rows}"
        );

        let mut pages = query("breakdown", from, to);
        pages.dimension = Some("page".into());
        let page_rows = store
            .query_product_analytics("tracker".into(), pages)
            .await
            .unwrap();
        let page_labels: Vec<&str> = page_rows
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|row| row["label"].as_str())
            .collect();
        assert!(
            page_labels.contains(&"shop.example/")
                && page_labels.contains(&"dash.example/")
                && page_labels.contains(&"shop.example/pricing"),
            "pages = {page_rows}"
        );

        let mut hosted = query("overview", from, to);
        hosted.filters = vec![AnalyticsFilter {
            dimension: "page".into(),
            operator: "is".into(),
            value: FilterValue::One("dash.example/".into()),
        }];
        let hosted_overview = store
            .query_product_analytics("tracker".into(), hosted)
            .await
            .unwrap();
        assert_eq!(
            hosted_overview["current"][0]["visitors"]
                .as_u64()
                .unwrap_or(0),
            1,
            "host page filter = {hosted_overview}"
        );

        let mut path_only = query("overview", from, to);
        path_only.filters = vec![AnalyticsFilter {
            dimension: "page".into(),
            operator: "is".into(),
            value: FilterValue::One("/pricing".into()),
        }];
        let path_overview = store
            .query_product_analytics("tracker".into(), path_only)
            .await
            .unwrap();
        assert_eq!(
            path_overview["current"][0]["visitors"]
                .as_u64()
                .unwrap_or(0),
            1,
            "path-only page filter = {path_overview}"
        );

        let paths = store
            .query_product_analytics("tracker".into(), query("paths", from, to))
            .await
            .unwrap();
        assert_eq!(paths[0]["from_path"], "shop.example/");
        assert_eq!(paths[0]["to_path"], "shop.example/pricing");

        let mut heat = query("heatmap", from, to);
        heat.pathname = Some("/".into());
        heat.event_type = Some("click".into());
        heat.viewport = Some(1440);
        heat.filters = vec![AnalyticsFilter {
            dimension: "hostname".into(),
            operator: "is".into(),
            value: FilterValue::One("shop.example".into()),
        }];
        let heat_rows = store
            .query_product_analytics("tracker".into(), heat)
            .await
            .unwrap();
        assert_eq!(
            heat_rows[0]["hits"].as_u64().unwrap_or(0),
            1,
            "heatmap = {heat_rows}"
        );

        let mut unknown = query("overview", from, to);
        unknown.filters = vec![AnalyticsFilter {
            dimension: "nope".into(),
            operator: "is".into(),
            value: FilterValue::One("x".into()),
        }];
        let err = store
            .query_product_analytics("tracker".into(), unknown)
            .await
            .expect_err("unknown filters must fail");
        assert!(
            err.to_string().contains("unknown analytics filter"),
            "err = {err}"
        );
    }

    #[tokio::test]
    async fn counts_experiment_conversions_past_range_end() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut exposure = event("$flag_called", "aid-exp", "/", "2026-08-01T12:00:00.000Z");
        exposure.props_keys = vec!["flag".into(), "variant".into(), "experiment_id".into()];
        exposure.props_values = vec!["pricing-v2".into(), "true".into(), "exp-1".into()];
        store.ingest_product_analytics(exposure).await.unwrap();
        store
            .ingest_product_analytics(event(
                "signup_completed",
                "aid-exp",
                "/thanks",
                "2026-08-04T12:00:00.000Z",
            ))
            .await
            .unwrap();
        let from = 1_785_542_400_000; // 2026-08-01T00:00:00Z
        let to = 1_785_715_200_000; // 2026-08-03T00:00:00Z
        let mut experiment = query("experiment", from, to);
        experiment.flag = Some("pricing-v2".into());
        experiment.metric_event = Some("signup_completed".into());
        experiment.window_seconds = Some(14 * 24 * 3600);
        experiment.experiment_id = Some("exp-1".into());
        let rows = store
            .query_product_analytics("tracker".into(), experiment)
            .await
            .unwrap();
        assert_eq!(rows[0]["variant"], "true", "rows = {rows}");
        assert_eq!(rows[0]["exposed"].as_u64().unwrap_or(0), 1, "rows = {rows}");
        assert_eq!(
            rows[0]["converted"].as_u64().unwrap_or(0),
            1,
            "conversion inside the experiment window but after report to must count: {rows}"
        );

        let mut renamed = query("experiment", from, to);
        renamed.flag = Some("renamed-key".into());
        renamed.metric_event = Some("signup_completed".into());
        renamed.window_seconds = Some(14 * 24 * 3600);
        renamed.experiment_id = Some("exp-1".into());
        let renamed_rows = store
            .query_product_analytics("tracker".into(), renamed)
            .await
            .unwrap();
        assert_eq!(
            renamed_rows[0]["exposed"].as_u64().unwrap_or(0),
            1,
            "experiment_id must find exposures after a flag key rename: {renamed_rows}"
        );
    }

    #[tokio::test]
    async fn acquisition_filters_use_visit_entry() {
        let volume = TempDir::new().unwrap();
        let store = Store::open(volume.path().to_owned()).await.unwrap();
        let mut landing = event("$pageview", "aid-utm", "/", "2026-08-01T13:00:00.000Z");
        landing.session_id = "sid-utm".into();
        landing.utm_source = "google".into();
        store.ingest_product_analytics(landing).await.unwrap();
        let mut later = event(
            "$pageview",
            "aid-utm",
            "/pricing",
            "2026-08-01T13:01:00.000Z",
        );
        later.session_id = "sid-utm".into();
        store.ingest_product_analytics(later).await.unwrap();
        let mut organic = event("$pageview", "aid-org", "/", "2026-08-01T13:00:00.000Z");
        organic.session_id = "sid-org".into();
        store.ingest_product_analytics(organic).await.unwrap();
        let mut organic_later = event(
            "$pageview",
            "aid-org",
            "/pricing",
            "2026-08-01T13:01:00.000Z",
        );
        organic_later.session_id = "sid-org".into();
        store.ingest_product_analytics(organic_later).await.unwrap();

        let from = 1_785_542_400_000;
        let to = 1_785_715_200_000;
        let utm = AnalyticsFilter {
            dimension: "utm_source".into(),
            operator: "is".into(),
            value: FilterValue::One("google".into()),
        };
        let mut overview = query("overview", from, to);
        overview.filters = vec![utm.clone()];
        let overview_rows = store
            .query_product_analytics("tracker".into(), overview)
            .await
            .unwrap();
        assert_eq!(
            overview_rows["current"][0]["visitors"]
                .as_u64()
                .unwrap_or(0),
            1,
            "overview visitors = {overview_rows}"
        );
        assert_eq!(
            overview_rows["current"][0]["pageviews"]
                .as_u64()
                .unwrap_or(0),
            2,
            "later pageviews without utm must stay in the visit: {overview_rows}"
        );

        let mut funnel = query("funnel", from, to);
        funnel.filters = vec![utm];
        funnel.steps = Some(vec![
            FunnelStep {
                kind: "path".into(),
                value: "/".into(),
                hostname: None,
            },
            FunnelStep {
                kind: "path".into(),
                value: "/pricing".into(),
                hostname: None,
            },
        ]);
        let funnel_rows = store
            .query_product_analytics("tracker".into(), funnel)
            .await
            .unwrap();
        let reached = funnel_rows
            .as_array()
            .unwrap()
            .iter()
            .find(|row| row["level"].as_u64() == Some(2))
            .map(|row| row["visitors"].as_u64().unwrap_or(0))
            .unwrap_or(0);
        assert_eq!(
            reached, 1,
            "utm funnel must count the later untagged step: {funnel_rows}"
        );
    }
}
