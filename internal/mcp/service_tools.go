package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/serviceconfig"
)

func adminTools() []Tool {
	configuration := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"imageReference":    map[string]any{"type": "string"},
			"imageCredentialId": map[string]any{"type": "string"},
			"command":           map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"args":              map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"environment":       map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}},
			"secretReferences":  map[string]any{"type": "array"},
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
		"required":             []string{"imageReference"},
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
				"enabled": map[string]any{"type": "boolean", "default": true}, "configuration": configuration,
			}, []string{"projectId", "name", "configuration"}),
		},
		{
			Name: "update_service", Description: "Update desired service state with optimistic concurrency. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": base["projectId"], "serviceId": base["serviceId"],
				"enabled": map[string]any{"type": "boolean"}, "expectedUpdatedAt": map[string]any{"type": "integer"},
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
	case "create_service", "update_service", "redeploy_service", "rollback_service", "create_managed_redis", "create_managed_postgres", "server_exec", "preview_managed_database_version_change", "start_managed_database_version_change", "create_service_volume", "delete_service_volume":
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
		ProjectID: input.ProjectID, Name: input.Name, Enabled: enabled, Configuration: input.Configuration,
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
		ExpectedUpdatedAt int64                  `json:"expectedUpdatedAt"`
		Configuration     serviceconfig.Snapshot `json:"configuration"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if input.Enabled == nil {
		return nil, errInvalidArguments
	}
	result, err := handler.services.Update(ctx, identity, automation.UpdateServiceInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Enabled: *input.Enabled,
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
