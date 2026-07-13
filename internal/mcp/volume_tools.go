package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

func listVolumesTool() Tool {
	return Tool{
		Name: "list_service_volumes", Description: "List ordinary writable volumes owned by one service in a visible project.",
		InputSchema: volumeTargetSchema(false),
	}
}

func volumeAdminTools() []Tool {
	return []Tool{
		{
			Name: "create_service_volume", Description: "Create an empty ordinary writable volume owned by one service. Mount it with a separate service update. Requires an admin token.",
			InputSchema: objectSchema(map[string]any{
				"projectId": map[string]string{"type": "string"},
				"serviceId": map[string]string{"type": "string"},
				"name":      map[string]string{"type": "string"},
				"ownerUid":  map[string]any{"type": "integer", "minimum": 0, "maximum": 1<<32 - 2},
				"ownerGid":  map[string]any{"type": "integer", "minimum": 0, "maximum": 1<<32 - 2},
			}, []string{"projectId", "serviceId", "name", "ownerUid", "ownerGid"}),
		},
		{
			Name: "delete_service_volume", Description: "Delete an unmounted ordinary service volume and its data. Requires an admin token.",
			InputSchema: volumeTargetSchema(true),
		},
	}
}

func volumeTargetSchema(withVolume bool) map[string]any {
	properties := map[string]any{
		"projectId": map[string]string{"type": "string"},
		"serviceId": map[string]string{"type": "string"},
	}
	required := []string{"projectId", "serviceId"}
	if withVolume {
		properties["volumeId"] = map[string]string{"type": "string"}
		required = append(required, "volumeId")
	}
	return objectSchema(properties, required)
}

func (handler *Handler) listVolumes(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		ServiceID string `json:"serviceId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	items, err := handler.volumes.List(ctx, identity, input.ProjectID, input.ServiceID)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, publicVolume(item))
	}
	return map[string]any{"volumes": result}, nil
}

func (handler *Handler) createVolume(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input automation.CreateVolumeInput
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.volumes.Create(ctx, identity, input)
	if err != nil {
		return nil, err
	}
	return map[string]any{"volume": publicVolume(result.Volume), "requestId": result.RequestID}, nil
}

func (handler *Handler) deleteVolume(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID string `json:"projectId"`
		ServiceID string `json:"serviceId"`
		VolumeID  string `json:"volumeId"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.volumes.Delete(ctx, identity, input.ProjectID, input.ServiceID, input.VolumeID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"volume": publicVolume(result.Volume), "requestId": result.RequestID}, nil
}

func publicVolume(item state.Volume) map[string]any {
	return map[string]any{
		"id": item.ID, "projectId": item.ProjectID, "serviceId": item.ServiceID,
		"name": item.Name, "ownerUid": item.OwnerUID, "ownerGid": item.OwnerGID,
		"createdAt": item.CreatedAtMillis,
	}
}
