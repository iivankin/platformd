package automationapi

import "net/http"

func addRegistryPaths(paths map[string]any) {
	paths["/api/v1/registry"] = registryReadOperation("Read the embedded Registry hostname")
	paths["/api/v1/registry/hostname"] = registryMutationOperation("Set or clear the embedded Registry hostname (unbound admin token)", "put", http.StatusOK, "RegistryHostnameRequest")
	paths["/api/v1/registry/repositories"] = registryCollectionOperation()
	paths["/api/v1/registry/repositories/{repositoryID}"] = registryRepositoryOperation()
	paths["/api/v1/registry/repositories/{repositoryID}/public-pull"] = registryMutationOperation("Enable or disable anonymous pulls (unbound admin token)", "put", http.StatusOK, "RegistryPublicPullRequest")
	paths["/api/v1/registry/repositories/{repositoryID}/images"] = registryReadOperation("List Registry manifests with bounded cursor pagination")
	paths["/api/v1/registry/repositories/{repositoryID}/images/{digest}"] = registryReadOperation("Read one Registry manifest and its referenced blobs")
	paths["/api/v1/registry/repositories/{repositoryID}/tags/{tag}"] = registryDeleteOperation("Delete one Registry tag (unbound admin token)", http.StatusOK)
	paths["/api/v1/registry/repositories/{repositoryID}/manifests/{digest}"] = registryDeleteOperation("Delete an unreferenced Registry manifest (unbound admin token)", http.StatusOK)
	paths["/api/v1/registry/repositories/{repositoryID}/credentials"] = registryCredentialCollectionOperation()
	paths["/api/v1/registry/repositories/{repositoryID}/credentials/{credentialID}"] = registryDeleteOperation("Delete one Registry credential (unbound admin token)", http.StatusNoContent)
	paths["/api/v1/registry/repositories/{repositoryID}/cleanup"] = registryMutationOperation("Preview or delete unreferenced Registry blobs (unbound admin token)", "post", http.StatusOK, "RegistryCleanupRequest")
}

func registryCollectionOperation() map[string]any {
	operation := registryReadOperation("List embedded Registry repositories and storage statistics")
	operation["post"] = registryWriteMethod("Create a Registry repository and return its initial credential once (unbound admin token)", http.StatusCreated, "RegistryRepositoryCreateRequest")
	return operation
}

func registryRepositoryOperation() map[string]any {
	operation := registryReadOperation("Read one embedded Registry repository")
	operation["delete"] = registryWriteMethod("Delete a confirmed Registry repository and its payloads (unbound admin token)", http.StatusNoContent, "RegistryRepositoryDeleteRequest")
	return operation
}

func registryCredentialCollectionOperation() map[string]any {
	operation := registryReadOperation("List Registry credentials without secret material")
	operation["post"] = registryWriteMethod("Create a Registry credential and return its secret once (unbound admin token)", http.StatusCreated, "RegistryCredentialCreateRequest")
	return operation
}

func registryReadOperation(summary string) map[string]any {
	return map[string]any{"get": map[string]any{
		"summary": summary,
		"responses": map[string]any{
			"200": map[string]string{"description": "Successful response"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Project-bound token denied"},
			"404": map[string]string{"description": "Registry resource not found"},
		},
	}}
}

func registryMutationOperation(summary, method string, status int, schema string) map[string]any {
	return map[string]any{method: registryWriteMethod(summary, status, schema)}
}

func registryDeleteOperation(summary string, status int) map[string]any {
	return map[string]any{"delete": map[string]any{
		"summary":   summary,
		"responses": registryMutationResponses(status),
	}}
}

func registryWriteMethod(summary string, status int, schema string) map[string]any {
	return map[string]any{
		"summary": summary,
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]string{"$ref": "#/components/schemas/" + schema},
			}},
		},
		"responses": registryMutationResponses(status),
	}
}

func registryMutationResponses(status int) map[string]any {
	return map[string]any{
		statusCode(status): map[string]string{"description": "Successful response"},
		"400":              map[string]string{"description": "Invalid Registry mutation input"},
		"401":              map[string]string{"description": "Missing or invalid Bearer token"},
		"403":              map[string]string{"description": "Unbound admin token required"},
		"404":              map[string]string{"description": "Registry resource not found"},
		"409":              map[string]string{"description": "Registry name, reference, or maintenance conflict"},
	}
}

func registryMutationSchemas() map[string]any {
	return map[string]any{
		"RegistryHostnameRequest": objectSchema([]string{"hostname"}, map[string]any{
			"hostname": map[string]string{"type": "string"},
		}),
		"RegistryRepositoryCreateRequest": objectSchema([]string{"name"}, map[string]any{
			"name": map[string]string{"type": "string"}, "publicPull": map[string]string{"type": "boolean"},
			"credentialName":       map[string]string{"type": "string"},
			"credentialPermission": map[string]any{"type": "string", "enum": []string{"pull", "pull_push"}},
		}),
		"RegistryPublicPullRequest": objectSchema([]string{"publicPull"}, map[string]any{
			"publicPull": map[string]string{"type": "boolean"},
		}),
		"RegistryRepositoryDeleteRequest": objectSchema([]string{"expectedName"}, map[string]any{
			"expectedName": map[string]string{"type": "string"},
		}),
		"RegistryCredentialCreateRequest": objectSchema([]string{"name", "permission"}, map[string]any{
			"name":       map[string]string{"type": "string"},
			"permission": map[string]any{"type": "string", "enum": []string{"pull", "pull_push"}},
		}),
		"RegistryCleanupRequest": objectSchema([]string{"dryRun"}, map[string]any{
			"dryRun": map[string]string{"type": "boolean"},
		}),
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": required, "properties": properties,
	}
}
