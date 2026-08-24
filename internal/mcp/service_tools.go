package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

func adminTools() []Tool {
	source := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":                  map[string]any{"type": "string", "enum": []string{"docker_image_upload", "public_image", "private_image"}},
			"autoUpdate":            map[string]any{"type": "boolean"},
			"minimumReleaseAgeDays": map[string]any{"type": "integer", "minimum": 1, "maximum": 36_500},
			"image": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"reference": map[string]any{"type": "string"},
				},
				"required": []string{"reference"},
			},
			"dockerUpload": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"repository":    map[string]any{"type": "string"},
					"branch":        map[string]any{"type": "string"},
					"workflows":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"previews":      map[string]any{"type": "boolean"},
					"previewDomain": map[string]any{"type": "string"},
				},
				"required": []string{"repository", "branch", "workflows", "previews"},
			},
		},
		"required":             []string{"type"},
		"additionalProperties": false,
	}
	configuration := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source": source,
			"beforeDeploy": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "maxLength": 262_144},
					"cloudflareHostnames": map[string]any{
						"type": "array", "maxItems": 30, "items": map[string]string{"type": "string"},
					},
				},
			},
			"portForward": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{
					"repository": map[string]any{
						"type": "string", "description": "Lowercase GitHub owner/name allowed to create port forwards with Actions OIDC.",
					},
					"workflows": map[string]any{
						"type": "array", "items": map[string]string{"type": "string"},
						"description": "Allowed .yml or .yaml workflow filenames; an empty list allows any workflow in the repository.",
					},
				},
				"required": []string{"repository", "workflows"},
			},
			"command":          map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"args":             map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"environment":      map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}},
			"secretReferences": map[string]any{"type": "array"},
			"healthCheck": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"port", "path", "timeoutSeconds"},
				"properties": map[string]any{
					"port":           map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
					"path":           map[string]any{"type": "string"},
					"timeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600},
				},
			},
			"cpuMillicores":  map[string]any{"type": "integer", "minimum": 0},
			"memoryMaxBytes": map[string]any{"type": "integer", "minimum": 0},
			"volumeMounts":   map[string]any{"type": "array"},
		},
		"required":             []string{"source"},
		"additionalProperties": false,
	}
	base := map[string]any{
		"projectId": map[string]any{"type": "string"},
		"serviceId": map[string]any{"type": "string"},
	}
	return []Tool{
		{
			Name: "create_service", Description: "Create and immediately reconcile a service. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
				"enabled":       map[string]any{"type": "boolean", "default": true},
				"hostId":        map[string]any{"type": "string", "description": "Child server ID from list_hosts. Omit or empty to run on the primary VPS. Services with volumes cannot change hosts later."},
				"configuration": configuration,
			}, []string{"projectId", "name", "configuration"}),
		},
		{
			Name: "update_service", Description: "Update desired service state with optimistic concurrency. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": base["projectId"], "serviceId": base["serviceId"],
				"enabled": map[string]any{"type": "boolean"}, "expectedUpdatedAt": map[string]any{"type": "integer"},
				"hostId":        map[string]any{"type": "string", "description": "Child server ID from list_hosts. Empty keeps the service on the primary VPS. Omit to leave placement unchanged. Services with volumes cannot change hosts."},
				"configuration": configuration,
			}, []string{"projectId", "serviceId", "enabled", "expectedUpdatedAt", "configuration"}),
		},
		{
			Name: "redeploy_service", Description: "Redeploy the current desired service state. Requires an admin token.",
			InputSchema: mutationTargetSchema(base, false),
		},
		{
			Name: "rollback_service", Description: "Copy a successful deployment snapshot into desired state and deploy it. Requires an admin token.",
			InputSchema: mutationTargetSchema(base, true),
		},
	}
}

func mutationTargetSchema(base map[string]any, rollback bool) map[string]any {
	properties := map[string]any{
		"projectId": base["projectId"], "serviceId": base["serviceId"],
		"expectedUpdatedAt": map[string]any{"type": "integer"},
	}
	required := []string{"projectId", "serviceId", "expectedUpdatedAt"}
	if rollback {
		properties["deploymentId"] = map[string]any{"type": "string"}
		required = append(required, "deploymentId")
	}
	return objectSchema(properties, required)
}

func isAdminMutationTool(name string) bool {
	switch name {
	case "create_project", "create_service", "update_service", "redeploy_service", "rollback_service",
		"update_service_issue_status", "set_service_telemetry_domain", "set_service_public_otlp", "set_service_browser_tunnel", "rotate_service_artifact_token",
		"create_service_telemetry_webhook", "delete_service_telemetry_webhook",
		"create_metric_chart", "update_metric_chart", "delete_metric_chart",
		"create_analytics_tracker", "update_analytics_tracker", "delete_analytics_tracker",
		"create_analytics_goal", "update_analytics_goal", "delete_analytics_goal",
		"create_analytics_funnel", "update_analytics_funnel", "delete_analytics_funnel",
		"create_analytics_flag", "update_analytics_flag", "delete_analytics_flag",
		"create_analytics_experiment", "stop_analytics_experiment", "ship_analytics_experiment",
		"create_analytics_chart", "update_analytics_chart", "delete_analytics_chart",
		"delete_service", "restart_service_deployment", "remove_service_deployment",
		"restart_managed_deployment", "remove_managed_deployment",
		"attach_service_domain", "detach_service_domain", "create_object_store",
		"create_network_gateway", "update_network_gateway", "delete_network_gateway",
		"set_backup_policy", "run_backup", "restore_backup",
		"query_managed_postgres", "mutate_redis_key",
		"create_managed_redis", "create_managed_postgres", "server_exec",
		"create_host_join_token", "delete_host", "delete_host_join_token",
		"preview_managed_database_version_change", "start_managed_database_version_change",
		"create_service_volume", "delete_service_volume", "create_port_forward":
		return true
	default:
		return false
	}
}

func (handler *Handler) createService(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID     string                 `json:"projectId"`
		Name          string                 `json:"name"`
		Enabled       *bool                  `json:"enabled"`
		HostID        string                 `json:"hostId"`
		Configuration serviceconfig.Snapshot `json:"configuration"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	result, err := handler.services.Create(ctx, identity, automation.CreateServiceInput{
		ProjectID: input.ProjectID, Name: input.Name, Enabled: enabled, HostID: input.HostID, Configuration: input.Configuration,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"service": publicService(result.Service), "requestId": result.RequestID}, nil
}

func (handler *Handler) updateService(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string                 `json:"projectId"`
		ServiceID         string                 `json:"serviceId"`
		Enabled           *bool                  `json:"enabled"`
		HostID            *string                `json:"hostId"`
		ExpectedUpdatedAt int64                  `json:"expectedUpdatedAt"`
		Configuration     serviceconfig.Snapshot `json:"configuration"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if input.Enabled == nil {
		return nil, errInvalidArguments
	}
	hostID := ""
	if input.HostID != nil {
		hostID = *input.HostID
	} else {
		current, err := handler.repository.Service(ctx, input.ProjectID, input.ServiceID)
		if err != nil {
			return nil, err
		}
		hostID = current.HostID
	}
	result, err := handler.services.Update(ctx, identity, automation.UpdateServiceInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Enabled: *input.Enabled, HostID: hostID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt, Configuration: input.Configuration,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"service": publicService(result.Service), "requestId": result.RequestID}, nil
}

func (handler *Handler) redeployService(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		ServiceID         string `json:"serviceId"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.services.Redeploy(ctx, identity, automation.RedeployServiceInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"service": publicService(result.Service), "requestId": result.RequestID}, nil
}

func (handler *Handler) rollbackService(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID         string `json:"projectId"`
		ServiceID         string `json:"serviceId"`
		DeploymentID      string `json:"deploymentId"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.services.Rollback(ctx, identity, automation.RollbackServiceInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, DeploymentID: input.DeploymentID,
		ExpectedUpdatedAt: input.ExpectedUpdatedAt,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"service": publicService(result.Service), "requestId": result.RequestID}, nil
}
