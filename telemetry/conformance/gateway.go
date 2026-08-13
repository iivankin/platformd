// Command gateway is a test-only bridge used by the SDK conformance workflow.
// Production traffic always uses platformd's authenticated service gateway.
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
)

func main() {
	listen := environment("PLATFORMD_CONFORMANCE_LISTEN", "127.0.0.1:18080")
	serviceID := environment("PLATFORMD_CONFORMANCE_SERVICE_ID", "conformanceservice000001")
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
		if strings.HasPrefix(request.In.URL.Path, "/api/0/organizations/") || strings.HasPrefix(request.In.URL.Path, "/api/0/projects/") {
			request.Out.Header.Set("X-Platformd-Artifact-Authorized", "1")
		}
	}}
	log.Printf("Sentry conformance gateway listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, proxy))
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
