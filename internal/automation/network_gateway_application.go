package automation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/iivankin/platformd/internal/hostnetwork"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

type NetworkGatewayRepository interface {
	NetworkGateways(context.Context, string) ([]state.NetworkGateway, error)
	NetworkGateway(context.Context, string, string) (state.NetworkGateway, error)
	HostNetworkAddresses(context.Context) ([]hostnetwork.Address, error)
	CreateNetworkGateway(context.Context, state.CreateNetworkGateway) (state.NetworkGateway, error)
	UpdateNetworkGateway(context.Context, state.UpdateNetworkGateway) (state.NetworkGateway, error)
	DeleteNetworkGateway(context.Context, state.DeleteNetworkGateway) error
}

type NetworkGatewayApplication struct {
	repository NetworkGatewayRepository
	now        func() time.Time
}

type NetworkGatewayMutationInput struct {
	ProjectID string
	GatewayID string
	Config    state.NetworkGatewayConfiguration
}

type NetworkGatewayMutationResult struct {
	Gateway   state.NetworkGateway
	RequestID string
}

func NewNetworkGatewayApplication(repository NetworkGatewayRepository, now func() time.Time) (*NetworkGatewayApplication, error) {
	if repository == nil {
		return nil, errors.New("network gateway repository is required")
	}
	if now == nil {
		now = time.Now
	}
	return &NetworkGatewayApplication{repository: repository, now: now}, nil
}

func (application *NetworkGatewayApplication) List(ctx context.Context, identity Identity, projectID string) ([]state.NetworkGateway, error) {
	if err := authorizeProjectRead(identity, projectID); err != nil {
		return nil, err
	}
	return application.repository.NetworkGateways(ctx, projectID)
}

func (application *NetworkGatewayApplication) Get(ctx context.Context, identity Identity, projectID, gatewayID string) (state.NetworkGateway, error) {
	if err := authorizeProjectRead(identity, projectID); err != nil {
		return state.NetworkGateway{}, err
	}
	if gatewayID == "" {
		return state.NetworkGateway{}, fmt.Errorf("%w: gatewayId is required", ErrInvalidInput)
	}
	return application.repository.NetworkGateway(ctx, projectID, gatewayID)
}

func (application *NetworkGatewayApplication) ListAddresses(ctx context.Context, identity Identity) ([]hostnetwork.Address, error) {
	if err := requireReadIdentity(identity); err != nil {
		return nil, err
	}
	return application.repository.HostNetworkAddresses(ctx)
}

func (application *NetworkGatewayApplication) Create(ctx context.Context, identity Identity, input NetworkGatewayMutationInput) (NetworkGatewayMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return NetworkGatewayMutationResult{}, err
	}
	gatewayID, auditID, requestID, err := application.identifiers()
	if err != nil {
		return NetworkGatewayMutationResult{}, err
	}
	gateway, err := application.repository.CreateNetworkGateway(ctx, state.CreateNetworkGateway{
		ID: gatewayID, ProjectID: input.ProjectID, Configuration: input.Config,
		AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, CreatedAtMillis: application.now().UnixMilli(),
	})
	return NetworkGatewayMutationResult{Gateway: gateway, RequestID: requestID}, err
}

func (application *NetworkGatewayApplication) Update(ctx context.Context, identity Identity, input NetworkGatewayMutationInput) (NetworkGatewayMutationResult, error) {
	if err := authorizeServiceMutation(identity, input.ProjectID); err != nil {
		return NetworkGatewayMutationResult{}, err
	}
	if input.GatewayID == "" {
		return NetworkGatewayMutationResult{}, fmt.Errorf("%w: gatewayId is required", ErrInvalidInput)
	}
	_, auditID, requestID, err := application.identifiers()
	if err != nil {
		return NetworkGatewayMutationResult{}, err
	}
	gateway, err := application.repository.UpdateNetworkGateway(ctx, state.UpdateNetworkGateway{
		ID: input.GatewayID, ProjectID: input.ProjectID, Configuration: input.Config,
		AuditEventID: auditID, ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, UpdatedAtMillis: application.now().UnixMilli(),
	})
	return NetworkGatewayMutationResult{Gateway: gateway, RequestID: requestID}, err
}

func (application *NetworkGatewayApplication) Delete(ctx context.Context, identity Identity, projectID, gatewayID string) (string, error) {
	if err := authorizeServiceMutation(identity, projectID); err != nil {
		return "", err
	}
	if gatewayID == "" {
		return "", fmt.Errorf("%w: gatewayId is required", ErrInvalidInput)
	}
	_, auditID, requestID, err := application.identifiers()
	if err != nil {
		return "", err
	}
	err = application.repository.DeleteNetworkGateway(ctx, state.DeleteNetworkGateway{
		ID: gatewayID, ProjectID: projectID, AuditEventID: auditID,
		ActorKind: "token", ActorID: identity.TokenID,
		RequestCorrelationID: requestID, DeletedAtMillis: application.now().UnixMilli(),
	})
	return requestID, err
}

func (application *NetworkGatewayApplication) identifiers() (string, string, string, error) {
	gatewayID, err := id.New()
	if err != nil {
		return "", "", "", fmt.Errorf("allocate network gateway ID: %w", err)
	}
	auditID, err := id.New()
	if err != nil {
		return "", "", "", fmt.Errorf("allocate network gateway audit ID: %w", err)
	}
	requestID, err := id.New()
	if err != nil {
		return "", "", "", fmt.Errorf("allocate network gateway request ID: %w", err)
	}
	return gatewayID, auditID, requestID, nil
}
