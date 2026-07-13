package ingress

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/iivankin/platformd/internal/deployment"
)

type backendStub struct {
	backend deployment.Backend
	present bool
	err     error
}

func (stub backendStub) ServiceBackend(string) (deployment.Backend, bool, error) {
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

func TestRouterDispatchesExactAutomationHostname(t *testing.T) {
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		AutomationHostname: "api.example.com",
		AutomationHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
		}),
		Backends: backendStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("api.example.com", "api.example.com"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("automation status = %d", response.Code)
	}
}

func TestRouterReloadsAutomationAtomicallyWithoutLosingOtherRoutes(t *testing.T) {
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		RegistryHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
		}),
		Backends: backendStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]string{"app.example.com": "service-a"})
	if err := router.ReloadRegistry("registry.example.com"); err != nil {
		t.Fatal(err)
	}
	first := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusAccepted)
	})
	if err := router.ReloadAutomation("api.example.com", first); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("api.example.com", "api.example.com"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("first automation status = %d", response.Code)
	}
	second := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	if err := router.ReloadAutomation("agents.example.com", second); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("api.example.com", "api.example.com"))
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("old automation status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("agents.example.com", "agents.example.com"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("new automation status = %d", response.Code)
	}
	if router.routes.Load().services["app.example.com"] != "service-a" || router.routes.Load().registryHostname != "registry.example.com" {
		t.Fatalf("automation reload lost routes: %+v", router.routes.Load())
	}
	if err := router.ReloadAutomation("", nil); err != nil {
		t.Fatal(err)
	}
}

func TestRouterDispatchesObjectStoreAndPreservesIndependentRouteViews(t *testing.T) {
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		ObjectStoreHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusCreated)
		}),
		Backends: backendStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]string{"app.example.com": "service-a"})
	router.ReloadObjectStores([]string{"objects.example.com"})
	router.Reload(map[string]string{"app.example.com": "service-b"})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("objects.example.com", "objects.example.com"))
	if response.Code != http.StatusCreated {
		t.Fatalf("object store status = %d", response.Code)
	}
	if router.routes.Load().services["app.example.com"] != "service-b" {
		t.Fatalf("service routes were lost: %#v", router.routes.Load().services)
	}
}

func TestRouterReloadsRegistryWithoutLosingOtherRoutes(t *testing.T) {
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		RegistryHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusAccepted)
		}),
		Backends: backendStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]string{"app.example.com": "service-a"})
	router.ReloadObjectStores([]string{"objects.example.com"})
	if err := router.ReloadRegistry("Registry.Example.com"); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("registry.example.com", "registry.example.com"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("registry status = %d", response.Code)
	}
	if router.routes.Load().services["app.example.com"] != "service-a" || len(router.routes.Load().objectStores) != 1 {
		t.Fatalf("registry reload lost routes: %+v", router.routes.Load())
	}
	if err := router.ReloadRegistry(""); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, tlsRequest("registry.example.com", "registry.example.com"))
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("disabled registry status = %d", response.Code)
	}
}

func TestRouterProxiesApplicationAndReplacesForwardingHeaders(t *testing.T) {
	received := make(chan *http.Request, 1)
	backend := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
	router, err := New(Config{
		AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(),
		Backends: backendStub{backend: deployment.Backend{Address: host, Port: port}, present: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]string{"app.example.com": "service-a"})
	request := tlsRequest("app.example.com", "app.example.com")
	request.RemoteAddr = "192.0.2.7:1234"
	request.Header.Set("CF-Connecting-IP", "203.0.113.9")
	request.Header.Set("Forwarded", "for=attacker")
	request.Header.Set("X-Forwarded-For", "attacker")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "proxied" {
		t.Fatalf("proxy response = %d %q", response.Code, response.Body.String())
	}
	proxied := <-received
	if proxied.Host != "app.example.com" || proxied.Header.Get("X-Forwarded-For") != "203.0.113.9" || proxied.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("proxied headers = host %q, %#v", proxied.Host, proxied.Header)
	}
	if proxied.Header.Get("Forwarded") != "" {
		t.Fatalf("spoofed Forwarded header survived: %q", proxied.Header.Get("Forwarded"))
	}
}

func TestRouterReturnsUnavailableWithoutPublishedBackend(t *testing.T) {
	router, err := New(Config{AdminHostname: "admin.example.com", AdminHandler: http.NotFoundHandler(), Backends: backendStub{}})
	if err != nil {
		t.Fatal(err)
	}
	router.Reload(map[string]string{"app.example.com": "service-a"})
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
