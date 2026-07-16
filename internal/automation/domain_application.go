package automation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type DomainRepository interface {
	ServiceDomains(context.Context, string, string) ([]state.ServiceDomain, error)
	AttachServiceDomain(context.Context, state.AttachServiceDomainInput) (state.ServiceDomain, error)
	DetachServiceDomain(context.Context, state.DetachServiceDomainInput) error
}

type DomainApplication struct {
	repository DomainRepository
	random     io.Reader
	now        func() time.Time
}

type AttachDomainInput struct {
	ProjectID  string
	ServiceID  string
	Hostname   string
	TargetPort int
	Move       bool
}

type DetachDomainInput struct {
	ProjectID string
	ServiceID string
	Hostname  string
}

type DomainMutationResult struct {
	Domain    state.ServiceDomain
	RequestID string
}

func NewDomainApplication(repository DomainRepository, random io.Reader, now func() time.Time) (*DomainApplication, error) {
	if repository == nil {
		return nil, errors.New("domain automation repository is required")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &DomainApplication{repository: repository, random: random, now: now}, nil
}

func (application *DomainApplication) List(ctx context.Context, identity Identity, projectID, serviceID string) ([]state.ServiceDomain, error) {
	if err := authorizeProjectRead(identity, projectID); err != nil {
		return nil, err
	}
	if serviceID == "" {
		return nil, fmt.Errorf("%w: serviceId is required", ErrInvalidInput)
	}
	return application.repository.ServiceDomains(ctx, projectID, serviceID)
}

func (application *DomainApplication) Attach(ctx context.Context, identity Identity, input AttachDomainInput) (DomainMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return DomainMutationResult{}, err
	}
	if input.ServiceID == "" || input.Hostname == "" || input.TargetPort < 1 || input.TargetPort > 65535 {
		return DomainMutationResult{}, fmt.Errorf("%w: serviceId, hostname, and targetPort are required", ErrInvalidInput)
	}
	auditID, requestID, timestamp, err := application.identifiers()
	if err != nil {
		return DomainMutationResult{}, err
	}
	domain, err := application.repository.AttachServiceDomain(ctx, state.AttachServiceDomainInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Hostname: input.Hostname, TargetPort: input.TargetPort, Move: input.Move,
		AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, CreatedAtMillis: timestamp,
	})
	return DomainMutationResult{Domain: domain, RequestID: requestID}, err
}

func (application *DomainApplication) Detach(ctx context.Context, identity Identity, input DetachDomainInput) (DomainMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return DomainMutationResult{}, err
	}
	if input.ServiceID == "" || input.Hostname == "" {
		return DomainMutationResult{}, fmt.Errorf("%w: serviceId and hostname are required", ErrInvalidInput)
	}
	auditID, requestID, timestamp, err := application.identifiers()
	if err != nil {
		return DomainMutationResult{}, err
	}
	err = application.repository.DetachServiceDomain(ctx, state.DetachServiceDomainInput{
		ProjectID: input.ProjectID, ServiceID: input.ServiceID, Hostname: input.Hostname,
		AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, CreatedAtMillis: timestamp,
	})
	return DomainMutationResult{RequestID: requestID}, err
}

func (application *DomainApplication) identifiers() (string, string, int64, error) {
	timestamp := application.now()
	auditID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return "", "", 0, fmt.Errorf("allocate domain audit ID: %w", err)
	}
	requestID, err := id.NewWith(timestamp, application.random)
	if err != nil {
		return "", "", 0, fmt.Errorf("allocate domain request ID: %w", err)
	}
	return auditID, requestID, timestamp.UnixMilli(), nil
}

func authorizeProjectRead(identity Identity, projectID string) error {
	if identity.TokenID == "" {
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
