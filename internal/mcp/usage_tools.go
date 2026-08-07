package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
)

func (handler *Handler) readResourceUsage(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		Kind       string `json:"kind"`
		ResourceID string `json:"resourceId"`
		Range      string `json:"range"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.Kind == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("%w: projectId, kind, and resourceId are required", errInvalidArguments)
	}
	return handler.usage.ReadResource(ctx, identity, automation.ReadResourceUsageInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID, Range: input.Range,
	})
}

func (handler *Handler) readProjectUsage(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		Range     string `json:"range"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", errInvalidArguments)
	}
	return handler.usage.ReadProject(ctx, identity, automation.ReadProjectUsageInput{
		ProjectID: input.ProjectID, Range: input.Range,
	})
}
