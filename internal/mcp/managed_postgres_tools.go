package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
)

func managedPostgresAdminTool() Tool {
	return Tool{
		Name:        "create_managed_postgres",
		Description: "Create a managed PostgreSQL owner database from an official image tag and return its persistent owner password. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
			"imageTag": map[string]any{"type": "string"}, "cpuMillicores": map[string]any{"type": "integer", "minimum": 0},
			"memoryBytes": map[string]any{"type": "integer", "minimum": 0},
		}, []string{"projectId", "name", "imageTag"}),
	}
}

func (handler *Handler) createManagedPostgres(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID     string `json:"projectId"`
		Name          string `json:"name"`
		ImageTag      string `json:"imageTag"`
		CPUMillicores int64  `json:"cpuMillicores"`
		MemoryBytes   int64  `json:"memoryBytes"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.postgres.Create(ctx, identity, automation.CreateManagedPostgresInput{
		ProjectID: input.ProjectID, Name: input.Name, ImageTag: input.ImageTag,
		CPUMillicores: input.CPUMillicores, MemoryBytes: input.MemoryBytes,
	})
	if err != nil {
		return nil, err
	}
	resource := result.Resource
	return map[string]any{
		"postgres": map[string]any{
			"id": resource.ID, "projectId": resource.ProjectID, "name": resource.Name,
			"hostname": resource.Name + "." + resource.ProjectName + ".internal", "port": 5432,
			"imageTag": resource.ImageTag, "imageDigest": resource.ImageDigest,
			"databaseName": resource.DatabaseName, "ownerUsername": resource.OwnerUsername,
			"ownerPassword": result.OwnerPassword, "cpuMillicores": resource.CPUMillicores,
			"memoryBytes": resource.MemoryMaxBytes,
		},
		"requestId": result.RequestID,
	}, nil
}
