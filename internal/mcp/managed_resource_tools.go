package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
)

func managedResourceReadTools() []Tool {
	identityProperties := map[string]any{
		"projectId": map[string]any{"type": "string"},
		"kind": map[string]any{
			"type": "string", "enum": []string{
				automation.ManagedResourcePostgres,
				automation.ManagedResourceRedis,
				automation.ManagedResourceObjectStore,
			},
		},
		"resourceId": map[string]any{"type": "string"},
	}
	return []Tool{
		{
			Name:        "list_managed_resources",
			Description: "List PostgreSQL, Redis, and private S3 lifecycle/configuration metadata in one visible project. Never returns database rows, Redis values, objects, passwords, or credentials.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]any{"type": "string"},
			}, []string{"projectId"}),
		},
		{
			Name:        "get_managed_resource",
			Description: "Read one PostgreSQL, Redis, or private S3 resource's lifecycle/configuration metadata without data or credentials.",
			InputSchema: objectSchema(identityProperties, []string{"projectId", "kind", "resourceId"}),
		},
		{
			Name:        "read_managed_resource_backups",
			Description: "Read one managed resource's backup policy and bounded backup history.",
			InputSchema: objectSchema(map[string]any{
				"projectId":    identityProperties["projectId"],
				"kind":         identityProperties["kind"],
				"resourceId":   identityProperties["resourceId"],
				"beforeMillis": map[string]any{"type": "integer", "minimum": 0},
				"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20},
			}, []string{"projectId", "kind", "resourceId"}),
		},
	}
}

func (handler *Handler) listManagedResources(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	resources, err := handler.managed.List(ctx, identity, input.ProjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"resources": resources}, nil
}

func (handler *Handler) getManagedResource(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	input, err := decodeManagedResourceIdentity(arguments)
	if err != nil {
		return nil, err
	}
	resource, err := handler.managed.Get(ctx, identity, input.ProjectID, input.Kind, input.ResourceID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"resource": resource}, nil
}

func (handler *Handler) readManagedResourceBackups(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		ProjectID    string `json:"projectId"`
		Kind         string `json:"kind"`
		ResourceID   string `json:"resourceId"`
		BeforeMillis int64  `json:"beforeMillis"`
		Limit        int    `json:"limit"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if input.Limit == 0 {
		input.Limit = 20
	}
	return handler.managed.BackupStatus(
		ctx, identity, input.ProjectID, input.Kind, input.ResourceID, input.BeforeMillis, input.Limit,
	)
}

type managedResourceIdentity struct {
	ProjectID  string `json:"projectId"`
	Kind       string `json:"kind"`
	ResourceID string `json:"resourceId"`
}

func decodeManagedResourceIdentity(arguments json.RawMessage) (managedResourceIdentity, error) {
	var input managedResourceIdentity
	if err := decodeArguments(arguments, &input); err != nil {
		return managedResourceIdentity{}, err
	}
	return input, nil
}
