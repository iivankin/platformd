package daemon

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automationapi"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/mcp"
	"github.com/iivankin/platformd/internal/portforward"
	"github.com/iivankin/platformd/internal/server"
)

type automationHandlerFactory struct {
	api           automationapi.Config
	mcp           mcp.Config
	authenticator *automationauth.Authenticator
	portForwards  *portforward.Application
	githubWebhook http.Handler
	available     bool
}

func newAutomationHandlerFactory(
	apiConfig automationapi.Config,
	mcpConfig mcp.Config,
	authenticator *automationauth.Authenticator,
	portForwards *portforward.Application,
	githubWebhook http.Handler,
	available bool,
) (*automationHandlerFactory, error) {
	if authenticator == nil || portForwards == nil || githubWebhook == nil {
		return nil, errors.New("automation handler security dependencies are missing")
	}
	return &automationHandlerFactory{
		api: apiConfig, mcp: mcpConfig, authenticator: authenticator,
		portForwards: portForwards, githubWebhook: githubWebhook, available: available,
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
	var handler http.Handler = automationHandler(
		factory.githubWebhook,
		forwardHandler,
		factory.authenticator.Protect(protectedMux),
	)
	if !factory.available {
		handler, err = newAvailabilityHandler(handler, false)
		if err != nil {
			return nil, err
		}
	}
	return handler, nil
}

func automationHandler(githubWebhook, forward, protected http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+server.GitHubWebhookPath, githubWebhook)
	mux.Handle(portforward.EndpointPath, forward)
	mux.Handle("/", protected)
	return mux
}
