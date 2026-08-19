package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/iivankin/platformd/internal/cloudflaredns"
	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

type liveDomainRepository struct {
	store         *state.Store
	certificates  *origin.Selector
	router        *ingress.Router
	publicMu      *sync.Mutex
	cloudflare    *cloudflaredns.Application
	adminHostname string
	remote        hostPublicSync
}

func (repository *liveDomainRepository) SetRemote(remote hostPublicSync) {
	if repository == nil || repository.publicMu == nil {
		return
	}
	repository.publicMu.Lock()
	repository.remote = remote
	repository.publicMu.Unlock()
}

func (repository *liveDomainRepository) notifyRemote(hostID, serviceID string) {
	notifyHostPublic(repository.remote, hostID, serviceID)
}

func (repository liveDomainRepository) ServiceDomains(ctx context.Context, projectID, serviceID string) ([]state.ServiceDomain, error) {
	return repository.store.ServiceDomains(ctx, projectID, serviceID)
}

func (repository liveDomainRepository) AttachServiceDomain(ctx context.Context, input state.AttachServiceDomainInput) (state.ServiceDomain, error) {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	hostname, err := publichostname.Normalize(input.Hostname)
	if err != nil {
		return state.ServiceDomain{}, err
	}
	if !repository.certificates.Covers(hostname) {
		return state.ServiceDomain{}, state.ErrCertificateCoverage
	}
	input.Hostname = hostname
	service, err := repository.store.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return state.ServiceDomain{}, err
	}
	createdDNSRecord := false
	if repository.cloudflare != nil {
		createdDNSRecord, err = repository.ensureDNS(ctx, hostname, service.HostID)
		if err != nil {
			return state.ServiceDomain{}, fmt.Errorf("configure Cloudflare DNS for %s: %w", hostname, err)
		}
	}
	domain, err := repository.store.AttachServiceDomain(ctx, input)
	if err != nil {
		if createdDNSRecord {
			_, cleanupErr := repository.cloudflare.DeleteServiceHostname(ctx, hostname)
			err = errors.Join(err, cleanupErr)
		}
		return state.ServiceDomain{}, err
	}
	if err := repository.reload(ctx); err != nil {
		repository.notifyRemote(service.HostID, service.ID)
		return state.ServiceDomain{}, err
	}
	repository.notifyRemote(service.HostID, service.ID)
	return domain, nil
}

func (repository liveDomainRepository) DetachServiceDomain(ctx context.Context, input state.DetachServiceDomainInput) error {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	hostID := ""
	if current, err := repository.store.ServiceDomains(ctx, input.ProjectID, input.ServiceID); err == nil {
		for _, domain := range current {
			if domain.Hostname == input.Hostname {
				hostID = domain.HostID
				break
			}
		}
	}
	deletedDNSRecord := false
	if repository.cloudflare != nil {
		var err error
		deletedDNSRecord, err = repository.cloudflare.DeleteServiceHostname(ctx, input.Hostname)
		if err != nil {
			return fmt.Errorf("remove Cloudflare DNS for %s: %w", input.Hostname, err)
		}
	}
	if err := repository.store.DetachServiceDomain(ctx, input); err != nil {
		if deletedDNSRecord {
			_, restoreErr := repository.ensureDNS(ctx, input.Hostname, hostID)
			return errors.Join(err, restoreErr)
		}
		return err
	}
	reloadErr := repository.reload(ctx)
	repository.notifyRemote(hostID, input.ServiceID)
	return reloadErr
}

func (repository liveDomainRepository) ServiceDomainDNSStatus(ctx context.Context, projectID, serviceID, hostname string) (cloudflaredns.DNSStatus, error) {
	domains, err := repository.store.ServiceDomains(ctx, projectID, serviceID)
	if err != nil {
		return "", err
	}
	normalized, err := publichostname.Normalize(hostname)
	if err != nil {
		return "", err
	}
	found := false
	for _, domain := range domains {
		if domain.Hostname == normalized {
			found = true
			break
		}
	}
	if !found {
		return "", state.ErrDomainNotFound
	}
	if repository.cloudflare == nil {
		return cloudflaredns.DNSStatusUnmanaged, nil
	}
	return repository.cloudflare.ServiceHostnameDNSStatus(ctx, normalized)
}

func (repository liveDomainRepository) reconcileDNS(ctx context.Context) error {
	if repository.cloudflare == nil {
		return nil
	}
	domains, err := repository.store.ApplicationDomains(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, domain := range domains {
		if _, err := repository.ensureDNS(ctx, domain.Hostname, domain.HostID); err != nil {
			result = errors.Join(result, fmt.Errorf("reconcile Cloudflare DNS for %s: %w", domain.Hostname, err))
		}
	}
	return result
}

func (repository liveDomainRepository) reload(ctx context.Context) error {
	domains, err := repository.store.ApplicationDomains(ctx)
	if err != nil {
		return err
	}
	previews, err := repository.store.ActivePreviewDeployments(ctx)
	if err != nil {
		return err
	}
	routes := make(map[string]ingress.Route, len(domains)+len(previews))
	for _, domain := range domains {
		if domain.HostID != "" {
			continue
		}
		routes[domain.Hostname] = ingress.Route{ServiceID: domain.ServiceID, TargetPort: domain.TargetPort}
	}
	for _, preview := range previews {
		routes[preview.Hostname] = ingress.Route{ServiceID: preview.ServiceID, PreviewID: preview.ID, TargetPort: preview.TargetPort}
	}
	repository.router.Reload(routes)
	return nil
}

func (repository liveDomainRepository) ensureDNS(ctx context.Context, hostname, hostID string) (bool, error) {
	if repository.cloudflare == nil {
		return false, nil
	}
	if hostID == "" {
		return repository.cloudflare.EnsureServiceHostname(ctx, hostname, repository.adminHostname)
	}
	host, err := repository.store.Host(ctx, hostID)
	if err != nil {
		return false, err
	}
	if host.PublicIPv4 == "" {
		return false, fmt.Errorf("host %s has no public IPv4 address", host.Name)
	}
	return repository.cloudflare.EnsureServiceHostnameAddress(ctx, hostname, host.PublicIPv4)
}
