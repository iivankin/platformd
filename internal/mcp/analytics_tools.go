package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
)

type Analytics interface {
	Project(context.Context, string) (state.ProjectSummary, error)
	ProjectServiceHostnames(context.Context, string) ([]string, error)
	AnalyticsTrackers(context.Context, string) ([]state.AnalyticsTracker, error)
	AnalyticsTracker(context.Context, string, string) (state.AnalyticsTracker, error)
	CreateAnalyticsTracker(context.Context, state.AnalyticsTracker) (state.AnalyticsTracker, error)
	UpdateAnalyticsTracker(context.Context, state.AnalyticsTracker, int64) (state.AnalyticsTracker, error)
	DeleteAnalyticsTracker(context.Context, string, string) error
	AnalyticsGoals(context.Context, string, string) ([]state.AnalyticsGoal, error)
	CreateAnalyticsGoal(context.Context, string, state.AnalyticsGoal) (state.AnalyticsGoal, error)
	UpdateAnalyticsGoal(context.Context, string, state.AnalyticsGoal, int64) (state.AnalyticsGoal, error)
	DeleteAnalyticsGoal(context.Context, string, string, string) error
	AnalyticsFunnels(context.Context, string, string) ([]state.AnalyticsFunnel, error)
	CreateAnalyticsFunnel(context.Context, string, state.AnalyticsFunnel) (state.AnalyticsFunnel, error)
	UpdateAnalyticsFunnel(context.Context, string, state.AnalyticsFunnel, int64) (state.AnalyticsFunnel, error)
	DeleteAnalyticsFunnel(context.Context, string, string, string) error
	AnalyticsFlags(context.Context, string, string) ([]state.AnalyticsFlag, error)
	CreateAnalyticsFlag(context.Context, string, state.AnalyticsFlag) (state.AnalyticsFlag, error)
	UpdateAnalyticsFlag(context.Context, string, state.AnalyticsFlag, int64) (state.AnalyticsFlag, error)
	DeleteAnalyticsFlag(context.Context, string, string, string) error
	AnalyticsExperiments(context.Context, string, string) ([]state.AnalyticsExperiment, error)
	CreateAnalyticsExperiment(context.Context, string, state.AnalyticsExperiment) (state.AnalyticsExperiment, error)
	StopAnalyticsExperiment(context.Context, string, string, string, int64) (state.AnalyticsExperiment, error)
	ShipAnalyticsExperiment(context.Context, string, string, string, string, int64) (state.AnalyticsExperiment, error)
	AnalyticsCharts(context.Context, string, string) ([]state.AnalyticsChart, error)
	CreateAnalyticsChart(context.Context, string, state.AnalyticsChart) (state.AnalyticsChart, error)
	UpdateAnalyticsChart(context.Context, string, state.AnalyticsChart, int64) (state.AnalyticsChart, error)
	DeleteAnalyticsChart(context.Context, string, string, string) error
	QueryAnalytics(context.Context, string, string, json.RawMessage) (int, string, []byte, error)
}

func analyticsProjectID() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "Exact project ID from list_projects. Web analytics is project-scoped, not a service tab.",
	}
}

func analyticsTrackerID() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "Exact tracker ID from list_analytics_trackers. Never invent this; one tracker covers a root domain and its hostnames.",
	}
}

func analyticsExpectedUpdatedAt() map[string]any {
	return map[string]any{
		"type": "integer", "minimum": 1,
		"description": "Exact updatedAt from the latest get_analytics_tracker payload for this object. Re-read after a conflict instead of retrying blindly.",
	}
}

func analyticsUnixMillis(description string) map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": description}
}

func analyticsWindowFields() (map[string]any, map[string]any) {
	return map[string]any{
			"type": "integer", "minimum": 1,
			"description": "Conversion/session window length. Funnel default 7, experiment default 14.",
		}, map[string]any{
			"type": "string", "enum": []string{"minute", "hour", "day"},
			"description": "Unit for windowValue. Defaults to day.",
		}
}

func analyticsFilterSchema() map[string]any {
	return objectSchema(map[string]any{
		"dimension": map[string]any{
			"type":        "string",
			"description": "One of page, entry_page, exit_page, hostname, source, channel, referrer, utm_medium, utm_source, utm_campaign, utm_content, utm_term, country, region, city, browser, os, device, event. Page values are hostname+pathname such as shop.example/pricing; a value that starts with / still matches that path on every host. Acquisition dimensions (source, channel, referrer, utm_*) match the visit's first pageview in the range, so later pages without those params still count.",
		},
		"operator": map[string]any{
			"type": "string", "enum": []string{"is", "is_not", "contains", "does_not_contain"},
		},
		"value": map[string]any{
			"description": "A string, or an array of at most 20 strings when operator is is/is_not.",
		},
	}, []string{"dimension", "operator", "value"})
}

func analyticsFunnelStepSchema() map[string]any {
	return objectSchema(map[string]any{
		"type":     map[string]any{"type": "string", "enum": []string{"path", "event"}, "description": "path matches pathname, event matches event_name"},
		"value":    map[string]any{"type": "string", "description": "Pathname such as /checkout or event name such as purchase"},
		"hostname": map[string]any{"type": "string", "description": "Optional hostname restriction; omit to match every host on the tracker"},
	}, []string{"type", "value"})
}

func analyticsFlagVariantSchema() map[string]any {
	return objectSchema(map[string]any{
		"key":        map[string]any{"type": "string", "description": "Variant key. Boolean flags typically use true and false."},
		"percentage": map[string]any{"type": "number", "minimum": 0, "maximum": 100, "description": "Weight among variants after a visitor is in the rollout bucket. Ignored for boolean flags: in-bucket is true, else false."},
	}, []string{"key", "percentage"})
}

func analyticsTargetingSchema() map[string]any {
	return objectSchema(map[string]any{
		"groups": map[string]any{
			"type":        "array",
			"description": "OR groups evaluated in order; first match wins. Empty groups means 100% rollout of the default variant split.",
			"items": objectSchema(map[string]any{
				"properties": map[string]any{
					"type":        "array",
					"description": "AND conditions against OpenFeature evaluation context. targetingKey is platformd.anonymousId().",
					"items": objectSchema(map[string]any{
						"key":      map[string]any{"type": "string", "description": "Context attribute, for example country or path"},
						"operator": map[string]any{"type": "string", "enum": []string{"exact", "icontains", "is_not", "is_set", "gt", "lt"}},
						"value":    map[string]any{"description": "Compare value. Unused for is_set."},
					}, []string{"key", "operator"}),
				},
				"rollout_percentage": map[string]any{"type": "integer", "minimum": 0, "maximum": 100, "description": "Percent of matching visitors who enter the experiment/flag. 0 means nobody. Empty targeting groups means 100%."},
				"rollout_steps": map[string]any{
					"type":        "array",
					"description": "Scheduled ramp. The last step with at <= now replaces rollout_percentage.",
					"items": objectSchema(map[string]any{
						"at":         analyticsUnixMillis("Unix milliseconds when this percentage becomes active"),
						"percentage": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
					}, []string{"at", "percentage"}),
				},
				"variant": map[string]any{"type": []string{"string", "null"}, "description": "If set, every in-bucket visitor gets this variant instead of the weight split"},
			}, []string{"rollout_percentage"}),
		},
	}, nil)
}

func analyticsMetricSchema() map[string]any {
	return objectSchema(map[string]any{
		"goalId":    map[string]any{"type": "string", "description": "Goal ID from get_analytics_tracker.goals. Prefer this over eventName."},
		"eventName": map[string]any{"type": "string", "description": "Raw event name when the metric is not a saved goal"},
	}, nil)
}

func analyticsSQLSchema(saved bool) map[string]any {
	text := "One restricted SELECT over the virtual analytics table. Must read FROM analytics. Typical result aliases are time and value, with optional series. Available columns: time, value, series, event_name, hostname, pathname, referrer_source, channel, country, device, browser, os, distinct_id, session_id, bot_kind. Validate with query_analytics report=sql before saving a chart."
	if saved {
		text = "SQL already validated with query_analytics report=sql for this tracker. Must read FROM analytics and typically return time, value, and optional series."
	}
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 16 << 10, "description": text}
}

func analyticsTrackerArgs() map[string]any {
	return map[string]any{"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID()}
}

func analyticsReadTools() []Tool {
	tracker := objectSchema(analyticsTrackerArgs(), []string{"projectId", "trackerId"})
	query := analyticsTrackerArgs()
	query["report"] = map[string]any{
		"type":        "string",
		"enum":        []string{"overview", "breakdown", "realtime", "funnel", "retention", "paths", "heatmap", "bots", "sessions", "experiment", "sql", "lookup"},
		"description": "overview=totals/timeseries/top lists; breakdown=group by dimension; realtime=visitors in the last few minutes (no from/to); funnel=saved funnelId; retention=return cohorts; paths=page-to-page flow; heatmap=clicks for pathname; bots=detected bots; sessions=session list; experiment=experimentId results; lookup=distinct values for a filter dimension; sql=restricted SELECT.",
	}
	query["from"] = analyticsUnixMillis("Inclusive start as Unix milliseconds. Required except for report=realtime.")
	query["to"] = analyticsUnixMillis("Inclusive end as Unix milliseconds. Required except for report=realtime.")
	query["filters"] = map[string]any{"type": "array", "maxItems": 8, "items": analyticsFilterSchema(), "description": "Optional AND filters. Applied to every report except realtime. Acquisition filters (source, channel, referrer, utm_*) select visits by the first pageview in the range, then include every later event in those visits."}
	query["dimension"] = map[string]any{"type": "string", "description": "Required for breakdown and lookup. Breakdown accepts page, hostname, source, channel, entry, exit, utm_*, country, region, city, browser, os, device, event, bot_kind, bot_name. Page, entry, and exit labels are hostname+pathname such as shop.example/pricing. Lookup page uses the same labels; lookup pathname is the bare path for heatmaps."}
	query["pathname"] = map[string]any{"type": "string", "description": "Required for heatmap. Exact page path such as /pricing."}
	query["viewport"] = map[string]any{"type": "integer", "minimum": 1, "description": "Optional heatmap viewport width in CSS pixels."}
	query["eventType"] = map[string]any{"type": "string", "description": "Optional event name restriction for event-oriented reports."}
	query["funnelId"] = map[string]any{"type": "string", "description": "Required for report=funnel. Copy from get_analytics_tracker.funnels[].id."}
	query["experimentId"] = map[string]any{"type": "string", "description": "Required for report=experiment. Copy from get_analytics_tracker.experiments[].id."}
	query["sql"] = analyticsSQLSchema(false)
	return []Tool{
		{Name: "list_analytics_trackers", Description: "List web-analytics trackers for a project. Start here before any analytics query. Each tracker is one root domain (example.com also matches www and app). Returns matching service hostnames and the internal OFREP URL. This is not service telemetry: do not use get_service_telemetry or invent PLATFORMD_* analytics environment variables.", InputSchema: objectSchema(map[string]any{"projectId": analyticsProjectID()}, []string{"projectId"})},
		{Name: "get_analytics_tracker", Description: "Read one tracker plus its goals, funnels, flags, experiments, and saved SQL charts. Copy IDs and updatedAt from this payload into query_analytics and mutations. internalOfrepUrl is the in-cluster OpenFeature SDK baseUrl (the provider appends /ofrep/v1). targetingKey is platformd.anonymousId(). Browser snippets load /analytics.js from the page origin once a tracker exists.", InputSchema: tracker},
		{Name: "query_analytics", Description: "Run one web-analytics report for a tracker. This is product analytics (pageviews, events, funnels, experiments), not metrics or traces. Call list_projects, then list_analytics_trackers, then this tool with the exact trackerId. Pass from and to as Unix milliseconds except for report=realtime. Prefer a built-in report; use sql only when no built-in report answers the question, and keep SQL to one SELECT FROM analytics.", InputSchema: objectSchema(query, []string{"projectId", "trackerId", "report"})},
	}
}

func analyticsAdminTools() []Tool {
	expected := analyticsExpectedUpdatedAt()
	windowValue, windowUnit := analyticsWindowFields()
	goalFields := analyticsTrackerArgs()
	goalFields["name"] = map[string]any{"type": "string", "description": "Human label shown in the UI and experiment metrics"}
	goalFields["actionType"] = map[string]any{"type": "string", "enum": []string{"path", "event"}}
	goalFields["actionValue"] = map[string]any{"type": "string", "description": "Pathname such as /signup or event name such as signup_completed"}
	goalFields["hostname"] = map[string]any{"type": "string", "description": "Optional hostname restriction"}
	updateGoal := copyProperties(goalFields)
	updateGoal["goalId"] = map[string]any{"type": "string", "description": "Exact goal ID from get_analytics_tracker.goals"}
	updateGoal["expectedUpdatedAt"] = expected
	funnelFields := analyticsTrackerArgs()
	funnelFields["name"] = map[string]any{"type": "string"}
	funnelFields["windowValue"] = windowValue
	funnelFields["windowUnit"] = windowUnit
	funnelFields["steps"] = map[string]any{"type": "array", "minItems": 2, "maxItems": 8, "items": analyticsFunnelStepSchema(), "description": "Ordered conversion steps. Visitors must complete them in order within the window."}
	updateFunnel := copyProperties(funnelFields)
	updateFunnel["funnelId"] = map[string]any{"type": "string", "description": "Exact funnel ID from get_analytics_tracker.funnels"}
	updateFunnel["expectedUpdatedAt"] = expected
	flagFields := analyticsTrackerArgs()
	flagFields["key"] = map[string]any{"type": "string", "description": "OpenFeature flag key used by SDKs"}
	flagFields["description"] = map[string]any{"type": "string"}
	flagFields["type"] = map[string]any{"type": "string", "enum": []string{"boolean", "multivariate"}}
	flagFields["enabled"] = map[string]any{"type": "boolean", "description": "False (default if omitted) means the flag is not served. Set true to start evaluating."}
	flagFields["variants"] = map[string]any{"type": "array", "minItems": 1, "items": analyticsFlagVariantSchema()}
	flagFields["payload"] = map[string]any{"type": "object", "description": "JSON returned as OFREP metadata on evaluation"}
	flagFields["targeting"] = analyticsTargetingSchema()
	updateFlag := copyProperties(flagFields)
	updateFlag["flagId"] = map[string]any{"type": "string", "description": "Exact flag ID from get_analytics_tracker.flags"}
	updateFlag["expectedUpdatedAt"] = expected
	chartFields := analyticsTrackerArgs()
	chartFields["title"] = map[string]any{"type": "string"}
	chartFields["sql"] = analyticsSQLSchema(true)
	chartFields["visualization"] = map[string]any{"type": "string", "enum": []string{"line", "area", "bar", "value", "table"}}
	chartFields["legend"] = map[string]any{"type": "string"}
	chartFields["unit"] = map[string]any{"type": "string"}
	updateChart := copyProperties(chartFields)
	updateChart["chartId"] = map[string]any{"type": "string", "description": "Exact chart ID from get_analytics_tracker.charts"}
	updateChart["expectedUpdatedAt"] = expected
	return []Tool{
		{Name: "create_analytics_tracker", Description: "Create a project web-analytics tracker for one root domain. Roots must not overlap (example.com already covers www.example.com). mode is cookieless, opt-out (default), or opt-in. After create, browsers load /analytics.js from the page origin; in-cluster OpenFeature uses internalOfrepUrl. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "name": map[string]any{"type": "string"},
			"rootDomain": map[string]any{"type": "string", "description": "Registrable domain such as example.com, without a scheme or path"},
			"mode":       map[string]any{"type": "string", "enum": []string{"cookieless", "opt-out", "opt-in"}, "description": "cookieless has no cookie; opt-out (default) sets a cookie unless the visitor opts out; opt-in waits for consent."},
		}, []string{"projectId", "name", "rootDomain"})},
		{Name: "update_analytics_tracker", Description: "Update a tracker name, root domain, or identity mode. Copy expectedUpdatedAt from get_analytics_tracker.tracker.updatedAt. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"name": map[string]any{"type": "string"}, "rootDomain": map[string]any{"type": "string"},
			"mode":              map[string]any{"type": "string", "enum": []string{"cookieless", "opt-out", "opt-in"}},
			"expectedUpdatedAt": expected,
		}, []string{"projectId", "trackerId", "name", "rootDomain", "mode", "expectedUpdatedAt"})},
		{Name: "delete_analytics_tracker", Description: "Delete a tracker and its goals, funnels, flags, experiments, and charts. Stored events expire by TTL and are not deleted immediately. Requires an admin token.", InputSchema: objectSchema(analyticsTrackerArgs(), []string{"projectId", "trackerId"})},
		{Name: "create_analytics_goal", Description: "Create a conversion goal. actionType=path matches a pathname; actionType=event matches an event name. Use the returned id as experiment metric.goalId. Requires an admin token.", InputSchema: objectSchema(goalFields, []string{"projectId", "trackerId", "name", "actionType", "actionValue"})},
		{Name: "update_analytics_goal", Description: "Replace a goal definition. Copy expectedUpdatedAt from get_analytics_tracker.goals[].updatedAt. Requires an admin token.", InputSchema: objectSchema(updateGoal, []string{"projectId", "trackerId", "goalId", "name", "actionType", "actionValue", "expectedUpdatedAt"})},
		{Name: "delete_analytics_goal", Description: "Delete a tracker goal by the ID from get_analytics_tracker.goals. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"goalId": map[string]any{"type": "string"},
		}, []string{"projectId", "trackerId", "goalId"})},
		{Name: "create_analytics_funnel", Description: "Create a sequential funnel of 2–8 path or event steps. Window defaults to 7 days. After create, query_analytics report=funnel with the returned id. Requires an admin token.", InputSchema: objectSchema(funnelFields, []string{"projectId", "trackerId", "name", "steps"})},
		{Name: "update_analytics_funnel", Description: "Replace a funnel name, window, or steps. Copy expectedUpdatedAt from get_analytics_tracker.funnels[].updatedAt. Requires an admin token.", InputSchema: objectSchema(updateFunnel, []string{"projectId", "trackerId", "funnelId", "name", "steps", "expectedUpdatedAt"})},
		{Name: "delete_analytics_funnel", Description: "Delete a tracker funnel by the ID from get_analytics_tracker.funnels. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"funnelId": map[string]any{"type": "string"},
		}, []string{"projectId", "trackerId", "funnelId"})},
		{Name: "create_analytics_flag", Description: "Create an OpenFeature flag on a tracker. Evaluation uses targetingKey = platformd.anonymousId() and OF evaluation context. Boolean flags ignore variant weights: in-bucket is true, else false. Omit targeting for a full rollout. Set enabled true to serve. Requires an admin token.", InputSchema: objectSchema(flagFields, []string{"projectId", "trackerId", "key", "type", "variants"})},
		{Name: "update_analytics_flag", Description: "Replace a flag key, variants, payload, or targeting. Copy enabled and targeting from the latest flag. Do not change variant weights while an experiment is running; ship or stop the experiment first. Copy expectedUpdatedAt from get_analytics_tracker.flags[].updatedAt. Requires an admin token.", InputSchema: objectSchema(updateFlag, []string{"projectId", "trackerId", "flagId", "key", "type", "enabled", "variants", "targeting", "expectedUpdatedAt"})},
		{Name: "delete_analytics_flag", Description: "Delete a tracker flag by the ID from get_analytics_tracker.flags. Stop or ship a running experiment on the flag first. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"flagId": map[string]any{"type": "string"},
		}, []string{"projectId", "trackerId", "flagId"})},
		{Name: "create_analytics_experiment", Description: "Start an A/B experiment on an existing flag. metric is {goalId} from get_analytics_tracker.goals or {eventName}. Window defaults to 14 days. controlVariant must be one of the flag variant keys. Query results with query_analytics report=experiment. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"flagId":         map[string]any{"type": "string", "description": "Exact flag ID from get_analytics_tracker.flags"},
			"controlVariant": map[string]any{"type": "string", "description": "Variant key treated as the control, usually false or control"},
			"metric":         analyticsMetricSchema(), "windowValue": windowValue, "windowUnit": windowUnit,
		}, []string{"projectId", "trackerId", "flagId", "controlVariant", "metric"})},
		{Name: "stop_analytics_experiment", Description: "Stop a running experiment without shipping a winner. Copy expectedUpdatedAt from get_analytics_tracker.experiments[].updatedAt. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"experimentId": map[string]any{"type": "string"}, "expectedUpdatedAt": expected,
		}, []string{"projectId", "trackerId", "experimentId", "expectedUpdatedAt"})},
		{Name: "ship_analytics_experiment", Description: "Ship a winner: roll that variant to 100% on the flag and stop the experiment. Copy expectedUpdatedAt from get_analytics_tracker.experiments[].updatedAt. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"experimentId":      map[string]any{"type": "string"},
			"variant":           map[string]any{"type": "string", "description": "Winning variant key to serve at 100%"},
			"expectedUpdatedAt": expected,
		}, []string{"projectId", "trackerId", "experimentId", "variant", "expectedUpdatedAt"})},
		{Name: "create_analytics_chart", Description: "Save a custom analytics chart after its SQL succeeds in query_analytics report=sql for the same tracker. Requires an admin token.", InputSchema: objectSchema(chartFields, []string{"projectId", "trackerId", "title", "sql", "visualization"})},
		{Name: "update_analytics_chart", Description: "Replace a saved SQL chart. Validate changed SQL with query_analytics report=sql first. Copy expectedUpdatedAt from get_analytics_tracker.charts[].updatedAt. Requires an admin token.", InputSchema: objectSchema(updateChart, []string{"projectId", "trackerId", "chartId", "title", "sql", "visualization", "expectedUpdatedAt"})},
		{Name: "delete_analytics_chart", Description: "Delete a saved SQL chart by the ID from get_analytics_tracker.charts. Requires an admin token.", InputSchema: objectSchema(map[string]any{
			"projectId": analyticsProjectID(), "trackerId": analyticsTrackerID(),
			"chartId": map[string]any{"type": "string"},
		}, []string{"projectId", "trackerId", "chartId"})},
	}
}

func (handler *Handler) callAnalyticsTool(ctx context.Context, name string, arguments json.RawMessage, identity automation.Identity) (any, error) {
	if handler.analytics == nil {
		return nil, errInvalidArguments
	}
	switch name {
	case "list_analytics_trackers":
		return handler.listAnalyticsTrackers(ctx, arguments, identity)
	case "get_analytics_tracker":
		return handler.getAnalyticsTracker(ctx, arguments, identity)
	case "query_analytics":
		return handler.queryAnalytics(ctx, arguments, identity)
	case "create_analytics_tracker":
		return handler.createAnalyticsTracker(ctx, arguments, identity)
	case "update_analytics_tracker":
		return handler.updateAnalyticsTracker(ctx, arguments, identity)
	case "delete_analytics_tracker":
		return handler.deleteAnalyticsTracker(ctx, arguments, identity)
	case "create_analytics_goal":
		return handler.createAnalyticsGoal(ctx, arguments, identity)
	case "update_analytics_goal":
		return handler.updateAnalyticsGoal(ctx, arguments, identity)
	case "delete_analytics_goal":
		return handler.deleteAnalyticsGoal(ctx, arguments, identity)
	case "create_analytics_funnel":
		return handler.createAnalyticsFunnel(ctx, arguments, identity)
	case "update_analytics_funnel":
		return handler.updateAnalyticsFunnel(ctx, arguments, identity)
	case "delete_analytics_funnel":
		return handler.deleteAnalyticsFunnel(ctx, arguments, identity)
	case "create_analytics_flag":
		return handler.createAnalyticsFlag(ctx, arguments, identity)
	case "update_analytics_flag":
		return handler.updateAnalyticsFlag(ctx, arguments, identity)
	case "delete_analytics_flag":
		return handler.deleteAnalyticsFlag(ctx, arguments, identity)
	case "create_analytics_experiment":
		return handler.createAnalyticsExperiment(ctx, arguments, identity)
	case "stop_analytics_experiment":
		return handler.stopAnalyticsExperiment(ctx, arguments, identity)
	case "ship_analytics_experiment":
		return handler.shipAnalyticsExperiment(ctx, arguments, identity)
	case "create_analytics_chart":
		return handler.createAnalyticsChart(ctx, arguments, identity)
	case "update_analytics_chart":
		return handler.updateAnalyticsChart(ctx, arguments, identity)
	case "delete_analytics_chart":
		return handler.deleteAnalyticsChart(ctx, arguments, identity)
	default:
		return nil, errInvalidArguments
	}
}

func (handler *Handler) listAnalyticsTrackers(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	trackers, err := handler.analytics.AnalyticsTrackers(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	payload, err := handler.publicAnalyticsTrackers(ctx, input.ProjectID, trackers)
	if err != nil {
		return nil, err
	}
	return map[string]any{"trackers": payload}, nil
}

func (handler *Handler) getAnalyticsTracker(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	projectID, trackerID, err := requireTrackerArgs(arguments, identity)
	if err != nil {
		return nil, err
	}
	tracker, err := handler.analytics.AnalyticsTracker(ctx, projectID, trackerID)
	if err != nil {
		return nil, err
	}
	payload, err := handler.publicAnalyticsTrackers(ctx, projectID, []state.AnalyticsTracker{tracker})
	if err != nil {
		return nil, err
	}
	goals, err := handler.analytics.AnalyticsGoals(ctx, projectID, trackerID)
	if err != nil {
		return nil, err
	}
	funnels, err := handler.analytics.AnalyticsFunnels(ctx, projectID, trackerID)
	if err != nil {
		return nil, err
	}
	flags, err := handler.analytics.AnalyticsFlags(ctx, projectID, trackerID)
	if err != nil {
		return nil, err
	}
	experiments, err := handler.analytics.AnalyticsExperiments(ctx, projectID, trackerID)
	if err != nil {
		return nil, err
	}
	charts, err := handler.analytics.AnalyticsCharts(ctx, projectID, trackerID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tracker": payload[0],
		"goals":   publicAnalyticsGoals(goals), "funnels": publicAnalyticsFunnels(funnels),
		"flags": publicAnalyticsFlags(flags), "experiments": publicAnalyticsExperiments(experiments),
		"charts": publicAnalyticsCharts(charts),
	}, nil
}

func (handler *Handler) queryAnalytics(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID    string           `json:"projectId"`
		TrackerID    string           `json:"trackerId"`
		Report       string           `json:"report"`
		From         *int64           `json:"from"`
		To           *int64           `json:"to"`
		Filters      []map[string]any `json:"filters"`
		Dimension    string           `json:"dimension"`
		Pathname     string           `json:"pathname"`
		Viewport     *int             `json:"viewport"`
		EventType    string           `json:"eventType"`
		FunnelID     string           `json:"funnelId"`
		ExperimentID string           `json:"experimentId"`
		SQL          string           `json:"sql"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.TrackerID == "" || input.Report == "" {
		return nil, fmt.Errorf("%w: projectId, trackerId, and report are required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	query, err := encodeAnalyticsQuery(input.Report, input.From, input.To, input.Filters, input.Dimension, input.Pathname, input.Viewport, input.EventType, input.FunnelID, input.ExperimentID, input.SQL)
	if err != nil {
		return nil, err
	}
	status, _, body, err := handler.analytics.QueryAnalytics(ctx, input.ProjectID, input.TrackerID, query)
	if err != nil {
		return nil, err
	}
	if status >= 300 {
		message := telemetryErrorMessage(body)
		if status >= 400 && status < 500 {
			return nil, fmt.Errorf("%w: %s", errInvalidArguments, message)
		}
		return nil, fmt.Errorf("analytics query returned HTTP %d: %s", status, message)
	}
	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func encodeAnalyticsQuery(report string, from, to *int64, filters []map[string]any, dimension, pathname string, viewport *int, eventType, funnelID, experimentID, sql string) (json.RawMessage, error) {
	query := map[string]any{"report": report}
	if from != nil {
		query["from"] = *from
	}
	if to != nil {
		query["to"] = *to
	}
	if len(filters) > 0 {
		query["filters"] = filters
	}
	if dimension != "" {
		query["dimension"] = dimension
	}
	if pathname != "" {
		query["pathname"] = pathname
	}
	if viewport != nil {
		query["viewport"] = *viewport
	}
	if eventType != "" {
		query["eventType"] = eventType
	}
	if funnelID != "" {
		query["funnelId"] = funnelID
	}
	if experimentID != "" {
		query["experimentId"] = experimentID
	}
	if sql != "" {
		query["sql"] = sql
	}
	encoded, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("encode analytics query: %w", err)
	}
	return encoded, nil
}

func (handler *Handler) createAnalyticsTracker(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		Name       string `json:"name"`
		RootDomain string `json:"rootDomain"`
		Mode       string `json:"mode"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	tracker, err := handler.analytics.CreateAnalyticsTracker(ctx, state.AnalyticsTracker{
		ProjectID: input.ProjectID, Name: input.Name, RootDomain: input.RootDomain, Mode: input.Mode,
	})
	if err != nil {
		return nil, err
	}
	payload, err := handler.publicAnalyticsTrackers(ctx, input.ProjectID, []state.AnalyticsTracker{tracker})
	if err != nil {
		return nil, err
	}
	return map[string]any{"tracker": payload[0]}, nil
}

func (handler *Handler) updateAnalyticsTracker(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		TrackerID         string `json:"trackerId"`
		Name              string `json:"name"`
		RootDomain        string `json:"rootDomain"`
		Mode              string `json:"mode"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.TrackerID == "" {
		return nil, fmt.Errorf("%w: projectId and trackerId are required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	tracker, err := handler.analytics.UpdateAnalyticsTracker(ctx, state.AnalyticsTracker{
		ID: input.TrackerID, ProjectID: input.ProjectID, Name: input.Name, RootDomain: input.RootDomain, Mode: input.Mode,
	}, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	payload, err := handler.publicAnalyticsTrackers(ctx, input.ProjectID, []state.AnalyticsTracker{tracker})
	if err != nil {
		return nil, err
	}
	return map[string]any{"tracker": payload[0]}, nil
}

func (handler *Handler) deleteAnalyticsTracker(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	projectID, trackerID, err := requireTrackerArgs(arguments, identity)
	if err != nil {
		return nil, err
	}
	if err := handler.analytics.DeleteAnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func (handler *Handler) createAnalyticsGoal(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID   string `json:"projectId"`
		TrackerID   string `json:"trackerId"`
		Name        string `json:"name"`
		ActionType  string `json:"actionType"`
		ActionValue string `json:"actionValue"`
		Hostname    string `json:"hostname"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	goal, err := handler.analytics.CreateAnalyticsGoal(ctx, input.ProjectID, state.AnalyticsGoal{
		TrackerID: input.TrackerID, Name: input.Name, ActionType: input.ActionType, ActionValue: input.ActionValue, Hostname: input.Hostname,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"goal": publicAnalyticsGoal(goal)}, nil
}

func (handler *Handler) updateAnalyticsGoal(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		TrackerID         string `json:"trackerId"`
		GoalID            string `json:"goalId"`
		Name              string `json:"name"`
		ActionType        string `json:"actionType"`
		ActionValue       string `json:"actionValue"`
		Hostname          string `json:"hostname"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	goal, err := handler.analytics.UpdateAnalyticsGoal(ctx, input.ProjectID, state.AnalyticsGoal{
		ID: input.GoalID, TrackerID: input.TrackerID, Name: input.Name, ActionType: input.ActionType,
		ActionValue: input.ActionValue, Hostname: input.Hostname,
	}, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"goal": publicAnalyticsGoal(goal)}, nil
}

func (handler *Handler) deleteAnalyticsGoal(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		TrackerID string `json:"trackerId"`
		GoalID    string `json:"goalId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if err := handler.analytics.DeleteAnalyticsGoal(ctx, input.ProjectID, input.TrackerID, input.GoalID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func (handler *Handler) createAnalyticsFunnel(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID   string                      `json:"projectId"`
		TrackerID   string                      `json:"trackerId"`
		Name        string                      `json:"name"`
		WindowValue int                         `json:"windowValue"`
		WindowUnit  string                      `json:"windowUnit"`
		Steps       []state.AnalyticsFunnelStep `json:"steps"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if input.WindowValue == 0 {
		input.WindowValue = 7
	}
	if input.WindowUnit == "" {
		input.WindowUnit = "day"
	}
	funnel, err := handler.analytics.CreateAnalyticsFunnel(ctx, input.ProjectID, state.AnalyticsFunnel{
		TrackerID: input.TrackerID, Name: input.Name, WindowValue: input.WindowValue, WindowUnit: input.WindowUnit, Steps: input.Steps,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"funnel": publicAnalyticsFunnel(funnel)}, nil
}

func (handler *Handler) updateAnalyticsFunnel(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string                      `json:"projectId"`
		TrackerID         string                      `json:"trackerId"`
		FunnelID          string                      `json:"funnelId"`
		Name              string                      `json:"name"`
		WindowValue       int                         `json:"windowValue"`
		WindowUnit        string                      `json:"windowUnit"`
		Steps             []state.AnalyticsFunnelStep `json:"steps"`
		ExpectedUpdatedAt int64                       `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if input.WindowValue == 0 {
		input.WindowValue = 7
	}
	if input.WindowUnit == "" {
		input.WindowUnit = "day"
	}
	funnel, err := handler.analytics.UpdateAnalyticsFunnel(ctx, input.ProjectID, state.AnalyticsFunnel{
		ID: input.FunnelID, TrackerID: input.TrackerID, Name: input.Name,
		WindowValue: input.WindowValue, WindowUnit: input.WindowUnit, Steps: input.Steps,
	}, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"funnel": publicAnalyticsFunnel(funnel)}, nil
}

func (handler *Handler) deleteAnalyticsFunnel(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		TrackerID string `json:"trackerId"`
		FunnelID  string `json:"funnelId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if err := handler.analytics.DeleteAnalyticsFunnel(ctx, input.ProjectID, input.TrackerID, input.FunnelID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func (handler *Handler) createAnalyticsFlag(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID   string                       `json:"projectId"`
		TrackerID   string                       `json:"trackerId"`
		Key         string                       `json:"key"`
		Description string                       `json:"description"`
		Type        string                       `json:"type"`
		Enabled     bool                         `json:"enabled"`
		Variants    []state.AnalyticsFlagVariant `json:"variants"`
		Payload     json.RawMessage              `json:"payload"`
		Targeting   json.RawMessage              `json:"targeting"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	flag, err := handler.analytics.CreateAnalyticsFlag(ctx, input.ProjectID, state.AnalyticsFlag{
		TrackerID: input.TrackerID, Key: input.Key, Description: input.Description, Type: input.Type,
		Enabled: input.Enabled, Variants: input.Variants, PayloadJSON: string(input.Payload), TargetingJSON: string(input.Targeting),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"flag": publicAnalyticsFlag(flag)}, nil
}

func (handler *Handler) updateAnalyticsFlag(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string                       `json:"projectId"`
		TrackerID         string                       `json:"trackerId"`
		FlagID            string                       `json:"flagId"`
		Key               string                       `json:"key"`
		Description       string                       `json:"description"`
		Type              string                       `json:"type"`
		Enabled           bool                         `json:"enabled"`
		Variants          []state.AnalyticsFlagVariant `json:"variants"`
		Payload           json.RawMessage              `json:"payload"`
		Targeting         json.RawMessage              `json:"targeting"`
		ExpectedUpdatedAt int64                        `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	flag, err := handler.analytics.UpdateAnalyticsFlag(ctx, input.ProjectID, state.AnalyticsFlag{
		ID: input.FlagID, TrackerID: input.TrackerID, Key: input.Key, Description: input.Description, Type: input.Type,
		Enabled: input.Enabled, Variants: input.Variants, PayloadJSON: string(input.Payload), TargetingJSON: string(input.Targeting),
	}, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"flag": publicAnalyticsFlag(flag)}, nil
}

func (handler *Handler) deleteAnalyticsFlag(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		TrackerID string `json:"trackerId"`
		FlagID    string `json:"flagId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if err := handler.analytics.DeleteAnalyticsFlag(ctx, input.ProjectID, input.TrackerID, input.FlagID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func (handler *Handler) createAnalyticsExperiment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID      string                          `json:"projectId"`
		TrackerID      string                          `json:"trackerId"`
		FlagID         string                          `json:"flagId"`
		ControlVariant string                          `json:"controlVariant"`
		Metric         state.AnalyticsExperimentMetric `json:"metric"`
		WindowValue    int                             `json:"windowValue"`
		WindowUnit     string                          `json:"windowUnit"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if input.WindowValue == 0 {
		input.WindowValue = 14
	}
	if input.WindowUnit == "" {
		input.WindowUnit = "day"
	}
	experiment, err := handler.analytics.CreateAnalyticsExperiment(ctx, input.ProjectID, state.AnalyticsExperiment{
		FlagID: input.FlagID, TrackerID: input.TrackerID, ControlVariant: input.ControlVariant,
		Metric: input.Metric, WindowValue: input.WindowValue, WindowUnit: input.WindowUnit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"experiment": publicAnalyticsExperiment(experiment)}, nil
}

func (handler *Handler) stopAnalyticsExperiment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		TrackerID         string `json:"trackerId"`
		ExperimentID      string `json:"experimentId"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	experiment, err := handler.analytics.StopAnalyticsExperiment(ctx, input.ProjectID, input.TrackerID, input.ExperimentID, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"experiment": publicAnalyticsExperiment(experiment)}, nil
}

func (handler *Handler) shipAnalyticsExperiment(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		TrackerID         string `json:"trackerId"`
		ExperimentID      string `json:"experimentId"`
		Variant           string `json:"variant"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	experiment, err := handler.analytics.ShipAnalyticsExperiment(ctx, input.ProjectID, input.TrackerID, input.ExperimentID, input.Variant, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"experiment": publicAnalyticsExperiment(experiment)}, nil
}

func (handler *Handler) createAnalyticsChart(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID     string `json:"projectId"`
		TrackerID     string `json:"trackerId"`
		Title         string `json:"title"`
		SQL           string `json:"sql"`
		Visualization string `json:"visualization"`
		Legend        string `json:"legend"`
		Unit          string `json:"unit"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	chart, err := handler.analytics.CreateAnalyticsChart(ctx, input.ProjectID, state.AnalyticsChart{
		TrackerID: input.TrackerID, Title: input.Title, SQL: input.SQL,
		Visualization: input.Visualization, Legend: input.Legend, Unit: input.Unit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"chart": publicAnalyticsChart(chart)}, nil
}

func (handler *Handler) updateAnalyticsChart(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		TrackerID         string `json:"trackerId"`
		ChartID           string `json:"chartId"`
		Title             string `json:"title"`
		SQL               string `json:"sql"`
		Visualization     string `json:"visualization"`
		Legend            string `json:"legend"`
		Unit              string `json:"unit"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	chart, err := handler.analytics.UpdateAnalyticsChart(ctx, input.ProjectID, state.AnalyticsChart{
		ID: input.ChartID, TrackerID: input.TrackerID, Title: input.Title, SQL: input.SQL,
		Visualization: input.Visualization, Legend: input.Legend, Unit: input.Unit,
	}, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"chart": publicAnalyticsChart(chart)}, nil
}

func (handler *Handler) deleteAnalyticsChart(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		TrackerID string `json:"trackerId"`
		ChartID   string `json:"chartId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if err := handler.analytics.DeleteAnalyticsChart(ctx, input.ProjectID, input.TrackerID, input.ChartID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func (handler *Handler) publicAnalyticsTrackers(ctx context.Context, projectID string, trackers []state.AnalyticsTracker) ([]map[string]any, error) {
	project, err := handler.analytics.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	hostnames, err := handler.analytics.ProjectServiceHostnames(ctx, projectID)
	if err != nil {
		return nil, err
	}
	payload := make([]map[string]any, 0, len(trackers))
	for _, tracker := range trackers {
		matching := make([]string, 0)
		for _, hostname := range hostnames {
			if state.HostMatchesTracker(hostname, tracker.RootDomain) {
				matching = append(matching, hostname)
			}
		}
		internal := telemetry.InternalAnalyticsHostname(project.Name, state.TrackerSlug(tracker.RootDomain))
		payload = append(payload, map[string]any{
			"id": tracker.ID, "projectId": tracker.ProjectID, "name": tracker.Name,
			"rootDomain": tracker.RootDomain, "mode": tracker.Mode,
			"internalHostname":  internal,
			"internalOfrepUrl":  "http://" + internal + ":" + strconv.Itoa(firewall.ServiceTelemetryPort),
			"matchingHostnames": matching, "createdAt": tracker.CreatedAtMillis, "updatedAt": tracker.UpdatedAtMillis,
		})
	}
	return payload, nil
}

func publicAnalyticsGoal(goal state.AnalyticsGoal) map[string]any {
	payload := map[string]any{
		"id": goal.ID, "trackerId": goal.TrackerID, "name": goal.Name,
		"actionType": goal.ActionType, "actionValue": goal.ActionValue,
		"createdAt": goal.CreatedAtMillis, "updatedAt": goal.UpdatedAtMillis,
	}
	if goal.Hostname != "" {
		payload["hostname"] = goal.Hostname
	}
	return payload
}

func publicAnalyticsGoals(goals []state.AnalyticsGoal) []map[string]any {
	payload := make([]map[string]any, 0, len(goals))
	for _, goal := range goals {
		payload = append(payload, publicAnalyticsGoal(goal))
	}
	return payload
}

func publicAnalyticsFunnel(funnel state.AnalyticsFunnel) map[string]any {
	return map[string]any{
		"id": funnel.ID, "trackerId": funnel.TrackerID, "name": funnel.Name,
		"windowValue": funnel.WindowValue, "windowUnit": funnel.WindowUnit, "steps": funnel.Steps,
		"createdAt": funnel.CreatedAtMillis, "updatedAt": funnel.UpdatedAtMillis,
	}
}

func publicAnalyticsFunnels(funnels []state.AnalyticsFunnel) []map[string]any {
	payload := make([]map[string]any, 0, len(funnels))
	for _, funnel := range funnels {
		payload = append(payload, publicAnalyticsFunnel(funnel))
	}
	return payload
}

func publicAnalyticsFlag(flag state.AnalyticsFlag) map[string]any {
	payload := json.RawMessage(flag.PayloadJSON)
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	targeting := json.RawMessage(flag.TargetingJSON)
	if len(targeting) == 0 {
		targeting = json.RawMessage(`{"groups":[]}`)
	}
	return map[string]any{
		"id": flag.ID, "trackerId": flag.TrackerID, "key": flag.Key, "description": flag.Description,
		"type": flag.Type, "enabled": flag.Enabled, "variants": flag.Variants,
		"payload": payload, "targeting": targeting,
		"createdAt": flag.CreatedAtMillis, "updatedAt": flag.UpdatedAtMillis,
	}
}

func publicAnalyticsFlags(flags []state.AnalyticsFlag) []map[string]any {
	payload := make([]map[string]any, 0, len(flags))
	for _, flag := range flags {
		payload = append(payload, publicAnalyticsFlag(flag))
	}
	return payload
}

func publicAnalyticsExperiment(experiment state.AnalyticsExperiment) map[string]any {
	payload := map[string]any{
		"id": experiment.ID, "flagId": experiment.FlagID, "trackerId": experiment.TrackerID,
		"controlVariant": experiment.ControlVariant, "metric": experiment.Metric,
		"windowValue": experiment.WindowValue, "windowUnit": experiment.WindowUnit,
		"startedAt": experiment.StartedAtMillis,
		"createdAt": experiment.CreatedAtMillis, "updatedAt": experiment.UpdatedAtMillis,
	}
	if experiment.EndedAtMillis > 0 {
		payload["endedAt"] = experiment.EndedAtMillis
	}
	return payload
}

func publicAnalyticsExperiments(experiments []state.AnalyticsExperiment) []map[string]any {
	payload := make([]map[string]any, 0, len(experiments))
	for _, experiment := range experiments {
		payload = append(payload, publicAnalyticsExperiment(experiment))
	}
	return payload
}

func publicAnalyticsChart(chart state.AnalyticsChart) map[string]any {
	return map[string]any{
		"id": chart.ID, "trackerId": chart.TrackerID, "title": chart.Title, "sql": chart.SQL,
		"visualization": chart.Visualization, "legend": chart.Legend, "unit": chart.Unit,
		"createdAt": chart.CreatedAtMillis, "updatedAt": chart.UpdatedAtMillis,
	}
}

func publicAnalyticsCharts(charts []state.AnalyticsChart) []map[string]any {
	payload := make([]map[string]any, 0, len(charts))
	for _, chart := range charts {
		payload = append(payload, publicAnalyticsChart(chart))
	}
	return payload
}

func requireTrackerArgs(arguments json.RawMessage, identity automation.Identity) (string, string, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		TrackerID string `json:"trackerId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.TrackerID == "" {
		return "", "", fmt.Errorf("%w: projectId and trackerId are required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return "", "", errProjectBoundary
	}
	return input.ProjectID, input.TrackerID, nil
}
