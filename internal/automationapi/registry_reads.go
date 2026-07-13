package automationapi

import (
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/registry"
)

func getRegistrySettings(settings registrySettings) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireUnboundIdentity(response, request); !ok {
			return
		}
		hostname, err := settings.RegistryHostname(request.Context())
		if err != nil {
			writeError(response, http.StatusInternalServerError, "internal_error", "Unable to load Registry settings")
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"hostname": hostname})
	}
}

func listRegistryRepositories(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireUnboundIdentity(response, request); !ok {
			return
		}
		repositories, err := application.RepositorySummaries(request.Context())
		if err != nil {
			writeRegistryError(response, err)
			return
		}
		result := make([]registryRepositoryResponse, 0, len(repositories))
		for _, repository := range repositories {
			result = append(result, publicRegistryRepository(repository.Repository, registry.CreateRepositoryResult{}, &repository))
		}
		writeJSON(response, http.StatusOK, map[string]any{"repositories": result})
	}
}

func getRegistryRepository(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireUnboundIdentity(response, request); !ok {
			return
		}
		summary, err := application.RepositorySummary(request.Context(), request.PathValue("repositoryID"))
		if writeRegistryError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicRegistryRepository(summary.Repository, registry.CreateRepositoryResult{}, &summary))
	}
}

func listRegistryImages(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireUnboundIdentity(response, request); !ok {
			return
		}
		limit := 100
		var err error
		if value := request.URL.Query().Get("limit"); value != "" {
			limit, err = strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 1000 {
				writeError(response, http.StatusBadRequest, "invalid_registry_query", "limit must be 1..1000")
				return
			}
		}
		images, more, err := application.Images(
			request.Context(), request.PathValue("repositoryID"), request.URL.Query().Get("after"), limit,
		)
		if writeRegistryError(response, err) {
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

func getRegistryImage(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireUnboundIdentity(response, request); !ok {
			return
		}
		image, err := application.Image(request.Context(), request.PathValue("repositoryID"), request.PathValue("digest"))
		if writeRegistryError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicRegistryImage(image, true))
	}
}

func listRegistryCredentials(application registryApplication) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireUnboundIdentity(response, request); !ok {
			return
		}
		credentials, err := application.Credentials(request.Context(), request.PathValue("repositoryID"))
		if writeRegistryError(response, err) {
			return
		}
		result := make([]registryCredentialResponse, 0, len(credentials))
		for _, credential := range credentials {
			result = append(result, publicRegistryCredential(credential, registry.CreateCredentialResult{}))
		}
		writeJSON(response, http.StatusOK, map[string]any{"credentials": result})
	}
}
