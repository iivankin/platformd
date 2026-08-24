package ingress

import (
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iivankin/platformd/internal/analytics"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/sentry"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type BackendResolver interface {
	ServiceBackend(string, int) (deployment.Backend, bool, error)
}

type previewBackendResolver interface {
	PreviewBackend(string, int) (deployment.Backend, bool, error)
}

type Route struct {
	ServiceID  string
	PreviewID  string
	TargetPort int
}

type ServiceTelemetryRoute struct {
	BrowserTunnelPath string
	OTLPPathPrefix    string
}

type Config struct {
	AdminHostname           string
	AdminHandler            http.Handler
	ObjectStoreHandler      http.Handler
	ServiceTelemetryHandler http.Handler
	AnalyticsHandler        http.Handler
	Backends                BackendResolver
	Traffic                 *trafficmetrics.Registry
}

type routeSnapshot struct {
	services         map[string]Route
	objectStores     map[string]struct{}
	serviceTelemetry map[string]ServiceTelemetryRoute
}

type Router struct {
	adminHostname           string
	adminHandler            http.Handler
	objectStoreHandler      http.Handler
	serviceTelemetryHandler http.Handler
	analyticsHandler        http.Handler
	backends                BackendResolver
	reloadMu                sync.Mutex
	routes                  atomic.Pointer[routeSnapshot]
	transport               *http.Transport
	bufferPool              *proxyBufferPool
	traffic                 *trafficmetrics.Registry
}

const (
	maximumHeaderCount = 100
	proxyBufferBytes   = 32 << 10
)

func New(config Config) (*Router, error) {
	adminHostname, err := publichostname.Normalize(config.AdminHostname)
	if err != nil {
		return nil, err
	}
	if config.AdminHandler == nil || config.Backends == nil {
		return nil, errors.New("ingress requires admin handler and backend resolver")
	}
	router := &Router{
		adminHostname:           adminHostname,
		adminHandler:            config.AdminHandler,
		objectStoreHandler:      config.ObjectStoreHandler,
		serviceTelemetryHandler: config.ServiceTelemetryHandler,
		analyticsHandler:        config.AnalyticsHandler,
		backends:                config.Backends,
		bufferPool:              newProxyBufferPool(),
		traffic:                 config.Traffic,
		transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
	router.routes.Store(&routeSnapshot{services: map[string]Route{}, objectStores: map[string]struct{}{}, serviceTelemetry: map[string]ServiceTelemetryRoute{}})
	return router, nil
}

// Reload replaces the complete immutable route view in one atomic store. A
// request therefore sees either the old or new domain set, never a partial move.
func (router *Router) Reload(routes map[string]Route) {
	router.reloadMu.Lock()
	defer router.reloadMu.Unlock()
	cloned := make(map[string]Route, len(routes))
	for hostname, serviceID := range routes {
		cloned[hostname] = serviceID
	}
	current := router.routes.Load()
	router.routes.Store(&routeSnapshot{
		services: cloned, objectStores: cloneSet(current.objectStores), serviceTelemetry: cloneServiceTelemetryRoutes(current.serviceTelemetry),
	})
}

// ReloadObjectStores replaces only the S3 hostname view. Service routes remain
// unchanged, so independent resource mutations cannot accidentally erase them.
func (router *Router) ReloadObjectStores(hostnames []string) {
	router.reloadMu.Lock()
	defer router.reloadMu.Unlock()
	cloned := make(map[string]struct{}, len(hostnames))
	for _, hostname := range hostnames {
		cloned[hostname] = struct{}{}
	}
	current := router.routes.Load()
	router.routes.Store(&routeSnapshot{
		services: cloneMap(current.services), objectStores: cloned, serviceTelemetry: cloneServiceTelemetryRoutes(current.serviceTelemetry),
	})
}

func (router *Router) ReloadServiceTelemetry(routes map[string]ServiceTelemetryRoute) {
	router.reloadMu.Lock()
	defer router.reloadMu.Unlock()
	current := router.routes.Load()
	router.routes.Store(&routeSnapshot{
		services: cloneMap(current.services), objectStores: cloneSet(current.objectStores), serviceTelemetry: cloneServiceTelemetryRoutes(routes),
	})
}

func (router *Router) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if countHeaders(request.Header) > maximumHeaderCount {
		response.Header().Set("Cache-Control", "no-store")
		http.Error(response, http.StatusText(http.StatusRequestHeaderFieldsTooLarge), http.StatusRequestHeaderFieldsTooLarge)
		return
	}
	hostname, err := publichostname.NormalizeHostHeader(request.Host)
	if err != nil || request.TLS == nil {
		misdirected(response)
		return
	}
	sni, err := publichostname.Normalize(request.TLS.ServerName)
	if err != nil || sni != hostname {
		misdirected(response)
		return
	}
	if hostname == router.adminHostname {
		router.adminHandler.ServeHTTP(response, request)
		return
	}
	routes := router.routes.Load()
	if _, exists := routes.objectStores[hostname]; exists {
		if router.objectStoreHandler == nil {
			unavailable(response)
			return
		}
		router.objectStoreHandler.ServeHTTP(response, request)
		return
	}
	telemetryRoute, hasTelemetryRoute := routes.serviceTelemetry[hostname]
	if hasTelemetryRoute && telemetryRoute.OTLPPathPrefix != "" &&
		(request.Method == http.MethodPost || request.Method == http.MethodOptions) {
		if signalPath, ok := publicOTLPSignalPath(telemetryRoute.OTLPPathPrefix, request.URL.Path); ok {
			router.servePublicOTLP(response, request, signalPath)
			return
		}
	}
	if analytics.Reserved(request.Method, request.URL.Path) {
		if router.analyticsHandler == nil {
			http.NotFound(response, request)
			return
		}
		router.analyticsHandler.ServeHTTP(response, request)
		return
	}
	if hasTelemetryRoute {
		if upstreamPath, telemetryPath := sentry.PublicDataPlanePath(request.Method, request.URL.Path, telemetryRoute.BrowserTunnelPath); telemetryPath {
			if router.serviceTelemetryHandler == nil {
				unavailable(response)
				return
			}
			forwarded := request.Clone(request.Context())
			forwarded.URL.Path = upstreamPath
			forwarded.URL.RawPath = ""
			router.serviceTelemetryHandler.ServeHTTP(response, forwarded)
			return
		}
		if _, sharedWithService := routes.services[hostname]; !sharedWithService {
			http.NotFound(response, request)
			return
		}
	}
	serviceRoute, exists := routes.services[hostname]
	if !exists {
		misdirected(response)
		return
	}
	startedAt := time.Now()
	streaming := false
	observedResponse := &statusResponseWriter{ResponseWriter: response}
	router.traffic.StartHTTP(serviceRoute.ServiceID)
	defer func() {
		if streaming {
			router.traffic.FinishStreamingHTTP(serviceRoute.ServiceID)
			return
		}
		router.traffic.FinishHTTP(serviceRoute.ServiceID, observedResponse.StatusCode(), time.Since(startedAt))
	}()
	response = observedResponse
	var backend deployment.Backend
	var available bool
	if serviceRoute.PreviewID != "" {
		previews, ok := router.backends.(previewBackendResolver)
		if !ok {
			unavailable(response)
			return
		}
		backend, available, err = previews.PreviewBackend(serviceRoute.PreviewID, serviceRoute.TargetPort)
	} else {
		backend, available, err = router.backends.ServiceBackend(serviceRoute.ServiceID, serviceRoute.TargetPort)
	}
	if err != nil || !available {
		unavailable(response)
		return
	}
	if recorder, ok := router.analyticsHandler.(interface {
		ObserveDocument(*http.Request, string)
	}); ok {
		recorder.ObserveDocument(request, serviceRoute.ServiceID)
	}
	router.proxy(backend, hostname, serviceRoute.ServiceID, func(statusCode int) {
		streaming = true
		router.traffic.ObserveStreamingHTTP(serviceRoute.ServiceID, statusCode, time.Since(startedAt))
	}).ServeHTTP(response, request)
}

func publicOTLPSignalPath(prefix, path string) (string, bool) {
	for _, signalPath := range []string{"/v1/traces", "/v1/logs"} {
		if path == prefix+signalPath {
			return signalPath, true
		}
	}
	return "", false
}

func (router *Router) servePublicOTLP(response http.ResponseWriter, request *http.Request, signalPath string) {
	response.Header().Set("Access-Control-Allow-Origin", "*")
	response.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	response.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Encoding")
	response.Header().Set("Access-Control-Max-Age", "86400")
	if request.Method == http.MethodOptions {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if router.serviceTelemetryHandler == nil {
		unavailable(response)
		return
	}
	forwarded := request.Clone(request.Context())
	forwarded.URL.Path = signalPath
	forwarded.URL.RawPath = ""
	router.serviceTelemetryHandler.ServeHTTP(response, forwarded)
}

func cloneMap(input map[string]Route) map[string]Route {
	result := make(map[string]Route, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneSet(input map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(input))
	for key := range input {
		result[key] = struct{}{}
	}
	return result
}

func cloneServiceTelemetryRoutes(input map[string]ServiceTelemetryRoute) map[string]ServiceTelemetryRoute {
	result := make(map[string]ServiceTelemetryRoute, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func countHeaders(header http.Header) int {
	count := 0
	for _, values := range header {
		count += len(values)
	}
	return count
}

func (router *Router) proxy(backend deployment.Backend, publicHost, serviceID string, observeStreaming func(int)) http.Handler {
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(backend.Address, strconv.Itoa(backend.Port))}
	return &httputil.ReverseProxy{
		Transport:  router.transport,
		BufferPool: router.bufferPool,
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			if proxyRequest.Out.Body != nil && router.traffic != nil {
				proxyRequest.Out.Body = &countingReadCloser{
					Reader: trafficmetrics.CountReader(proxyRequest.Out.Body, func(bytes uint64) {
						router.traffic.AddIngress(serviceID, bytes)
					}),
					Closer: proxyRequest.Out.Body,
				}
			}
			proxyRequest.SetURL(target)
			proxyRequest.Out.Host = publicHost
			proxyRequest.Out.Header.Set("X-Forwarded-For", clientAddress(proxyRequest.In))
			proxyRequest.Out.Header.Set("X-Forwarded-Host", publicHost)
			proxyRequest.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		ModifyResponse: func(response *http.Response) error {
			if response.Body != nil && router.traffic != nil {
				addEgress := func(bytes uint64) { router.traffic.AddEgress(serviceID, bytes) }
				if upgraded, ok := response.Body.(io.ReadWriteCloser); ok && response.StatusCode == http.StatusSwitchingProtocols {
					response.Body = &countingReadWriteCloser{
						ReadWriteCloser: upgraded,
						addRead:         addEgress,
						addWrite:        func(bytes uint64) { router.traffic.AddIngress(serviceID, bytes) },
					}
				} else {
					response.Body = &countingReadCloser{
						Reader: trafficmetrics.CountReader(response.Body, addEgress),
						Closer: response.Body,
					}
				}
			}
			if observeStreaming != nil && isStreamingResponse(response) {
				observeStreaming(response.StatusCode)
			}
			return nil
		},
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			response.Header().Set("Cache-Control", "no-store")
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
}

type proxyBufferPool struct {
	pool sync.Pool
}

func newProxyBufferPool() *proxyBufferPool {
	buffers := &proxyBufferPool{}
	buffers.pool.New = func() any {
		return new([proxyBufferBytes]byte)
	}
	return buffers
}

func (buffers *proxyBufferPool) Get() []byte {
	return buffers.pool.Get().(*[proxyBufferBytes]byte)[:]
}

func (buffers *proxyBufferPool) Put(buffer []byte) {
	if cap(buffer) != proxyBufferBytes {
		return
	}
	buffer = buffer[:proxyBufferBytes]
	buffers.pool.Put((*[proxyBufferBytes]byte)(buffer))
}

type countingReadCloser struct {
	io.Reader
	io.Closer
}

type countingReadWriteCloser struct {
	io.ReadWriteCloser
	addRead  func(uint64)
	addWrite func(uint64)
}

func (stream *countingReadWriteCloser) Read(buffer []byte) (int, error) {
	count, err := stream.ReadWriteCloser.Read(buffer)
	if count > 0 {
		stream.addRead(uint64(count))
	}
	return count, err
}

func (stream *countingReadWriteCloser) Write(buffer []byte) (int, error) {
	count, err := stream.ReadWriteCloser.Write(buffer)
	if count > 0 {
		stream.addWrite(uint64(count))
	}
	return count, err
}

func isStreamingResponse(response *http.Response) bool {
	if response.StatusCode == http.StatusSwitchingProtocols {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	return err == nil && mediaType == "text/event-stream"
}

type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (writer *statusResponseWriter) WriteHeader(statusCode int) {
	if writer.statusCode != 0 {
		return
	}
	writer.statusCode = statusCode
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer *statusResponseWriter) Write(buffer []byte) (int, error) {
	if writer.statusCode == 0 {
		writer.statusCode = http.StatusOK
	}
	return writer.ResponseWriter.Write(buffer)
}

// Unwrap preserves optional interfaces used by ReverseProxy through
// http.ResponseController, including streaming flushes and upgrades.
func (writer *statusResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *statusResponseWriter) StatusCode() int {
	if writer.statusCode == 0 {
		return http.StatusOK
	}
	return writer.statusCode
}

func clientAddress(request *http.Request) string {
	if values := request.Header.Values("CF-Connecting-IP"); len(values) == 1 {
		value := strings.TrimSpace(values[0])
		if address, err := netip.ParseAddr(value); err == nil {
			return address.Unmap().String()
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return ""
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return ""
	}
	return address.Unmap().String()
}

func misdirected(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	http.Error(response, http.StatusText(http.StatusMisdirectedRequest), http.StatusMisdirectedRequest)
}

func unavailable(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
}
