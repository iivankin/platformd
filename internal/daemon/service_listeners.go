package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/hosthub"
	"github.com/iivankin/platformd/internal/portproxy"
	"github.com/iivankin/platformd/internal/state"
)

type hostPublicSync interface {
	SyncPublic(hostID, serviceID string) error
}

type liveServiceListenerRepository struct {
	store  *state.Store
	proxy  *portproxy.Manager
	remote hostPublicSync
	mu     sync.Mutex
}

func (repository *liveServiceListenerRepository) SetRemote(remote hostPublicSync) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.remote = remote
}

func notifyHostPublic(remote hostPublicSync, hostID, serviceID string) {
	if hostID == "" || remote == nil {
		return
	}
	if err := remote.SyncPublic(hostID, serviceID); err != nil && !errors.Is(err, hosthub.ErrHostOffline) {
		log.Printf("sync public routes to child %s service %s: %v", hostID, serviceID, err)
	}
}

func (repository *liveServiceListenerRepository) notifyRemote(hostID, serviceID string) {
	notifyHostPublic(repository.remote, hostID, serviceID)
}

func (repository *liveServiceListenerRepository) WithdrawService(ctx context.Context, serviceID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	desired, err := repository.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	if desired.HostID != "" {
		return nil
	}
	listeners, err := repository.store.ApplicationListeners(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, listener := range listeners {
		if listener.ServiceID == serviceID {
			failures = append(failures, repository.proxy.Remove(listenerRouteID(listener.Protocol, listener.PublicPort)))
		}
	}
	return errors.Join(failures...)
}

func (repository *liveServiceListenerRepository) ServiceListeners(ctx context.Context, projectID, serviceID string) ([]state.ServiceListener, error) {
	return repository.store.ServiceListeners(ctx, projectID, serviceID)
}

func (repository *liveServiceListenerRepository) AttachServiceListener(ctx context.Context, input state.AttachServiceListenerInput) (state.ServiceListener, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))

	service, err := repository.store.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return state.ServiceListener{}, err
	}

	existing, err := repository.matchingListener(ctx, service.HostID, input.Protocol, input.PublicPort)
	if err != nil {
		return state.ServiceListener{}, err
	}
	if existing != nil && existing.ServiceID != input.ServiceID {
		// Let the state layer return the structured owner information used by the API.
		return repository.store.AttachServiceListener(ctx, input)
	}

	if service.HostID == "" {
		requested := portproxy.Route{
			ID:       listenerRouteID(input.Protocol, input.PublicPort),
			Protocol: input.Protocol, ListenAddress: "0.0.0.0", ListenPort: input.PublicPort,
			Target: portproxy.ServiceTarget{ServiceID: input.ServiceID, Port: input.TargetPort},
		}
		if err := repository.proxy.Add(requested); err != nil {
			return state.ServiceListener{}, state.ErrPublicPortUnavailable
		}
	}
	listener, err := repository.store.AttachServiceListener(ctx, input)
	if err == nil {
		if service.HostID != "" {
			repository.notifyRemote(service.HostID, service.ID)
		}
		return listener, nil
	}

	if service.HostID == "" {
		if existing == nil {
			_ = repository.proxy.Remove(listenerRouteID(input.Protocol, input.PublicPort))
		} else {
			_ = repository.proxy.Add(listenerRoute(*existing))
		}
	}
	return state.ServiceListener{}, err
}

func (repository *liveServiceListenerRepository) DetachServiceListener(ctx context.Context, input state.DetachServiceListenerInput) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	service, err := repository.store.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return err
	}
	if err := repository.store.DetachServiceListener(ctx, input); err != nil {
		return err
	}
	if service.HostID == "" {
		if err := repository.proxy.Remove(listenerRouteID(input.Protocol, input.PublicPort)); err != nil {
			return err
		}
	}
	repository.notifyRemote(service.HostID, service.ID)
	return nil
}

func (repository *liveServiceListenerRepository) SyncServiceHost(ctx context.Context, projectID, serviceID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	desired, err := repository.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	listeners, err := repository.store.ServiceListeners(ctx, projectID, serviceID)
	if err != nil {
		return err
	}
	var failures []error
	for _, listener := range listeners {
		routeID := listenerRouteID(listener.Protocol, listener.PublicPort)
		if err := repository.proxy.Remove(routeID); err != nil {
			failures = append(failures, err)
			continue
		}
		if desired.HostID != "" {
			continue
		}
		if err := repository.proxy.Add(listenerRoute(listener)); err != nil {
			failures = append(failures, err)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	repository.notifyRemote(desired.HostID, desired.ID)
	return nil
}

func (repository *liveServiceListenerRepository) Restore(ctx context.Context) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	listeners, err := repository.store.ApplicationListeners(ctx)
	if err != nil {
		return err
	}
	for _, listener := range listeners {
		desired, err := repository.store.DesiredService(ctx, listener.ServiceID)
		if err != nil {
			return err
		}
		if desired.HostID != "" {
			continue
		}
		if err := repository.proxy.Add(listenerRoute(listener)); err != nil {
			return fmt.Errorf("restore public %s port %d: %w", listener.Protocol, listener.PublicPort, err)
		}
	}
	return nil
}

func (repository *liveServiceListenerRepository) matchingListener(ctx context.Context, hostID, protocol string, publicPort int) (*state.ServiceListener, error) {
	listeners, err := repository.store.ApplicationListeners(ctx)
	if err != nil {
		return nil, err
	}
	for index := range listeners {
		if listeners[index].Protocol != protocol || listeners[index].PublicPort != publicPort {
			continue
		}
		desired, err := repository.store.DesiredService(ctx, listeners[index].ServiceID)
		if err != nil {
			return nil, err
		}
		if desired.HostID == hostID {
			return &listeners[index], nil
		}
	}
	return nil, nil
}

func listenerRoute(listener state.ServiceListener) portproxy.Route {
	return portproxy.Route{
		ID:       listenerRouteID(listener.Protocol, listener.PublicPort),
		Protocol: listener.Protocol, ListenAddress: "0.0.0.0", ListenPort: listener.PublicPort,
		Target: portproxy.ServiceTarget{ServiceID: listener.ServiceID, Port: listener.TargetPort},
	}
}

func listenerRouteID(protocol string, publicPort int) string {
	return fmt.Sprintf("service-listener:%s:%d", strings.ToLower(strings.TrimSpace(protocol)), publicPort)
}
