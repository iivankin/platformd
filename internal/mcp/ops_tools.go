package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/journallogs"
)

func readInstallationUsageTool() Tool {
	return Tool{
		Name:        "read_installation_usage",
		Description: "Read live installation usage or rolled-up installation/host history. Requires an unbound admin token.",
		InputSchema: objectSchema(map[string]any{
			"range": map[string]any{"type": "string", "enum": []string{"1h", "6h", "1d", "7d", "30d"}},
			"scope": map[string]any{"type": "string", "enum": []string{"installation", "host"}},
		}, nil),
	}
}

func readManagedResourceLogsTool() Tool {
	return Tool{
		Name:        "read_managed_resource_logs",
		Description: "Read a bounded recent log window for one PostgreSQL, Redis, or object store resource.",
		InputSchema: objectSchema(map[string]any{
			"projectId":    map[string]any{"type": "string"},
			"kind":         map[string]any{"type": "string", "enum": []string{"postgres", "redis", "object_store"}},
			"resourceId":   map[string]any{"type": "string"},
			"deploymentId": map[string]any{"type": "string"},
			"contains":     map[string]any{"type": "string", "maxLength": 256},
			"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": containerlogs.MaximumLimit},
		}, []string{"projectId", "kind", "resourceId"}),
	}
}

func readInfrastructureLogsTool() Tool {
	return Tool{
		Name:        "read_infrastructure_logs",
		Description: "Read a bounded recent host journald window for platformd infrastructure units.",
		InputSchema: objectSchema(map[string]any{
			"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": journallogs.MaximumLimit},
			"beforeCursor": map[string]any{"type": "string"},
		}, nil),
	}
}

func readDiskPressureTool() Tool {
	return Tool{
		Name: "read_disk_pressure", Description: "Read the current disk pressure snapshot and component usage.",
		InputSchema: objectSchema(nil, nil),
	}
}

func runContainerImageGCTool() Tool {
	return Tool{
		Name: "run_container_image_gc", Description: "Force container image garbage collection. Requires an admin token.",
		InputSchema: objectSchema(nil, nil),
	}
}

func listAuditEventsTool() Tool {
	return Tool{
		Name:        "list_audit_events",
		Description: "List bounded audit events with optional project and target filters.",
		InputSchema: objectSchema(map[string]any{
			"projectId":  map[string]any{"type": "string"},
			"actorKind":  map[string]any{"type": "string"},
			"action":     map[string]any{"type": "string"},
			"result":     map[string]any{"type": "string"},
			"targetKind": map[string]any{"type": "string"},
			"targetId":   map[string]any{"type": "string"},
			"cursor":     map[string]any{"type": "string"},
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
		}, nil),
	}
}

func (handler *Handler) readInstallationUsage(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		Range string `json:"range"`
		Scope string `json:"scope"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	return handler.usage.ReadInstallation(ctx, identity, automation.ReadInstallationUsageInput{Range: input.Range, Scope: input.Scope})
}

func (handler *Handler) readManagedResourceLogs(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID    string `json:"projectId"`
		Kind         string `json:"kind"`
		ResourceID   string `json:"resourceId"`
		DeploymentID string `json:"deploymentId"`
		Contains     string `json:"contains"`
		Limit        int    `json:"limit"`
	}
	if err := decodeArguments(arguments, &input); err != nil || input.ProjectID == "" || input.Kind == "" || input.ResourceID == "" || input.Limit < 0 || input.Limit > containerlogs.MaximumLimit {
		return nil, fmt.Errorf("%w: projectId, kind, and resourceId are required", errInvalidArguments)
	}
	return handler.logs.ReadResource(ctx, identity, automation.ReadResourceLogsInput{
		ProjectID: input.ProjectID, Kind: input.Kind, ResourceID: input.ResourceID,
		DeploymentID: input.DeploymentID, Contains: input.Contains, Limit: input.Limit,
	})
}

func (handler *Handler) readInfrastructureLogs(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		Limit        int    `json:"limit"`
		BeforeCursor string `json:"beforeCursor"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	return handler.infrastructureLogs.Read(ctx, identity, automation.ReadInfrastructureLogsInput{
		Limit: input.Limit, BeforeCursor: input.BeforeCursor,
	})
}

func (handler *Handler) readDiskPressure(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var empty struct{}
	if err := decodeArguments(arguments, &empty); err != nil {
		return nil, err
	}
	return handler.diskPressure.Read(ctx, identity)
}

func (handler *Handler) runContainerImageGC(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var empty struct{}
	if err := decodeArguments(arguments, &empty); err != nil {
		return nil, err
	}
	return handler.imageGC.Force(ctx, identity)
}

func (handler *Handler) listAuditEvents(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID  string `json:"projectId"`
		ActorKind  string `json:"actorKind"`
		Action     string `json:"action"`
		Result     string `json:"result"`
		TargetKind string `json:"targetKind"`
		TargetID   string `json:"targetId"`
		Cursor     string `json:"cursor"`
		Limit      int    `json:"limit"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	return handler.audit.List(ctx, identity, automation.ListAuditEventsInput{
		ProjectID: input.ProjectID, ActorKind: input.ActorKind, Action: input.Action, Result: input.Result,
		TargetKind: input.TargetKind, TargetID: input.TargetID, Cursor: input.Cursor, Limit: input.Limit,
	})
}
