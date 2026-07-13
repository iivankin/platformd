package objectstore

import (
	"net/http"
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

func (handler *HTTPHandler) preflight(response http.ResponseWriter, request *http.Request, store state.ObjectStore, requestID string) {
	origin := request.Header.Get("Origin")
	method := request.Header.Get("Access-Control-Request-Method")
	if !allowedOrigin(store.CORSOrigins, origin) || (method != http.MethodGet && method != http.MethodHead && method != http.MethodPut) || !allowedCORSHeaders(request.Header.Get("Access-Control-Request-Headers")) {
		writeS3Error(response, http.StatusForbidden, "AccessDenied", "CORS preflight is not allowed", requestID)
		return
	}
	applyCORS(response, request, store)
	response.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT")
	if requested := request.Header.Get("Access-Control-Request-Headers"); requested != "" {
		response.Header().Set("Access-Control-Allow-Headers", requested)
	}
	response.Header().Set("Access-Control-Max-Age", "600")
	response.Header().Add("Vary", "Access-Control-Request-Method")
	response.Header().Add("Vary", "Access-Control-Request-Headers")
	response.WriteHeader(http.StatusNoContent)
}

func applyCORS(response http.ResponseWriter, request *http.Request, store state.ObjectStore) {
	origin := request.Header.Get("Origin")
	if !allowedOrigin(store.CORSOrigins, origin) {
		return
	}
	response.Header().Set("Access-Control-Allow-Origin", origin)
	response.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Length, Content-Range, ETag, Last-Modified, X-Amz-Request-Id")
	response.Header().Add("Vary", "Origin")
}

func allowedOrigin(origins []string, candidate string) bool {
	for _, origin := range origins {
		if candidate == origin {
			return true
		}
	}
	return false
}

func allowedCORSHeaders(value string) bool {
	if value == "" {
		return true
	}
	for _, header := range strings.Split(value, ",") {
		header = strings.ToLower(strings.TrimSpace(header))
		if header == "" {
			return false
		}
		switch header {
		case "authorization", "content-type", "range", "x-amz-content-sha256", "x-amz-date", "x-amz-security-token", "x-amz-user-agent":
		default:
			if !strings.HasPrefix(header, "x-amz-meta-") {
				return false
			}
		}
	}
	return true
}
