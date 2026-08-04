package daemon

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/automationapi"
	"github.com/iivankin/platformd/internal/automationauth"
	"github.com/iivankin/platformd/internal/mcp"
	"github.com/iivankin/platformd/internal/portforward"
)

type publicHandlerFactory struct {
	api           automationapi.Config
	mcp           mcp.Config
	authenticator *automationauth.Authenticator
	portForwards  *portforward.Application
	imageUpload   http.Handler
	available     bool
}

func newPublicHandlerFactory(
	apiConfig automationapi.Config,
	mcpConfig mcp.Config,
	authenticator *automationauth.Authenticator,
	portForwards *portforward.Application,
	imageUpload http.Handler,
	available bool,
) (*publicHandlerFactory, error) {
	if authenticator == nil || portForwards == nil || imageUpload == nil {
		return nil, errors.New("public handler security dependencies are missing")
	}
	return &publicHandlerFactory{
		api: apiConfig, mcp: mcpConfig, authenticator: authenticator,
		portForwards: portForwards, imageUpload: imageUpload, available: available,
	}, nil
}

func (factory *publicHandlerFactory) Build(hostname string) (http.Handler, error) {
	if hostname == "" {
		return nil, nil
	}
	apiConfig := factory.api
	apiConfig.Hostname = hostname
	publicAPI, err := automationapi.Handler(apiConfig)
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
	createForward, err := automationapi.CreatePortForwardHandler(automationapi.PortForwardCreateConfig{
		Hostname: hostname, Application: factory.portForwards, Authenticator: factory.authenticator,
	})
	if err != nil {
		return nil, err
	}
	protectedMux := http.NewServeMux()
	protectedMux.Handle("/public/mcp", mcpHandler)
	protectedMux.Handle("/", publicAPI)
	var handler http.Handler = publicHandler(
		factory.imageUpload,
		forwardHandler,
		createForward,
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

func publicHandler(imageUpload, forward, createForward, protected http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /public/api/v1/projects/{projectID}/services/{serviceID}/image", imageUpload)
	mux.Handle("POST /public/api/v1/projects/{projectID}/services/{serviceID}/image", imageUpload)
	mux.Handle("POST /public/api/v1/projects/{projectName}/resources/{resourceName}/port-forwards", createForward)
	mux.Handle(portforward.EndpointPath, forward)
	mux.Handle("/", protected)
	return mux
}

func adminHostnameHandler(admin, public http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/public/", public)
	mux.Handle("/", admin)
	return mux
}
