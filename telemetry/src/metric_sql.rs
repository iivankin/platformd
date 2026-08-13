use std::collections::HashSet;
use std::ops::ControlFlow;

use serde::Serialize;
use serde_json::Value;
use sqlparser::ast::{ObjectName, ObjectNamePart, Query, Select, TableFactor, Visit, Visitor};
use sqlparser::dialect::GenericDialect;
use sqlparser::parser::Parser;

use crate::error::{Error, Result};

pub(crate) const MAX_METRIC_QUERY_BYTES: usize = 16 << 10;
pub(crate) const MAX_METRIC_QUERY_ROWS: usize = 4_000;

#[derive(Clone, Debug)]
pub(crate) struct MetricSqlQuery {
    pub(crate) sql: String,
    pub(crate) from_unix_nano: u64,
    pub(crate) to_unix_nano: u64,
    pub(crate) step_nano: u64,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct MetricSqlRow {
    #[serde(serialize_with = "serialize_u64_string")]
    pub(crate) time_unix_nano: u64,
    pub(crate) value: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(crate) series: Option<String>,
}

impl MetricSqlQuery {
    pub(crate) fn compile(&self, service_ids: &[String]) -> Result<String> {
        self.validate_range()?;
        let user_query = validate_sql(&self.sql)?;
        let source = logical_metrics_source(
            service_ids,
            self.from_unix_nano,
            self.to_unix_nano,
            self.step_nano,
        );
        Ok(format!(
            "WITH metrics AS ({source}) \
             SELECT * FROM ({user_query}) AS metric_chart_result LIMIT {} \
             SETTINGS max_execution_time=5, max_memory_usage=268435456, max_threads=2",
            MAX_METRIC_QUERY_ROWS + 1
        ))
    }

    fn validate_range(&self) -> Result<()> {
        if self.from_unix_nano == 0
            || self.to_unix_nano < self.from_unix_nano
            || self.step_nano < 1_000_000_000
            || (self.to_unix_nano - self.from_unix_nano) / self.step_nano > 2_000
        {
            return Err(Error::InvalidRequest("invalid metric query range".into()));
        }
        Ok(())
    }
}

pub(crate) fn decode_rows(output: &str) -> Result<Vec<MetricSqlRow>> {
    let mut rows = Vec::new();
    for line in output.lines().filter(|line| !line.is_empty()) {
        let row: Value = serde_json::from_str(line)
            .map_err(|error| Error::Storage(format!("decode chDB metric SQL row: {error}")))?;
        let object = row
            .as_object()
            .ok_or_else(|| Error::InvalidRequest("metric SQL must return object rows".into()))?;
        let time_unix_nano = object
            .get("time")
            .and_then(json_u64)
            .ok_or_else(|| Error::InvalidRequest("metric SQL must return UInt64 time".into()))?;
        let Some(value) = object.get("value") else {
            return Err(Error::InvalidRequest(
                "metric SQL must return numeric value".into(),
            ));
        };
        if value.is_null() {
            continue;
        }
        let value = json_f64(value)
            .filter(|value| value.is_finite())
            .ok_or_else(|| {
                Error::InvalidRequest("metric SQL must return finite numeric value".into())
            })?;
        let series = object
            .get("series")
            .filter(|value| !value.is_null())
            .map(display_series)
            .transpose()?;
        rows.push(MetricSqlRow {
            time_unix_nano,
            value,
            series,
        });
    }
    if rows.len() > MAX_METRIC_QUERY_ROWS {
        return Err(Error::InvalidRequest(format!(
            "metric SQL returned more than {MAX_METRIC_QUERY_ROWS} rows"
        )));
    }
    Ok(rows)
}

fn validate_sql(sql: &str) -> Result<String> {
    let sql = sql.trim();
    if sql.is_empty() || sql.len() > MAX_METRIC_QUERY_BYTES || sql.contains('\0') {
        return Err(Error::InvalidRequest("invalid metric SQL".into()));
    }
    let mut statements = Parser::parse_sql(&GenericDialect {}, sql)
        .map_err(|error| Error::InvalidRequest(format!("invalid metric SQL: {error}")))?;
    if statements.len() != 1 {
        return Err(Error::InvalidRequest(
            "metric SQL must contain one SELECT query".into(),
        ));
    }
    let sqlparser::ast::Statement::Query(query) = statements.remove(0) else {
        return Err(Error::InvalidRequest(
            "metric SQL must contain one SELECT query".into(),
        ));
    };
    let mut validator = MetricSqlValidator::default();
    if let ControlFlow::Break(message) = query.visit(&mut validator) {
        return Err(Error::InvalidRequest(message));
    }
    if !validator.reads_metrics {
        return Err(Error::InvalidRequest(
            "metric SQL must read from metrics".into(),
        ));
    }
    Ok(query.to_string())
}

#[derive(Default)]
struct MetricSqlValidator {
    cte_scopes: Vec<HashSet<String>>,
    reads_metrics: bool,
}

impl Visitor for MetricSqlValidator {
    type Break = String;

    fn pre_visit_query(&mut self, query: &Query) -> ControlFlow<Self::Break> {
        if query.settings.is_some()
            || query.format_clause.is_some()
            || !query.locks.is_empty()
            || query.for_clause.is_some()
            || !query.pipe_operators.is_empty()
            || query.with.as_ref().is_some_and(|with| with.recursive)
        {
            return ControlFlow::Break("unsupported metric SQL clause".into());
        }
        if let Some(with) = query.with.as_ref() {
            let mut scope = HashSet::new();
            for cte in &with.cte_tables {
                let name = cte.alias.name.value.to_ascii_lowercase();
                if matches!(name.as_str(), "documents" | "logs" | "metrics" | "spans")
                    || !scope.insert(name)
                {
                    return ControlFlow::Break("invalid metric SQL CTE name".into());
                }
            }
            self.cte_scopes.push(scope);
        } else {
            self.cte_scopes.push(HashSet::new());
        }
        ControlFlow::Continue(())
    }

    fn post_visit_query(&mut self, _query: &Query) -> ControlFlow<Self::Break> {
        self.cte_scopes.pop();
        ControlFlow::Continue(())
    }

    fn pre_visit_select(&mut self, select: &Select) -> ControlFlow<Self::Break> {
        if select.into.is_some()
            || !select.optimizer_hints.is_empty()
            || !select.lateral_views.is_empty()
        {
            return ControlFlow::Break("unsupported metric SQL SELECT clause".into());
        }
        ControlFlow::Continue(())
    }

    fn pre_visit_table_factor(&mut self, table: &TableFactor) -> ControlFlow<Self::Break> {
        match table {
            TableFactor::Table {
                args,
                with_hints,
                version,
                with_ordinality,
                partitions,
                json_path,
                sample,
                index_hints,
                ..
            } if args.is_none()
                && with_hints.is_empty()
                && version.is_none()
                && !with_ordinality
                && partitions.is_empty()
                && json_path.is_none()
                && sample.is_none()
                && index_hints.is_empty() =>
            {
                ControlFlow::Continue(())
            }
            TableFactor::Derived {
                lateral: false,
                sample: None,
                ..
            }
            | TableFactor::NestedJoin { .. } => ControlFlow::Continue(()),
            _ => ControlFlow::Break("metric SQL table functions are not allowed".into()),
        }
    }

    fn pre_visit_relation(&mut self, relation: &ObjectName) -> ControlFlow<Self::Break> {
        let [ObjectNamePart::Identifier(identifier)] = relation.0.as_slice() else {
            return ControlFlow::Break("metric SQL may only read the metrics table".into());
        };
        let name = identifier.value.to_ascii_lowercase();
        if name == "metrics" {
            self.reads_metrics = true;
            return ControlFlow::Continue(());
        }
        if self
            .cte_scopes
            .iter()
            .rev()
            .any(|scope| scope.contains(&name))
        {
            return ControlFlow::Continue(());
        }
        ControlFlow::Break("metric SQL may only read the metrics table".into())
    }
}

fn logical_metrics_source(service_ids: &[String], from: u64, to: u64, step: u64) -> String {
    let scan_from = from.saturating_sub(step);
    let service_filter = service_filter(service_ids);
    let stream = "service_id, name, kind, attribute_keys, attribute_values, resource, scope";
    let empty_f64 = "CAST([], 'Array(Float64)')";
    let empty_u64 = "CAST([], 'Array(UInt64)')";
    let sample_values = format!(
        "multiIf(\
           kind = 'histogram', arrayMap(i -> multiIf(\
             i = 1, explicit_bounds[1],\
             i <= length(explicit_bounds), (explicit_bounds[i - 1] + explicit_bounds[i]) / 2,\
             explicit_bounds[-1]), arrayEnumerate(bucket_counts)),\
           kind = 'exponential_histogram', arrayConcat(\
             arrayMap(i -> -pow(pow(2., pow(2., -toFloat64(exponential_scale))),\
               toFloat64(negative_offset + toInt64(i) - 1) + 0.5), arrayEnumerate(negative_counts)),\
             [0.],\
             arrayMap(i -> pow(pow(2., pow(2., -toFloat64(exponential_scale))),\
               toFloat64(positive_offset + toInt64(i) - 1) + 0.5), arrayEnumerate(positive_counts))),\
           {empty_f64})"
    );
    let sample_counts = format!(
        "multiIf(\
           kind = 'histogram', bucket_counts,\
           kind = 'exponential_histogram', arrayConcat(negative_counts, [zero_count], positive_counts),\
           {empty_u64})"
    );
    format!(
        "SELECT \
           service_id,\
           time_unix_nano AS timestamp,\
           {from} + intDiv(time_unix_nano - {from}, {step}) * {step} AS bucket,\
           {step} / 1000000000. AS step_seconds,\
           name, description, unit, kind, start_time_unix_nano,\
           mapFromArrays(attribute_keys, attribute_values) AS attributes,\
           scalar_value AS value,\
           if(kind = 'sum' AND scalar_value IS NOT NULL,\
             if(aggregation_temporality = 1, scalar_value,\
               if(NOT is_monotonic OR scalar_value >= previous_value, scalar_value - previous_value, greatest(scalar_value, 0))),\
             NULL) AS delta,\
           count,\
           if(count IS NULL, NULL, if(aggregation_temporality = 1, count,\
             if(count >= previous_count, count - previous_count, count))) AS count_delta,\
           sum,\
           if(sum IS NULL, NULL, if(aggregation_temporality = 1, sum,\
             if(count >= previous_count, sum - previous_sum, sum))) AS sum_delta,\
           min, max, sample_values AS histogram_values, sample_counts AS histogram_counts,\
           if(aggregation_temporality = 1 OR length(sample_counts) != length(previous_counts),\
             sample_counts, arrayMap((current, previous) ->\
               if(current >= previous, toUInt64(current - previous), current), sample_counts, previous_counts))\
             AS histogram_delta_counts,\
           aggregation_temporality, is_monotonic \
         FROM (\
           SELECT *,\
             lagInFrame(scalar_value, 1, scalar_value) OVER (PARTITION BY {stream} \
               ORDER BY time_unix_nano, id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS previous_value,\
             lagInFrame(count, 1, count) OVER (PARTITION BY {stream} \
               ORDER BY time_unix_nano, id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS previous_count,\
             lagInFrame(sum, 1, sum) OVER (PARTITION BY {stream} \
               ORDER BY time_unix_nano, id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS previous_sum,\
             lagInFrame(sample_counts, 1, sample_counts) OVER (PARTITION BY {stream} \
               ORDER BY time_unix_nano, id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS previous_counts \
           FROM (\
             SELECT *, coalesce(value_double, toFloat64(value_int)) AS scalar_value,\
               {sample_values} AS sample_values, {sample_counts} AS sample_counts \
             FROM telemetry.metrics \
             WHERE {service_filter} AND time_unix_nano BETWEEN {scan_from} AND {to}\
           )\
         ) WHERE time_unix_nano >= {from}"
    )
}

pub(crate) fn service_filter(service_ids: &[String]) -> String {
    if service_ids.is_empty() {
        return "0".into();
    }
    let values = service_ids
        .iter()
        .map(|service_id| chdb_string(service_id))
        .collect::<Vec<_>>()
        .join(", ");
    format!("service_id IN ({values})")
}

fn chdb_string(value: &str) -> String {
    format!("'{}'", value.replace('\\', "\\\\").replace('\'', "\\'"))
}

fn json_u64(value: &Value) -> Option<u64> {
    value
        .as_u64()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn json_f64(value: &Value) -> Option<f64> {
    value
        .as_f64()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn display_series(value: &Value) -> Result<String> {
    let value = match value {
        Value::String(value) => value.clone(),
        Value::Number(value) => value.to_string(),
        Value::Bool(value) => value.to_string(),
        _ => {
            return Err(Error::InvalidRequest(
                "metric SQL series must be a scalar value".into(),
            ));
        }
    };
    if value.len() > 256 {
        return Err(Error::InvalidRequest(
            "metric SQL series is too long".into(),
        ));
    }
    Ok(value)
}

fn serialize_u64_string<S>(value: &u64, serializer: S) -> std::result::Result<S::Ok, S::Error>
where
    S: serde::Serializer,
{
    serializer.serialize_str(&value.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_composed_metric_queries() {
        let sql = "WITH totals AS ( \
          SELECT bucket, sumIf(delta, name = 'errors') AS error_count, \
            sumIf(delta, name = 'requests') AS request_count \
          FROM metrics GROUP BY bucket) \
          SELECT bucket AS time, error_count / nullIf(request_count, 0) AS value FROM totals";
        let result = validate_sql(sql);
        assert!(result.is_ok(), "{result:?}");
    }

    #[test]
    fn rejects_physical_tables_and_table_functions() {
        for sql in [
            "SELECT 1 AS time, count() AS value FROM telemetry.metrics",
            "SELECT 1 AS time, count() AS value FROM system.tables",
            "SELECT 1 AS time, count() AS value FROM file('/etc/passwd')",
            "SELECT 1 AS time, count() AS value",
            "SELECT 1; SELECT 2",
            "WITH logs AS (SELECT * FROM logs) SELECT bucket AS time, value FROM metrics",
            "SELECT nested.time, nested.value FROM (WITH totals AS (SELECT bucket AS time, sum(value) AS value FROM metrics GROUP BY bucket) SELECT * FROM totals) AS nested CROSS JOIN totals",
        ] {
            assert!(validate_sql(sql).is_err(), "accepted {sql}");
        }
    }
}
