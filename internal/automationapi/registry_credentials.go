package automationapi

import (
	"net/http"

	"github.com/iivankin/platformd/internal/registry"
)

func createRegistryCredential(application registryApplication) http.HandlerFunc {
	type requestBody struct {
		Name       string `json:"name"`
		Permission string `json:"permission"`
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
		result, err := application.CreateCredential(request.Context(), registry.CreateCredentialInput{
			RepositoryID: request.PathValue("repositoryID"), Name: body.Name, Permission: body.Permission,
			Actor: tokenRegistryActor(identity.TokenID),
		})
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", result.RequestID)
		writeJSON(response, http.StatusCreated, publicRegistryCredential(result.Credential, result))
	}
}

func deleteRegistryCredential(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireUnboundAdmin(response, request)
		if !ok {
			return
		}
		requestID, err := application.DeleteCredential(
			request.Context(), request.PathValue("repositoryID"), request.PathValue("credentialID"),
			tokenRegistryActor(identity.TokenID),
		)
		if writeRegistryError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		response.WriteHeader(http.StatusNoContent)
	}
}

func cleanupRegistryRepository(application registryApplication) http.HandlerFunc {
	type requestBody struct {
		DryRun *bool `json:"dryRun"`
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
		if body.DryRun == nil {
			writeError(response, http.StatusBadRequest, "invalid_registry_input", "dryRun is required")
			return
		}
		result, err := application.Cleanup(
			request.Context(), request.PathValue("repositoryID"), *body.DryRun, tokenRegistryActor(identity.TokenID),
		)
		if writeRegistryError(response, err) {
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
