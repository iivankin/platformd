package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
)

type ServiceTelemetry interface {
	QueryService(context.Context, string, string, string, any) (any, error)
	ServiceTelemetry(context.Context, string, string) (telemetry.ServiceConfiguration, error)
	RotateServiceArtifactToken(context.Context, string, string) (string, error)
	ServiceTelemetryWebhooks(context.Context, string, string) ([]state.ServiceTelemetryWebhook, error)
	CreateServiceTelemetryWebhook(context.Context, string, string, string, []string) (state.ServiceTelemetryWebhook, string, error)
	DeleteServiceTelemetryWebhook(context.Context, string, string, string) error
	MetricCharts(context.Context, state.MetricScope) ([]state.MetricChart, error)
	CreateMetricChart(context.Context, state.MetricChart) (state.MetricChart, error)
	UpdateMetricChart(context.Context, state.MetricChart, int64) (state.MetricChart, error)
	DeleteMetricChart(context.Context, state.MetricScope, string) error
	QueryMetricScope(context.Context, state.MetricScope, string, json.RawMessage) (telemetry.MetricScopeResponse, error)
	UpdateServiceTelemetryPublicAccess(context.Context, state.UpdateServiceSentryPublicAccess) (telemetry.ServiceConfiguration, error)
}

func serviceTelemetryReadTools() []Tool {
	list := map[string]any{
		"projectId": map[string]any{"type": "string", "description": "Exact project ID from list_projects"},
		"serviceId": map[string]any{"type": "string", "description": "Exact service ID from list_services"},
		"query":     map[string]any{"type": "string", "maxLength": 256, "description": "Optional free-text search"},
		"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Page size; defaults to 50"},
		"offset":    map[string]any{"type": "integer", "minimum": 0, "description": "Zero-based page offset"},
	}
	detail := func(id string) map[string]any {
		identifier := map[string]any{"type": "string", "description": "Exact identifier returned by the corresponding list or parent detail tool"}
		if id == "traceId" {
			identifier["pattern"] = "^[0-9a-fA-F]{32}$"
		}
		return objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string", "description": "Exact project ID from list_projects"},
			"serviceId": map[string]any{"type": "string", "description": "Exact service ID from list_services"},
			id:          identifier,
		}, []string{"projectId", "serviceId", id})
	}
	return append([]Tool{
		{Name: "get_service_telemetry", Description: "Call before configuring a service SDK or artifact upload. Returns the project-network Sentry DSN and OTLP endpoint, an optional public DSN for browsers/external workloads, public-domain revision, webhooks, and sentry-cli environments.", InputSchema: detailSchema()},
		{Name: "list_service_issues", Description: "Primary error inbox: list errors grouped by fingerprint, with status and occurrence counts. Start here unless you already have an exact event, trace, or replay ID.", InputSchema: objectSchema(list, []string{"projectId", "serviceId"})},
		{Name: "get_service_issue", Description: "Read one grouped issue plus its recent occurrences and debugging context. Use returned event, trace, or replay IDs with their detail tools.", InputSchema: detail("issueId")},
		{Name: "list_service_error_events", Description: "Search individual Sentry error occurrences. Prefer list_service_issues for normal triage; use this when occurrence-level ordering or free-text search is required.", InputSchema: objectSchema(list, []string{"projectId", "serviceId"})},
		{Name: "get_service_error_event", Description: "Read one exact Sentry occurrence, including exception, breadcrumbs, user/device/request context, stack frames, source context when available, and symbolication result.", InputSchema: detail("eventId")},
		{Name: "list_service_replays", Description: "Search browser replay sessions and obtain replay IDs. Fetch one replay for metadata or its recording for the decoded timeline.", InputSchema: objectSchema(list, []string{"projectId", "serviceId"})},
		{Name: "get_service_replay", Description: "Read one browser replay's metadata and stored segments. Use get_service_replay_recording for the decoded event timeline.", InputSchema: detail("replayId")},
		{Name: "get_service_replay_recording", Description: "Read a bounded page of decoded browser replay timeline events plus related errors. Continue while offset + returned events length is less than totalEventCount.", InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string", "description": "Exact project ID from list_projects"},
			"serviceId": map[string]any{"type": "string", "description": "Exact service ID from list_services"},
			"replayId":  map[string]any{"type": "string", "description": "Exact replay ID returned by an issue, event, trace, or list_service_replays"},
			"offset":    map[string]any{"type": "integer", "minimum": 0, "maximum": 1_000_000, "description": "Decoded event offset; defaults to 0"},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 2_000, "description": "Decoded event count; defaults to 500"},
		}, []string{"projectId", "serviceId", "replayId"})},
		{Name: "list_service_artifacts", Description: "Inspect uploaded source maps and native debug artifacts used for decoding. To upload, first call get_service_telemetry and use its internal or public artifactUploads environment.", InputSchema: objectSchema(list, []string{"projectId", "serviceId"})},
		{Name: "list_service_traces", Description: "List recent distributed traces, including traces that contain AI work. The trace name always identifies its real root; aiAgentRunCount reports nested agent runs. Results include duration/errors plus model and tool call counts, token/cache usage, reported cost, TTFT, and throughput when available. Free-text search covers trace names, model/provider/agent names, prompts, responses, and tool inputs/results. Open a trace to inspect each agent subtree in its causal HTTP/database/queue context and correlate logs.", InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string", "description": "Exact project ID from list_projects"},
			"serviceId": map[string]any{"type": "string", "description": "Exact service ID from list_services"},
			"query":     map[string]any{"type": "string", "maxLength": 256, "description": "Optional full-text search across span names and AI prompt/response/tool content"},
			"from":      map[string]any{"type": "integer", "minimum": 0, "description": "Optional inclusive start time as Unix milliseconds"},
			"to":        map[string]any{"type": "integer", "minimum": 0, "description": "Optional inclusive end time as Unix milliseconds"},
			"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "Page size; defaults to 50"},
			"offset":    map[string]any{"type": "integer", "minimum": 0, "maximum": 10_000, "description": "Zero-based page offset"},
		}, []string{"projectId", "serviceId"})},
		{Name: "get_service_trace", Description: "Read every span in a trace. AI spans include normalized agent/model/tool classification, token and cache usage, reported cost, TTFT, throughput, and the underlying OpenTelemetry message/tool attributes. Pass traceId or a spanId to read_service_logs for correlated logs.", InputSchema: detail("traceId")},
	}, metricReadTools()...)
}

func serviceTelemetryAdminTools() []Tool {
	return append([]Tool{{
		Name: "update_service_issue_status", Description: "Set one grouped issue to open, resolved, or ignored. A later occurrence reopens a resolved issue. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "serviceId": map[string]any{"type": "string"},
			"issueId": map[string]any{"type": "string"},
			"status":  map[string]any{"type": "string", "enum": []string{"open", "resolved", "ignored"}},
		}, []string{"projectId", "serviceId", "issueId", "status"}),
	}, {
		Name: "set_service_telemetry_domain", Description: "Set the public Sentry hostname for browser/external SDKs, or pass an empty hostname to remove it. Copy expectedUpdatedAt from the latest get_service_telemetry result. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "serviceId": map[string]any{"type": "string"},
			"publicHostname":    map[string]any{"type": "string", "maxLength": 253, "description": "Hostname only, without scheme or path; empty removes public access"},
			"expectedUpdatedAt": map[string]any{"type": "integer", "minimum": 1, "description": "Exact updatedAt from get_service_telemetry"},
		}, []string{"projectId", "serviceId", "publicHostname", "expectedUpdatedAt"}),
	}, {
		Name: "rotate_service_artifact_token", Description: "Invalidate the previous public sentry-cli artifact credential and return the replacement plus ready-to-use environment once. Internal project-network uploads do not need this token. Requires an admin token.",
		InputSchema: detailSchema(),
	}, {
		Name: "create_service_telemetry_webhook", Description: "Create an issue/event webhook and return its secret once. Verify X-Platformd-Signature as sha256=<lowercase hex HMAC-SHA256 of the raw request body>; X-Platformd-Delivery and X-Platformd-Event identify the delivery. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "serviceId": map[string]any{"type": "string"},
			"url":    map[string]any{"type": "string", "maxLength": 2048},
			"events": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "enum": []string{"event_received", "issue_created", "issue_regressed", "issue_resolved"}}},
		}, []string{"projectId", "serviceId", "url", "events"}),
	}, {
		Name: "delete_service_telemetry_webhook", Description: "Permanently remove a service telemetry webhook by the ID returned from get_service_telemetry. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "serviceId": map[string]any{"type": "string"},
			"webhookId": map[string]any{"type": "string"},
		}, []string{"projectId", "serviceId", "webhookId"}),
	}}, metricAdminTools()...)
}

func detailSchema() map[string]any {
	return objectSchema(map[string]any{
		"projectId": map[string]any{"type": "string", "description": "Exact project ID from list_projects"},
		"serviceId": map[string]any{"type": "string", "description": "Exact service ID from list_services"},
	}, []string{"projectId", "serviceId"})
}

type serviceTelemetryArguments struct {
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
	IssueID   string `json:"issueId"`
	EventID   string `json:"eventId"`
	ReplayID  string `json:"replayId"`
	TraceID   string `json:"traceId"`
	Query     string `json:"query"`
	Status    string `json:"status"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
	From      int64  `json:"from"`
	To        int64  `json:"to"`
}

func (handler *Handler) callServiceTelemetry(ctx context.Context, name string, raw json.RawMessage, identity automation.Identity) (any, error) {
	var input serviceTelemetryArguments
	if err := decodeArguments(raw, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" {
		return nil, fmt.Errorf("%w: projectId and serviceId are required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	maximumLimit := 100
	maximumOffset := int(^uint(0) >> 1)
	if name == "list_service_traces" {
		maximumLimit, maximumOffset = 200, 10_000
	} else if name == "get_service_replay_recording" {
		maximumLimit, maximumOffset = 2_000, 1_000_000
	}
	if input.Query != "" && len(input.Query) > 256 || input.Limit < 0 || input.Limit > maximumLimit || input.Offset < 0 || input.Offset > maximumOffset || input.From < 0 || input.To < 0 || input.From > 0 && input.To > 0 && input.To < input.From {
		return nil, fmt.Errorf("%w: telemetry list query is invalid", errInvalidArguments)
	}
	service, err := handler.repository.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	if input.Query != "" {
		values.Set("query", input.Query)
	}
	if input.Limit > 0 {
		values.Set("limit", fmt.Sprint(input.Limit))
	}
	if input.Offset > 0 {
		values.Set("offset", fmt.Sprint(input.Offset))
	}
	if input.From > 0 {
		values.Set("from", fmt.Sprint(input.From))
	}
	if input.To > 0 {
		values.Set("to", fmt.Sprint(input.To))
	}
	listPath := func(kind string) string {
		path := "/" + kind
		if encoded := values.Encode(); encoded != "" {
			path += "?" + encoded
		}
		return path
	}
	method, path, body := http.MethodGet, "", any(nil)
	switch name {
	case "list_service_issues":
		path = listPath("issues")
	case "get_service_issue":
		if input.IssueID == "" {
			return nil, fmt.Errorf("%w: issueId is required", errInvalidArguments)
		}
		path = "/issues/" + url.PathEscape(input.IssueID)
	case "list_service_error_events":
		path = listPath("events")
	case "get_service_error_event":
		if input.EventID == "" {
			return nil, fmt.Errorf("%w: eventId is required", errInvalidArguments)
		}
		path = "/events/" + url.PathEscape(input.EventID)
	case "list_service_replays":
		path = listPath("replays")
	case "get_service_replay":
		if input.ReplayID == "" {
			return nil, fmt.Errorf("%w: replayId is required", errInvalidArguments)
		}
		path = "/replays/" + url.PathEscape(input.ReplayID)
	case "get_service_replay_recording":
		if input.ReplayID == "" {
			return nil, fmt.Errorf("%w: replayId is required", errInvalidArguments)
		}
		if input.Limit == 0 {
			values.Set("limit", "500")
		}
		path = "/replays/" + url.PathEscape(input.ReplayID) + "/recording?" + values.Encode()
	case "list_service_artifacts":
		path = listPath("artifacts")
	case "list_service_traces":
		path = listPath("traces")
	case "get_service_trace":
		if input.TraceID == "" {
			return nil, fmt.Errorf("%w: traceId is required", errInvalidArguments)
		}
		path = "/traces/" + url.PathEscape(input.TraceID)
	case "update_service_issue_status":
		if input.IssueID == "" || (input.Status != "open" && input.Status != "resolved" && input.Status != "ignored") {
			return nil, fmt.Errorf("%w: issueId or status is invalid", errInvalidArguments)
		}
		method, path, body = http.MethodPatch, "/issues/"+url.PathEscape(input.IssueID), map[string]string{"status": input.Status}
	default:
		return nil, fmt.Errorf("%w: unknown telemetry tool", errInvalidArguments)
	}
	return handler.telemetry.QueryService(ctx, service.ID, method, path, body)
}

type serviceTelemetryControlArguments struct {
	ProjectID         string   `json:"projectId"`
	ServiceID         string   `json:"serviceId"`
	PublicHostname    string   `json:"publicHostname"`
	ExpectedUpdatedAt int64    `json:"expectedUpdatedAt"`
	URL               string   `json:"url"`
	Events            []string `json:"events"`
	WebhookID         string   `json:"webhookId"`
}

func (handler *Handler) callServiceTelemetryControl(ctx context.Context, name string, raw json.RawMessage, identity automation.Identity) (any, error) {
	var input serviceTelemetryControlArguments
	if err := decodeArguments(raw, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" {
		return nil, fmt.Errorf("%w: projectId and serviceId are required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	if _, err := handler.repository.Service(ctx, input.ProjectID, input.ServiceID); err != nil {
		return nil, err
	}
	switch name {
	case "get_service_telemetry":
		return handler.serviceTelemetryConfiguration(ctx, input.ProjectID, input.ServiceID)
	case "set_service_telemetry_domain":
		if input.ExpectedUpdatedAt <= 0 || len(input.PublicHostname) > 253 {
			return nil, fmt.Errorf("%w: expectedUpdatedAt is required", errInvalidArguments)
		}
		auditID, err := id.New()
		if err != nil {
			return nil, err
		}
		requestID, err := id.New()
		if err != nil {
			return nil, err
		}
		_, err = handler.telemetry.UpdateServiceTelemetryPublicAccess(ctx, state.UpdateServiceSentryPublicAccess{
			ID: input.ServiceID, ProjectID: input.ProjectID, PublicHostname: input.PublicHostname,
			ExpectedUpdatedMillis: input.ExpectedUpdatedAt, AuditEventID: auditID,
			ActorKind: "token", ActorID: identity.TokenID, RequestCorrelationID: requestID,
			UpdatedAtMillis: time.Now().UnixMilli(),
		})
		if err != nil {
			return nil, err
		}
		return handler.serviceTelemetryConfiguration(ctx, input.ProjectID, input.ServiceID)
	case "rotate_service_artifact_token":
		token, err := handler.telemetry.RotateServiceArtifactToken(ctx, input.ProjectID, input.ServiceID)
		if err != nil {
			return nil, err
		}
		configuration, err := handler.telemetry.ServiceTelemetry(ctx, input.ProjectID, input.ServiceID)
		if err != nil {
			return nil, err
		}
		return artifactTokenResponse(configuration, token), nil
	case "create_service_telemetry_webhook":
		if input.URL == "" || len(input.URL) > 2048 || len(input.Events) == 0 {
			return nil, fmt.Errorf("%w: url and events are required", errInvalidArguments)
		}
		webhook, secret, err := handler.telemetry.CreateServiceTelemetryWebhook(ctx, input.ProjectID, input.ServiceID, input.URL, input.Events)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"webhook": publicTelemetryWebhook(webhook), "secret": secret,
			"signing": map[string]string{
				"algorithm": "HMAC-SHA256", "signatureHeader": "X-Platformd-Signature",
				"signatureFormat": "sha256=<lowercase hex>", "signedContent": "raw request body",
				"deliveryHeader": "X-Platformd-Delivery", "eventHeader": "X-Platformd-Event",
			},
		}, nil
	case "delete_service_telemetry_webhook":
		if input.WebhookID == "" {
			return nil, fmt.Errorf("%w: webhookId is required", errInvalidArguments)
		}
		if err := handler.telemetry.DeleteServiceTelemetryWebhook(ctx, input.ProjectID, input.ServiceID, input.WebhookID); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, nil
	default:
		return nil, fmt.Errorf("%w: unknown telemetry control tool", errInvalidArguments)
	}
}

func (handler *Handler) serviceTelemetryConfiguration(ctx context.Context, projectID, serviceID string) (any, error) {
	configuration, err := handler.telemetry.ServiceTelemetry(ctx, projectID, serviceID)
	if err != nil {
		return nil, err
	}
	webhooks, err := handler.telemetry.ServiceTelemetryWebhooks(ctx, projectID, serviceID)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(webhooks))
	for _, webhook := range webhooks {
		items = append(items, publicTelemetryWebhook(webhook))
	}
	return map[string]any{
		"serviceId": configuration.ServiceID, "internalHostname": configuration.InternalHostname,
		"internalDsn": configuration.InternalDSN, "internalOtlpEndpoint": configuration.InternalOTLPEndpoint,
		"publicHostname": configuration.PublicHostname, "publicDsn": configuration.PublicDSN,
		"updatedAt": configuration.UpdatedAt, "webhooks": items,
		"artifactUploads": artifactUploadConfiguration(configuration),
	}, nil
}

func publicTelemetryWebhook(webhook state.ServiceTelemetryWebhook) map[string]any {
	return map[string]any{
		"id": webhook.ID, "url": webhook.URL, "events": webhook.EventTypes, "enabled": webhook.Enabled,
		"createdAt": webhook.CreatedAtMillis, "updatedAt": webhook.UpdatedAtMillis,
	}
}

func artifactUploadConfiguration(configuration telemetry.ServiceConfiguration) map[string]any {
	result := map[string]any{
		"internal": map[string]any{
			"authRequired": false,
			"environment":  artifactUploadEnvironment(configuration.InternalDSN, configuration.ServiceID, ""),
		},
	}
	if configuration.PublicDSN != "" {
		result["public"] = map[string]any{
			"authRequired":   true,
			"credentialTool": "rotate_service_artifact_token",
			"environment":    artifactUploadEnvironment(configuration.PublicDSN, configuration.ServiceID, ""),
		}
	}
	return result
}

func artifactTokenResponse(configuration telemetry.ServiceConfiguration, token string) map[string]any {
	dsn := configuration.PublicDSN
	visibility := "public"
	if dsn == "" {
		dsn = configuration.InternalDSN
		visibility = "internal"
	}
	return map[string]any{
		"authToken": token, "visibility": visibility,
		"environment": artifactUploadEnvironment(dsn, configuration.ServiceID, token),
	}
}

func artifactUploadEnvironment(dsn, serviceID, token string) map[string]string {
	parsed, _ := url.Parse(dsn)
	environment := map[string]string{
		"SENTRY_URL": parsed.Scheme + "://" + parsed.Host,
		"SENTRY_ORG": "platformd", "SENTRY_PROJECT": serviceID,
	}
	if token != "" {
		environment["SENTRY_AUTH_TOKEN"] = token
	}
	return environment
}
