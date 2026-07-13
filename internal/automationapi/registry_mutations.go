package automationapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/state"
)

const maximumRegistryMutationBytes = 32 << 10

func setRegistryHostname(settings registrySettings) http.HandlerFunc {
	type requestBody struct {
		Hostname *string `json:"hostname"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		if body.Hostname == nil {
			writeError(response, http.StatusBadRequest, "invalid_registry_input", "hostname is required")
			return
		}
		timestamp := time.Now()
		auditID, requestID, err := registryRequestIDs(timestamp)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate Registry identifiers")
			return
		}
		hostname, err := settings.SetRegistryHostname(request.Context(), state.SetRegistryHostnameInput{
			Hostname: *body.Hostname, AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
			RequestCorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if writeRegistryError(response, err) {
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

func createRegistryRepository(application registryApplication) http.HandlerFunc {
	type requestBody struct {
		Name                 string `json:"name"`
		PublicPull           bool   `json:"publicPull"`
		CredentialName       string `json:"credentialName"`
		CredentialPermission string `json:"credentialPermission"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		result, err := application.CreateRepository(request.Context(), registry.CreateRepositoryInput{
			Name: body.Name, PublicPull: body.PublicPull, CredentialName: body.CredentialName,
			CredentialPermission: body.CredentialPermission,
			Actor:                tokenRegistryActor(identity.TokenID),
		})
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/registry/repositories/"+result.Repository.ID)
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicRegistryRepository(result.Repository, result, nil))
	}
}

func setRegistryRepositoryPublicPull(application registryApplication) http.HandlerFunc {
	type requestBody struct {
		PublicPull *bool `json:"publicPull"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		if body.PublicPull == nil {
			writeError(response, http.StatusBadRequest, "invalid_registry_input", "publicPull is required")
			return
		}
		repository, requestID, err := application.SetPublicPull(request.Context(), registry.SetPublicPullInput{
			RepositoryID: request.PathValue("repositoryID"), PublicPull: *body.PublicPull,
			Actor: tokenRegistryActor(identity.TokenID),
		})
		if writeRegistryError(response, err) {
			return
		}
		summary, err := application.RepositorySummary(request.Context(), repository.ID)
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicRegistryRepository(repository, registry.CreateRepositoryResult{}, &summary))
	}
}

func deleteRegistryTag(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		digest, requestID, err := application.DeleteTag(request.Context(), registry.DeleteInput{
			RepositoryID: request.PathValue("repositoryID"), Reference: request.PathValue("tag"),
			Actor: tokenRegistryActor(identity.TokenID),
		})
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, map[string]string{"manifestDigest": digest})
	}
}

func deleteRegistryManifest(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		tags, requestID, err := application.DeleteManifest(request.Context(), registry.DeleteInput{
			RepositoryID: request.PathValue("repositoryID"), Reference: request.PathValue("digest"),
			Actor: tokenRegistryActor(identity.TokenID),
		})
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, map[string]any{"deletedTags": tags})
	}
}

func deleteRegistryRepository(application registryApplication) http.HandlerFunc {
	type requestBody struct {
		ExpectedName string `json:"expectedName"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeRegistryJSON(response, request, &body) {
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), 30*time.Second)
		defer cancel()
		requestID, err := application.DeleteRepository(ctx, registry.DeleteInput{
			RepositoryID: request.PathValue("repositoryID"), ExpectedName: body.ExpectedName,
			Actor: tokenRegistryActor(identity.TokenID),
		})
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func tokenRegistryActor(tokenID string) registry.Actor {
	return registry.Actor{Kind: "token", ID: tokenID}
}

func registryRequestIDs(timestamp time.Time) (string, string, error) {
	auditID, err := id.NewWith(timestamp, rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate Registry audit ID: %w", err)
	}
	requestID, err := id.NewWith(timestamp, rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate Registry request ID: %w", err)
	}
	return auditID, requestID, nil
}

func decodeRegistryJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumRegistryMutationBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid Registry fields")
		return false
	}
	return true
}
