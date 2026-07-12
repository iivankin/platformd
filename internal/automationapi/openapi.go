package automationapi

import (
	"net/http"
	"strconv"
)

func serveOpenAPI(hostname string) http.HandlerFunc {
	document := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title": "platformd Automation API", "version": "v1",
		},
		"servers":  []map[string]string{{"url": "https://" + hostname}},
		"security": []map[string][]string{{"bearerAuth": {}}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]string{"type": "http", "scheme": "bearer"},
			},
			"schemas": serviceMutationSchemas(),
		},
		"paths": map[string]any{
			"/api/v1/me":                                                    readOperation("Read current token identity"),
			"/api/v1/managed-images/{engine}/tags":                          managedImageTagsOperation(),
			"/api/v1/projects":                                              readOperation("List visible projects"),
			"/api/v1/projects/{projectID}":                                  readOperation("Get one visible project"),
			"/api/v1/projects/{projectID}/services":                         readWriteOperation("List services in one visible project", "Create a service (admin token)"),
			"/api/v1/projects/{projectID}/services/{serviceID}":             readUpdateOperation("Get one visible service", "Update a service (admin token)"),
			"/api/v1/projects/{projectID}/services/{serviceID}/deployments": readOperation("List bounded deployment history"),
			"/api/v1/projects/{projectID}/services/{serviceID}/logs":        logReadOperation(),
			"/api/v1/projects/{projectID}/services/{serviceID}/redeploy":    mutationOperation("Redeploy a service (admin token)", "ServiceRedeployRequest"),
			"/api/v1/projects/{projectID}/services/{serviceID}/rollback":    mutationOperation("Rollback a service (admin token)", "ServiceRollbackRequest"),
		},
	}
	return func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, document)
	}
}

func managedImageTagsOperation() map[string]any {
	return map[string]any{"get": map[string]any{
		"summary": "List one Docker Hub page of official PostgreSQL or Redis image tags",
		"parameters": []map[string]any{
			{"name": "engine", "in": "path", "required": true, "schema": map[string]any{"type": "string", "enum": []string{"postgres", "redis"}}},
			{"name": "page", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "default": 1}},
			{"name": "pageSize", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}},
			{"name": "search", "in": "query", "description": "Case-insensitive filter within the fetched page", "schema": map[string]any{"type": "string", "maxLength": 128}},
		},
		"responses": map[string]any{
			"200": map[string]string{"description": "Stateless official tag page"},
			"400": map[string]string{"description": "Invalid engine or page"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"502": map[string]string{"description": "Docker Hub unavailable"},
		},
	}}
}

func logReadOperation() map[string]any {
	return map[string]any{"get": map[string]any{
		"summary": "Read a bounded recent service log window",
		"parameters": []map[string]any{
			{"name": "deploymentId", "in": "query", "schema": map[string]string{"type": "string"}},
			{"name": "contains", "in": "query", "schema": map[string]any{"type": "string", "maxLength": 256}},
			{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "default": 500}},
		},
		"responses": map[string]any{
			"200": map[string]string{"description": "Structured timestamp/stream/text records"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Outside token project boundary"},
			"404": map[string]string{"description": "Service not found"},
		},
	}}
}

func readWriteOperation(readSummary, writeSummary string) map[string]any {
	operation := readOperation(readSummary)
	operation["post"] = writeMethod(writeSummary, http.StatusCreated, "ServiceCreateRequest")
	return operation
}

func readUpdateOperation(readSummary, writeSummary string) map[string]any {
	operation := readOperation(readSummary)
	operation["put"] = writeMethod(writeSummary, http.StatusOK, "ServiceUpdateRequest")
	return operation
}

func mutationOperation(summary, schema string) map[string]any {
	return map[string]any{"post": writeMethod(summary, http.StatusOK, schema)}
}

func writeMethod(summary string, successStatus int, schema string) map[string]any {
	return map[string]any{
		"summary": summary,
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]string{"$ref": "#/components/schemas/" + schema},
			}},
		},
		"responses": map[string]any{
			statusCode(successStatus): map[string]string{"description": "Successful response"},
			"400":                     map[string]string{"description": "Invalid mutation input"},
			"401":                     map[string]string{"description": "Missing or invalid Bearer token"},
			"403":                     map[string]string{"description": "Admin role or project boundary denied"},
			"409":                     map[string]string{"description": "Optimistic concurrency or service state conflict"},
		},
	}
}

func serviceMutationSchemas() map[string]any {
	configuration := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"imageReference"},
		"properties": map[string]any{
			"imageReference":    map[string]string{"type": "string"},
			"imageCredentialId": map[string]string{"type": "string"},
			"command":           map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"args":              map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
			"environment":       map[string]any{"type": "object", "additionalProperties": map[string]string{"type": "string"}},
			"secretReferences": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"environmentName", "secretId"},
				"properties": map[string]any{"environmentName": map[string]string{"type": "string"}, "secretId": map[string]string{"type": "string"}},
			}},
			"targetPort":            map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"healthPath":            map[string]string{"type": "string"},
			"startupTimeoutSeconds": map[string]any{"type": "integer", "minimum": 1, "maximum": 3600},
			"cpuMillicores":         map[string]any{"type": "integer", "minimum": 0},
			"memoryMaxBytes":        map[string]any{"type": "integer", "minimum": 0},
			"volumeMounts": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"volumeId", "containerPath"},
				"properties": map[string]any{"volumeId": map[string]string{"type": "string"}, "containerPath": map[string]string{"type": "string"}},
			}},
		},
	}
	return map[string]any{
		"ServiceConfiguration": configuration,
		"ServiceCreateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name", "configuration"},
			"properties": map[string]any{
				"name": map[string]string{"type": "string"}, "enabled": map[string]any{"type": "boolean", "default": true},
				"configuration": map[string]string{"$ref": "#/components/schemas/ServiceConfiguration"},
			},
		},
		"ServiceUpdateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"enabled", "expectedUpdatedAt", "configuration"},
			"properties": map[string]any{
				"enabled": map[string]string{"type": "boolean"}, "expectedUpdatedAt": map[string]string{"type": "integer"},
				"configuration": map[string]string{"$ref": "#/components/schemas/ServiceConfiguration"},
			},
		},
		"ServiceRedeployRequest": expectedUpdatedSchema(nil),
		"ServiceRollbackRequest": expectedUpdatedSchema(map[string]any{"deploymentId": map[string]string{"type": "string"}}),
	}
}

func expectedUpdatedSchema(extra map[string]any) map[string]any {
	properties := map[string]any{"expectedUpdatedAt": map[string]string{"type": "integer"}}
	required := []string{"expectedUpdatedAt"}
	for name, schema := range extra {
		properties[name] = schema
		required = append(required, name)
	}
	return map[string]any{
		"type": "object", "additionalProperties": false, "required": required, "properties": properties,
	}
}

func statusCode(status int) string {
	return strconv.Itoa(status)
}

func readOperation(summary string) map[string]any {
	return map[string]any{
		"get": map[string]any{
			"summary": summary,
			"responses": map[string]any{
				"200": map[string]string{"description": "Successful response"},
				"401": map[string]string{"description": "Missing or invalid Bearer token"},
				"403": map[string]string{"description": "Outside token project boundary"},
			},
		},
	}
}
