package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

type liveDomainRepository struct {
	store        *state.Store
	certificates *origin.Selector
	router       *ingress.Router
}

func (repository liveDomainRepository) ServiceDomains(ctx context.Context, projectID, serviceID string) ([]state.ServiceDomain, error) {
	return repository.store.ServiceDomains(ctx, projectID, serviceID)
}

func (repository liveDomainRepository) AttachServiceDomain(ctx context.Context, input state.AttachServiceDomainInput) (state.ServiceDomain, error) {
	hostname, err := publichostname.Normalize(input.Hostname)
	if err != nil {
		return state.ServiceDomain{}, err
	}
	if !repository.certificates.Covers(hostname) {
		return state.ServiceDomain{}, state.ErrCertificateCoverage
	}
	input.Hostname = hostname
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
	routes := make(map[string]string, len(domains))
	for _, domain := range domains {
		routes[domain.Hostname] = domain.ServiceID
	}
	repository.router.Reload(routes)
	return nil
}
