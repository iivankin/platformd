package server

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerfiles"
)

const maximumContainerFileUploadBytes = 100 << 20

func registerContainerFileRoutes(mux *http.ServeMux, application *containerfiles.Application) {
	mux.HandleFunc("GET /api/v1/projects/{projectID}/resources/{resourceKind}/{resourceID}/files", func(response http.ResponseWriter, request *http.Request) {
		if !containerFileIdentity(response, request) {
			return
		}
		rootPath := request.URL.Query().Get("path")
		if rootPath == "" {
			rootPath = "/"
		}
		entries, err := application.List(
			request.Context(), request.PathValue("projectID"), request.PathValue("resourceKind"), request.PathValue("resourceID"), rootPath,
		)
		if err != nil {
			writeContainerFileError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, struct {
			Root    string                               `json:"root"`
			Entries []containerengine.ContainerFileEntry `json:"entries"`
		}{Root: rootPath, Entries: entries})
	})

	mux.HandleFunc("GET /api/v1/projects/{projectID}/resources/{resourceKind}/{resourceID}/files/content", func(response http.ResponseWriter, request *http.Request) {
		if !containerFileIdentity(response, request) {
			return
		}
		file, entry, err := application.Open(
			request.Context(), request.PathValue("projectID"), request.PathValue("resourceKind"), request.PathValue("resourceID"), request.URL.Query().Get("path"),
		)
		if err != nil {
			writeContainerFileError(response, err)
			return
		}
		defer file.Close() //nolint:errcheck // the response cannot report cleanup errors after streaming begins
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(entry.Path)}))
		response.Header().Set("Content-Length", fmt.Sprint(entry.SizeBytes))
		response.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(response, file)
	})

	mux.HandleFunc("PUT /api/v1/projects/{projectID}/resources/{resourceKind}/{resourceID}/files/content", func(response http.ResponseWriter, request *http.Request) {
		if !containerFileIdentity(response, request) {
			return
		}
		if request.ContentLength < 0 || request.ContentLength > maximumContainerFileUploadBytes {
			writeAPIError(response, http.StatusRequestEntityTooLarge, "container_file_too_large", "File uploads must include a size up to 100 MiB")
			return
		}
		body := http.MaxBytesReader(response, request.Body, maximumContainerFileUploadBytes)
		defer body.Close() //nolint:errcheck // request body cleanup does not change the mutation result
		err := application.Write(
			request.Context(), request.PathValue("projectID"), request.PathValue("resourceKind"), request.PathValue("resourceID"),
			request.URL.Query().Get("path"), body, request.ContentLength,
		)
		if err != nil {
			writeContainerFileError(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
}

func containerFileIdentity(response http.ResponseWriter, request *http.Request) bool {
	if _, ok := access.IdentityFromContext(request.Context()); ok {
		return true
	}
	writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
	return false
}

func writeContainerFileError(response http.ResponseWriter, err error) {
	if errors.Is(err, containerfiles.ErrResourceNotRunning) {
		writeAPIError(response, http.StatusConflict, "resource_not_running", "The resource has no running container")
		return
	}
	writeAPIError(response, http.StatusConflict, "container_file_failed", "Unable to access container files")
}
