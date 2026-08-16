package ingress

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

type backendStub struct {
	backend deployment.Backend
	present bool
	err     error
	ports   chan<- int
}

type previewBackendStub struct {
	backendStub
	previewIDs chan<- string
}

func (stub previewBackendStub) PreviewBackend(previewID string, targetPort int) (deployment.Backend, bool, error) {
	if stub.previewIDs != nil {
		stub.previewIDs <- previewID
	}
	if stub.ports != nil {
		stub.ports <- targetPort
	}
	return stub.backend, stub.present, stub.err
}

func (stub backendStub) ServiceBackend(_ string, targetPort int) (deployment.Backend, bool, error) {
	if stub.ports != nil {
		stub.ports <- targetPort
	}
	return stub.backend, stub.present, stub.err
}

func TestRouterDispatchesAdminAndRejectsHostSNIMismatch(t *testing.T) {
	router, err := New(Config{
		AdminHostname: "admin.example.com",
		AdminHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusNoContent)
		}),
		Backends: backendStub{},
	})
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("admin.example.com", "admin.example.com"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("admin status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("app.example.com", "other.example.com"))
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("mismatch status = %d", response.Code)
	}
}

func TestRouterDispatchesResourceHandlersAndPreservesIndependentRouteViews(t *testing.T) {
	telemetryPaths := make(chan string, 4)
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		ObjectStoreHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
		}),
		ServiceTelemetryHandler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			telemetryPaths <- request.URL.Path
			response.WriteHeader(http.StatusAccepted)
		}),
		Backends: backendStub{}, Traffic: trafficmetrics.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-a", TargetPort: 8080}})
	router.ReloadObjectStores([]string{"objects.example.com"})
	router.ReloadServiceTelemetry(map[string]ServiceTelemetryRoute{"errors.example.com": {}})
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-b", TargetPort: 8081}})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("objects.example.com", "objects.example.com"))
	if response.Code != http.StatusCreated {
		t.Fatalf("object store status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	telemetryRequest := tlsRequest("errors.example.com", "errors.example.com")
	telemetryRequest.Method = http.MethodPost
	telemetryRequest.URL.Path = "/api/1/envelope/"
	router.ServeHTTP(response, telemetryRequest)
	if response.Code != http.StatusAccepted {
		t.Fatalf("Sentry ingress status = %d", response.Code)
	}
	if path := <-telemetryPaths; path != "/api/1/envelope/" {
		t.Fatalf("Sentry upstream path = %q", path)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("errors.example.com", "errors.example.com"))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unreserved dedicated hostname path status = %d", response.Code)
	}
	if router.routes.Load().services["app.example.com"].ServiceID != "service-b" {
		t.Fatalf("service routes were lost: %#v", router.routes.Load().services)
	}

	// Resource route removal must release the hostname for later service use.
	// Resource routes have priority over service routes in ServeHTTP.
	router.ReloadServiceTelemetry(nil)
	router.Reload(map[string]Route{"errors.example.com": {ServiceID: "service-c", TargetPort: 8082}})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("errors.example.com", "errors.example.com"))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("released Sentry hostname status = %d", response.Code)
	}
}

func TestRouterSharesServiceHostnameWithTelemetryOnReservedPaths(t *testing.T) {
	telemetryPaths := make(chan string, 2)
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		ServiceTelemetryHandler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			telemetryPaths <- request.URL.Path
			response.WriteHeader(http.StatusAccepted)
		}),
		Backends: backendStub{}, Traffic: trafficmetrics.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-a", TargetPort: 8080}})
	router.ReloadServiceTelemetry(map[string]ServiceTelemetryRoute{
		"app.example.com": {BrowserTunnelPath: "/client-report"},
	})

	ingest := tlsRequest("app.example.com", "app.example.com")
	ingest.Method = http.MethodPost
	ingest.URL.Path = "/api/1/envelope/"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, ingest)
	if response.Code != http.StatusAccepted || <-telemetryPaths != "/api/1/envelope/" {
		t.Fatalf("shared Sentry ingest = %d", response.Code)
	}

	tunnel := tlsRequest("app.example.com", "app.example.com")
	tunnel.Method = http.MethodPost
	tunnel.URL.Path = "/client-report"
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tunnel)
	if response.Code != http.StatusAccepted || <-telemetryPaths != "/api/1/envelope/" {
		t.Fatalf("shared browser tunnel = %d", response.Code)
	}

	artifact := tlsRequest("app.example.com", "app.example.com")
	artifact.Method = http.MethodPost
	artifact.URL.Path = "/api/0/organizations/platformd/artifactbundle/assemble/"
	response = httptest.NewRecorder()
	router.ServeHTTP(response, artifact)
	if response.Code != http.StatusAccepted || <-telemetryPaths != artifact.URL.Path {
		t.Fatalf("shared Sentry artifact upload = %d", response.Code)
	}

	for _, path := range []string{"/", "/client-report/", "/api/0/users/me/", "/api/1/envelope", "/_sentry/api/1/envelope/"} {
		request := tlsRequest("app.example.com", "app.example.com")
		request.URL.Path = path
		if strings.Contains(path, "envelope") {
			request.Method = http.MethodPost
		}
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("application path %q status = %d", path, response.Code)
		}
	}
}

func TestRouterProxiesApplicationAndReplacesForwardingHeaders(t *testing.T) {
	received := make(chan *http.Request, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if string(body) != "request-body" {
			t.Errorf("proxied request body = %q", body)
		}
		received <- request.Clone(request.Context())
		_, _ = response.Write([]byte("proxied"))
	}))
	t.Cleanup(backend.Close)
	host, portText, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	requestedPorts := make(chan int, 1)
	traffic := trafficmetrics.NewRegistry()
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		Backends: backendStub{backend: deployment.Backend{Address: host, Port: port}, present: true, ports: requestedPorts},
		Traffic:  traffic,
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-a", TargetPort: port}})
	request := tlsRequest("app.example.com", "app.example.com")
	request.Method = http.MethodPost
	request.Body = io.NopCloser(strings.NewReader("request-body"))
	request.ContentLength = int64(len("request-body"))
	request.RemoteAddr = "192.0.2.7:1234"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("Forwarded", "for=attacker")
	request.Header.Set("X-Forwarded-For", "attacker")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "proxied" {
		t.Fatalf("proxy response = %d %q", response.Code, response.Body.String())
	}
	if requested := <-requestedPorts; requested != port {
		t.Fatalf("requested target port = %d, want %d", requested, port)
	}
	proxied := <-received
	if proxied.Host != "app.example.com" || proxied.Header.Get("X-Forwarded-For") != "203.0.113.9" || proxied.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("proxied headers = host %q, %#v", proxied.Host, proxied.Header)
	}
	if proxied.Header.Get("Forwarded") != "" {
		t.Fatalf("spoofed Forwarded header survived: %q", proxied.Header.Get("Forwarded"))
	}
	counters := traffic.Snapshot()["service-a"]
	if counters.IngressBytes != uint64(len("request-body")) || counters.EgressBytes != uint64(len("proxied")) {
		t.Fatalf("public HTTP counters = %+v", counters)
	}
	if counters.HTTPRequestsTotal != 1 || counters.HTTPActiveRequests != 0 || counters.HTTPResponses2xxTotal != 1 {
		t.Fatalf("public HTTP request metrics = %+v", counters)
	}
	var durationSamples uint64
	for _, count := range counters.HTTPDurationBuckets {
		durationSamples += count
	}
	if durationSamples != 1 {
		t.Fatalf("public HTTP duration samples = %d", durationSamples)
	}
}

func TestRouterProxiesWebSocketAndCountsBothDirections(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Errorf("accept backend WebSocket: %v", err)
			return
		}
		defer connection.CloseNow()
		messageType, payload, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read backend WebSocket: %v", err)
			return
		}
		if err := connection.Write(request.Context(), messageType, append([]byte("echo:"), payload...)); err != nil {
			t.Errorf("write backend WebSocket: %v", err)
		}
		<-release
	}))
	t.Cleanup(backend.Close)

	traffic := trafficmetrics.NewRegistry()
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		Backends: backendStub{backend: testBackend(t, backend), present: true}, Traffic: traffic,
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-a", TargetPort: 8080}})
	proxy := httptest.NewTLSServer(router)
	t.Cleanup(proxy.Close)

	connection, response, err := websocket.Dial(context.Background(), "wss://app.example.com/socket", &websocket.DialOptions{
		HTTPClient: testProxyClient(proxy),
	})
	if err != nil {
		if response != nil {
			t.Fatalf("dial WebSocket: %v (status %d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("WebSocket status = %d", response.StatusCode)
	}
	if err := connection.Write(context.Background(), websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	messageType, payload, err := connection.Read(context.Background())
	if err != nil || messageType != websocket.MessageText || string(payload) != "echo:hello" {
		t.Fatalf("WebSocket echo = %d %q, %v", messageType, payload, err)
	}
	counters := waitForWebSocketTraffic(t, traffic)
	if counters.HTTPActiveRequests != 1 || counters.HTTPActiveRequestsPeak != 1 ||
		counters.IngressBytes == 0 || counters.EgressBytes == 0 {
		t.Fatalf("live WebSocket counters = %+v", counters)
	}
	releaseOnce.Do(func() { close(release) })
	_ = connection.Close(websocket.StatusNormalClosure, "done")
	waitForHTTPRequests(t, traffic, 0)
	counters = traffic.Snapshot()["service-a"]
	if counters.HTTPRequestsTotal != 1 || durationSamples(counters) != 1 {
		t.Fatalf("final WebSocket counters = %+v", counters)
	}
}

func TestRouterFlushesSSEAndRecordsHeaderLatencyBeforeStreamCloses(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(response, "data: ready\n\n")
		if err := http.NewResponseController(response).Flush(); err != nil {
			t.Errorf("flush backend SSE: %v", err)
			return
		}
		<-release
		_, _ = io.WriteString(response, "data: done\n\n")
	}))
	t.Cleanup(backend.Close)

	traffic := trafficmetrics.NewRegistry()
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		Backends: backendStub{backend: testBackend(t, backend), present: true}, Traffic: traffic,
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-a", TargetPort: 8080}})
	proxy := httptest.NewTLSServer(router)
	t.Cleanup(proxy.Close)

	response, err := testProxyClient(proxy).Get("https://app.example.com/events")
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, len("data: ready\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "data: ready\n\n" {
		t.Fatalf("first SSE event = %q, %v", first, err)
	}
	counters := traffic.Snapshot()["service-a"]
	if counters.HTTPActiveRequests != 1 || counters.HTTPResponses2xxTotal != 1 || durationSamples(counters) != 1 {
		t.Fatalf("live SSE counters = %+v", counters)
	}
	releaseOnce.Do(func() { close(release) })
	rest, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(rest) != "data: done\n\n" {
		t.Fatalf("final SSE event = %q, %v", rest, err)
	}
	waitForHTTPRequests(t, traffic, 0)
}

func testBackend(t *testing.T, server *httptest.Server) deployment.Backend {
	t.Helper()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return deployment.Backend{Address: host, Port: port}
}

func testProxyClient(server *httptest.Server) *http.Client {
	transport := server.Client().Transport.(*http.Transport).Clone()
	address := server.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return &http.Client{Transport: transport}
}

func waitForHTTPRequests(t *testing.T, traffic *trafficmetrics.Registry, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if traffic.Snapshot()["service-a"].HTTPActiveRequests == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("active HTTP requests = %d, want %d", traffic.Snapshot()["service-a"].HTTPActiveRequests, want)
}

func waitForWebSocketTraffic(t *testing.T, traffic *trafficmetrics.Registry) trafficmetrics.Counters {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		counters := traffic.Snapshot()["service-a"]
		if counters.IngressBytes > 0 && counters.EgressBytes > 0 {
			return counters
		}
		time.Sleep(time.Millisecond)
	}
	return traffic.Snapshot()["service-a"]
}

func durationSamples(counters trafficmetrics.Counters) uint64 {
	var total uint64
	for _, count := range counters.HTTPDurationBuckets {
		total += count
	}
	return total
}

func TestRouterDispatchesPreviewRouteToPreviewBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(backend.Close)
	host, portText, err := net.SplitHostPort(backend.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	previewIDs := make(chan string, 1)
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		Backends: previewBackendStub{
			backendStub: backendStub{backend: deployment.Backend{Address: host, Port: port}, present: true},
			previewIDs:  previewIDs,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{
		"preview.example.com": {PreviewID: "preview-1", TargetPort: port},
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("preview.example.com", "preview.example.com"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("preview response status = %d", response.Code)
	}
	if previewID := <-previewIDs; previewID != "preview-1" {
		t.Fatalf("preview backend ID = %q", previewID)
	}
}

func TestRouterReturnsUnavailableWithoutPublishedBackend(t *testing.T) {
	router, err := New(Config{AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(), Backends: backendStub{}})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]Route{"app.example.com": {ServiceID: "service-a", TargetPort: 8080}})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("app.example.com", "app.example.com"))
	if response.Code != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d: %s", response.Code, body)
	}
}

func TestRouterRejectsExcessiveHeaderCount(t *testing.T) {
	router, err := New(Config{AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(), Backends: backendStub{}})
	if err != nil {
		t.Fatal(err)
	}
	request := tlsRequest("admin.example.com", "admin.example.com")
	for index := 0; index <= maximumHeaderCount; index++ {
		request.Header.Add("X-Many", strconv.Itoa(index))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status = %d", response.Code)
	}
}

func tlsRequest(host, sni string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://"+host+"/path", nil)
	request.Host = host
	request.TLS = &tls.ConnectionState{ServerName: sni}
	return request
}
