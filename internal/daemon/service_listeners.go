package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/portproxy"
	"github.com/iivankin/platformd/internal/state"
)

type liveServiceListenerRepository struct {
	store *state.Store
	proxy *portproxy.Manager
	mu    sync.Mutex
}

func (repository *liveServiceListenerRepository) WithdrawService(ctx context.Context, serviceID string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	listeners, err := repository.store.ApplicationListeners(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, listener := range listeners {
		if listener.ServiceID == serviceID {
			failures = append(failures, repository.proxy.Remove(listener.Protocol, listener.PublicPort))
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

	existing, err := repository.matchingListener(ctx, input.Protocol, input.PublicPort)
	if err != nil {
		return state.ServiceListener{}, err
	}
	if existing != nil && existing.ServiceID != input.ServiceID {
		// Let the state layer return the structured owner information used by the API.
		return repository.store.AttachServiceListener(ctx, input)
	}

	requested := portproxy.Route{
		Protocol: input.Protocol, PublicPort: input.PublicPort,
		ServiceID: input.ServiceID, TargetPort: input.TargetPort,
	}
	if err := repository.proxy.Add(requested); err != nil {
		return state.ServiceListener{}, state.ErrPublicPortUnavailable
	}
	listener, err := repository.store.AttachServiceListener(ctx, input)
	if err == nil {
		return listener, nil
	}

	// The socket is acquired before committing state so an unavailable VPS port
	// can never be persisted. Restore the prior route if the state write fails.
	if existing == nil {
		_ = repository.proxy.Remove(input.Protocol, input.PublicPort)
	} else {
		_ = repository.proxy.Add(listenerRoute(*existing))
	}
	return state.ServiceListener{}, err
}

func (repository *liveServiceListenerRepository) DetachServiceListener(ctx context.Context, input state.DetachServiceListenerInput) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := repository.store.DetachServiceListener(ctx, input); err != nil {
		return err
	}
	return repository.proxy.Remove(input.Protocol, input.PublicPort)
}

func (repository *liveServiceListenerRepository) Restore(ctx context.Context) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	listeners, err := repository.store.ApplicationListeners(ctx)
	if err != nil {
		return err
	}
	for _, listener := range listeners {
		if err := repository.proxy.Add(listenerRoute(listener)); err != nil {
			return fmt.Errorf("restore public %s port %d: %w", listener.Protocol, listener.PublicPort, err)
		}
	}
	return nil
}

func (repository *liveServiceListenerRepository) matchingListener(ctx context.Context, protocol string, publicPort int) (*state.ServiceListener, error) {
	listeners, err := repository.store.ApplicationListeners(ctx)
	if err != nil {
		return nil, err
	}
	for index := range listeners {
		if listeners[index].Protocol == protocol && listeners[index].PublicPort == publicPort {
			return &listeners[index], nil
		}
	}
	return nil, nil
}

func listenerRoute(listener state.ServiceListener) portproxy.Route {
	return portproxy.Route{
		Protocol: listener.Protocol, PublicPort: listener.PublicPort,
		ServiceID: listener.ServiceID, TargetPort: listener.TargetPort,
	}
}
