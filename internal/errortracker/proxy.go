package errortracker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

type targetContextKey struct{}

type TargetResolver interface {
	Target(string) (*url.URL, bool)
}

type Proxy struct {
	resolver  TargetResolver
	proxy     *httputil.ReverseProxy
	transport *http.Transport
}

func NewProxy(resolver TargetResolver) (*Proxy, error) {
	if resolver == nil {
		return nil, errors.New("error tracker proxy resolver is required")
	}
	transport := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 16,
		ReadBufferSize:      32 << 10,
		WriteBufferSize:     32 << 10,
	}
	handler := &Proxy{resolver: resolver, transport: transport}
	handler.proxy = &httputil.ReverseProxy{
		Transport: transport,
		BufferPool: proxyBuffers{pool: &sync.Pool{New: func() any {
			buffer := make([]byte, 64<<10)
			return &buffer
		}}},
		Rewrite: func(request *httputil.ProxyRequest) {
			target := request.In.Context().Value(targetContextKey{}).(*url.URL)
			request.SetURL(target)
			request.Out.Host = target.Host
		},
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			response.Header().Set("Cache-Control", "no-store")
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
	return handler, nil
}

func (handler *Proxy) Serve(response http.ResponseWriter, request *http.Request, trackerID string) {
	target, ok := handler.resolver.Target(trackerID)
	if !ok {
		response.Header().Set("Cache-Control", "no-store")
		http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	ctx := context.WithValue(request.Context(), targetContextKey{}, target)
	handler.proxy.ServeHTTP(response, request.WithContext(ctx))
}

func DataPlanePathAllowed(path string) bool {
	if path == "/public/mcp" {
		return true
	}
	if path == "/public/api/v1" || strings.HasPrefix(path, "/public/api/v1/") {
		return true
	}
	if strings.HasPrefix(path, "/api/0/organizations/") || strings.HasPrefix(path, "/api/0/projects/") {
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "api" || parts[1] == "" {
		return false
	}
	switch parts[2] {
	case "envelope", "store", "minidump", "apple-crash-report":
		return true
	default:
		return false
	}
}

type proxyBuffers struct{ pool *sync.Pool }

func (buffers proxyBuffers) Get() []byte { return *buffers.pool.Get().(*[]byte) }

func (buffers proxyBuffers) Put(buffer []byte) {
	if cap(buffer) != 64<<10 {
		return
	}
	buffer = buffer[:cap(buffer)]
	buffers.pool.Put(&buffer)
}
