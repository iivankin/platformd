package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/databaseversion"
	"github.com/iivankin/platformd/internal/state"
)

func databaseVersionProperties(withImageTag, withExpectedDigest, withOperation bool) map[string]any {
	properties := map[string]any{
		"projectId": map[string]any{"type": "string"},
		"kind": map[string]any{
			"type": "string", "enum": []string{databaseversion.Postgres, databaseversion.Redis},
		},
		"resourceId": map[string]any{"type": "string"},
	}
	if withImageTag {
		properties["imageTag"] = map[string]any{"type": "string"}
	}
	if withExpectedDigest {
		properties["expectedTargetDigest"] = map[string]any{"type": "string"}
	}
	if withOperation {
		properties["operationId"] = map[string]any{"type": "string"}
	}
	return properties
}

func readDatabaseVersionTool() Tool {
	return Tool{
		Name:        "read_managed_database_version_change",
		Description: "Read one observational PostgreSQL or Redis new-volume version-change operation in a visible project.",
		InputSchema: objectSchema(databaseVersionProperties(false, false, true),
			[]string{"projectId", "kind", "resourceId", "operationId"}),
	}
}

func startDatabaseVersionTool() Tool {
	return Tool{
		Name:        "start_managed_database_version_change",
		Description: "Start a PostgreSQL or Redis image change through a new volume and direct data transfer, passing the exact target digest returned by preview. The database is unavailable during migration and rollback after pointer publication requires a backup. Requires an admin token.",
		InputSchema: objectSchema(databaseVersionProperties(true, true, false),
			[]string{"projectId", "kind", "resourceId", "imageTag", "expectedTargetDigest"}),
	}
}

func previewDatabaseVersionTool() Tool {
	return Tool{
		Name:        "preview_managed_database_version_change",
		Description: "Resolve an exact target digest and report current data size and free-space requirements before a PostgreSQL or Redis version change. No preview state is stored. Requires an admin token.",
		InputSchema: objectSchema(databaseVersionProperties(true, false, false),
			[]string{"projectId", "kind", "resourceId", "imageTag"}),
	}
}

type databaseVersionToolIdentity struct {
	ProjectID  string `json:"projectId"`
	Kind       string `json:"kind"`
	ResourceID string `json:"resourceId"`
}

func (handler *Handler) startDatabaseVersionChange(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		databaseVersionToolIdentity
		ImageTag             string `json:"imageTag"`
		ExpectedTargetDigest string `json:"expectedTargetDigest"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, automation.ErrProjectBoundary
	}
	result, err := handler.versions.Start(
		ctx, input.Kind, input.ProjectID, input.ResourceID, input.ImageTag, input.ExpectedTargetDigest,
		databaseversion.Actor{Kind: "token", ID: identity.TokenID},
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"operation": publicDatabaseVersionOperation(result.Operation),
		"sourceTag": result.SourceTag, "sourceDigest": result.SourceDigest,
		"targetTag": result.TargetTag, "targetDigest": result.TargetDigest,
	}, nil
}

func (handler *Handler) previewDatabaseVersionChange(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		databaseVersionToolIdentity
		ImageTag string `json:"imageTag"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, automation.ErrProjectBoundary
	}
	return handler.versions.Preview(ctx, input.Kind, input.ProjectID, input.ResourceID, input.ImageTag)
}

func (handler *Handler) readDatabaseVersionChange(
	ctx context.Context,
	arguments json.RawMessage,
	identity automation.Identity,
) (any, error) {
	var input struct {
		databaseVersionToolIdentity
		OperationID string `json:"operationId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	if !identity.AllowsProject(input.ProjectID) {
		return nil, automation.ErrProjectBoundary
	}
	operation, err := handler.versions.Operation(
		ctx, input.Kind, input.ProjectID, input.ResourceID, input.OperationID,
	)
	if err != nil {
		return nil, err
	}
	return map[string]any{"operation": publicDatabaseVersionOperation(operation)}, nil
}

func publicDatabaseVersionOperation(operation state.Operation) map[string]any {
	result := map[string]any{
		"id": operation.ID, "kind": operation.Kind, "targetId": operation.TargetID,
		"status": operation.Status, "progress": operation.Progress,
		"errorCode": operation.ErrorCode, "errorMessage": operation.ErrorMessage,
		"startedAt": operation.StartedAtMillis,
	}
	if operation.FinishedAtMillis > 0 {
		result["finishedAt"] = operation.FinishedAtMillis
	}
	return result
}
