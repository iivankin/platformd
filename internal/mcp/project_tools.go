package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
)

func createProjectTool() Tool {
	return Tool{
		Name:        "create_project",
		Description: "Create a project. Requires an unbound admin token.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string"},
		}, []string{"name"}),
	}
}

func (handler *Handler) getProject(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" {
		return nil, fmt.Errorf("%w: projectId is required", errInvalidArguments)
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, automation.ErrProjectBoundary
	}
	project, err := handler.repository.Project(ctx, input.ProjectID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": publicProject(project)}, nil
}

func (handler *Handler) createProject(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.Name == "" {
		return nil, fmt.Errorf("%w: name is required", errInvalidArguments)
	}
	result, err := handler.projects.Create(ctx, identity, input.Name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": publicProject(result.Project), "requestId": result.RequestID}, nil
}
