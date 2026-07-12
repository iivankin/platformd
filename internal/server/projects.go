package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

const maximumProjectRequestBytes = 4096

type ProjectRepository interface {
	Projects(context.Context) ([]state.ProjectSummary, error)
	ProjectCanvas(context.Context, string) (state.ProjectCanvas, error)
	CreateProject(context.Context, state.CreateProject) (state.ProjectSummary, error)
}

type projectResponse struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ServiceCount     int    `json:"serviceCount"`
	PostgresCount    int    `json:"postgresCount"`
	RedisCount       int    `json:"redisCount"`
	ObjectStoreCount int    `json:"objectStoreCount"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt"`
}

type projectCanvasResponse struct {
	Project     projectResponse           `json:"project"`
	Resources   []projectCanvasResource   `json:"resources"`
	Connections []projectCanvasConnection `json:"connections"`
}

type projectCanvasResource struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Name             string `json:"name"`
	InternalHostname string `json:"internalHostname"`
	ImageReference   string `json:"imageReference,omitempty"`
	BucketName       string `json:"bucketName,omitempty"`
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
	StatusMessage    string `json:"statusMessage,omitempty"`
	ActiveDeployment string `json:"activeDeploymentId,omitempty"`
	ImageDigest      string `json:"imageDigest,omitempty"`
}

type projectCanvasConnection struct {
	SourceID         string   `json:"sourceId"`
	TargetID         string   `json:"targetId"`
	EnvironmentNames []string `json:"environmentNames"`
}

func registerProjectRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/projects", listProjects(config.projects))
	mux.HandleFunc("POST /api/v1/projects", createProject(config))
	mux.HandleFunc("GET /api/v1/projects/{projectID}/canvas", getProjectCanvas(config.projects))
}

func getProjectCanvas(repository ProjectRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		canvas, err := repository.ProjectCanvas(request.Context(), request.PathValue("projectID"))
		if errors.Is(err, state.ErrProjectNotFound) {
			writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to load project canvas")
			return
		}
		resources := make([]projectCanvasResource, 0, len(canvas.Resources))
		for _, resource := range canvas.Resources {
			resources = append(resources, projectCanvasResource{
				ID: resource.ID, Kind: resource.Kind, Name: resource.Name,
				InternalHostname: resource.InternalHostname,
				ImageReference:   resource.ImageReference, BucketName: resource.BucketName,
				Enabled: resource.Enabled, Status: resource.Status,
				StatusMessage: resource.StatusMessage, ActiveDeployment: resource.ActiveDeployment,
				ImageDigest: resource.ImageDigest,
			})
		}
		connections := make([]projectCanvasConnection, 0, len(canvas.Connections))
		for _, connection := range canvas.Connections {
			connections = append(connections, projectCanvasConnection{
				SourceID: connection.SourceID, TargetID: connection.TargetID,
				EnvironmentNames: connection.EnvironmentNames,
			})
		}
		writeJSON(response, http.StatusOK, projectCanvasResponse{
			Project: publicProject(canvas.Project), Resources: resources, Connections: connections,
		})
	}
}

func listProjects(repository ProjectRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := access.IdentityFromContext(request.Context()); !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		projects, err := repository.Projects(request.Context())
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to list projects")
			return
		}
		result := make([]projectResponse, 0, len(projects))
		for _, project := range projects {
			result = append(result, publicProject(project))
		}
		writeJSON(response, http.StatusOK, result)
	}
}

func createProject(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Name string `json:"name"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumProjectRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain only a project name")
			return
		}
		if err := requireJSONEnd(decoder); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body must contain one JSON object")
			return
		}
		if err := resourcename.Validate(body.Name); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_name", err.Error())
			return
		}
		timestamp := config.now()
		projectID, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate project identifiers")
			return
		}
		created, err := config.projects.CreateProject(request.Context(), state.CreateProject{
			ID: projectID, Name: body.Name, AuditEventID: auditID,
			ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if errors.Is(err, state.ErrProjectNameConflict) {
			writeAPIError(response, http.StatusConflict, "project_name_conflict", "A project with this name already exists")
			return
		}
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to create project")
			return
		}
		response.Header().Set("Location", "/api/v1/projects/"+created.ID)
		response.Header().Set("X-Request-ID", correlationID)
		writeJSON(response, http.StatusCreated, publicProject(created))
	}
}

func createRequestIDs(timestamp time.Time, random io.Reader) (string, string, string, error) {
	values := make([]string, 3)
	for index := range values {
		value, err := id.NewWith(timestamp, random)
		if err != nil {
			return "", "", "", fmt.Errorf("generate request ID: %w", err)
		}
		values[index] = value
	}
	return values[0], values[1], values[2], nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected JSON content")
	}
	return nil
}

func publicProject(project state.ProjectSummary) projectResponse {
	return projectResponse{
		ID: project.ID, Name: project.Name,
		ServiceCount: project.ServiceCount, PostgresCount: project.PostgresCount,
		RedisCount: project.RedisCount, ObjectStoreCount: project.ObjectStoreCount,
		CreatedAt: project.CreatedAtMillis, UpdatedAt: project.UpdatedAtMillis,
	}
}

func writeAPIError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
