package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

func metricReadTools() []Tool {
	scope := metricScopeSchema()
	query := copyProperties(scope)
	query["sql"] = map[string]any{
		"type": "string", "minLength": 1, "maxLength": 16 << 10,
		"description": "One restricted SELECT over virtual table metrics. Return UInt64 time and numeric value aliases; series is optional. Available columns: service_id, timestamp, bucket, step_seconds, name, description, unit, kind, start_time_unix_nano, attributes, value, delta, count, count_delta, sum, sum_delta, min, max, histogram_values, histogram_counts, histogram_delta_counts, aggregation_temporality, is_monotonic.",
	}
	query["from"] = map[string]any{"type": "integer", "minimum": 1, "description": "Inclusive Unix timestamp in milliseconds"}
	query["to"] = map[string]any{"type": "integer", "minimum": 1, "description": "Inclusive Unix timestamp in milliseconds"}
	query["step"] = map[string]any{"type": "integer", "minimum": 1_000, "description": "Bucket width in milliseconds; at least 1000 and no more than 2000 buckets across the range"}
	return []Tool{
		{Name: "get_metric_catalog", Description: "Call before query_metrics. Lists metrics actually observed in the selected scope, including kind, unit, attribute keys, and last-seen time so SQL does not have to guess names or dimensions.", InputSchema: objectSchema(scope, []string{"scope"})},
		{Name: "query_metrics", Description: "Validate and execute one bounded, read-only ClickHouse SELECT over platformd's virtual metrics table. The service/project scope is injected by the server; SQL cannot access other tables. Returns chart points as timeUnixNano, value, and optional series.", InputSchema: objectSchema(query, []string{"scope", "sql", "from", "to", "step"})},
		{Name: "list_metric_charts", Description: "List saved custom metric charts in exactly one service, project, or installation scope. Use a chart's updatedAt as expectedUpdatedAt when updating it.", InputSchema: objectSchema(scope, []string{"scope"})},
	}
}

func metricAdminTools() []Tool {
	chart := copyProperties(metricScopeSchema())
	chart["title"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 80}
	chart["sql"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 16 << 10, "description": "A query already validated with query_metrics; it must return time, value, and optional series"}
	chart["visualization"] = map[string]any{"type": "string", "enum": []string{"line", "area", "bar", "value"}}
	chart["legend"] = map[string]any{"type": "string", "maxLength": 80}
	chart["unit"] = map[string]any{"type": "string", "maxLength": 32}
	update := copyProperties(chart)
	update["chartId"] = map[string]any{"type": "string"}
	update["expectedUpdatedAt"] = map[string]any{"type": "integer", "minimum": 1}
	remove := copyProperties(metricScopeSchema())
	remove["chartId"] = map[string]any{"type": "string"}
	return []Tool{
		{Name: "create_metric_chart", Description: "Save a custom metric chart after its SQL succeeds in query_metrics for the same scope. Requires an admin token.", InputSchema: objectSchema(chart, []string{"scope", "title", "sql", "visualization", "legend"})},
		{Name: "update_metric_chart", Description: "Replace a saved chart definition. Copy chartId and expectedUpdatedAt from list_metric_charts and validate changed SQL first. Requires an admin token.", InputSchema: objectSchema(update, []string{"scope", "chartId", "title", "sql", "visualization", "legend", "expectedUpdatedAt"})},
		{Name: "delete_metric_chart", Description: "Permanently delete a saved chart by the ID from list_metric_charts. Requires an admin token.", InputSchema: objectSchema(remove, []string{"scope", "chartId"})},
	}
}

func metricScopeSchema() map[string]any {
	return map[string]any{
		"scope": map[string]any{
			"type": "string", "enum": []string{"service", "project", "installation"},
			"description": "service requires projectId+serviceId; project requires only projectId; installation requires neither and is available only to an unbound admin token",
		},
		"projectId": map[string]any{"type": "string", "description": "Exact project ID from list_projects; omit only for installation scope"},
		"serviceId": map[string]any{"type": "string", "description": "Exact service ID from list_services; set only for service scope"},
	}
}

func copyProperties(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

type metricArguments struct {
	Scope             string `json:"scope"`
	ProjectID         string `json:"projectId"`
	ServiceID         string `json:"serviceId"`
	SQL               string `json:"sql"`
	From              int64  `json:"from"`
	To                int64  `json:"to"`
	Step              int64  `json:"step"`
	ChartID           string `json:"chartId"`
	Title             string `json:"title"`
	Visualization     string `json:"visualization"`
	Legend            string `json:"legend"`
	Unit              string `json:"unit"`
	ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
}

func (handler *Handler) callMetricTool(ctx context.Context, name string, raw json.RawMessage, identity automation.Identity) (any, error) {
	var input metricArguments
	if err := decodeArguments(raw, &input); err != nil {
		return nil, err
	}
	scope, err := handler.metricScope(ctx, input, identity)
	if err != nil {
		return nil, err
	}
	switch name {
	case "get_metric_catalog":
		return handler.queryMetricScope(ctx, scope, "catalog", nil)
	case "query_metrics":
		input.SQL = strings.TrimSpace(input.SQL)
		if input.SQL == "" || len(input.SQL) > 16<<10 || input.From <= 0 || input.To < input.From ||
			input.Step < 1_000 || (input.To-input.From)/input.Step > 2_000 {
			return nil, fmt.Errorf("%w: metric SQL and time range are invalid", errInvalidArguments)
		}
		query, err := json.Marshal(map[string]any{"sql": input.SQL, "from": input.From, "to": input.To, "step": input.Step})
		if err != nil {
			return nil, err
		}
		return handler.queryMetricScope(ctx, scope, "query", query)
	case "list_metric_charts":
		charts, err := handler.telemetry.MetricCharts(ctx, scope)
		if err != nil {
			return nil, err
		}
		return publicMetricCharts(charts), nil
	case "create_metric_chart":
		chart, err := metricChart(input, scope)
		if err != nil || input.ChartID != "" || input.ExpectedUpdatedAt != 0 {
			return nil, fmt.Errorf("%w: metric chart fields are invalid", errInvalidArguments)
		}
		created, err := handler.telemetry.CreateMetricChart(ctx, chart)
		if err != nil {
			return nil, err
		}
		return publicMetricChart(created), nil
	case "update_metric_chart":
		chart, err := metricChart(input, scope)
		if err != nil || input.ChartID == "" || input.ExpectedUpdatedAt <= 0 {
			return nil, fmt.Errorf("%w: metric chart fields are invalid", errInvalidArguments)
		}
		chart.ID = input.ChartID
		updated, err := handler.telemetry.UpdateMetricChart(ctx, chart, input.ExpectedUpdatedAt)
		if err != nil {
			return nil, err
		}
		return publicMetricChart(updated), nil
	case "delete_metric_chart":
		if input.ChartID == "" {
			return nil, fmt.Errorf("%w: chartId is required", errInvalidArguments)
		}
		if err := handler.telemetry.DeleteMetricChart(ctx, scope, input.ChartID); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, nil
	default:
		return nil, fmt.Errorf("%w: unknown metric tool", errInvalidArguments)
	}
}

func (handler *Handler) metricScope(ctx context.Context, input metricArguments, identity automation.Identity) (state.MetricScope, error) {
	scope := state.MetricScope{Kind: input.Scope, ProjectID: input.ProjectID, ServiceID: input.ServiceID}
	switch input.Scope {
	case state.MetricScopeService:
		if input.ProjectID == "" || input.ServiceID == "" {
			return state.MetricScope{}, fmt.Errorf("%w: projectId and serviceId are required for a service scope", errInvalidArguments)
		}
		if !identity.AllowsProject(input.ProjectID) {
			return state.MetricScope{}, errProjectBoundary
		}
		if _, err := handler.repository.Service(ctx, input.ProjectID, input.ServiceID); err != nil {
			return state.MetricScope{}, err
		}
	case state.MetricScopeProject:
		if input.ProjectID == "" || input.ServiceID != "" {
			return state.MetricScope{}, fmt.Errorf("%w: only projectId is allowed for a project scope", errInvalidArguments)
		}
		if !identity.AllowsProject(input.ProjectID) {
			return state.MetricScope{}, errProjectBoundary
		}
		if _, err := handler.repository.Project(ctx, input.ProjectID); err != nil {
			return state.MetricScope{}, err
		}
	case state.MetricScopeInstallation:
		if input.ProjectID != "" || input.ServiceID != "" || !identity.IsAdmin() || identity.ProjectID != nil {
			return state.MetricScope{}, automation.ErrAdminRequired
		}
	default:
		return state.MetricScope{}, fmt.Errorf("%w: metric scope is invalid", errInvalidArguments)
	}
	return scope, nil
}

func (handler *Handler) queryMetricScope(ctx context.Context, scope state.MetricScope, operation string, query json.RawMessage) (any, error) {
	response, err := handler.telemetry.QueryMetricScope(ctx, scope, operation, query)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("telemetry metric query returned HTTP %d: %s", response.StatusCode, telemetryErrorMessage(response.Body))
	}
	var result any
	if err := json.Unmarshal(response.Body, &result); err != nil {
		return nil, fmt.Errorf("decode telemetry metric response: %w", err)
	}
	return result, nil
}

func telemetryErrorMessage(body []byte) string {
	var value struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &value) == nil && value.Message != "" {
		if value.Code != "" {
			return value.Code + ": " + value.Message
		}
		return value.Message
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return "empty response"
	}
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func metricChart(input metricArguments, scope state.MetricScope) (state.MetricChart, error) {
	chart := state.MetricChart{
		Scope: scope, Title: strings.TrimSpace(input.Title), SQL: strings.TrimSpace(input.SQL),
		Visualization: input.Visualization, Legend: strings.TrimSpace(input.Legend), Unit: strings.TrimSpace(input.Unit),
	}
	if chart.Title == "" || len(chart.Title) > 80 || chart.SQL == "" || len(chart.SQL) > 16<<10 ||
		!validMetricVisualization(chart.Visualization) || len(chart.Legend) > 80 || len(chart.Unit) > 32 {
		return state.MetricChart{}, fmt.Errorf("%w: metric chart fields are invalid", errInvalidArguments)
	}
	return chart, nil
}

func validMetricVisualization(value string) bool {
	return value == "line" || value == "area" || value == "bar" || value == "value"
}

func publicMetricCharts(charts []state.MetricChart) []map[string]any {
	result := make([]map[string]any, 0, len(charts))
	for _, chart := range charts {
		result = append(result, publicMetricChart(chart))
	}
	return result
}

func publicMetricChart(chart state.MetricChart) map[string]any {
	return map[string]any{
		"id": chart.ID, "title": chart.Title, "sql": chart.SQL, "visualization": chart.Visualization,
		"legend": chart.Legend, "unit": chart.Unit, "createdAt": chart.CreatedAtMillis, "updatedAt": chart.UpdatedAtMillis,
	}
}
