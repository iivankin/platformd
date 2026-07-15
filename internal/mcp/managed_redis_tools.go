package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
)

func managedRedisAdminTool() Tool {
	return Tool{
		Name:        "create_managed_redis",
		Description: "Create a managed RDB-only Redis resource from an official image tag and return its persistent password. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId":     map[string]any{"type": "string"},
			"name":          map[string]any{"type": "string"},
			"imageTag":      map[string]any{"type": "string"},
			"cpuMillicores": map[string]any{"type": "integer", "minimum": 0},
			"memoryBytes":   map[string]any{"type": "integer", "minimum": 0},
		}, []string{"projectId", "name", "imageTag"}),
	}
}

func (handler *Handler) createManagedRedis(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
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
	result, err := handler.redis.Create(ctx, identity, automation.CreateManagedRedisInput{
		ProjectID: input.ProjectID, Name: input.Name, ImageTag: input.ImageTag,
		CPUMillicores: input.CPUMillicores, MemoryBytes: input.MemoryBytes,
	})
	if err != nil {
		return nil, err
	}
	resource := result.Resource
	return map[string]any{
		"redis": map[string]any{
			"id": resource.ID, "projectId": resource.ProjectID, "name": resource.Name,
			"hostname": resource.Name + "." + resource.ProjectName + ".internal", "port": 6379,
			"imageTag": resource.ImageTag, "imageDigest": resource.ImageDigest,
			"cpuMillicores": resource.CPUMillicores, "memoryBytes": resource.MemoryMaxBytes,
			"password": result.Password,
		},
		"requestId": result.RequestID,
	}, nil
}
