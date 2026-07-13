package daemon

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

type liveRegistrySettings struct {
	store        *state.Store
	runtime      *runtimeStack
	certificates *origin.Selector
	router       *ingress.Router
}

func (settings *liveRegistrySettings) RegistryHostname(ctx context.Context) (string, error) {
	installation, err := settings.store.Installation(ctx)
	if err != nil || installation.RegistryHostname == nil {
		return "", err
	}
	return *installation.RegistryHostname, nil
}

func (settings *liveRegistrySettings) SetRegistryHostname(ctx context.Context, input state.SetRegistryHostnameInput) (*string, error) {
	if input.Hostname != "" {
		hostname, err := publichostname.Normalize(input.Hostname)
		if err != nil {
			return nil, err
		}
		if !settings.certificates.Covers(hostname) {
			return nil, state.ErrCertificateCoverage
		}
		input.Hostname = hostname
	}
	hostname, err := settings.store.SetRegistryHostname(ctx, input)
	if err != nil {
		return nil, err
	}
	value := ""
	if hostname != nil {
		value = *hostname
	}
	settings.runtime.SetEmbeddedRegistryHost(value)
	if settings.router == nil {
		return hostname, errors.New("registry ingress router is not configured")
	}
	if err := settings.router.ReloadRegistry(value); err != nil {
		return hostname, err
	}
	return hostname, nil
}
