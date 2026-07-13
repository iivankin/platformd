package ingress

import (
	"errors"
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

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/publichostname"
)

type BackendResolver interface {
	ServiceBackend(string) (deployment.Backend, bool, error)
}

type Config struct {
	AdminHostname      string
	AdminHandler       http.Handler
	AutomationHostname string
	AutomationHandler  http.Handler
	RegistryHostname   string
	RegistryHandler    http.Handler
	ObjectStoreHandler http.Handler
	Backends           BackendResolver
}

type routeSnapshot struct {
	services           map[string]string
	objectStores       map[string]struct{}
	registryHostname   string
	automationHostname string
	automationHandler  http.Handler
}

type Router struct {
	adminHostname      string
	adminHandler       http.Handler
	registryHandler    http.Handler
	objectStoreHandler http.Handler
	backends           BackendResolver
	reloadMu           sync.Mutex
	routes             atomic.Pointer[routeSnapshot]
	transport          *http.Transport
}

const maximumHeaderCount = 100

func New(config Config) (*Router, error) {
	adminHostname, err := publichostname.Normalize(config.AdminHostname)
	if err != nil {
		return nil, err
	}
	if config.AdminHandler == nil || config.Backends == nil {
		return nil, errors.New("ingress requires admin handler and backend resolver")
	}
	var automationHostname string
	if config.AutomationHostname != "" || config.AutomationHandler != nil {
		if config.AutomationHostname == "" || config.AutomationHandler == nil {
			return nil, errors.New("automation hostname and handler must be configured together")
		}
		automationHostname, err = publichostname.Normalize(config.AutomationHostname)
		if err != nil {
			return nil, err
		}
		if automationHostname == adminHostname {
			return nil, errors.New("admin and automation hostnames must differ")
		}
	}
	var registryHostname string
	if config.RegistryHostname != "" {
		if config.RegistryHandler == nil {
			return nil, errors.New("registry hostname requires registry handler")
		}
		registryHostname, err = publichostname.Normalize(config.RegistryHostname)
		if err != nil {
			return nil, err
		}
		if registryHostname == adminHostname || registryHostname == automationHostname {
			return nil, errors.New("registry hostname must differ from admin and automation hostnames")
		}
	}
	router := &Router{
		adminHostname:      adminHostname,
		adminHandler:       config.AdminHandler,
		registryHandler:    config.RegistryHandler,
		objectStoreHandler: config.ObjectStoreHandler,
		backends:           config.Backends,
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
	router.routes.Store(&routeSnapshot{
		services: map[string]string{}, objectStores: map[string]struct{}{}, registryHostname: registryHostname,
		automationHostname: automationHostname, automationHandler: config.AutomationHandler,
	})
	return router, nil
}

// Reload replaces the complete immutable route view in one atomic store. A
// request therefore sees either the old or new domain set, never a partial move.
func (router *Router) Reload(routes map[string]string) {
	router.reloadMu.Lock()
	defer router.reloadMu.Unlock()
	cloned := make(map[string]string, len(routes))
	for hostname, serviceID := range routes {
		cloned[hostname] = serviceID
	}
	current := router.routes.Load()
	router.routes.Store(&routeSnapshot{
		services: cloned, objectStores: cloneSet(current.objectStores), registryHostname: current.registryHostname,
		automationHostname: current.automationHostname, automationHandler: current.automationHandler,
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
		services: cloneMap(current.services), objectStores: cloned, registryHostname: current.registryHostname,
		automationHostname: current.automationHostname, automationHandler: current.automationHandler,
	})
}

func (router *Router) ReloadRegistry(hostname string) error {
	normalized := ""
	if hostname != "" {
		if router.registryHandler == nil {
			return errors.New("registry handler is not configured")
		}
		var err error
		normalized, err = publichostname.Normalize(hostname)
		if err != nil {
			return err
		}
		if normalized == router.adminHostname {
			return errors.New("registry hostname conflicts with a control-plane hostname")
		}
	}
	router.reloadMu.Lock()
	defer router.reloadMu.Unlock()
	current := router.routes.Load()
	if normalized != "" && normalized == current.automationHostname {
		return errors.New("registry hostname conflicts with a control-plane hostname")
	}
	router.routes.Store(&routeSnapshot{
		services: cloneMap(current.services), objectStores: cloneSet(current.objectStores), registryHostname: normalized,
		automationHostname: current.automationHostname, automationHandler: current.automationHandler,
	})
	return nil
}

// ReloadAutomation atomically publishes the automation hostname and the
// hostname-specific API/MCP handler, so a request can never observe a new
// hostname with the old handler configuration.
func (router *Router) ReloadAutomation(hostname string, handler http.Handler) error {
	normalized := ""
	if hostname != "" || handler != nil {
		if hostname == "" || handler == nil {
			return errors.New("automation hostname and handler must be configured together")
		}
		var err error
		normalized, err = publichostname.Normalize(hostname)
		if err != nil {
			return err
		}
		if normalized == router.adminHostname {
			return errors.New("admin and automation hostnames must differ")
		}
	}
	router.reloadMu.Lock()
	defer router.reloadMu.Unlock()
	current := router.routes.Load()
	if normalized != "" && normalized == current.registryHostname {
		return errors.New("automation and registry hostnames must differ")
	}
	router.routes.Store(&routeSnapshot{
		services: cloneMap(current.services), objectStores: cloneSet(current.objectStores),
		registryHostname: current.registryHostname, automationHostname: normalized, automationHandler: handler,
	})
	return nil
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
	if hostname == routes.automationHostname {
		routes.automationHandler.ServeHTTP(response, request)
		return
	}
	if hostname == routes.registryHostname {
		if router.registryHandler == nil {
			unavailable(response)
			return
		}
		router.registryHandler.ServeHTTP(response, request)
		return
	}
	if _, exists := routes.objectStores[hostname]; exists {
		if router.objectStoreHandler == nil {
			unavailable(response)
			return
		}
		router.objectStoreHandler.ServeHTTP(response, request)
		return
	}
	serviceID, exists := routes.services[hostname]
	if !exists {
		misdirected(response)
		return
	}
	backend, available, err := router.backends.ServiceBackend(serviceID)
	if err != nil || !available {
		unavailable(response)
		return
	}
	router.proxy(backend, hostname).ServeHTTP(response, request)
}

func cloneMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
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

func countHeaders(header http.Header) int {
	count := 0
	for _, values := range header {
		count += len(values)
	}
	return count
}

func (router *Router) proxy(backend deployment.Backend, publicHost string) http.Handler {
	target := &url.URL{Scheme: "http", Host: net.JoinHostPort(backend.Address, strconv.Itoa(backend.Port))}
	return &httputil.ReverseProxy{
		Transport:     router.transport,
		FlushInterval: -1,
		Rewrite: func(proxyRequest *httputil.ProxyRequest) {
			proxyRequest.SetURL(target)
			proxyRequest.Out.Host = publicHost
			proxyRequest.Out.Header.Set("X-Forwarded-For", clientAddress(proxyRequest.In))
			proxyRequest.Out.Header.Set("X-Forwarded-Host", publicHost)
			proxyRequest.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			response.Header().Set("Cache-Control", "no-store")
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
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
