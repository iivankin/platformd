package daemon

import (
	"context"
	"errors"
	"sync"

	"github.com/iivankin/platformd/internal/cloudflaremesh"
	"github.com/iivankin/platformd/internal/hostnetwork"
	"github.com/iivankin/platformd/internal/portproxy"
	"github.com/iivankin/platformd/internal/state"
)

type liveNetworkGatewayRepository struct {
	store   *state.Store
	runtime *runtimeStack
	proxy   *portproxy.Manager
	mesh    interface {
		Address() (cloudflaremesh.NetworkAddress, error)
	}
	mu sync.Mutex
}

type effectiveNetworkGateway struct {
	gateway      state.NetworkGateway
	namespacePID int
}

func (repository *liveNetworkGatewayRepository) NetworkGateways(ctx context.Context, projectID string) ([]state.NetworkGateway, error) {
	return repository.store.NetworkGateways(ctx, projectID)
}

func (repository *liveNetworkGatewayRepository) NetworkGateway(ctx context.Context, projectID, gatewayID string) (state.NetworkGateway, error) {
	return repository.store.NetworkGateway(ctx, projectID, gatewayID)
}

func (repository *liveNetworkGatewayRepository) HostNetworkAddresses(context.Context) ([]hostnetwork.Address, error) {
	return hostnetwork.Addresses()
}

func (repository *liveNetworkGatewayRepository) CreateNetworkGateway(ctx context.Context, input state.CreateNetworkGateway) (state.NetworkGateway, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	prebound := false
	if input.Configuration.Mode == "export" {
		candidate := state.NetworkGateway{
			ID: input.ID, Mode: input.Configuration.Mode, Protocol: input.Configuration.Protocol,
			Transport: input.Configuration.Transport, InterfaceName: input.Configuration.InterfaceName,
			SourceAddress: input.Configuration.SourceAddress, ListenPort: input.Configuration.ListenPort,
			TargetServiceID: input.Configuration.TargetServiceID, TargetPort: input.Configuration.TargetPort,
		}
		effective, err := repository.effectiveGateway(candidate)
		if err != nil {
			return state.NetworkGateway{}, err
		}
		if err := repository.proxy.Add(exportNetworkGatewayRoute(effective)); err != nil {
			return state.NetworkGateway{}, state.ErrPublicPortUnavailable
		}
		prebound = true
	}
	created, err := repository.store.CreateNetworkGateway(ctx, input)
	if err != nil {
		if prebound {
			_ = repository.proxy.Remove(networkGatewayRouteID(input.ID))
		}
		return state.NetworkGateway{}, err
	}
	// Desired state is authoritative. A missing Mesh/VPC address after disaster
	// recovery leaves the resource visibly degraded instead of binding broadly.
	effective, effectiveErr := repository.effectiveGateway(created)
	if effectiveErr != nil {
		repository.runtime.recordNetworkGatewayFailure(created.ID, effectiveErr)
		return created, nil
	}
	if err := repository.runtime.EnableNetworkGateway(effective, repository.proxy); err != nil {
		if created.Mode == "export" {
			_ = repository.proxy.Remove(networkGatewayRouteID(created.ID))
		}
		repository.runtime.recordNetworkGatewayFailure(created.ID, err)
	}
	return created, nil
}

func (repository *liveNetworkGatewayRepository) UpdateNetworkGateway(ctx context.Context, input state.UpdateNetworkGateway) (state.NetworkGateway, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, err := repository.store.NetworkGateway(ctx, input.ProjectID, input.ID)
	if err != nil {
		return state.NetworkGateway{}, err
	}
	effectiveCurrent, effectiveCurrentErr := repository.effectiveGateway(current)
	if err := repository.runtime.DisableNetworkGateway(current, repository.proxy); err != nil {
		return state.NetworkGateway{}, err
	}
	restoreCurrent := func() {
		if effectiveCurrentErr == nil {
			_ = repository.runtime.EnableNetworkGateway(effectiveCurrent, repository.proxy)
		}
	}
	if input.Configuration.Mode == "export" {
		candidate := state.NetworkGateway{
			ID: input.ID, Mode: input.Configuration.Mode, Protocol: input.Configuration.Protocol,
			Transport: input.Configuration.Transport, InterfaceName: input.Configuration.InterfaceName,
			SourceAddress: input.Configuration.SourceAddress, ListenPort: input.Configuration.ListenPort,
			TargetServiceID: input.Configuration.TargetServiceID, TargetPort: input.Configuration.TargetPort,
		}
		effective, effectiveErr := repository.effectiveGateway(candidate)
		if effectiveErr != nil {
			restoreCurrent()
			return state.NetworkGateway{}, effectiveErr
		}
		if err := repository.proxy.Add(exportNetworkGatewayRoute(effective)); err != nil {
			restoreCurrent()
			return state.NetworkGateway{}, state.ErrPublicPortUnavailable
		}
	}
	updated, err := repository.store.UpdateNetworkGateway(ctx, input)
	if err != nil {
		if input.Configuration.Mode == "export" {
			_ = repository.proxy.Remove(networkGatewayRouteID(input.ID))
		}
		restoreCurrent()
		return state.NetworkGateway{}, err
	}
	effective, effectiveErr := repository.effectiveGateway(updated)
	if effectiveErr != nil {
		repository.runtime.recordNetworkGatewayFailure(updated.ID, effectiveErr)
		return updated, nil
	}
	if err := repository.runtime.EnableNetworkGateway(effective, repository.proxy); err != nil {
		if updated.Mode == "export" {
			_ = repository.proxy.Remove(networkGatewayRouteID(updated.ID))
		}
		repository.runtime.recordNetworkGatewayFailure(updated.ID, err)
	}
	return updated, nil
}

func (repository *liveNetworkGatewayRepository) DeleteNetworkGateway(ctx context.Context, input state.DeleteNetworkGateway) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	current, err := repository.store.NetworkGateway(ctx, input.ProjectID, input.ID)
	if err != nil {
		return err
	}
	effectiveCurrent, effectiveCurrentErr := repository.effectiveGateway(current)
	if err := repository.runtime.DisableNetworkGateway(current, repository.proxy); err != nil {
		return err
	}
	if err := repository.store.DeleteNetworkGateway(ctx, input); err != nil {
		if effectiveCurrentErr == nil {
			_ = repository.runtime.EnableNetworkGateway(effectiveCurrent, repository.proxy)
		}
		return err
	}
	return nil
}

func (repository *liveNetworkGatewayRepository) Restore(ctx context.Context) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	gateways, err := repository.store.ApplicationNetworkGateways(ctx)
	if err != nil {
		return err
	}
	for _, gateway := range gateways {
		effective, effectiveErr := repository.effectiveGateway(gateway)
		if effectiveErr != nil {
			repository.runtime.recordNetworkGatewayFailure(gateway.ID, effectiveErr)
			continue
		}
		if err := repository.runtime.EnableNetworkGateway(effective, repository.proxy); err != nil {
			repository.runtime.recordNetworkGatewayFailure(gateway.ID, err)
		}
	}
	// Missing host addresses are expected after moving a control backup to a new
	// VPS. Keep startup available and surface every gateway as degraded.
	return nil
}

func (repository *liveNetworkGatewayRepository) WithdrawProject(gateways []state.NetworkGateway) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	var failures []error
	for _, gateway := range gateways {
		failures = append(failures, repository.runtime.DisableNetworkGateway(gateway, repository.proxy))
	}
	return errors.Join(failures...)
}

func (repository *liveNetworkGatewayRepository) ReconcileMeshNetworkGateways(ctx context.Context) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	gateways, err := repository.store.ApplicationNetworkGateways(ctx)
	if err != nil {
		return err
	}
	for _, gateway := range gateways {
		if gateway.Transport != "mesh" {
			continue
		}
		if err := repository.runtime.DisableNetworkGateway(gateway, repository.proxy); err != nil {
			repository.runtime.recordNetworkGatewayFailure(gateway.ID, err)
			continue
		}
		effective, effectiveErr := repository.effectiveGateway(gateway)
		if effectiveErr != nil {
			repository.runtime.recordNetworkGatewayFailure(gateway.ID, effectiveErr)
			continue
		}
		if err := repository.runtime.EnableNetworkGateway(effective, repository.proxy); err != nil {
			repository.runtime.recordNetworkGatewayFailure(gateway.ID, err)
		}
	}
	// Per-gateway runtime failures are observable through the canvas status.
	// Credential configuration itself remains successful and retryable.
	return nil
}

func (repository *liveNetworkGatewayRepository) effectiveGateway(gateway state.NetworkGateway) (effectiveNetworkGateway, error) {
	if gateway.Transport != "mesh" {
		return effectiveNetworkGateway{gateway: gateway}, nil
	}
	if repository.mesh == nil {
		return effectiveNetworkGateway{}, errors.New("Cloudflare Mesh integration is unavailable")
	}
	address, err := repository.mesh.Address()
	if err != nil {
		return effectiveNetworkGateway{}, err
	}
	gateway.InterfaceName = address.InterfaceName
	gateway.SourceAddress = address.Address
	return effectiveNetworkGateway{gateway: gateway, namespacePID: address.NamespacePID}, nil
}
