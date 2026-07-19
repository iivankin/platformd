package automationapi

import (
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

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

type serviceSummaryResponse struct {
	ID                 string               `json:"id"`
	Name               string               `json:"name"`
	Enabled            bool                 `json:"enabled"`
	Status             string               `json:"status"`
	StatusMessage      string               `json:"statusMessage,omitempty"`
	InternalHostname   string               `json:"internalHostname"`
	Source             servicesource.Source `json:"source"`
	ImageDigest        string               `json:"imageDigest,omitempty"`
	ActiveDeploymentID string               `json:"activeDeploymentId,omitempty"`
}

type serviceResponse struct {
	ID                 string                 `json:"id"`
	ProjectID          string                 `json:"projectId"`
	Name               string                 `json:"name"`
	Enabled            bool                   `json:"enabled"`
	Configuration      serviceconfig.Snapshot `json:"configuration"`
	ActiveDeploymentID string                 `json:"activeDeploymentId,omitempty"`
	ActiveImageDigest  string                 `json:"activeImageDigest,omitempty"`
	CreatedAt          int64                  `json:"createdAt"`
	UpdatedAt          int64                  `json:"updatedAt"`
}

type deploymentResponse struct {
	ID            string                 `json:"id"`
	ImageDigest   string                 `json:"imageDigest"`
	ConfigHash    string                 `json:"serviceConfigHash"`
	Configuration serviceconfig.Snapshot `json:"configuration"`
	Status        string                 `json:"status"`
	ErrorCode     string                 `json:"errorCode,omitempty"`
	ErrorMessage  string                 `json:"errorMessage,omitempty"`
	CreatedAt     int64                  `json:"createdAt"`
	FinishedAt    int64                  `json:"finishedAt,omitempty"`
}

func publicProject(project state.ProjectSummary) projectResponse {
	return projectResponse{
		ID: project.ID, Name: project.Name, ServiceCount: project.ServiceCount,
		PostgresCount: project.PostgresCount, RedisCount: project.RedisCount,
		ObjectStoreCount: project.ObjectStoreCount,
		CreatedAt:        project.CreatedAtMillis, UpdatedAt: project.UpdatedAtMillis,
	}
}

func publicServiceSummary(resource state.CanvasResource) serviceSummaryResponse {
	return serviceSummaryResponse{
		ID: resource.ID, Name: resource.Name, Enabled: resource.Enabled,
		Status: resource.Status, StatusMessage: resource.StatusMessage,
		InternalHostname: resource.InternalHostname, Source: resource.Source,
		ImageDigest: resource.ImageDigest, ActiveDeploymentID: resource.ActiveDeployment,
	}
}

func publicService(service state.ServiceDesired) serviceResponse {
	return serviceResponse{
		ID: service.ID, ProjectID: service.ProjectID, Name: service.Name,
		Enabled: service.Enabled, Configuration: service.Snapshot,
		ActiveDeploymentID: service.ActiveDeploymentID, ActiveImageDigest: service.ActiveImageDigest,
		CreatedAt: service.CreatedAtMillis, UpdatedAt: service.UpdatedAtMillis,
	}
}

func publicDeployment(deployment state.DeploymentRecord) deploymentResponse {
	return deploymentResponse{
		ID: deployment.ID, ImageDigest: deployment.ImageDigest, ConfigHash: deployment.ConfigHash,
		Configuration: deployment.Snapshot, Status: deployment.Status,
		ErrorCode: deployment.ErrorCode, ErrorMessage: deployment.ErrorMessage,
		CreatedAt: deployment.CreatedAtMillis, FinishedAt: deployment.FinishedAtMillis,
	}
}
