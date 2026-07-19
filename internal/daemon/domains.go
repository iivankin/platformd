package daemon

import (
	"context"
	"sync"

	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

type liveDomainRepository struct {
	store        *state.Store
	certificates *origin.Selector
	router       *ingress.Router
	publicMu     *sync.Mutex
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
	if err := repository.validatePreviewDomainAttach(ctx, input); err != nil {
		return state.ServiceDomain{}, err
	}
	domain, err := repository.store.AttachServiceDomain(ctx, input)
	if err != nil {
		return state.ServiceDomain{}, err
	}
	if err := repository.reload(ctx); err != nil {
		return state.ServiceDomain{}, err
	}
	return domain, nil
}

func (repository liveDomainRepository) DetachServiceDomain(ctx context.Context, input state.DetachServiceDomainInput) error {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	if err := repository.validatePreviewDomainDetach(ctx, input); err != nil {
		return err
	}
	if err := repository.store.DetachServiceDomain(ctx, input); err != nil {
		return err
	}
	return repository.reload(ctx)
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
		routes[domain.Hostname] = ingress.Route{ServiceID: domain.ServiceID, TargetPort: domain.TargetPort}
	}
	for _, preview := range previews {
		routes[preview.Hostname] = ingress.Route{ServiceID: preview.ServiceID, PreviewID: preview.ID, TargetPort: preview.TargetPort}
	}
	repository.router.Reload(routes)
	return nil
}

func (repository liveDomainRepository) validatePreviewDomainAttach(ctx context.Context, input state.AttachServiceDomainInput) error {
	service, err := repository.store.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return err
	}
	if service.Snapshot.Source.GitHub == nil || service.Snapshot.Source.GitHub.PullRequestPreview == nil {
		return nil
	}
	domains, err := repository.store.ServiceDomains(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return err
	}
	for _, domain := range domains {
		if domain.Hostname == input.Hostname {
			return nil
		}
	}
	return state.ErrPreviewDomainCount
}

func (repository liveDomainRepository) validatePreviewDomainDetach(ctx context.Context, input state.DetachServiceDomainInput) error {
	service, err := repository.store.Service(ctx, input.ProjectID, input.ServiceID)
	if err != nil {
		return err
	}
	if service.Snapshot.Source.GitHub != nil && service.Snapshot.Source.GitHub.PullRequestPreview != nil {
		return state.ErrPreviewDomainCount
	}
	return nil
}
