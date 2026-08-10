package errortracker

import (
	"context"
	"net/http"

	"github.com/iivankin/platformd/internal/state"
)

type PublicRepository interface {
	ErrorTrackerByHostname(context.Context, string) (state.ErrorTracker, error)
}

type PublicHandler struct {
	repository PublicRepository
	proxy      *Proxy
}

func NewPublicHandler(repository PublicRepository, proxy *Proxy) *PublicHandler {
	return &PublicHandler{repository: repository, proxy: proxy}
}

func (handler *PublicHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.repository == nil || handler.proxy == nil || !DataPlanePathAllowed(request.URL.Path) {
		http.NotFound(response, request)
		return
	}
	hostname, err := requestHostname(request.Host)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	tracker, err := handler.repository.ErrorTrackerByHostname(request.Context(), hostname)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	handler.proxy.Serve(response, request, tracker.ID)
}
