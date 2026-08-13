package mcp

import (
	"encoding/json"
	"net/http"
)

const serverInstructions = `platformd is the authoritative control plane for projects, services, managed resources, and telemetry. Do not invent identifiers or assume a current project: start with list_projects, then list_services or list_managed_resources, and pass the exact IDs returned by those tools. A project-bound token can only see its project; installation scope requires an unbound admin token. Mutation tools are visible only to admin tokens.

Tool result text is JSON. Read the latest object before a mutation that requires expectedUpdatedAt, copy its updatedAt value exactly, and re-read after a conflict instead of retrying blindly. Credentials and webhook secrets returned by create or rotate operations are shown once; preserve them for the user and never rotate again unless requested.

For service telemetry, call get_service_telemetry before configuring an SDK or CI job. Use internalDsn and internalOtlpEndpoint only from containers in the project network. Browser SDKs and external workloads require publicDsn, which exists only after a public telemetry domain is configured. Errors are grouped into issues: normally use list_service_issues, then get_service_issue, then fetch a particular event, trace, replay, or replay recording by the IDs in those results. Correlate a trace with read_service_logs using traceId or spanId. Internal artifact uploads need no token; public sentry-cli uploads require the one-time credential returned by rotate_service_artifact_token.

For custom metrics, call get_metric_catalog before writing SQL, then validate it with query_metrics before saving a chart. Metric queries are one restricted SELECT over the virtual metrics table and must return time and value, with optional series. All metric range arguments are Unix milliseconds.`

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
