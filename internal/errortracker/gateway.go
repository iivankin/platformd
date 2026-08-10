package errortracker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/firewall"
)

type projectGateway struct {
	server *http.Server
	done   chan error
	proxy  *Proxy
	mu     sync.RWMutex
	routes map[string]string
}

func startProjectGateway(address netip.Addr, proxy *Proxy) (*projectGateway, error) {
	if !address.IsValid() || proxy == nil {
		return nil, errors.New("error tracker gateway configuration is incomplete")
	}
	gateway := &projectGateway{proxy: proxy, routes: make(map[string]string), done: make(chan error, 1)}
	gateway.server = &http.Server{
		Handler:           gateway,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(address.String(), strconv.Itoa(firewall.ErrorTrackerPort)))
	if err != nil {
		return nil, err
	}
	go func() { gateway.done <- gateway.server.Serve(listener) }()
	return gateway, nil
}

func (gateway *projectGateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	hostname, err := requestHostname(request.Host)
	if err != nil || !DataPlanePathAllowed(request.URL.Path) {
		http.NotFound(response, request)
		return
	}
	gateway.mu.RLock()
	trackerID, exists := gateway.routes[hostname]
	gateway.mu.RUnlock()
	if !exists {
		http.NotFound(response, request)
		return
	}
	gateway.proxy.Serve(response, request, trackerID)
}

func (gateway *projectGateway) Set(hostname, trackerID string) {
	gateway.mu.Lock()
	gateway.routes[hostname] = trackerID
	gateway.mu.Unlock()
}

func (gateway *projectGateway) Delete(hostname string) {
	gateway.mu.Lock()
	delete(gateway.routes, hostname)
	gateway.mu.Unlock()
}

func (gateway *projectGateway) Close() error {
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := gateway.server.Shutdown(shutdown)
	if err != nil {
		// Shutdown can return while Serve is still active after its context expires.
		// Force the listener closed before waiting for the serving goroutine.
		err = errors.Join(err, gateway.server.Close())
	}
	serveErr := <-gateway.done
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(err, serveErr)
}

func requestHostname(value string) (string, error) {
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else if strings.Contains(value, ":") {
		return "", err
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if value == "" {
		return "", errors.New("hostname is empty")
	}
	return value, nil
}
