package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
)

func readManagedResourceStatsTool() Tool {
	return Tool{
		Name: "read_managed_resource_stats",
		Description: "Read live engine stats or rolled-up stats history for one PostgreSQL, Redis, or object store. " +
			"Omit range for the live snapshot; set range for history. Distinct from read_resource_usage (cgroup CPU/memory).",
		InputSchema: objectSchema(map[string]any{
			"projectId":  map[string]any{"type": "string"},
			"kind":       map[string]any{"type": "string", "enum": []string{"postgres", "redis", "object_store"}},
			"resourceId": map[string]any{"type": "string"},
			"range":      map[string]any{"type": "string", "enum": []string{"1h", "6h", "1d", "7d", "30d"}},
		}, []string{"projectId", "kind", "resourceId"}),
	}
}

func (handler *Handler) readManagedResourceStats(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		Kind       string `json:"kind"`
		ResourceID string `json:"resourceId"`
		Range      string `json:"range"`
	}
	if err := decodeArguments(arguments, &input); err != nil ||
		input.ProjectID == "" || input.Kind == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("%w: projectId, kind, and resourceId are required", errInvalidArguments)
	}
	return handler.managedStats.Read(ctx, identity, automation.ReadManagedResourceStatsInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID, Range: input.Range,
	})
}
