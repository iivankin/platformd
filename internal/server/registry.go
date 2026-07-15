package server

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/state"
)

const maximumRegistryMutationBytes = 32 << 10

type RegistrySettings interface {
	RegistryHostname(context.Context) (string, error)
	SetRegistryHostname(context.Context, state.SetRegistryHostnameInput) (*string, error)
}

type registryRepositoryResponse struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	PublicPull           bool   `json:"publicPull"`
	BackupEnabled        bool   `json:"backupEnabled"`
	BackupCron           string `json:"backupCron,omitempty"`
	BackupRetentionCount int    `json:"backupRetentionCount"`
	CreatedAt            int64  `json:"createdAt"`
	UpdatedAt            int64  `json:"updatedAt"`
	ManifestCount        int    `json:"manifestCount"`
	TagCount             int    `json:"tagCount"`
	BlobCount            int    `json:"blobCount"`
	TotalBlobBytes       int64  `json:"totalBlobBytes"`
	ReferencedBlobBytes  int64  `json:"referencedBlobBytes"`
	LastPushedAt         int64  `json:"lastPushedAt,omitempty"`
	CredentialName       string `json:"credentialName,omitempty"`
	CredentialPermission string `json:"credentialPermission,omitempty"`
	Username             string `json:"username,omitempty"`
	Secret               string `json:"secret,omitempty"`
}

type registryImageResponse struct {
	Digest              string                   `json:"digest"`
	Tags                []string                 `json:"tags"`
	MediaType           string                   `json:"mediaType"`
	Platforms           []registry.ImagePlatform `json:"platforms"`
	PushedAt            int64                    `json:"pushedAt"`
	ManifestSize        int64                    `json:"manifestSize"`
	ReferencedBlobBytes int64                    `json:"referencedBlobBytes"`
	BlobDigests         []string                 `json:"blobDigests"`
	Manifest            json.RawMessage          `json:"manifest,omitempty"`
}

type registryCredentialResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Permission      string `json:"permission"`
	CreatedAt       int64  `json:"createdAt"`
	LastUsedAt      int64  `json:"lastUsedAt,omitempty"`
	Username        string `json:"username"`
	Secret          string `json:"secret,omitempty"`
	SecretAvailable bool   `json:"secretAvailable"`
}

func registerRegistryRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/registry", getRegistrySettings(config.registrySettings))
	mux.HandleFunc("PUT /api/v1/registry/hostname", setRegistryHostname(config))
	mux.HandleFunc("GET /api/v1/registry/repositories", listRegistryRepositories(config.registry))
	mux.HandleFunc("POST /api/v1/registry/repositories", createRegistryRepository(config))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}", getRegistryRepository(config.registry))
	mux.HandleFunc("PUT /api/v1/registry/repositories/{repositoryID}/public-pull", setRegistryRepositoryPublicPull(config))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}/images", listRegistryImages(config.registry))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}/images/{digest}", getRegistryImage(config.registry))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}/tags/{tag}", deleteRegistryTag(config))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}/manifests/{digest}", deleteRegistryManifest(config))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}", deleteRegistryRepository(config))
	mux.HandleFunc("GET /api/v1/registry/repositories/{repositoryID}/credentials", listRegistryCredentials(config.registry))
	mux.HandleFunc("POST /api/v1/registry/repositories/{repositoryID}/credentials", createRegistryCredential(config))
	mux.HandleFunc("DELETE /api/v1/registry/repositories/{repositoryID}/credentials/{credentialID}", deleteRegistryCredential(config))
	mux.HandleFunc("POST /api/v1/registry/repositories/{repositoryID}/cleanup", cleanupRegistryRepository(config))
}

func setRegistryRepositoryPublicPull(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		PublicPull bool `json:"publicPull"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		repository, requestID, err := config.registry.SetPublicPull(request.Context(), registry.SetPublicPullInput{
			RepositoryID: request.PathValue("repositoryID"), PublicPull: body.PublicPull,
			Actor: registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		summary, err := config.registry.RepositorySummary(request.Context(), repository.ID)
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicRegistryRepository(repository, registry.CreateRepositoryResult{}, &summary))
	}
}

func cleanupRegistryRepository(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		DryRun bool `json:"dryRun"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		result, err := config.registry.Cleanup(
			request.Context(), request.PathValue("repositoryID"), body.DryRun,
			registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		)
		if writeRegistryAdminError(response, err) {
			return
		}
		if result.RequestID != "" {
			response.Header().Set("X-Request-ID", result.RequestID)
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"blobCount": result.BlobCount, "bytes": result.Bytes, "deleted": result.Deleted,
			"previewDigests": result.PreviewDigests, "previewTruncated": result.PreviewTruncated,
		})
	}
}

func listRegistryCredentials(application *registry.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		credentials, err := application.CredentialDetails(request.Context(), request.PathValue("repositoryID"))
		if writeRegistryAdminError(response, err) {
			return
		}
		result := make([]registryCredentialResponse, 0, len(credentials))
		for _, credential := range credentials {
			result = append(result, publicRegistryCredential(credential))
		}
		writeJSON(response, http.StatusOK, map[string]any{"credentials": result})
	}
}

func createRegistryCredential(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Name       string `json:"name"`
		Permission string `json:"permission"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		result, err := config.registry.CreateCredential(request.Context(), registry.CreateCredentialInput{
			RepositoryID: request.PathValue("repositoryID"), Name: body.Name, Permission: body.Permission,
			Actor: registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicRegistryCredential(registry.CredentialDetails{
			Credential: result.Credential, Username: result.Username, Secret: result.Secret, SecretAvailable: true,
		}))
	}
}

func deleteRegistryCredential(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		requestID, err := config.registry.DeleteCredential(
			request.Context(), request.PathValue("repositoryID"), request.PathValue("credentialID"),
			registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		)
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func getRegistrySettings(settings RegistrySettings) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		hostname, err := settings.RegistryHostname(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load Registry settings")
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"hostname": hostname})
	}
}

func setRegistryHostname(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Hostname string `json:"hostname"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate Registry identifiers")
			return
		}
		hostname, err := config.registrySettings.SetRegistryHostname(request.Context(), state.SetRegistryHostnameInput{
			Hostname: body.Hostname, AuditEventID: auditID, ActorKind: "access",
			ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		value := ""
		if hostname != nil {
			value = *hostname
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, map[string]string{"hostname": value})
	}
}

func listRegistryRepositories(application *registry.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		repositories, err := application.RepositorySummaries(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list Registry repositories")
			return
		}
		result := make([]registryRepositoryResponse, 0, len(repositories))
		for _, repository := range repositories {
			result = append(result, publicRegistryRepository(repository.Repository, registry.CreateRepositoryResult{}, &repository))
		}
		writeJSON(response, http.StatusOK, map[string]any{"repositories": result})
	}
}

func getRegistryRepository(application *registry.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		summary, err := application.RepositorySummary(request.Context(), request.PathValue("repositoryID"))
		if writeRegistryAdminError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicRegistryRepository(summary.Repository, registry.CreateRepositoryResult{}, &summary))
	}
}

func createRegistryRepository(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Name                 string `json:"name"`
		PublicPull           bool   `json:"publicPull"`
		CredentialName       string `json:"credentialName"`
		CredentialPermission string `json:"credentialPermission"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		result, err := config.registry.CreateRepository(request.Context(), registry.CreateRepositoryInput{
			Name: body.Name, PublicPull: body.PublicPull, CredentialName: body.CredentialName,
			CredentialPermission: body.CredentialPermission,
			Actor:                registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/registry/repositories/"+result.Repository.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicRegistryRepository(result.Repository, result, nil))
	}
}

func listRegistryImages(application *registry.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		limit := 100
		var err error
		if value := request.URL.Query().Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 1000 {
				writeAPIError(response, http.StatusBadRequest, "invalid_registry_query", "limit must be 1..1000")
				return
			}
		}
		images, more, err := application.Images(request.Context(), request.PathValue("repositoryID"), request.URL.Query().Get("after"), limit)
		if writeRegistryAdminError(response, err) {
			return
		}
		result := make([]registryImageResponse, 0, len(images))
		for _, image := range images {
			result = append(result, publicRegistryImage(image, false))
		}
		next := ""
		if more && len(images) != 0 {
			next = images[len(images)-1].Digest
		}
		writeJSON(response, http.StatusOK, map[string]any{"images": result, "nextCursor": next})
	}
}

func getRegistryImage(application *registry.Application) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		image, err := application.Image(request.Context(), request.PathValue("repositoryID"), request.PathValue("digest"))
		if writeRegistryAdminError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicRegistryImage(image, true))
	}
}

func deleteRegistryTag(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		digest, requestID, err := config.registry.DeleteTag(request.Context(), registry.DeleteInput{
			RepositoryID: request.PathValue("repositoryID"), Reference: request.PathValue("tag"),
			Actor: registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, map[string]string{"manifestDigest": digest})
	}
}

func deleteRegistryManifest(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		tags, requestID, err := config.registry.DeleteManifest(request.Context(), registry.DeleteInput{
			RepositoryID: request.PathValue("repositoryID"), Reference: request.PathValue("digest"),
			Actor: registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, map[string]any{"deletedTags": tags})
	}
}

func deleteRegistryRepository(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		ExpectedName string `json:"expectedName"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		requestID, err := config.registry.DeleteRepository(ctx, registry.DeleteInput{
			RepositoryID: request.PathValue("repositoryID"), ExpectedName: body.ExpectedName,
			Actor: registry.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if writeRegistryAdminError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeRegistryJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumRegistryMutationBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid Registry fields")
		return false
	}
	return true
}

func publicRegistryRepository(repository state.RegistryRepository, created registry.CreateRepositoryResult, summary *registry.RepositorySummary) registryRepositoryResponse {
	result := registryRepositoryResponse{
		ID: repository.ID, Name: repository.Name, PublicPull: repository.PublicPull,
		BackupEnabled: repository.BackupEnabled, BackupCron: repository.BackupCron,
		BackupRetentionCount: repository.BackupRetentionCount,
		CreatedAt:            repository.CreatedAtMillis, UpdatedAt: repository.UpdatedAtMillis,
		CredentialName: created.Credential.Name, CredentialPermission: created.Credential.Permission,
		Username: created.Username, Secret: created.Secret,
	}
	if summary != nil {
		result.ManifestCount = summary.ManifestCount
		result.TagCount = summary.TagCount
		result.BlobCount = summary.BlobCount
		result.TotalBlobBytes = summary.TotalBlobBytes
		result.ReferencedBlobBytes = summary.ReferencedBlobBytes
		result.LastPushedAt = summary.LastPushedAtMillis
	}
	return result
}

func publicRegistryImage(image registry.Image, includeManifest bool) registryImageResponse {
	result := registryImageResponse{
		Digest: image.Digest, Tags: image.Tags, MediaType: image.MediaType, Platforms: image.Platforms,
		PushedAt: image.PushedAtMillis, ManifestSize: image.ManifestSize,
		ReferencedBlobBytes: image.ReferencedBlobBytes, BlobDigests: image.BlobDigests,
	}
	if includeManifest {
		result.Manifest = json.RawMessage(image.ManifestJSON)
	}
	return result
}

func publicRegistryCredential(details registry.CredentialDetails) registryCredentialResponse {
	credential := details.Credential
	return registryCredentialResponse{
		ID: credential.ID, Name: credential.Name, Permission: credential.Permission,
		CreatedAt: credential.CreatedAtMillis, LastUsedAt: credential.LastUsedAtMillis,
		Username: details.Username, Secret: details.Secret, SecretAvailable: details.SecretAvailable,
	}
}

func writeRegistryAdminError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, state.ErrRegistryRepositoryNotFound):
		writeAPIError(response, http.StatusNotFound, "registry_repository_not_found", "Registry repository not found")
	case errors.Is(err, state.ErrRegistryNameConflict):
		writeAPIError(response, http.StatusConflict, "registry_name_conflict", "Registry repository name already exists")
	case errors.Is(err, state.ErrRegistryCredentialNameConflict):
		writeAPIError(response, http.StatusConflict, "registry_credential_name_conflict", "Registry credential name already exists")
	case errors.Is(err, state.ErrRegistryCredentialNotFound):
		writeAPIError(response, http.StatusNotFound, "registry_credential_not_found", "Registry credential not found")
	case errors.Is(err, state.ErrHostnameInUse):
		writeAPIError(response, http.StatusConflict, "hostname_in_use", "Hostname is already assigned to another public role")
	case errors.Is(err, state.ErrCertificateCoverage):
		writeAPIError(response, http.StatusUnprocessableEntity, "certificate_coverage", "No configured Origin certificate covers this hostname")
	case errors.Is(err, registry.ErrInvalidInput):
		writeAPIError(response, http.StatusBadRequest, "invalid_registry_input", err.Error())
	case errors.Is(err, registry.ErrRepositoryBusy):
		writeAPIError(response, http.StatusConflict, "repository_busy", "Registry repository is busy")
	case errors.Is(err, state.ErrRegistryManifestNotFound):
		writeAPIError(response, http.StatusNotFound, "registry_manifest_not_found", "Registry manifest or tag not found")
	default:
		var referenced *registry.ManifestReferencedError
		if errors.As(err, &referenced) {
			writeJSON(response, http.StatusConflict, map[string]any{"error": map[string]any{
				"code": "manifest_referenced", "message": err.Error(), "parentDigests": referenced.Parents,
			}})
		} else {
			writeAPIError(response, http.StatusBadRequest, "registry_mutation_failed", err.Error())
		}
	}
	return true
}
