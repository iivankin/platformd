// Command gateway is a test-only bridge used by the SDK conformance workflow.
// Production traffic always uses platformd's authenticated service gateway.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/iivankin/platformd/internal/sentry"
	"github.com/iivankin/platformd/internal/telemetry"
)

func main() {
	listen := environment("PLATFORMD_CONFORMANCE_LISTEN", "127.0.0.1:18080")
	serviceID := environment("PLATFORMD_CONFORMANCE_SERVICE_ID", "tz4a98xxat96iws9zmbrgj3a")
	upstream, err := url.Parse(environment("PLATFORMD_CONFORMANCE_UPSTREAM", "http://127.0.0.1:18081"))
	if err != nil {
		log.Fatal(err)
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(request *httputil.ProxyRequest) {
		request.SetURL(upstream)
		request.Out.Host = upstream.Host
		request.Out.Header.Del("X-Platformd-Service-Id")
		request.Out.Header.Del("X-Platformd-Artifact-Authorized")
		request.Out.Header.Set("X-Platformd-Service-Id", serviceID)
		if strings.HasPrefix(request.In.URL.Path, "/errors/") {
			request.Out.URL.Path = "/internal/services/" + serviceID + strings.TrimPrefix(request.In.URL.Path, "/errors")
		}
		if upstreamPath, ok := sentry.PublicDataPlanePath(request.In.Method, request.In.URL.Path, ""); ok {
			request.Out.URL.Path = upstreamPath
		}
		if strings.HasPrefix(request.Out.URL.Path, "/api/0/organizations/") || strings.HasPrefix(request.Out.URL.Path, "/api/0/projects/") {
			request.Out.Header.Set("X-Platformd-Artifact-Authorized", "1")
		}
	}}
	handler := http.NewServeMux()
	handler.HandleFunc("GET /configuration", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(map[string]string{
			"serviceId": serviceID,
			"dsn":       publicDSN(environment("PLATFORMD_CONFORMANCE_CONTAINER_HOST", "host.docker.internal:18080"), serviceID),
			"hostDsn":   publicDSN(environment("PLATFORMD_CONFORMANCE_HOST", "127.0.0.1:18080"), serviceID),
		}); err != nil {
			log.Printf("encode conformance configuration: %v", err)
		}
	})
	handler.Handle("/", proxy)
	log.Printf("Sentry conformance gateway listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, handler))
}

func publicDSN(hostname, serviceID string) string {
	return telemetry.SentryDSN("http", hostname, serviceID)
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
