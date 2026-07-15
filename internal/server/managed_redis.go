package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/managedimages"
	"github.com/iivankin/platformd/internal/managedredis"
	"github.com/iivankin/platformd/internal/state"
)

const maximumManagedRedisRequestBytes = 16 << 10

type ManagedRedisRepository interface {
	Create(context.Context, managedredis.CreateInput) (managedredis.CreateResult, error)
	Resource(context.Context, string, string) (state.ManagedRedis, error)
	Password(context.Context, string, string) (string, error)
	Resources(context.Context, string) ([]state.ManagedRedis, error)
	Persistence(context.Context, string, string) (managedredis.PersistenceReport, error)
	Stats(context.Context, string, string) (managedredis.Stats, error)
	Keys(context.Context, string, string, managedredis.ScanQuery) (managedredis.KeyPage, error)
	Preview(context.Context, string, string, managedredis.PreviewQuery) (managedredis.Preview, error)
	Mutate(context.Context, managedredis.DataMutationInput) (managedredis.DataMutationResult, error)
	Deployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error)
	Deployment(context.Context, string, string, string) (state.RuntimeDeployment, error)
	RestartDeployment(context.Context, string, string, string) error
	RemoveDeployment(context.Context, string, string, string) error
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

type managedRedisPersistenceResponse struct {
	ObservedAt                   int64 `json:"observedAt"`
	LastSuccessfulSaveAt         int64 `json:"lastSuccessfulSaveAt"`
	ActualRPOMillis              int64 `json:"actualRpoMillis"`
	TargetRPOMillis              int64 `json:"targetRpoMillis"`
	BackgroundSaveInProgress     bool  `json:"backgroundSaveInProgress"`
	LastBackgroundSaveSuccessful bool  `json:"lastBackgroundSaveSuccessful"`
	NeedsAttention               bool  `json:"needsAttention"`
}

func registerManagedRedisRoutes(mux *http.ServeMux, repository ManagedRedisRepository) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis", listManagedRedis(repository))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/redis", createManagedRedis(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}", getManagedRedis(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}/persistence", getManagedRedisPersistence(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}/stats", getManagedRedisStats(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}/keys", scanManagedRedisKeys(repository))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/redis/{redisID}/preview", previewManagedRedisKey(repository))
	mux.HandleFunc("POST /api/v1/projects/{projectID}/redis/{redisID}/data/mutations", mutateManagedRedisData(repository))
	registerManagedDeploymentRoutes(mux, "redis", repository, writeManagedRedisError)
}

func getManagedRedisStats(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		stats, err := repository.Stats(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, stats)
	}
}

func getManagedRedisPersistence(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		report, err := repository.Persistence(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, managedRedisPersistenceResponse{
			ObservedAt: report.ObservedAtMillis, LastSuccessfulSaveAt: report.LastSuccessfulSaveAtMillis,
			ActualRPOMillis: report.ActualRPOMillis, TargetRPOMillis: report.TargetRPOMillis,
			BackgroundSaveInProgress:     report.BackgroundSaveInProgress,
			LastBackgroundSaveSuccessful: report.LastBackgroundSaveSuccessful,
			NeedsAttention:               report.NeedsAttention,
		})
	}
}

func previewManagedRedisKey(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		encodedKey, present := request.URL.Query()["key"]
		if !present || len(encodedKey) != 1 {
			writeAPIError(response, http.StatusBadRequest, "invalid_redis_browser_query", "one base64url key parameter is required")
			return
		}
		key, err := base64.RawURLEncoding.DecodeString(encodedKey[0])
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_redis_browser_query", "key must be unpadded base64url")
			return
		}
		count := 0
		if value := request.URL.Query().Get("count"); value != "" {
			count, err = strconv.Atoi(value)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_redis_browser_query", "count must be an integer from 1 to 100")
				return
			}
		}
		preview, err := repository.Preview(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"), managedredis.PreviewQuery{Key: key, Count: count})
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicRedisPreview(preview))
	}
}

func scanManagedRedisKeys(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		query, ok := parseManagedRedisScanQuery(response, request)
		if !ok {
			return
		}
		page, err := repository.Keys(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"), query)
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicKeyPage(page))
	}
}

func parseManagedRedisScanQuery(response http.ResponseWriter, request *http.Request) (managedredis.ScanQuery, bool) {
	query := managedredis.ScanQuery{Match: request.URL.Query().Get("match")}
	if value := request.URL.Query().Get("cursor"); value != "" {
		cursor, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_redis_browser_query", "cursor must be an unsigned integer")
			return managedredis.ScanQuery{}, false
		}
		query.Cursor = cursor
	}
	if value := request.URL.Query().Get("count"); value != "" {
		count, err := strconv.Atoi(value)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_redis_browser_query", "count must be an integer from 1 to 100")
			return managedredis.ScanQuery{}, false
		}
		query.Count = count
	}
	return query, true
}

func publicKeyPage(page managedredis.KeyPage) map[string]any {
	keys := make([]map[string]any, 0, len(page.Keys))
	for _, key := range page.Keys {
		item := map[string]any{
			"keyBase64": base64.RawURLEncoding.EncodeToString(key.Key), "type": key.Type,
			"sizeBytes": key.SizeBytes,
		}
		if text, ok := printableRedisKey(key.Key); ok {
			item["keyText"] = text
		}
		if key.ExpiresInMillis != nil {
			item["expiresInMillis"] = *key.ExpiresInMillis
		}
		keys = append(keys, item)
	}
	return map[string]any{"nextCursor": strconv.FormatUint(page.NextCursor, 10), "keys": keys}
}

func printableRedisKey(value []byte) (string, bool) {
	if !utf8.Valid(value) {
		return "", false
	}
	text := string(value)
	if strings.IndexFunc(text, func(character rune) bool { return unicode.IsControl(character) }) >= 0 {
		return "", false
	}
	return text, true
}

func publicRedisPreview(preview managedredis.Preview) map[string]any {
	items := make([]map[string]any, 0, len(preview.Items))
	for _, item := range preview.Items {
		values := make([]map[string]string, 0, len(item.Values))
		for _, value := range item.Values {
			encoded := map[string]string{"base64": base64.RawURLEncoding.EncodeToString(value)}
			if text, ok := printableRedisKey(value); ok {
				encoded["text"] = text
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

func listManagedRedis(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		resources, err := repository.Resources(request.Context(), request.PathValue("projectID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		result := make([]managedRedisResponse, 0, len(resources))
		for _, resource := range resources {
			result = append(result, publicManagedRedis(resource, ""))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedRedis(repository ManagedRedisRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		resource, err := repository.Resource(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		password, err := repository.Password(request.Context(), request.PathValue("projectID"), request.PathValue("redisID"))
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicManagedRedis(resource, password))
	}
}

func createManagedRedis(repository ManagedRedisRepository) http.HandlerFunc {
	type requestBody struct {
		Name          string `json:"name"`
		ImageTag      string `json:"imageTag"`
		CPUMillicores int64  `json:"cpuMillicores"`
		MemoryBytes   int64  `json:"memoryBytes"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumManagedRedisRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain only managed Redis fields")
			return
		}
		result, err := repository.Create(request.Context(), managedredis.CreateInput{
			ProjectID: request.PathValue("projectID"), Name: body.Name, ImageTag: body.ImageTag,
			CPUMillicores: body.CPUMillicores, MemoryBytes: body.MemoryBytes,
			Actor: managedredis.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			writeManagedRedisError(response, err)
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+result.Resource.ProjectID+"/redis/"+result.Resource.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicManagedRedis(result.Resource, result.Password))
	}
}

func requireAccessIdentity(response http.ResponseWriter, request *http.Request) (access.Identity, bool) {
	identity, ok := access.IdentityFromContext(request.Context())
	if !ok {
		writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
	}
	return identity, ok
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

func writeManagedRedisError(response http.ResponseWriter, err error) {
	var commandError *managedredis.CommandError
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrManagedRedisNotFound):
		writeAPIError(response, http.StatusNotFound, "redis_not_found", "Managed Redis resource not found")
	case errors.Is(err, state.ErrResourceNameConflict):
		writeAPIError(response, http.StatusConflict, "resource_name_conflict", "A project resource with this name already exists")
	case errors.Is(err, managedredis.ErrImageUnavailable):
		writeAPIError(response, http.StatusBadGateway, "managed_redis_image_unavailable", "Unable to resolve the selected official Redis image")
	case errors.Is(err, managedredis.ErrInvalidInput), errors.Is(err, managedimages.ErrInvalidQuery):
		writeAPIError(response, http.StatusBadRequest, "invalid_managed_redis", err.Error())
	case errors.Is(err, managedredis.ErrInvalidBrowserQuery):
		writeAPIError(response, http.StatusBadRequest, "invalid_redis_browser_query", err.Error())
	case errors.Is(err, managedredis.ErrMaintenance):
		writeAPIError(response, http.StatusConflict, "resource_busy", "Managed Redis is in maintenance")
	case errors.Is(err, managedredis.ErrNotRunning):
		writeAPIError(response, http.StatusServiceUnavailable, "redis_not_running", "Managed Redis resource is not running")
	case errors.Is(err, managedredis.ErrKeyNotFound):
		writeAPIError(response, http.StatusNotFound, "redis_key_not_found", "Redis key no longer exists")
	case errors.As(err, &commandError):
		writeAPIError(response, http.StatusConflict, "redis_mutation_rejected", commandError.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage Redis resource")
	}
}
