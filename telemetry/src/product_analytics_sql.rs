use std::collections::HashSet;
use std::ops::ControlFlow;

use sqlparser::ast::{ObjectName, ObjectNamePart, Query, Select, TableFactor, Visit, Visitor};
use sqlparser::dialect::GenericDialect;
use sqlparser::parser::Parser;

use crate::error::{Error, Result};
use crate::metric_sql::MAX_METRIC_QUERY_ROWS;
use crate::storage::chdb_string;

pub(crate) const ANALYTICS_EVENTS: &str = "FROM telemetry.analytics_events";
pub(crate) const INTERNAL_EVENTS: &str = "event_name != '$flag_called'";
pub(crate) const VISIT_EVENTS: &str = "event_name NOT IN ('$flag_called', '$pageleave')";

pub fn compile(
    sql: &str,
    tracker_id: &str,
    from: Option<i64>,
    to: Option<i64>,
    extra: &str,
) -> Result<String> {
    let user_query = validate_sql(sql)?;
    let mut time = String::new();
    if let Some(from) = from {
        time.push_str(&format!(
            " AND timestamp >= fromUnixTimestamp64Milli({from})"
        ));
    }
    if let Some(to) = to {
        time.push_str(&format!(" AND timestamp <= fromUnixTimestamp64Milli({to})"));
    }
    if !extra.is_empty() {
        time.push_str(" AND ");
        time.push_str(extra);
    }
    let source = format!(
        "SELECT toUnixTimestamp64Milli(timestamp) AS time, 1. AS value, event_name AS series,\n\
         tracker_id, distinct_id, session_id, event_name, hostname, pathname, referrer_source,\n\
         channel, country, device, browser, os, bot_kind\n\
         {ANALYTICS_EVENTS} \
         WHERE tracker_id = {} AND bot_kind = '' AND {VISIT_EVENTS}{time}",
        chdb_string(tracker_id)
    );
    Ok(format!(
        "WITH analytics AS ({source}) SELECT * FROM ({user_query}) AS analytics_chart_result LIMIT {} \
         SETTINGS max_execution_time=5, max_memory_usage=268435456, max_threads=2",
        MAX_METRIC_QUERY_ROWS + 1
    ))
}

fn validate_sql(sql: &str) -> Result<String> {
    let sql = sql.trim();
    if sql.is_empty() || sql.len() > crate::metric_sql::MAX_METRIC_QUERY_BYTES || sql.contains('\0')
    {
        return Err(Error::InvalidRequest("invalid analytics SQL".into()));
    }
    let mut statements = Parser::parse_sql(&GenericDialect {}, sql)
        .map_err(|error| Error::InvalidRequest(format!("invalid analytics SQL: {error}")))?;
    if statements.len() != 1 {
        return Err(Error::InvalidRequest(
            "analytics SQL must contain one SELECT query".into(),
        ));
    }
    let sqlparser::ast::Statement::Query(query) = statements.remove(0) else {
        return Err(Error::InvalidRequest(
            "analytics SQL must contain one SELECT query".into(),
        ));
    };
    let mut validator = AnalyticsSqlValidator::default();
    if let ControlFlow::Break(message) = query.visit(&mut validator) {
        return Err(Error::InvalidRequest(message));
    }
    if !validator.reads_analytics {
        return Err(Error::InvalidRequest(
            "analytics SQL must read from analytics".into(),
        ));
    }
    Ok(query.to_string())
}

#[derive(Default)]
struct AnalyticsSqlValidator {
    cte_scopes: Vec<HashSet<String>>,
    reads_analytics: bool,
}

impl Visitor for AnalyticsSqlValidator {
    type Break = String;

    fn pre_visit_query(&mut self, query: &Query) -> ControlFlow<Self::Break> {
        if query.settings.is_some()
            || query.format_clause.is_some()
            || !query.locks.is_empty()
            || query.for_clause.is_some()
            || !query.pipe_operators.is_empty()
            || query.with.as_ref().is_some_and(|with| with.recursive)
        {
            return ControlFlow::Break("unsupported analytics SQL clause".into());
        }
        if let Some(with) = query.with.as_ref() {
            let mut scope = HashSet::new();
            for cte in &with.cte_tables {
                let name = cte.alias.name.value.to_ascii_lowercase();
                if matches!(
                    name.as_str(),
                    "documents" | "logs" | "metrics" | "spans" | "analytics" | "analytics_events"
                ) || !scope.insert(name)
                {
                    return ControlFlow::Break("invalid analytics SQL CTE name".into());
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
            return ControlFlow::Break("unsupported analytics SQL SELECT clause".into());
        }
        ControlFlow::Continue(())
    }

    fn pre_visit_table_factor(&mut self, table: &TableFactor) -> ControlFlow<Self::Break> {
        match table {
            TableFactor::Table {
                args: None,
                with_hints,
                version,
                with_ordinality: false,
                partitions,
                json_path: None,
                sample: None,
                index_hints,
                ..
            } if with_hints.is_empty()
                && version.is_none()
                && partitions.is_empty()
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
            _ => ControlFlow::Break("analytics SQL table functions are not allowed".into()),
        }
    }

    fn pre_visit_relation(&mut self, relation: &ObjectName) -> ControlFlow<Self::Break> {
        let [ObjectNamePart::Identifier(identifier)] = relation.0.as_slice() else {
            return ControlFlow::Break("analytics SQL may only read the analytics table".into());
        };
        let name = identifier.value.to_ascii_lowercase();
        if name == "analytics" {
            self.reads_analytics = true;
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
        ControlFlow::Break("analytics SQL may only read the analytics table".into())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn accepts_analytics_table_queries() {
        let compiled = compile(
            "SELECT time, count() AS value FROM analytics GROUP BY time",
            "tracker",
            Some(1),
            Some(2),
            "",
        );
        assert!(compiled.is_ok(), "{compiled:?}");
        let sql = compiled.unwrap();
        assert!(sql.contains("WITH analytics AS"));
        assert!(sql.contains("tracker_id = "));
        assert!(sql.contains("$pageleave"));
    }

    #[test]
    fn rejects_physical_tables() {
        for sql in [
            "SELECT time, value FROM telemetry.analytics_events",
            "SELECT time, value FROM metrics",
            "WITH analytics AS (SELECT 1 AS time, 1 AS value) SELECT time, value FROM analytics",
            "SELECT 1 AS time, 1 AS value FROM file('/etc/passwd')",
            "SELECT 1; SELECT 2",
        ] {
            assert!(
                compile(sql, "tracker", None, None, "").is_err(),
                "accepted {sql}"
            );
        }
    }
}
