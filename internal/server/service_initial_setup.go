package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type initialServiceDomainRequest struct {
	Hostname   string `json:"hostname"`
	TargetPort int    `json:"targetPort"`
}

type initialServiceListenerRequest struct {
	Protocol   string `json:"protocol"`
	PublicPort int    `json:"publicPort"`
	TargetPort int    `json:"targetPort"`
}

type initialServiceVolumeRequest struct {
	Name          string `json:"name"`
	ContainerPath string `json:"containerPath"`
}

type initialServiceSetup struct {
	Domains   []initialServiceDomainRequest
	Listeners []initialServiceListenerRequest
	Volumes   []initialServiceVolumeRequest
}

func (setup initialServiceSetup) empty() bool {
	return len(setup.Domains) == 0 && len(setup.Listeners) == 0 && len(setup.Volumes) == 0
}

type initialServiceSetupStage string

const (
	initialServiceSetupDomains   initialServiceSetupStage = "domains"
	initialServiceSetupListeners initialServiceSetupStage = "listeners"
	initialServiceSetupService   initialServiceSetupStage = "service"
	initialServiceSetupVolumes   initialServiceSetupStage = "volumes"
)

func applyInitialServiceSetup(
	ctx context.Context,
	config handlerConfig,
	created state.ServiceDesired,
	snapshot serviceconfig.Snapshot,
	setup initialServiceSetup,
	identity access.Identity,
	enabled bool,
) (state.ServiceDesired, initialServiceSetupStage, error) {
	mounts := make([]serviceconfig.VolumeMount, 0, len(setup.Volumes))
	for _, requested := range setup.Volumes {
		if config.volumes == nil {
			return created, initialServiceSetupVolumes, errors.New("volume management is unavailable")
		}
		result, err := config.volumes.Create(ctx, volume.CreateInput{
			ProjectID: created.ProjectID, ServiceID: created.ID,
			Name:  requested.Name,
			Actor: volume.Actor{Kind: "access", ID: identity.Subject, Email: identity.Email},
		})
		if err != nil {
			return created, initialServiceSetupVolumes, err
		}
		if requested.ContainerPath != "" {
			mounts = append(mounts, serviceconfig.VolumeMount{
				VolumeID: result.Volume.ID, ContainerPath: requested.ContainerPath,
			})
		}
	}
	for _, requested := range setup.Domains {
		if config.domains == nil {
			return created, initialServiceSetupDomains, errors.New("domain management is unavailable")
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			return created, initialServiceSetupDomains, err
		}
		_, err = config.domains.AttachServiceDomain(ctx, state.AttachServiceDomainInput{
			ProjectID: created.ProjectID, ServiceID: created.ID,
			Hostname: requested.Hostname, TargetPort: requested.TargetPort,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			return created, initialServiceSetupDomains, err
		}
	}
	for _, requested := range setup.Listeners {
		if config.listeners == nil {
			return created, initialServiceSetupListeners, errors.New("listener management is unavailable")
		}
		timestamp := config.now()
		_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			return created, initialServiceSetupListeners, err
		}
		_, err = config.listeners.AttachServiceListener(ctx, state.AttachServiceListenerInput{
			ProjectID: created.ProjectID, ServiceID: created.ID,
			Protocol: requested.Protocol, PublicPort: requested.PublicPort, TargetPort: requested.TargetPort,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, CreatedAtMillis: timestamp.UnixMilli(),
		})
		if err != nil {
			return created, initialServiceSetupListeners, err
		}
	}

	snapshot.VolumeMounts = mounts
	timestamp := config.now()
	_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
	if err != nil {
		return created, initialServiceSetupService, err
	}
	updated, err := config.services.UpdateService(ctx, state.UpdateServiceInput{
		ID: created.ID, ProjectID: created.ProjectID, Enabled: enabled,
		Snapshot: snapshot, ExpectedUpdatedMillis: created.UpdatedAtMillis,
		AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
		RequestCorrelationID: correlationID, UpdatedAtMillis: timestamp.UnixMilli(),
	})
	if err != nil {
		return created, initialServiceSetupService, err
	}
	return updated, initialServiceSetupService, nil
}

func rollbackInitialService(
	ctx context.Context,
	config handlerConfig,
	created state.ServiceDesired,
	identity access.Identity,
) error {
	timestamp := config.now()
	_, auditID, correlationID, err := createRequestIDs(timestamp, config.random)
	if err != nil {
		return err
	}
	_, err = config.services.DeleteService(ctx, state.DeleteServiceInput{
		ID: created.ID, ProjectID: created.ProjectID,
		ExpectedUpdatedMillis: created.UpdatedAtMillis,
		AuditEventID:          auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
		RequestCorrelationID: correlationID, DeletedAtMillis: timestamp.UnixMilli(),
	})
	return err
}

func writeInitialServiceSetupError(
	response http.ResponseWriter,
	stage initialServiceSetupStage,
	err error,
) {
	switch stage {
	case initialServiceSetupDomains:
		writeDomainMutationError(response, err)
	case initialServiceSetupListeners:
		writeListenerMutationError(response, err)
	case initialServiceSetupVolumes:
		writeVolumeError(response, err)
	default:
		writeServiceMutationError(response, err)
	}
}
