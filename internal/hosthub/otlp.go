package hosthub

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/hosttoken"
)

func (hub *Hub) OTLPHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		token, ok := bearerHostToken(request.Header.Values("Authorization"))
		if !ok {
			response.Header().Set("WWW-Authenticate", `Bearer realm="platformd-host"`)
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		hostID, secret, err := hosttoken.ParseHost(token)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		hub.mu.Lock()
		current := hub.sessions[hostID]
		hub.mu.Unlock()
		if current == nil || !hosttoken.Verify("host", hostID, secret, current.tokenHMAC) {
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		path, ok := collectorOTLPPath(request.URL.Path)
		if !ok {
			http.NotFound(response, request)
			return
		}
		contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || contentType != "application/x-protobuf" {
			http.Error(response, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
			return
		}
		if request.ContentLength > hostconn.MaximumOTLPRequestBytes {
			http.Error(response, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, hostconn.MaximumOTLPRequestBytes)
		upstream, err := http.NewRequestWithContext(
			request.Context(), http.MethodPost, hub.otlpBaseURL+path, request.Body,
		)
		if err != nil {
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		upstream.ContentLength = request.ContentLength
		upstream.Header.Set("Content-Type", request.Header.Get("Content-Type"))
		if encoding := strings.TrimSpace(request.Header.Get("Content-Encoding")); encoding != "" {
			upstream.Header.Set("Content-Encoding", encoding)
		}
		collectorResponse, err := hub.otlpClient.Do(upstream)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(response, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
			return
		}
		defer collectorResponse.Body.Close()
		if contentType := collectorResponse.Header.Get("Content-Type"); contentType != "" {
			response.Header().Set("Content-Type", contentType)
		}
		if encoding := collectorResponse.Header.Get("Content-Encoding"); encoding != "" {
			response.Header().Set("Content-Encoding", encoding)
		}
		response.WriteHeader(collectorResponse.StatusCode)
		_, _ = io.Copy(response, io.LimitReader(collectorResponse.Body, hostconn.MaximumOTLPResponseBytes))
	})
}

func collectorOTLPPath(path string) (string, bool) {
	path = strings.TrimPrefix(path, hostconn.OTLPPathPrefix)
	switch path {
	case "/v1/logs", "/v1/metrics", "/v1/traces":
		return path, true
	default:
		return "", false
	}
}
