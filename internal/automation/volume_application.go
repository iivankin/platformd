package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type VolumeApplication struct {
	application *volume.Application
}

type CreateVolumeInput struct {
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
	Name      string `json:"name"`
}

func NewVolumeApplication(application *volume.Application) (*VolumeApplication, error) {
	if application == nil {
		return nil, errors.New("volume automation application is required")
	}
	return &VolumeApplication{application: application}, nil
}

func (application *VolumeApplication) List(
	ctx context.Context,
	identity Identity,
	projectID string,
	serviceID string,
) ([]state.Volume, error) {
	if err := authorizeVolume(identity, projectID, serviceID, false); err != nil {
		return nil, err
	}
	return application.application.List(ctx, projectID, serviceID)
}

func (application *VolumeApplication) Create(
	ctx context.Context,
	identity Identity,
	input CreateVolumeInput,
) (volume.MutationResult, error) {
	if err := authorizeVolume(identity, input.ProjectID, input.ServiceID, true); err != nil {
		return volume.MutationResult{}, err
	}
	return application.application.Create(ctx, volume.CreateInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Name: input.Name,
		Actor: volume.Actor{Kind: "token", ID: identity.TokenID},
	})
}

func (application *VolumeApplication) Delete(
	ctx context.Context,
	identity Identity,
	projectID string,
	serviceID string,
	volumeID string,
) (volume.MutationResult, error) {
	if err := authorizeVolume(identity, projectID, serviceID, true); err != nil {
		return volume.MutationResult{}, err
	}
	if volumeID == "" {
		return volume.MutationResult{}, ErrInvalidInput
	}
	return application.application.Delete(ctx, volume.DeleteInput{
		ProjectID: projectID, ServiceID: serviceID, VolumeID: volumeID,
		Actor: volume.Actor{Kind: "token", ID: identity.TokenID},
	})
}

func authorizeVolume(identity Identity, projectID, serviceID string, admin bool) error {
	if identity.TokenID == "" || (identity.Role != "read" && identity.Role != "admin") || (admin && !identity.IsAdmin()) {
		return ErrAdminRequired
	}
	if projectID == "" || serviceID == "" {
		return ErrInvalidInput
	}
	if !identity.AllowsProject(projectID) {
		return ErrProjectBoundary
	}
	return nil
}
