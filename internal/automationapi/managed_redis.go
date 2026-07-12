package automationapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

type managedRedisRepository interface {
	ManagedRedisInProject(context.Context, string, string) (state.ManagedRedis, error)
	ManagedRedisByProject(context.Context, string) ([]state.ManagedRedis, error)
}

type managedRedisBrowser interface {
	Keys(context.Context, string, string, managedredis.ScanQuery) (managedredis.KeyPage, error)
	Preview(context.Context, string, string, managedredis.PreviewQuery) (managedredis.Preview, error)
}

func previewManagedRedisKey(browser managedRedisBrowser) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		encodedKey, present := request.URL.Query()["key"]
		if !present || len(encodedKey) != 1 {
			writeError(response, http.StatusBadRequest, "invalid_redis_browser_query", "one base64url key parameter is required")
			return
		}
		key, err := base64.RawURLEncoding.DecodeString(encodedKey[0])
		if err != nil {
			writeError(response, http.StatusBadRequest, "invalid_redis_browser_query", "key must be unpadded base64url")
			return
		}
		count := 0
		if value := request.URL.Query().Get("count"); value != "" {
			count, err = strconv.Atoi(value)
			if err != nil {
				writeError(response, http.StatusBadRequest, "invalid_redis_browser_query", "count must be an integer from 1 to 100")
				return
			}
		}
		preview, err := browser.Preview(request.Context(), projectID, request.PathValue("redisID"), managedredis.PreviewQuery{Key: key, Count: count})
		if err != nil {
			writeManagedRedisBrowserError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedRedisPreview(preview))
	}
}

type managedRedisResponse struct {
	ID                   string `json:"id"`
	ProjectID            string `json:"projectId"`
	Name                 string `json:"name"`
	Hostname             string `json:"hostname"`
	Port                 int    `json:"port"`
	ImageTag             string `json:"imageTag"`
	ImageDigest          string `json:"imageDigest"`
	CPUMillicores        int64  `json:"cpuMillicores,omitempty"`
	MemoryBytes          int64  `json:"memoryBytes,omitempty"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
	Password             string `json:"password,omitempty"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
}

func listManagedRedis(repository managedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		resources, err := repository.ManagedRedisByProject(request.Context(), projectID)
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		result := make([]managedRedisResponse, 0, len(resources))
		for _, resource := range resources {
			result = append(result, publicManagedRedis(resource, ""))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedRedis(repository managedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		resource, err := repository.ManagedRedisInProject(request.Context(), projectID, request.PathValue("redisID"))
		if err != nil {
			writeRepositoryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedRedis(resource, ""))
	}
}

func scanManagedRedisKeys(browser managedRedisBrowser) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		projectID := request.PathValue("projectID")
		if !requireProject(response, request, projectID) {
			return
		}
		query := managedredis.ScanQuery{Match: request.URL.Query().Get("match")}
		if value := request.URL.Query().Get("cursor"); value != "" {
			cursor, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				writeError(response, http.StatusBadRequest, "invalid_redis_browser_query", "cursor must be an unsigned integer")
				return
			}
			query.Cursor = cursor
		}
		if value := request.URL.Query().Get("count"); value != "" {
			count, err := strconv.Atoi(value)
			if err != nil {
				writeError(response, http.StatusBadRequest, "invalid_redis_browser_query", "count must be an integer from 1 to 100")
				return
			}
			query.Count = count
		}
		page, err := browser.Keys(request.Context(), projectID, request.PathValue("redisID"), query)
		if err != nil {
			writeManagedRedisBrowserError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedRedisKeyPage(page))
	}
}

func publicManagedRedisKeyPage(page managedredis.KeyPage) map[string]any {
	keys := make([]map[string]any, 0, len(page.Keys))
	for _, key := range page.Keys {
		item := map[string]any{
			"keyBase64": base64.RawURLEncoding.EncodeToString(key.Key), "type": key.Type,
			"sizeBytes": key.SizeBytes,
		}
		if utf8.Valid(key.Key) {
			text := string(key.Key)
			if strings.IndexFunc(text, func(character rune) bool { return unicode.IsControl(character) }) < 0 {
				item["keyText"] = text
			}
		}
		if key.ExpiresInMillis != nil {
			item["expiresInMillis"] = *key.ExpiresInMillis
		}
		keys = append(keys, item)
	}
	return map[string]any{"nextCursor": strconv.FormatUint(page.NextCursor, 10), "keys": keys}
}

func publicManagedRedisPreview(preview managedredis.Preview) map[string]any {
	items := make([]map[string]any, 0, len(preview.Items))
	for _, item := range preview.Items {
		values := make([]map[string]string, 0, len(item.Values))
		for _, value := range item.Values {
			encoded := map[string]string{"base64": base64.RawURLEncoding.EncodeToString(value)}
			if utf8.Valid(value) {
				text := string(value)
				if strings.IndexFunc(text, func(character rune) bool { return unicode.IsControl(character) }) < 0 {
					encoded["text"] = text
				}
			}
			values = append(values, encoded)
		}
		items = append(items, map[string]any{"values": values})
	}
	return map[string]any{
		"type": preview.Type, "length": preview.Length,
		"nextCursor": strconv.FormatUint(preview.NextCursor, 10),
		"truncated":  preview.Truncated, "items": items,
	}
}

func writeManagedRedisBrowserError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrManagedRedisNotFound):
		writeError(response, http.StatusNotFound, "redis_not_found", "Managed Redis resource not found")
	case errors.Is(err, managedredis.ErrInvalidBrowserQuery):
		writeError(response, http.StatusBadRequest, "invalid_redis_browser_query", err.Error())
	case errors.Is(err, managedredis.ErrNotRunning):
		writeError(response, http.StatusServiceUnavailable, "redis_not_running", "Managed Redis resource is not running")
	case errors.Is(err, managedredis.ErrKeyNotFound):
		writeError(response, http.StatusNotFound, "redis_key_not_found", "Redis key no longer exists")
	default:
		writeError(response, http.StatusBadGateway, "redis_browser_unavailable", "Unable to read managed Redis keys")
	}
}

func createManagedRedis(application *automation.ManagedRedisApplication) http.HandlerFunc {
	type requestBody struct {
		Name          string `json:"name"`
		ImageTag      string `json:"imageTag"`
		CPUMillicores int64  `json:"cpuMillicores"`
		MemoryBytes   int64  `json:"memoryBytes"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAdminProject(response, request, request.PathValue("projectID"))
		if !ok {
			return
		}
		var body requestBody
		if !decodeMutationJSON(response, request, &body) {
			return
		}
		result, err := application.Create(request.Context(), identity, automation.CreateManagedRedisInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, ImageTag: body.ImageTag,
			CPUMillicores: body.CPUMillicores, MemoryBytes: body.MemoryBytes,
		})
		if err != nil {
			writeManagedRedisMutationError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Resource.ProjectID+"/redis/"+result.Resource.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicManagedRedis(result.Resource, result.Password))
	}
}

func publicManagedRedis(resource state.ManagedRedis, password string) managedRedisResponse {
	return managedRedisResponse{
		ID: resource.ID, ProjectID: resource.ProjectID, Name: resource.Name,
		Hostname: resource.Name + "." + resource.ProjectName + ".internal", Port: managedredis.Port,
		ImageTag: resource.ImageTag, ImageDigest: resource.ImageDigest,
		CPUMillicores: resource.CPUMillicores, MemoryBytes: resource.MemoryMaxBytes,
		BackupEnabled: resource.BackupEnabled, BackupCron: resource.BackupCron,
		BackupRetentionCount: resource.BackupRetentionCount, Password: password,
		CreatedAt: resource.CreatedAtMillis, UpdatedAt: resource.UpdatedAtMillis,
	}
}

func writeManagedRedisMutationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, automation.ErrAdminRequired):
		writeError(response, http.StatusForbidden, "admin_token_required", "An admin token is required")
	case errors.Is(err, automation.ErrProjectBoundary):
		writeError(response, http.StatusForbidden, "project_forbidden", "Project is outside this token boundary")
	case errors.Is(err, managedredis.ErrInvalidInput):
		writeError(response, http.StatusBadRequest, "invalid_managed_redis", err.Error())
	case errors.Is(err, managedredis.ErrImageUnavailable):
		writeError(response, http.StatusBadGateway, "managed_redis_image_unavailable", "Unable to resolve the selected official Redis image")
	case errors.Is(err, state.ErrProjectNotFound):
		writeError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "Unable to create managed Redis resource")
	}
}
