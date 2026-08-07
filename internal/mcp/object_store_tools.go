package mcp

import (
	"context"
	"encoding/json"

	"github.com/iivankin/platformd/internal/automation"
)

func objectStoreAdminTool() Tool {
	return Tool{
		Name:        "create_object_store",
		Description: "Create a private S3 object store and return its initial access key and secret. Requires an admin token.",
		InputSchema: objectSchema(map[string]any{
			"projectId":            map[string]any{"type": "string"},
			"name":                 map[string]any{"type": "string"},
			"bucketName":           map[string]any{"type": "string"},
			"publicHostname":       map[string]any{"type": "string"},
			"corsOrigins":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"credentialName":       map[string]any{"type": "string"},
			"credentialPermission": map[string]any{"type": "string", "enum": []string{"read_only", "read_write"}},
		}, []string{"projectId", "name"}),
	}
}

func (handler *Handler) createObjectStore(ctx context.Context, arguments json.RawMessage, identity automation.Identity) (any, error) {
	var input struct {
		ProjectID            string   `json:"projectId"`
		Name                 string   `json:"name"`
		BucketName           string   `json:"bucketName"`
		PublicHostname       string   `json:"publicHostname"`
		CORSOrigins          []string `json:"corsOrigins"`
		CredentialName       string   `json:"credentialName"`
		CredentialPermission string   `json:"credentialPermission"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return nil, err
	}
	result, err := handler.objectStores.Create(ctx, identity, automation.CreateObjectStoreInput{
		ProjectID: input.ProjectID, Name: input.Name, BucketName: input.BucketName,
		PublicHostname: input.PublicHostname, CORSOrigins: input.CORSOrigins,
		CredentialName: input.CredentialName, CredentialPermission: input.CredentialPermission,
	})
	if err != nil {
		return nil, err
	}
	store := result.Store
	return map[string]any{
		"objectStore": map[string]any{
			"id": store.ID, "projectId": store.ProjectID, "name": store.Name,
			"bucketName": store.BucketName, "internalHostname": store.Name + "." + store.ProjectName + ".internal",
			"publicHostname": store.PublicHostname, "corsOrigins": store.CORSOrigins,
			"accessKey": result.AccessKey, "secret": result.Secret,
			"credentialPermission": result.Credential.Permission, "region": "us-east-1",
		},
		"requestId": result.RequestID,
	}, nil
}
