package hostagent

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/hostconn"
)

type Forwarder struct {
	parentURL string
	hostToken string
	client    *http.Client
	server    *http.Server
	mu        sync.Mutex
	listeners []net.Listener
}

type ForwarderOptions struct {
	ParentURL  string
	HostToken  string
	HTTPClient *http.Client
}

func NewForwarder(parentURL, hostToken string) (*Forwarder, error) {
	return NewForwarderWithOptions(ForwarderOptions{ParentURL: parentURL, HostToken: hostToken})
}

func NewForwarderWithOptions(options ForwarderOptions) (*Forwarder, error) {
	parentURL, err := parentHTTPURL(options.ParentURL, hostconn.OTLPPathPrefix)
	if err != nil {
		return nil, err
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	forwarder := &Forwarder{
		parentURL: parentURL,
		hostToken: options.HostToken,
		client:    client,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", forwarder.ingest)
	mux.HandleFunc("/v1/metrics", forwarder.ingest)
	mux.HandleFunc("/v1/traces", forwarder.ingest)
	forwarder.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	return forwarder, nil
}

func (forwarder *Forwarder) Listen(ctx context.Context, addresses ...string) error {
	if err := forwarder.Add(addresses...); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = forwarder.Close()
	}()
	return nil
}

func (forwarder *Forwarder) Add(addresses ...string) error {
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		forwarder.mu.Lock()
		forwarder.listeners = append(forwarder.listeners, listener)
		forwarder.mu.Unlock()
		go func(listener net.Listener) { _ = forwarder.server.Serve(listener) }(listener)
	}
	return nil
}

func (forwarder *Forwarder) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return forwarder.server.Shutdown(ctx)
}

func (forwarder *Forwarder) ingest(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if request.ContentLength > hostconn.MaximumOTLPRequestBytes {
		http.Error(response, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	switch request.URL.Path {
	case "/v1/logs", "/v1/metrics", "/v1/traces":
	default:
		http.NotFound(response, request)
		return
	}
	request.Body = http.MaxBytesReader(response, request.Body, hostconn.MaximumOTLPRequestBytes)
	upstream, err := http.NewRequestWithContext(
		request.Context(), http.MethodPost, forwarder.parentURL+request.URL.Path, request.Body,
	)
	if err != nil {
		http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	upstream.ContentLength = request.ContentLength
	upstream.Header.Set("Authorization", "Bearer "+forwarder.hostToken)
	copyHeader(upstream.Header, request.Header, "Content-Type")
	copyHeader(upstream.Header, request.Header, "Content-Encoding")
	parentResponse, err := forwarder.client.Do(upstream)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(response, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	defer parentResponse.Body.Close()
	response.Header().Set("Cache-Control", "no-store")
	copyHeader(response.Header(), parentResponse.Header, "Content-Type")
	copyHeader(response.Header(), parentResponse.Header, "Content-Encoding")
	response.WriteHeader(parentResponse.StatusCode)
	_, _ = io.Copy(response, io.LimitReader(parentResponse.Body, hostconn.MaximumOTLPResponseBytes))
}

func copyHeader(destination, source http.Header, name string) {
	if value := strings.TrimSpace(source.Get(name)); value != "" {
		destination.Set(name, value)
	}
}
