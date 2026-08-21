package telemetry

import (
	"context"
	"net/http"

	"github.com/iivankin/platformd/internal/sentry"
	"github.com/iivankin/platformd/internal/state"
)

type PublicRepository interface {
	ServiceBySentryHostname(context.Context, string) (state.ServiceDesired, error)
	ServiceByOTLPTraceHostname(context.Context, string) (state.ServiceDesired, error)
}

type PublicHandler struct {
	repository PublicRepository
	manager    *ServiceManager
}

func NewPublicHandler(repository PublicRepository, manager *ServiceManager) *PublicHandler {
	return &PublicHandler{repository: repository, manager: manager}
}

func (handler *PublicHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.repository == nil || handler.manager == nil {
		http.NotFound(response, request)
		return
	}
	hostname, err := requestHostname(request.Host)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/traces" {
		service, err := handler.repository.ServiceByOTLPTraceHostname(request.Context(), hostname)
		if err != nil {
			http.NotFound(response, request)
			return
		}
		handler.manager.ServePublicOTLPTraces(response, request, service.ID)
		return
	}
	if !sentry.DataPlanePathAllowed(request.URL.Path) {
		http.NotFound(response, request)
		return
	}
	service, err := handler.repository.ServiceBySentryHostname(request.Context(), hostname)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	handler.manager.ServePublic(response, request, service.ID)
}
