package daemon

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automationapi"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/mcp"
)

type automationHandlerFactory struct {
	api           automationapi.Config
	mcp           mcp.Config
	authenticator *automationauth.Authenticator
	available     bool
}

func newAutomationHandlerFactory(
	apiConfig automationapi.Config,
	mcpConfig mcp.Config,
	authenticator *automationauth.Authenticator,
	available bool,
) (*automationHandlerFactory, error) {
	if authenticator == nil {
		return nil, errors.New("automation handler authenticator is missing")
	}
	return &automationHandlerFactory{
		api: apiConfig, mcp: mcpConfig, authenticator: authenticator, available: available,
	}, nil
}

func (factory *automationHandlerFactory) Build(hostname string) (http.Handler, error) {
	if hostname == "" {
		return nil, nil
	}
	apiConfig := factory.api
	apiConfig.Hostname = hostname
	automationAPI, err := automationapi.Handler(apiConfig)
	if err != nil {
		return nil, err
	}
	mcpConfig := factory.mcp
	mcpConfig.Hostname = hostname
	mcpHandler, err := mcp.New(mcpConfig)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/", automationAPI)
	var handler http.Handler = factory.authenticator.Protect(mux)
	if !factory.available {
		handler, err = newAvailabilityHandler(handler, false)
		if err != nil {
			return nil, err
		}
	}
	return handler, nil
}
