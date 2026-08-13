package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
)

func (handler *Handler) readServiceLogs(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID    string `json:"projectId"`
		ServiceID    string `json:"serviceId"`
		DeploymentID string `json:"deploymentId"`
		Contains     string `json:"contains"`
		SeverityText string `json:"severityText"`
		TraceID      string `json:"traceId"`
		SpanID       string `json:"spanId"`
		From         int64  `json:"from"`
		To           int64  `json:"to"`
		Order        string `json:"order"`
		Limit        int    `json:"limit"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.ServiceID == "" ||
		input.From < 0 || input.To < 0 || input.From > 0 && input.To > 0 && input.To < input.From ||
		(input.Order != "" && input.Order != "desc" && input.Order != "asc") ||
		input.Limit < 0 || input.Limit > containerlogs.MaximumLimit {
		return nil, fmt.Errorf("%w: projectId/serviceId are required and limit must be 1..2000 when set", errInvalidArguments)
	}
	var from, to time.Time
	if input.From > 0 {
		from = time.UnixMilli(input.From)
	}
	if input.To > 0 {
		to = time.UnixMilli(input.To)
	}
	window, err := handler.logs.ReadService(ctx, identity, automation.ReadServiceLogsInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, DeploymentID: input.DeploymentID,
		Contains: input.Contains, SeverityText: input.SeverityText, TraceID: input.TraceID, SpanID: input.SpanID,
		From: from, To: to, Limit: input.Limit, Ascending: input.Order == "asc",
	})
	if err != nil {
		return nil, err
	}
	return window, nil
}
