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
	Backends           BackendResolver
}

type routeSnapshot struct {
	services map[string]string
}

type Router struct {
	adminHostname      string
	adminHandler       http.Handler
	automationHostname string
	automationHandler  http.Handler
	backends           BackendResolver
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
	router := &Router{
		adminHostname:      adminHostname,
		adminHandler:       config.AdminHandler,
		automationHostname: automationHostname,
		automationHandler:  config.AutomationHandler,
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
	router.Reload(nil)
	return router, nil
}

// Reload replaces the complete immutable route view in one atomic store. A
// request therefore sees either the old or new domain set, never a partial move.
func (router *Router) Reload(routes map[string]string) {
	cloned := make(map[string]string, len(routes))
	for hostname, serviceID := range routes {
		cloned[hostname] = serviceID
	}
	router.routes.Store(&routeSnapshot{services: cloned})
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
	if hostname == router.automationHostname {
		router.automationHandler.ServeHTTP(response, request)
		return
	}
	serviceID, exists := router.routes.Load().services[hostname]
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
