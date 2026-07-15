package automationapi

import (
	"net/http"
	"strconv"
)

type openAPIFeatures struct {
	projects         bool
	objectStores     bool
	domains          bool
	serverExec       bool
	managedResources bool
	databaseVersions bool
	volumes          bool
	registry         bool
}

func serveOpenAPI(hostname string, features openAPIFeatures) http.HandlerFunc {
	schemas := serviceMutationSchemas()
	paths := map[string]any{
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
		"/api/v1/projects/{projectID}/redis":                            managedRedisOperation(),
		"/api/v1/projects/{projectID}/redis/{redisID}":                  readOperation("Get one managed Redis resource"),
		"/api/v1/projects/{projectID}/postgres":                         managedPostgresOperation(),
		"/api/v1/projects/{projectID}/postgres/{postgresID}":            readOperation("Get one managed PostgreSQL resource"),
	}
	if features.projects {
		paths["/api/v1/projects"] = projectCollectionOperation()
	}
	if features.objectStores {
		paths["/api/v1/projects/{projectID}/object-stores"] = objectStoreCollectionOperation()
		paths["/api/v1/projects/{projectID}/object-stores/{storeID}"] = readOperation("Get one private S3 resource")
	}
	if features.domains {
		paths["/api/v1/projects/{projectID}/services/{serviceID}/domains"] = domainCollectionOperation()
		paths["/api/v1/projects/{projectID}/services/{serviceID}/domains/{hostname}"] = domainDeleteOperation()
	}
	if features.serverExec {
		paths["/api/v1/server/exec"] = serverExecOperation()
	}
	if features.managedResources {
		paths["/api/v1/projects/{projectID}/managed-resources"] = managedResourceListOperation()
		paths["/api/v1/projects/{projectID}/managed-resources/{kind}/{resourceID}"] = managedResourceReadOperation("Read one managed resource's lifecycle/configuration metadata")
		paths["/api/v1/projects/{projectID}/managed-resources/{kind}/{resourceID}/backups"] = managedResourceBackupReadOperation()
	}
	if features.databaseVersions {
		paths["/api/v1/projects/{projectID}/managed-databases/{kind}/{resourceID}/version-change/preview"] = databaseVersionPreviewOperation()
		paths["/api/v1/projects/{projectID}/managed-databases/{kind}/{resourceID}/version-change"] = databaseVersionStartOperation()
		paths["/api/v1/projects/{projectID}/managed-databases/{kind}/{resourceID}/version-change/{operationID}"] = databaseVersionReadOperation()
	}
	if features.volumes {
		paths["/api/v1/projects/{projectID}/services/{serviceID}/volumes"] = volumeCollectionOperation()
		paths["/api/v1/projects/{projectID}/services/{serviceID}/volumes/{volumeID}"] = volumeDeleteOperation()
	}
	if features.registry {
		addRegistryPaths(paths)
		for name, schema := range registryMutationSchemas() {
			schemas[name] = schema
		}
	}
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
			"schemas": schemas,
		},
		"paths": paths,
	}
	return func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, document)
	}
}

func volumeCollectionOperation() map[string]any {
	operation := readOperation("List ordinary writable volumes owned by one service")
	operation["post"] = writeMethod("Create an empty ordinary service volume (admin token)", http.StatusCreated, "VolumeCreateRequest")
	return operation
}

func projectCollectionOperation() map[string]any {
	operation := readOperation("List visible projects")
	operation["post"] = writeMethod("Create a project (unbound admin token)", http.StatusCreated, "ProjectCreateRequest")
	return operation
}

func objectStoreCollectionOperation() map[string]any {
	operation := readOperation("List private S3 resources in one visible project")
	operation["post"] = writeMethod("Create a private S3 resource with persistent connection credentials (admin token)", http.StatusCreated, "ObjectStoreCreateRequest")
	return operation
}

func domainCollectionOperation() map[string]any {
	operation := readOperation("List public domains attached to one service")
	operation["post"] = writeMethod("Attach or explicitly move a public domain (admin token)", http.StatusCreated, "DomainAttachRequest")
	return operation
}

func domainDeleteOperation() map[string]any {
	return map[string]any{"delete": map[string]any{
		"summary": "Detach a public domain from one service (admin token)",
		"responses": map[string]any{
			"204": map[string]string{"description": "Domain detached"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Admin role or project boundary denied"},
			"404": map[string]string{"description": "Service or domain not found"},
		},
	}}
}

func volumeDeleteOperation() map[string]any {
	return map[string]any{"delete": map[string]any{
		"summary": "Delete an unmounted ordinary service volume and its data (admin token)",
		"responses": map[string]any{
			"204": map[string]string{"description": "Volume metadata deleted; orphan-safe filesystem cleanup requested"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Admin role or project boundary denied"},
			"404": map[string]string{"description": "Volume not found"},
			"409": map[string]string{"description": "Volume is still referenced"},
		},
	}}
}

func databaseVersionPreviewOperation() map[string]any {
	return map[string]any{"post": map[string]any{
		"summary":    "Resolve a managed database target image and report capacity requirements without storing preview state (admin token)",
		"parameters": managedDatabaseIdentityParameters(false),
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]string{"$ref": "#/components/schemas/DatabaseVersionPreviewRequest"},
			}},
		},
		"responses": map[string]any{
			"200": map[string]string{"description": "Exact source/target digests, current data size, required free bytes, and current available bytes"},
			"400": map[string]string{"description": "Invalid target tag"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Admin role or project boundary denied"},
		},
	}}
}

func databaseVersionStartOperation() map[string]any {
	return map[string]any{"post": map[string]any{
		"summary":    "Start a new-volume managed database image change with expected downtime (admin token)",
		"parameters": managedDatabaseIdentityParameters(false),
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]string{"$ref": "#/components/schemas/DatabaseVersionStartRequest"},
			}},
		},
		"responses": map[string]any{
			"202": map[string]string{"description": "Observational operation and exact source/target digests"},
			"400": map[string]string{"description": "Invalid target tag"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Admin role or project boundary denied"},
			"409": map[string]string{"description": "Resource is busy, target digest is already active, or the tag moved after preview"},
			"507": map[string]string{"description": "Insufficient free space for a second database volume"},
		},
	}}
}

func databaseVersionReadOperation() map[string]any {
	return map[string]any{"get": map[string]any{
		"summary":    "Read one managed database version-change operation",
		"parameters": managedDatabaseIdentityParameters(true),
		"responses":  readResponses("Version-change operation"),
	}}
}

func managedDatabaseIdentityParameters(withOperation bool) []map[string]any {
	parameters := []map[string]any{
		{"name": "projectID", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
		{"name": "kind", "in": "path", "required": true, "schema": map[string]any{"type": "string", "enum": []string{"postgres", "redis"}}},
		{"name": "resourceID", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
	}
	if withOperation {
		parameters = append(parameters, map[string]any{
			"name": "operationID", "in": "path", "required": true, "schema": map[string]string{"type": "string"},
		})
	}
	return parameters
}

func managedResourceListOperation() map[string]any {
	return map[string]any{"get": map[string]any{
		"summary": "List managed PostgreSQL, Redis, and private S3 metadata",
		"parameters": []map[string]any{
			{"name": "projectID", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
		},
		"responses": readResponses("Managed resource metadata list"),
	}}
}

func managedResourceReadOperation(summary string) map[string]any {
	return map[string]any{"get": map[string]any{
		"summary":    summary,
		"parameters": managedResourceIdentityParameters(),
		"responses":  readResponses("Managed resource metadata"),
	}}
}

func managedResourceBackupReadOperation() map[string]any {
	parameters := append(managedResourceIdentityParameters(),
		map[string]any{"name": "beforeMillis", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0}},
		map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
	)
	return map[string]any{"get": map[string]any{
		"summary":    "Read a managed resource's backup policy and bounded backup history",
		"parameters": parameters,
		"responses":  readResponses("Managed resource backup status"),
	}}
}

func managedResourceIdentityParameters() []map[string]any {
	return []map[string]any{
		{"name": "projectID", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
		{"name": "kind", "in": "path", "required": true, "schema": map[string]any{"type": "string", "enum": []string{"postgres", "redis", "object_store"}}},
		{"name": "resourceID", "in": "path", "required": true, "schema": map[string]string{"type": "string"}},
	}
}

func readResponses(successDescription string) map[string]any {
	return map[string]any{
		"200": map[string]string{"description": successDescription},
		"400": map[string]string{"description": "Invalid query"},
		"401": map[string]string{"description": "Missing or invalid Bearer token"},
		"403": map[string]string{"description": "Outside token project boundary"},
		"404": map[string]string{"description": "Managed resource not found"},
	}
}

func serverExecOperation() map[string]any {
	return map[string]any{"post": map[string]any{
		"summary":     "Execute one bounded non-interactive host-root command (unbound admin token only)",
		"description": "An unbound admin token is a full root credential. Command and output are returned only in this response and are not stored in audit history.",
		"requestBody": map[string]any{
			"required": true,
			"content": map[string]any{"application/json": map[string]any{
				"schema": map[string]string{"$ref": "#/components/schemas/ServerExecRequest"},
			}},
		},
		"responses": map[string]any{
			"200": map[string]string{"description": "Bounded stdout, stderr, exit, timeout, truncation, and duration result"},
			"400": map[string]string{"description": "Invalid command or timeout"},
			"401": map[string]string{"description": "Missing or invalid Bearer token"},
			"403": map[string]string{"description": "Token is read-only or project-bound"},
		},
	}}
}

func managedPostgresOperation() map[string]any {
	operation := readOperation("List managed PostgreSQL resources in one visible project")
	operation["post"] = writeMethod("Create managed PostgreSQL with a persistent owner password (admin token)", http.StatusCreated, "ManagedPostgresCreateRequest")
	return operation
}

func managedRedisOperation() map[string]any {
	operation := readOperation("List managed Redis resources in one visible project")
	operation["post"] = writeMethod("Create managed Redis with a persistent password (admin token)", http.StatusCreated, "ManagedRedisCreateRequest")
	return operation
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
			"resourceReferences": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "required": []string{"environmentName", "resourceKind", "resourceId", "outputName"},
				"properties": map[string]any{
					"environmentName": map[string]string{"type": "string"},
					"resourceKind":    map[string]any{"type": "string", "enum": []string{"service", "postgres", "redis", "object_store"}},
					"resourceId":      map[string]string{"type": "string"},
					"outputName":      map[string]string{"type": "string"},
				},
			}},
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
		"ProjectCreateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required":   []string{"name"},
			"properties": map[string]any{"name": map[string]string{"type": "string"}},
		},
		"ObjectStoreCreateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name", "bucketName"},
			"properties": map[string]any{
				"name": map[string]string{"type": "string"}, "bucketName": map[string]string{"type": "string"},
				"publicHostname":       map[string]string{"type": "string"},
				"corsOrigins":          map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
				"credentialName":       map[string]string{"type": "string"},
				"credentialPermission": map[string]any{"type": "string", "enum": []string{"read_only", "read_write"}},
			},
		},
		"DomainAttachRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"hostname"},
			"properties": map[string]any{
				"hostname": map[string]string{"type": "string"},
				"move":     map[string]any{"type": "boolean", "default": false},
			},
		},
		"VolumeCreateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name", "ownerUid", "ownerGid"},
			"properties": map[string]any{
				"name":     map[string]string{"type": "string"},
				"ownerUid": map[string]any{"type": "integer", "minimum": 0, "maximum": 1<<32 - 2},
				"ownerGid": map[string]any{"type": "integer", "minimum": 0, "maximum": 1<<32 - 2},
			},
		},
		"DatabaseVersionPreviewRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required":   []string{"imageTag"},
			"properties": map[string]any{"imageTag": map[string]string{"type": "string"}},
		},
		"DatabaseVersionStartRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"imageTag", "expectedTargetDigest"},
			"properties": map[string]any{
				"imageTag":             map[string]string{"type": "string"},
				"expectedTargetDigest": map[string]string{"type": "string"},
			},
		},
		"ServerExecRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"command"},
			"properties": map[string]any{
				"command": map[string]any{
					"type": "string", "maxLength": 65_536,
					"description": "Valid UTF-8 without NUL; server enforces a 64 KiB byte limit",
				},
				"timeoutSeconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 300, "default": 30},
			},
		},
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
		"ManagedRedisCreateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name", "imageTag"},
			"properties": map[string]any{
				"name": map[string]string{"type": "string"}, "imageTag": map[string]string{"type": "string"},
				"cpuMillicores": map[string]any{"type": "integer", "minimum": 0},
				"memoryBytes":   map[string]any{"type": "integer", "minimum": 0},
			},
		},
		"ManagedPostgresCreateRequest": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"name", "imageTag"},
			"properties": map[string]any{
				"name": map[string]string{"type": "string"}, "imageTag": map[string]string{"type": "string"},
				"cpuMillicores": map[string]any{"type": "integer", "minimum": 0},
				"memoryBytes":   map[string]any{"type": "integer", "minimum": 0},
			},
		},
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
