package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
)

func (handler *Handler) readServiceLogs(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID    string `json:"projectId"`
		ServiceID    string `json:"serviceId"`
		DeploymentID string `json:"deploymentId"`
		Contains     string `json:"contains"`
		Limit        int    `json:"limit"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" || input.Limit < 0 || input.Limit > containerlogs.MaximumLimit {
		return nil, fmt.Errorf("%w: projectId/serviceId are required and limit must be 1..2000 when set", errInvalidArguments)
	}
	window, err := handler.logs.ReadService(ctx, identity, automation.ReadServiceLogsInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, DeploymentID: input.DeploymentID,
		Contains: input.Contains, Limit: input.Limit,
	})
	if err != nil {
		return nil, err
	}
	return window, nil
}
