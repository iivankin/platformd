package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/iivankin/platformd/internal/state"
)

type managedDeploymentRepository interface {
	Deployments(context.Context, string, string, string, int) (state.RuntimeDeploymentPage, error)
	Deployment(context.Context, string, string, string) (state.RuntimeDeployment, error)
	RestartDeployment(context.Context, string, string, string) error
	RemoveDeployment(context.Context, string, string, string) error
}

type runtimeDeploymentResponse struct {
	ID           string `json:"id"`
	ResourceKind string `json:"resourceKind"`
	ResourceID   string `json:"resourceId"`
	ImageTag     string `json:"imageTag"`
	ImageDigest  string `json:"imageDigest"`
	Status       string `json:"status"`
	Active       bool   `json:"active"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	FinishedAt   int64  `json:"finishedAt,omitempty"`
}

type runtimeDeploymentPageResponse struct {
	Deployments []runtimeDeploymentResponse `json:"deployments"`
	NextCursor  string                      `json:"nextCursor,omitempty"`
}

func registerManagedDeploymentRoutes(mux *http.ServeMux, collection string, repository managedDeploymentRepository, writeResourceError func(http.ResponseWriter, error)) {
	base := "/api/v1/projects/{projectID}/" + collection + "/{resourceID}/deployments"
	mux.HandleFunc("GET "+base, listManagedDeployments(repository, writeResourceError))
	mux.HandleFunc("GET "+base+"/{deploymentID}", getManagedDeployment(repository, writeResourceError))
	mux.HandleFunc("POST "+base+"/{deploymentID}/restart", restartManagedDeployment(repository, writeResourceError))
	mux.HandleFunc("POST "+base+"/{deploymentID}/remove", removeManagedDeployment(repository, writeResourceError))
}

func listManagedDeployments(repository managedDeploymentRepository, writeResourceError func(http.ResponseWriter, error)) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		limit := 0
		if value := request.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_page_size", "limit must be an integer")
				return
			}
			limit = parsed
		}
		page, err := repository.Deployments(request.Context(), request.PathValue("projectID"), request.PathValue("resourceID"), request.URL.Query().Get("cursor"), limit)
		if errors.Is(err, state.ErrDeploymentPageInvalid) || errors.Is(err, state.ErrDeploymentCursorInvalid) || errors.Is(err, state.ErrRuntimeDeploymentInvalid) {
			writeAPIError(response, http.StatusBadRequest, "invalid_deployment_page", err.Error())
			return
		}
		if err != nil {
			writeResourceError(response, err)
			return
		}
		result := runtimeDeploymentPageResponse{
			Deployments: make([]runtimeDeploymentResponse, 0, len(page.Deployments)),
			NextCursor:  page.NextCursor,
		}
		for _, deployment := range page.Deployments {
			result.Deployments = append(result.Deployments, publicRuntimeDeployment(deployment))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func getManagedDeployment(repository managedDeploymentRepository, writeResourceError func(http.ResponseWriter, error)) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		deployment, err := repository.Deployment(request.Context(), request.PathValue("projectID"), request.PathValue("resourceID"), request.PathValue("deploymentID"))
		if errors.Is(err, state.ErrRuntimeDeploymentNotFound) {
			writeAPIError(response, http.StatusNotFound, "deployment_not_found", "Deployment not found for this resource")
			return
		}
		if err != nil {
			writeResourceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicRuntimeDeployment(deployment))
	}
}

func restartManagedDeployment(repository managedDeploymentRepository, writeResourceError func(http.ResponseWriter, error)) http.HandlerFunc {
	return managedDeploymentAction(repository.RestartDeployment, writeResourceError)
}

func removeManagedDeployment(repository managedDeploymentRepository, writeResourceError func(http.ResponseWriter, error)) http.HandlerFunc {
	return managedDeploymentAction(repository.RemoveDeployment, writeResourceError)
}

func managedDeploymentAction(action func(context.Context, string, string, string) error, writeResourceError func(http.ResponseWriter, error)) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		err := action(request.Context(), request.PathValue("projectID"), request.PathValue("resourceID"), request.PathValue("deploymentID"))
		if errors.Is(err, state.ErrRuntimeDeploymentNotFound) {
			writeAPIError(response, http.StatusNotFound, "deployment_not_found", "Deployment not found for this resource")
			return
		}
		if err != nil {
			writeResourceError(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func publicRuntimeDeployment(deployment state.RuntimeDeployment) runtimeDeploymentResponse {
	return runtimeDeploymentResponse{
		ID: deployment.ID, ResourceKind: deployment.ResourceKind, ResourceID: deployment.ResourceID,
		ImageTag: deployment.ImageTag, ImageDigest: deployment.ImageDigest,
		Status: deployment.Status, Active: deployment.Active,
		ErrorCode: deployment.ErrorCode, ErrorMessage: deployment.ErrorMessage,
		CreatedAt: deployment.CreatedAtMillis, FinishedAt: deployment.FinishedAtMillis,
	}
}
