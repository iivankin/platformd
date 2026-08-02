package daemon

import (
	"context"
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/state"
)

type objectStoreProxyRouteKey struct{}

type objectStoreProxyRoute struct {
	target  *url.URL
	storeID string
}

const objectStorePublicStoreHeader = "X-Platformd-Public-Store"

type objectStoreProxy struct {
	stores interface {
		ObjectStoreByHostname(context.Context, string) (state.ObjectStore, error)
	}
	runtime *runtimeStack
	proxy   *httputil.ReverseProxy
}

func newObjectStoreProxy(stores interface {
	ObjectStoreByHostname(context.Context, string) (state.ObjectStore, error)
}, runtime *runtimeStack) (*objectStoreProxy, error) {
	if stores == nil || runtime == nil {
		return nil, errors.New("object store proxy dependencies are incomplete")
	}
	handler := &objectStoreProxy{stores: stores, runtime: runtime}
	handler.proxy = &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			route := request.In.Context().Value(objectStoreProxyRouteKey{}).(objectStoreProxyRoute)
			request.Out.URL.Scheme = route.target.Scheme
			request.Out.URL.Host = route.target.Host
			// SigV4 signs Host, path and query. Only the transport destination is
			// changed; Rust verifies the original client request unchanged.
			request.Out.Host = request.In.Host
			// The public hostname selects exactly one logical store. Always replace
			// a client-supplied value so credentials for another store in the same
			// project cannot escape that routing boundary.
			request.Out.Header.Set(objectStorePublicStoreHeader, route.storeID)
		},
		Transport: &http.Transport{
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 256,
			ReadBufferSize:      64 << 10,
			WriteBufferSize:     64 << 10,
		},
		BufferPool: pooledProxyBuffers{pool: &sync.Pool{New: func() any {
			buffer := make([]byte, 128<<10)
			return &buffer
		}}},
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			writeProxyS3Error(response, http.StatusBadGateway, "InternalError", "S3 data plane is unavailable")
		},
	}
	return handler, nil
}

func (handler *objectStoreProxy) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	hostname, err := proxyHostname(request.Host)
	if err != nil {
		writeProxyS3Error(response, http.StatusBadRequest, "InvalidRequest", "Host header is invalid")
		return
	}
	store, err := handler.stores.ObjectStoreByHostname(request.Context(), hostname)
	if err != nil {
		writeProxyS3Error(response, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	}
	target, err := handler.runtime.objectStoreProxyTarget(store.ProjectID)
	if err != nil {
		writeProxyS3Error(response, http.StatusServiceUnavailable, "ServiceUnavailable", "S3 data plane is not ready")
		return
	}
	ctx := context.WithValue(request.Context(), objectStoreProxyRouteKey{}, objectStoreProxyRoute{
		target: target, storeID: store.ID,
	})
	handler.proxy.ServeHTTP(response, request.WithContext(ctx))
}

func (stack *runtimeStack) objectStoreProxyTarget(projectID string) (*url.URL, error) {
	stack.mu.Lock()
	network, exists := stack.projectNetworks[projectID]
	running := stack.objectStoreProjects[projectID]
	closed := stack.closed
	stack.mu.Unlock()
	if closed || !exists || !running {
		return nil, errors.New("project S3 endpoint is unavailable")
	}
	gateway, err := netip.ParseAddr(network.Gateway)
	if err != nil {
		return nil, err
	}
	return &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(gateway.String(), strconv.Itoa(firewall.ObjectStorePort)),
	}, nil
}

func proxyHostname(value string) (string, error) {
	if hostname, _, err := net.SplitHostPort(value); err == nil {
		value = hostname
	} else if strings.Contains(value, ":") {
		return "", err
	}
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if value == "" {
		return "", errors.New("hostname is empty")
	}
	return value, nil
}

type pooledProxyBuffers struct {
	pool *sync.Pool
}

func (buffers pooledProxyBuffers) Get() []byte {
	return *buffers.pool.Get().(*[]byte)
}

func (buffers pooledProxyBuffers) Put(buffer []byte) {
	if cap(buffer) != 128<<10 {
		return
	}
	buffer = buffer[:cap(buffer)]
	buffers.pool.Put(&buffer)
}

func writeProxyS3Error(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/xml")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(status)
	_ = xml.NewEncoder(response).Encode(struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}{Code: code, Message: message})
}
