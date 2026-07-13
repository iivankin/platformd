package daemon

import (
	"errors"
	"net/http"
)

type availabilityHandler struct {
	target  http.Handler
	enabled bool
}

func newAvailabilityHandler(target http.Handler, enabled bool) (*availabilityHandler, error) {
	if target == nil {
		return nil, errors.New("availability handler target is required")
	}
	return &availabilityHandler{target: target, enabled: enabled}, nil
}

func (handler *availabilityHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !handler.enabled {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		response.Header().Set("Retry-After", "60")
		http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	handler.target.ServeHTTP(response, request)
}
