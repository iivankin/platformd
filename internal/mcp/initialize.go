package mcp

import (
	"encoding/json"
	"net/http"
)

const serverInstructions = `platformd is the authoritative control plane for projects, services, managed resources, and telemetry. Do not invent identifiers or assume a current project: start with list_projects, then list_services or list_managed_resources, and pass the exact IDs returned by those tools. A project-bound token can only see its project; installation scope requires an unbound admin token. Mutation tools are visible only to admin tokens.

Tool result text is JSON. Read the latest object before a mutation that requires expectedUpdatedAt, copy its updatedAt value exactly, and re-read after a conflict instead of retrying blindly. Credentials and webhook secrets returned by create or rotate operations are shown once; preserve them for the user and never rotate again unless requested.

For service telemetry, call get_service_telemetry before configuring an SDK or CI job. Use internalDsn and internalOtlpEndpoint only from containers in the project network. Browser SDKs and external workloads use publicDsn for Sentry errors/replay and publicOtlpEndpoint for OTLP traces/logs; each exists only after its public endpoint is configured. Errors are grouped into issues: normally use list_service_issues, then get_service_issue, then fetch a particular event, trace, replay, or replay recording by the IDs in those results. Correlate a trace with read_service_logs using traceId or spanId. Internal artifact uploads need no token; public sentry-cli uploads require the one-time credential returned by rotate_service_artifact_token.

For custom metrics, call get_metric_catalog before writing SQL, then validate it with query_metrics before saving a chart. Metric queries are one restricted SELECT over the virtual metrics table and must return time and value, with optional series. All metric range arguments are Unix milliseconds.

For project web analytics, call list_projects, then list_analytics_trackers, then get_analytics_tracker or query_analytics. Trackers are keyed by root domain, not by service; do not use get_service_telemetry or invent PLATFORMD_* analytics environment variables. query_analytics is product analytics (pageviews, events, funnels, experiments), not metrics or traces. Pass report plus the exact trackerId, and from/to as Unix milliseconds except for report=realtime. Built-in reports are overview, breakdown, realtime, funnel, retention, paths, heatmap, bots, sessions, experiment, sql, and lookup. Prefer a built-in report; use sql only for custom charts. SQL is one restricted SELECT FROM analytics and typically returns time and value, with optional series. Validate SQL with report=sql before create_analytics_chart. For funnels and experiments, pass funnelId or experimentId from get_analytics_tracker rather than inventing steps at query time. Browser apps install @platformd/analytics, choose cookieless, opt-in, or opt-out in application code, and configure the tracker cookie domain. In-cluster OpenFeature uses internalOfrepUrl as the SDK baseUrl (it already appends /ofrep/v1) with targetingKey = analytics.anonymousId() when available.

Child servers run services only; PostgreSQL, Redis, object stores, and preview deployments stay on the primary VPS. Call list_hosts before create_service or update_service when placing a service. hostId is a child ID from list_hosts, or empty for the primary. Services with volumes cannot change hosts. Join tokens and host deletion require an unbound admin token; create_host_join_token returns the token once.`

func (handler *Handler) initialize(response http.ResponseWriter, message requestMessage) {
	if len(message.ID) == 0 {
		writeRPCError(response, nil, codeInvalidRequest, "initialize requires an id")
		return
	}
	var params struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	if err := json.Unmarshal(message.Params, &params); err != nil || params.ProtocolVersion == "" || params.Capabilities == nil || params.ClientInfo.Name == "" || params.ClientInfo.Version == "" {
		writeRPCError(response, message.ID, codeInvalidParams, "Invalid initialize params")
		return
	}
	protocolVersion := ProtocolVersion
	if supportsProtocolVersion(params.ProtocolVersion) {
		protocolVersion = params.ProtocolVersion
	}
	writeRPCResult(response, message.ID, map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]string{
			"name": "platformd", "version": handler.version,
		},
		"instructions": serverInstructions,
	})
}
