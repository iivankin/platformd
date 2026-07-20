package daemon

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automationapi"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/mcp"
	"github.com/iivankin/platformd/internal/portforward"
)

type automationHandlerFactory struct {
	api           automationapi.Config
	mcp           mcp.Config
	authenticator *automationauth.Authenticator
	portForwards  *portforward.Application
	available     bool
}

func newAutomationHandlerFactory(
	apiConfig automationapi.Config,
	mcpConfig mcp.Config,
	authenticator *automationauth.Authenticator,
	portForwards *portforward.Application,
	available bool,
) (*automationHandlerFactory, error) {
	if authenticator == nil || portForwards == nil {
		return nil, errors.New("automation handler security dependencies are missing")
	}
	return &automationHandlerFactory{
		api: apiConfig, mcp: mcpConfig, authenticator: authenticator,
		portForwards: portForwards, available: available,
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
	forwardHandler, err := portforward.Handler(portforward.HandlerConfig{Application: factory.portForwards})
	if err != nil {
		return nil, err
	}
	protectedMux := http.NewServeMux()
	protectedMux.Handle("/mcp", mcpHandler)
	protectedMux.Handle("/", automationAPI)
	mux := http.NewServeMux()
	mux.Handle(portforward.EndpointPath, forwardHandler)
	mux.Handle("/", factory.authenticator.Protect(protectedMux))
	var handler http.Handler = mux
	if !factory.available {
		handler, err = newAvailabilityHandler(handler, false)
		if err != nil {
			return nil, err
		}
	}
	return handler, nil
}
