package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedpostgres"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type Repository interface {
	Projects(context.Context) ([]state.ProjectSummary, error)
	Project(context.Context, string) (state.ProjectSummary, error)
	ProjectCanvas(context.Context, string) (state.ProjectCanvas, error)
	Service(context.Context, string, string) (state.ServiceDesired, error)
	ServiceDeployments(context.Context, string, string, string, int) (state.DeploymentPage, error)
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

func readTools() []Tool {
	projectProperty := map[string]any{
		"projectId": map[string]any{"type": "string", "description": "Exact project ID"},
	}
	serviceProperties := map[string]any{
		"projectId": map[string]any{"type": "string", "description": "Exact project ID"},
		"serviceId": map[string]any{"type": "string", "description": "Exact service ID"},
	}
	return []Tool{
		{
			Name: "list_projects", Description: "List projects visible to this token.",
			InputSchema: objectSchema(nil, nil),
		},
		{
			Name: "list_services", Description: "List services in one visible project.",
			InputSchema: objectSchema(projectProperty, []string{"projectId"}),
		},
		{
			Name: "get_service", Description: "Read one service and its current deployment pointer.",
			InputSchema: objectSchema(serviceProperties, []string{"projectId", "serviceId"}),
		},
		{
			Name: "list_service_deployments", Description: "Read bounded deployment history for one service.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"},
				"serviceId": map[string]any{"type": "string"},
				"cursor":    map[string]any{"type": "string"},
				"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}, []string{"projectId", "serviceId"}),
		},
		{
			Name: "read_service_logs", Description: "Read a bounded recent service log window with optional deployment and contains filters.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "serviceId": map[string]any{"type": "string"},
				"deploymentId": map[string]any{"type": "string"}, "contains": map[string]any{"type": "string", "maxLength": 256},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": containerlogs.MaximumLimit},
			}, []string{"projectId", "serviceId"}),
		},
		{
			Name: "list_managed_image_tags", Description: "List one documented Docker Hub page for official PostgreSQL or Redis tags; search filters only that page and manual tag input remains valid.",
			InputSchema: objectSchema(map[string]any{
				"engine":   map[string]any{"type": "string", "enum": []string{"postgres", "redis"}},
				"page":     map[string]any{"type": "integer", "minimum": 1},
				"pageSize": map[string]any{"type": "integer", "minimum": 1, "maximum": managedimages.MaximumPageSize},
				"search":   map[string]any{"type": "string", "maxLength": 128},
			}, []string{"engine"}),
		},
	}
}

func configuredReadTools(managedResources bool) []Tool {
	tools := readTools()
	if managedResources {
		tools = append(tools, managedResourceReadTools()...)
	}
	return tools
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	result := map[string]any{
		"type": "object", "properties": properties, "additionalProperties": false,
	}
	if len(required) != 0 {
		result["required"] = required
	}
	return result
}

func (handler *Handler) listTools(response http.ResponseWriter, message requestMessage, identity automation.Identity) {
	if len(message.Params) != 0 {
		var params struct {
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(message.Params, &params); err != nil || params.Cursor != "" {
			writeRPCError(response, message.ID, codeInvalidParams, "tools/list cursor is not supported")
			return
		}
	}
	tools := handler.tools
	if identity.IsAdmin() {
		tools = append(append([]Tool(nil), tools...), adminTools()...)
		if handler.redis != nil {
			tools = append(tools, managedRedisAdminTool())
		}
		if handler.postgres != nil {
			tools = append(tools, managedPostgresAdminTool())
		}
		if handler.serverExec != nil && identity.ProjectID == nil {
			tools = append(tools, serverExecTool())
		}
	}
	writeRPCResult(response, message.ID, map[string]any{"tools": tools})
}

func (handler *Handler) callTool(response http.ResponseWriter, request *http.Request, message requestMessage, identity automation.Identity) {
	var call toolCallParams
	if err := decodeArguments(message.Params, &call); err != nil || call.Name == "" {
		writeRPCError(response, message.ID, codeInvalidParams, "Invalid tools/call params")
		return
	}
	if isAdminMutationTool(call.Name) && !identity.IsAdmin() {
		writeToolResult(response, message.ID, map[string]string{"error": automation.ErrAdminRequired.Error()}, true)
		return
	}
	var lease *admission.Lease
	if isAdminMutationTool(call.Name) {
		var err error
		lease, err = handler.admission.Begin("mcp_tool", call.Name)
		if err != nil {
			writeRPCErrorStatus(response, message.ID, http.StatusConflict, codeInternalError, "platform_updating")
			return
		}
		defer lease.Release()
	}
	var output any
	var err error
	switch call.Name {
	case "list_projects":
		output, err = handler.listProjects(request.Context(), call.Arguments, identity)
	case "list_services":
		output, err = handler.listServices(request.Context(), call.Arguments, identity)
	case "get_service":
		output, err = handler.getService(request.Context(), call.Arguments, identity)
	case "list_service_deployments":
		output, err = handler.listDeployments(request.Context(), call.Arguments, identity)
	case "read_service_logs":
		output, err = handler.readServiceLogs(request.Context(), call.Arguments, identity)
	case "list_managed_image_tags":
		output, err = handler.listManagedImageTags(request.Context(), call.Arguments)
	case "list_managed_resources":
		if handler.managed == nil {
			writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
			return
		}
		output, err = handler.listManagedResources(request.Context(), call.Arguments, identity)
	case "get_managed_resource":
		if handler.managed == nil {
			writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
			return
		}
		output, err = handler.getManagedResource(request.Context(), call.Arguments, identity)
	case "read_managed_resource_backups":
		if handler.managed == nil {
			writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
			return
		}
		output, err = handler.readManagedResourceBackups(request.Context(), call.Arguments, identity)
	case "create_service":
		output, err = handler.createService(request.Context(), call.Arguments, identity)
	case "update_service":
		output, err = handler.updateService(request.Context(), call.Arguments, identity)
	case "redeploy_service":
		output, err = handler.redeployService(request.Context(), call.Arguments, identity)
	case "rollback_service":
		output, err = handler.rollbackService(request.Context(), call.Arguments, identity)
	case "create_managed_redis":
		if handler.redis == nil {
			writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
			return
		}
		output, err = handler.createManagedRedis(request.Context(), call.Arguments, identity)
	case "create_managed_postgres":
		if handler.postgres == nil {
			writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
			return
		}
		output, err = handler.createManagedPostgres(request.Context(), call.Arguments, identity)
	case "server_exec":
		if handler.serverExec == nil {
			writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
			return
		}
		output, err = handler.executeServerCommand(request.Context(), call.Arguments, identity)
	default:
		writeRPCError(response, message.ID, codeInvalidParams, "Unknown tool")
		return
	}
	if err != nil {
		if errors.Is(err, errInvalidArguments) || errors.Is(err, automation.ErrInvalidInput) || errors.Is(err, automation.ErrManagedResourceInput) || errors.Is(err, containerlogs.ErrInvalidQuery) || errors.Is(err, managedimages.ErrInvalidQuery) || errors.Is(err, managedredis.ErrInvalidInput) || errors.Is(err, managedpostgres.ErrInvalidInput) {
			writeRPCError(response, message.ID, codeInvalidParams, err.Error())
			return
		}
		writeToolResult(response, message.ID, map[string]string{"error": err.Error()}, true)
		return
	}
	writeToolResult(response, message.ID, output, false)
}

var errInvalidArguments = errors.New("invalid tool arguments")
var errProjectBoundary = errors.New("project is outside this token boundary")

func (handler *Handler) listProjects(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var empty struct{}
	if err := decodeArguments(arguments, &empty); err != nil {
		return nil, fmt.Errorf("%w: list_projects requires an empty object", errInvalidArguments)
	}
	if identity.ProjectID != nil {
		project, err := handler.repository.Project(ctx, *identity.ProjectID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"projects": []projectOutput{publicProject(project)}}, nil
	}
	projects, err := handler.repository.Projects(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]projectOutput, 0, len(projects))
	for _, project := range projects {
		result = append(result, publicProject(project))
	}
	return map[string]any{"projects": result}, nil
}

func (handler *Handler) listServices(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	canvas, err := handler.repository.ProjectCanvas(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	services := make([]serviceSummaryOutput, 0)
	for _, resource := range canvas.Resources {
		if resource.Kind == "service" {
			services = append(services, serviceSummaryOutput{
				ID: resource.ID, Name: resource.Name, Enabled: resource.Enabled,
				Status: resource.Status, InternalHostname: resource.InternalHostname,
				ImageReference: resource.ImageReference, ImageDigest: resource.ImageDigest,
				ActiveDeploymentID: resource.ActiveDeployment,
			})
		}
	}
	return map[string]any{"services": services}, nil
}

func (handler *Handler) getService(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	input, err := serviceArguments(arguments)
	if err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	service, err := handler.repository.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"service": publicService(service)}, nil
}

func (handler *Handler) listDeployments(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		ServiceID string `json:"serviceId"`
		Cursor    string `json:"cursor"`
		Limit     int    `json:"limit"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" || input.Limit < 0 || input.Limit > 100 {
		return nil, fmt.Errorf("%w: projectId/serviceId are required and limit must be 1..100 when set", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, errProjectBoundary
	}
	page, err := handler.repository.ServiceDeployments(ctx, input.ProjectID, input.ServiceID, input.Cursor, input.Limit)
	if err != nil {
		return nil, err
	}
	deployments := make([]deploymentOutput, 0, len(page.Deployments))
	for _, deployment := range page.Deployments {
		deployments = append(deployments, publicDeployment(deployment))
	}
	return map[string]any{"deployments": deployments, "nextCursor": page.NextCursor}, nil
}

func serviceArguments(arguments json.RawMessage) (struct {
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
}, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		ServiceID string `json:"serviceId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" {
		return input, fmt.Errorf("%w: projectId and serviceId are required", errInvalidArguments)
	}
	return input, nil
}

func decodeArguments(value json.RawMessage, destination any) error {
	if len(value) == 0 {
		value = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireEnd(decoder) != nil {
		return errInvalidArguments
	}
	return nil
}

func writeToolResult(response http.ResponseWriter, id json.RawMessage, output any, isError bool) {
	encoded, err := json.Marshal(output)
	if err != nil {
		writeRPCError(response, id, codeInternalError, "Unable to encode tool output")
		return
	}
	writeRPCResult(response, id, toolResult{
		Content: []toolContent{{Type: "text", Text: string(encoded)}}, IsError: isError,
	})
}

type projectOutput struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ServiceCount     int    `json:"serviceCount"`
	PostgresCount    int    `json:"postgresCount"`
	RedisCount       int    `json:"redisCount"`
	ObjectStoreCount int    `json:"objectStoreCount"`
}

type serviceSummaryOutput struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Enabled            bool   `json:"enabled"`
	Status             string `json:"status"`
	InternalHostname   string `json:"internalHostname"`
	ImageReference     string `json:"imageReference,omitempty"`
	ImageDigest        string `json:"imageDigest,omitempty"`
	ActiveDeploymentID string `json:"activeDeploymentId,omitempty"`
}

type serviceOutput struct {
	ID                 string                 `json:"id"`
	ProjectID          string                 `json:"projectId"`
	Name               string                 `json:"name"`
	Enabled            bool                   `json:"enabled"`
	Snapshot           serviceconfig.Snapshot `json:"configuration"`
	ActiveDeploymentID string                 `json:"activeDeploymentId,omitempty"`
	ActiveImageDigest  string                 `json:"activeImageDigest,omitempty"`
	CreatedAt          int64                  `json:"createdAt"`
	UpdatedAt          int64                  `json:"updatedAt"`
}

type deploymentOutput struct {
	ID           string `json:"id"`
	ImageDigest  string `json:"imageDigest"`
	ConfigHash   string `json:"serviceConfigHash"`
	Status       string `json:"status"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	FinishedAt   int64  `json:"finishedAt,omitempty"`
}

func publicProject(project state.ProjectSummary) projectOutput {
	return projectOutput{
		ID: project.ID, Name: project.Name, ServiceCount: project.ServiceCount,
		PostgresCount: project.PostgresCount, RedisCount: project.RedisCount,
		ObjectStoreCount: project.ObjectStoreCount,
	}
}

func publicService(service state.ServiceDesired) serviceOutput {
	return serviceOutput{
		ID: service.ID, ProjectID: service.ProjectID, Name: service.Name,
		Enabled: service.Enabled, Snapshot: service.Snapshot,
		ActiveDeploymentID: service.ActiveDeploymentID, ActiveImageDigest: service.ActiveImageDigest,
		CreatedAt: service.CreatedAtMillis, UpdatedAt: service.UpdatedAtMillis,
	}
}

func publicDeployment(deployment state.DeploymentRecord) deploymentOutput {
	return deploymentOutput{
		ID: deployment.ID, ImageDigest: deployment.ImageDigest, ConfigHash: deployment.ConfigHash,
		Status: deployment.Status, ErrorCode: deployment.ErrorCode, ErrorMessage: deployment.ErrorMessage,
		CreatedAt: deployment.CreatedAtMillis, FinishedAt: deployment.FinishedAtMillis,
	}
}
