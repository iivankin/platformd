package telemetry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/sentry"
)

type serviceGateway struct {
	sentryServer *http.Server
	otlpServer   *http.Server
	sentryDone   chan error
	otlpDone     chan error
	sentryProxy  *sentry.Proxy
	otlpProxy    *httputil.ReverseProxy
	mu           sync.RWMutex
	sentryRoutes map[string]string
	otlpRoutes   map[string]serviceRoute
}

type serviceRoute struct {
	serviceID string
}

func startServiceGateway(address netip.Addr, proxy *sentry.Proxy) (*serviceGateway, error) {
	if !address.IsValid() || proxy == nil {
		return nil, errors.New("service Sentry gateway configuration is incomplete")
	}
	target, err := url.Parse("http://" + OTLPHTTPAddress)
	if err != nil {
		return nil, err
	}
	gateway := &serviceGateway{
		sentryProxy: proxy, otlpProxy: httputil.NewSingleHostReverseProxy(target),
		sentryRoutes: make(map[string]string), otlpRoutes: make(map[string]serviceRoute),
		sentryDone: make(chan error, 1), otlpDone: make(chan error, 1),
	}
	// The receiver is process-local. Never let environment proxy settings turn
	// this trusted hop into an external request.
	gateway.otlpProxy.Transport = &http.Transport{Proxy: nil}
	gateway.sentryServer = &http.Server{
		Handler: gateway, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}
	sentryListener, err := listenServiceGateway(address, firewall.ServiceTelemetryPort)
	if err != nil {
		return nil, err
	}
	gateway.otlpServer = &http.Server{
		Handler: http.HandlerFunc(gateway.serveOTLP), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}
	otlpListener, err := listenServiceGateway(address, firewall.OTLPHTTPPort)
	if err != nil {
		_ = sentryListener.Close()
		return nil, err
	}
	go func() { gateway.sentryDone <- gateway.sentryServer.Serve(sentryListener) }()
	go func() { gateway.otlpDone <- gateway.otlpServer.Serve(otlpListener) }()
	return gateway, nil
}

func (gateway *serviceGateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	hostname := strings.ToLower(strings.TrimSuffix(request.Host, "."))
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = host
	}
	if hostname == "" || !sentry.DataPlanePathAllowed(request.URL.Path) {
		http.NotFound(response, request)
		return
	}
	gateway.mu.RLock()
	serviceID, exists := gateway.sentryRoutes[hostname]
	gateway.mu.RUnlock()
	if !exists {
		http.NotFound(response, request)
		return
	}
	gateway.sentryProxy.Serve(response, request, serviceID)
}

func (gateway *serviceGateway) serveOTLP(response http.ResponseWriter, request *http.Request) {
	hostname := strings.ToLower(strings.TrimSuffix(request.Host, "."))
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = host
	}
	if request.Method != http.MethodPost ||
		(request.URL.Path != "/v1/logs" && request.URL.Path != "/v1/metrics" && request.URL.Path != "/v1/traces") {
		http.NotFound(response, request)
		return
	}
	gateway.mu.RLock()
	route, exists := gateway.otlpRoutes[hostname]
	gateway.mu.RUnlock()
	if !exists {
		http.NotFound(response, request)
		return
	}
	request.Header.Del("X-Platformd-Service-Id")
	request.Header.Set("X-Platformd-Service-Id", route.serviceID)
	request.Body = http.MaxBytesReader(response, request.Body, 20<<20)
	gateway.otlpProxy.ServeHTTP(response, request)
}

func (gateway *serviceGateway) SetSentry(hostname, serviceID string) {
	gateway.mu.Lock()
	gateway.sentryRoutes[hostname] = serviceID
	gateway.mu.Unlock()
}

func (gateway *serviceGateway) SetOTLP(hostname, serviceID string) {
	gateway.mu.Lock()
	gateway.otlpRoutes[hostname] = serviceRoute{serviceID: serviceID}
	gateway.mu.Unlock()
}

func (gateway *serviceGateway) DeleteSentry(hostname string) {
	gateway.mu.Lock()
	delete(gateway.sentryRoutes, hostname)
	gateway.mu.Unlock()
}

func (gateway *serviceGateway) DeleteOTLP(hostname string) {
	gateway.mu.Lock()
	delete(gateway.otlpRoutes, hostname)
	gateway.mu.Unlock()
}

func (gateway *serviceGateway) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := errors.Join(gateway.sentryServer.Shutdown(ctx), gateway.otlpServer.Shutdown(ctx))
	if err != nil {
		err = errors.Join(err, gateway.sentryServer.Close(), gateway.otlpServer.Close())
	}
	sentryErr := <-gateway.sentryDone
	if errors.Is(sentryErr, http.ErrServerClosed) {
		sentryErr = nil
	}
	otlpErr := <-gateway.otlpDone
	if errors.Is(otlpErr, http.ErrServerClosed) {
		otlpErr = nil
	}
	return errors.Join(err, sentryErr, otlpErr)
}
