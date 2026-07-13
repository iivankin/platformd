package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/state"
)

type objectStoreServer struct {
	server *http.Server
}

func (stack *runtimeStack) ConfigureObjectStores(ctx context.Context, store *state.Store, handler http.Handler) error {
	if store == nil || handler == nil {
		return errors.New("object store runtime dependencies are incomplete")
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.objectStoreHandler = handler
	stack.mu.Unlock()

	stores, err := store.ObjectStores(ctx)
	if err != nil {
		return err
	}
	for _, objectStore := range stores {
		if err := stack.EnableObjectStore(objectStore); err != nil {
			stack.recordObjectStoreFailure(objectStore.ProjectID, err)
		}
	}
	return nil
}

// EnableObjectStore publishes a project-local S3 endpoint after desired state
// is committed. Repeating it is safe and repairs DNS/firewall state after a
// transient publication failure without introducing another durable state.
func (stack *runtimeStack) EnableObjectStore(objectStore state.ObjectStore) error {
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	if stack.objectStoreHandler == nil {
		stack.mu.Unlock()
		return errors.New("object store runtime is not configured")
	}
	network, networkExists := stack.projectNetworks[objectStore.ProjectID]
	zone := stack.dnsZones[objectStore.ProjectID]
	project, projectExists := stack.firewallProjects[objectStore.ProjectID]
	if !networkExists || zone == nil || !projectExists {
		stack.mu.Unlock()
		return fmt.Errorf("project %s network runtime is unavailable", objectStore.ProjectID)
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		stack.mu.Unlock()
		return fmt.Errorf("parse project gateway: %w", err)
	}

	server := stack.objectStoreServers[objectStore.ProjectID]
	createdServer := false
	if server == nil {
		listener, listenErr := listenObjectStore(stack.ctx, gateway, firewall.ObjectStorePort)
		if listenErr != nil {
			stack.mu.Unlock()
			return fmt.Errorf("listen project S3 endpoint: %w", listenErr)
		}
		httpServer := &http.Server{
			Handler:           stack.objectStoreHandler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    64 << 10,
		}
		server = &objectStoreServer{server: httpServer}
		stack.objectStoreServers[objectStore.ProjectID] = server
		createdServer = true
		go stack.serveObjectStore(objectStore.ProjectID, httpServer, listener)
	}

	hostname := objectStore.Name + "." + objectStore.ProjectName + ".internal"
	if err := zone.Set(hostname, gateway); err != nil {
		if createdServer {
			delete(stack.objectStoreServers, objectStore.ProjectID)
			_ = server.server.Close()
		}
		stack.mu.Unlock()
		return err
	}
	if !project.ObjectStoreEnabled {
		project.ObjectStoreEnabled = true
		candidate := make([]firewall.Project, 0, len(stack.firewallProjects))
		for projectID, current := range stack.firewallProjects {
			if projectID == objectStore.ProjectID {
				current = project
			}
			candidate = append(candidate, current)
		}
		if err := stack.firewall.Apply(candidate); err != nil {
			_ = zone.Delete(hostname)
			if createdServer {
				delete(stack.objectStoreServers, objectStore.ProjectID)
				_ = server.server.Close()
			}
			stack.mu.Unlock()
			return err
		}
		stack.firewallProjects[objectStore.ProjectID] = project
	}
	delete(stack.objectStoreFailures, objectStore.ProjectID)
	stack.mu.Unlock()
	return nil
}

func (stack *runtimeStack) serveObjectStore(projectID string, server *http.Server, listener net.Listener) {
	err := server.Serve(listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		stack.recordObjectStoreFailure(projectID, fmt.Errorf("serve project S3 endpoint: %w", err))
	}
}

func (stack *runtimeStack) ObjectStoreStatus(projectID string) (string, string) {
	stack.mu.Lock()
	_, running := stack.objectStoreServers[projectID]
	failure := stack.objectStoreFailures[projectID]
	closed := stack.closed
	stack.mu.Unlock()
	if failure != nil {
		return "failed", failure.Error()
	}
	if closed || !running {
		return "pending", "S3 endpoint is not ready"
	}
	return "running", ""
}

func (stack *runtimeStack) recordObjectStoreFailure(projectID string, err error) {
	stack.mu.Lock()
	stack.objectStoreFailures[projectID] = err
	stack.mu.Unlock()
}
