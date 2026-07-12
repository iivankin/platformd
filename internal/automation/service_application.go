package automation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

var (
	ErrAdminRequired   = errors.New("admin token is required")
	ErrProjectBoundary = errors.New("project is outside this token boundary")
	ErrInvalidInput    = errors.New("invalid service mutation input")
)

type ServiceMutationRepository interface {
	CreateService(context.Context, state.CreateService) (state.ServiceDesired, error)
	UpdateService(context.Context, state.UpdateServiceInput) (state.ServiceDesired, error)
	RollbackService(context.Context, state.RollbackServiceInput) (state.ServiceDesired, error)
	RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error)
}

type ServiceApplication struct {
	repository ServiceMutationRepository
	random     io.Reader
	now        func() time.Time
}

type CreateServiceInput struct {
	ProjectID     string
	Name          string
	Enabled       bool
	Configuration serviceconfig.Snapshot
}

type UpdateServiceInput struct {
	ProjectID         string
	ServiceID         string
	Enabled           bool
	ExpectedUpdatedAt int64
	Configuration     serviceconfig.Snapshot
}

type RedeployServiceInput struct {
	ProjectID         string
	ServiceID         string
	ExpectedUpdatedAt int64
}

type RollbackServiceInput struct {
	ProjectID         string
	ServiceID         string
	DeploymentID      string
	ExpectedUpdatedAt int64
}

type ServiceMutationResult struct {
	Service   state.ServiceDesired
	RequestID string
}

func NewServiceApplication(repository ServiceMutationRepository, random io.Reader, now func() time.Time) (*ServiceApplication, error) {
	if repository == nil {
		return nil, errors.New("service automation repository is required")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &ServiceApplication{repository: repository, random: random, now: now}, nil
}

func (application *ServiceApplication) Create(ctx context.Context, identity Identity, input CreateServiceInput) (ServiceMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return ServiceMutationResult{}, err
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return ServiceMutationResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	snapshot, err := serviceconfig.Normalize(input.Configuration)
	if err != nil {
		return ServiceMutationResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 3)
	if err != nil {
		return ServiceMutationResult{}, err
	}
	service, err := application.repository.CreateService(ctx, state.CreateService{
		ID: identifiers[0], ProjectID: input.ProjectID, Name: input.Name,
		Enabled: input.Enabled, Snapshot: snapshot,
		AuditEventID: identifiers[1], ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: identifiers[2], CreatedAtMillis: timestamp.UnixMilli(),
	})
	return ServiceMutationResult{Service: service, RequestID: identifiers[2]}, err
}

func (application *ServiceApplication) Update(ctx context.Context, identity Identity, input UpdateServiceInput) (ServiceMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return ServiceMutationResult{}, err
	}
	if input.ServiceID == "" || input.ExpectedUpdatedAt <= 0 {
		return ServiceMutationResult{}, fmt.Errorf("%w: serviceId and expectedUpdatedAt are required", ErrInvalidInput)
	}
	snapshot, err := serviceconfig.Normalize(input.Configuration)
	if err != nil {
		return ServiceMutationResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return ServiceMutationResult{}, err
	}
	service, err := application.repository.UpdateService(ctx, state.UpdateServiceInput{
		ID: input.ServiceID, ProjectID: input.ProjectID, Enabled: input.Enabled,
		Snapshot: snapshot, ExpectedUpdatedMillis: input.ExpectedUpdatedAt,
		AuditEventID: identifiers[0], ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: identifiers[1], UpdatedAtMillis: timestamp.UnixMilli(),
	})
	return ServiceMutationResult{Service: service, RequestID: identifiers[1]}, err
}

func (application *ServiceApplication) Redeploy(ctx context.Context, identity Identity, input RedeployServiceInput) (ServiceMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return ServiceMutationResult{}, err
	}
	if input.ServiceID == "" || input.ExpectedUpdatedAt <= 0 {
		return ServiceMutationResult{}, fmt.Errorf("%w: serviceId and expectedUpdatedAt are required", ErrInvalidInput)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return ServiceMutationResult{}, err
	}
	service, err := application.repository.RedeployService(ctx, state.RedeployServiceInput{
		ID: input.ServiceID, ProjectID: input.ProjectID, ExpectedUpdatedMillis: input.ExpectedUpdatedAt,
		AuditEventID: identifiers[0], ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: identifiers[1], CreatedAtMillis: timestamp.UnixMilli(),
	})
	return ServiceMutationResult{Service: service, RequestID: identifiers[1]}, err
}

func (application *ServiceApplication) Rollback(ctx context.Context, identity Identity, input RollbackServiceInput) (ServiceMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return ServiceMutationResult{}, err
	}
	if input.ServiceID == "" || input.DeploymentID == "" || input.ExpectedUpdatedAt <= 0 {
		return ServiceMutationResult{}, fmt.Errorf("%w: serviceId, deploymentId, and expectedUpdatedAt are required", ErrInvalidInput)
	}
	timestamp := application.now()
	identifiers, err := application.identifiers(timestamp, 2)
	if err != nil {
		return ServiceMutationResult{}, err
	}
	service, err := application.repository.RollbackService(ctx, state.RollbackServiceInput{
		ID: input.ServiceID, ProjectID: input.ProjectID, DeploymentID: input.DeploymentID,
		ExpectedUpdatedMillis: input.ExpectedUpdatedAt,
		AuditEventID:          identifiers[0], ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: identifiers[1], UpdatedAtMillis: timestamp.UnixMilli(),
	})
	return ServiceMutationResult{Service: service, RequestID: identifiers[1]}, err
}

func authorizeServiceMutation(identity Identity, projectID string) error {
	if identity.TokenID == "" || !identity.IsAdmin() {
		return ErrAdminRequired
	}
	if projectID == "" {
		return fmt.Errorf("%w: projectId is required", ErrInvalidInput)
	}
	if !identity.AllowsProject(projectID) {
		return ErrProjectBoundary
	}
	return nil
}

func (application *ServiceApplication) identifiers(timestamp time.Time, count int) ([]string, error) {
	identifiers := make([]string, count)
	for index := range identifiers {
		value, err := id.NewWith(timestamp, application.random)
		if err != nil {
			return nil, fmt.Errorf("allocate service mutation identifiers: %w", err)
		}
		identifiers[index] = value
	}
	return identifiers, nil
}
