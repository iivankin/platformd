package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const shutdownTimeout = 120 * time.Second
const maximumHTTPSConnections = 4096

func serveListener(ctx context.Context, httpServer *http.Server, listener net.Listener, started func() error) error {
	return serveHTTPListeners(ctx, []httpListener{{server: httpServer, listener: listener}}, started)
}

type httpListener struct {
	server   *http.Server
	listener net.Listener
}

type httpServeResult struct {
	address string
	err     error
}

func serveHTTPListeners(ctx context.Context, listeners []httpListener, started func() error) error {
	results := make(chan httpServeResult, len(listeners))
	for _, endpoint := range listeners {
		go func(endpoint httpListener) {
			results <- httpServeResult{address: endpoint.server.Addr, err: endpoint.server.Serve(endpoint.listener)}
		}(endpoint)
	}

	var returnErr error
	if started != nil {
		if err := started(); err != nil {
			returnErr = err
		}
	}
	completed := 0
	if returnErr == nil {
		select {
		case result := <-results:
			completed++
			if !errors.Is(result.err, http.ErrServerClosed) {
				returnErr = fmt.Errorf("serve %s: %w", result.address, result.err)
			}
		case <-ctx.Done():
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	for _, endpoint := range listeners {
		if err := endpoint.server.Shutdown(shutdownContext); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("shutdown %s: %w", endpoint.server.Addr, err))
		}
	}
	for ; completed < len(listeners); completed++ {
		result := <-results
		if !errors.Is(result.err, http.ErrServerClosed) {
			returnErr = errors.Join(returnErr, fmt.Errorf("serve %s: %w", result.address, result.err))
		}
	}
	return returnErr
}
