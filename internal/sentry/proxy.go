package sentry

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"sync"
)

const webhookEventsHeader = "X-Platformd-Telemetry-Events"

type targetContextKey struct{}

type proxyTarget struct {
	url               *url.URL
	cloudflareCountry string
	serviceID         string
	artifactAllowed   bool
}

type TargetResolver interface {
	Target(string) (*url.URL, bool)
}

type Proxy struct {
	resolver      TargetResolver
	authorize     func(context.Context, string, string) bool
	notifications func(string, []byte)
	proxy         *httputil.ReverseProxy
	transport     *http.Transport
}

func NewProxy(
	resolver TargetResolver,
	authorizeArtifact func(context.Context, string, string) bool,
	notifications func(string, []byte),
) (*Proxy, error) {
	if resolver == nil || authorizeArtifact == nil || notifications == nil {
		return nil, errors.New("Sentry proxy resolver is required")
	}
	transport := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 16,
		ReadBufferSize:      32 << 10,
		WriteBufferSize:     32 << 10,
	}
	handler := &Proxy{
		resolver: resolver, authorize: authorizeArtifact, notifications: notifications, transport: transport,
	}
	handler.proxy = &httputil.ReverseProxy{
		Transport: transport,
		BufferPool: proxyBuffers{pool: &sync.Pool{New: func() any {
			buffer := make([]byte, 64<<10)
			return &buffer
		}}},
		Rewrite: func(request *httputil.ProxyRequest) {
			target := request.In.Context().Value(targetContextKey{}).(proxyTarget)
			request.SetURL(target.url)
			request.Out.Host = target.url.Host
			if address := forwardedClientAddress(request.In); address != "" {
				request.Out.Header.Set("X-Forwarded-For", address)
			}
			request.Out.Header.Del("Cf-IpCountry")
			request.Out.Header.Del("X-Platformd-Service-Id")
			request.Out.Header.Del("X-Platformd-Artifact-Authorized")
			request.Out.Header.Set("X-Platformd-Service-Id", target.serviceID)
			if target.artifactAllowed {
				request.Out.Header.Set("X-Platformd-Artifact-Authorized", "1")
			}
			if target.cloudflareCountry != "" {
				request.Out.Header.Set("Cf-IpCountry", target.cloudflareCountry)
			}
		},
		ModifyResponse: func(response *http.Response) error {
			encoded := response.Header.Get(webhookEventsHeader)
			response.Header.Del(webhookEventsHeader)
			if encoded == "" || len(encoded) > 64<<10 {
				return nil
			}
			payload, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil || len(payload) > 48<<10 {
				return nil
			}
			target, ok := response.Request.Context().Value(targetContextKey{}).(proxyTarget)
			if ok {
				handler.notifications(target.serviceID, payload)
			}
			return nil
		},
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, _ error) {
			response.Header().Set("Cache-Control", "no-store")
			http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		},
	}
	return handler, nil
}

// The public ingress replaces X-Forwarded-For with a validated client address.
// Preserve that single address across this second, internal reverse-proxy hop.
func forwardedClientAddress(request *http.Request) string {
	if values := request.Header.Values("X-Forwarded-For"); len(values) == 1 {
		value := strings.TrimSpace(values[0])
		if address, err := netip.ParseAddr(value); err == nil {
			return address.Unmap().String()
		}
	}
	return directClientAddress(request)
}

func publicClientAddress(request *http.Request) string {
	if values := request.Header.Values("CF-Connecting-IP"); len(values) == 1 {
		value := strings.TrimSpace(values[0])
		if address, err := netip.ParseAddr(value); err == nil {
			return address.Unmap().String()
		}
	}
	return directClientAddress(request)
}

func directClientAddress(request *http.Request) string {
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

func (handler *Proxy) Serve(response http.ResponseWriter, request *http.Request, serviceID string) {
	handler.serve(response, request, serviceID, "", false)
}

func (handler *Proxy) ServePublic(response http.ResponseWriter, request *http.Request, serviceID string) {
	forwarded := request.Clone(request.Context())
	forwarded.Header = request.Header.Clone()
	forwarded.Header.Del("X-Forwarded-For")
	if address := publicClientAddress(request); address != "" {
		forwarded.Header.Set("X-Forwarded-For", address)
	}
	handler.serve(response, forwarded, serviceID, request.Header.Get("Cf-IpCountry"), true)
}

func (handler *Proxy) serve(
	response http.ResponseWriter,
	request *http.Request,
	serviceID string,
	cloudflareCountry string,
	requireArtifactToken bool,
) {
	target, ok := handler.resolver.Target(serviceID)
	if !ok {
		response.Header().Set("Cache-Control", "no-store")
		http.Error(response, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	artifactAllowed := false
	if artifactPath(request.URL.Path) {
		if requireArtifactToken {
			token, ok := bearerToken(request.Header)
			if !ok || !handler.authorize(request.Context(), serviceID, token) {
				response.Header().Set("Cache-Control", "no-store")
				http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
		}
		artifactAllowed = true
	}
	ctx := context.WithValue(request.Context(), targetContextKey{}, proxyTarget{
		url: target, cloudflareCountry: cloudflareCountry, serviceID: serviceID,
		artifactAllowed: artifactAllowed,
	})
	handler.proxy.ServeHTTP(response, request.WithContext(ctx))
}

func DataPlanePathAllowed(path string) bool {
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

func artifactPath(path string) bool {
	return strings.HasPrefix(path, "/api/0/organizations/") || strings.HasPrefix(path, "/api/0/projects/")
}

func bearerToken(headers http.Header) (string, bool) {
	values := headers.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != "" && !strings.ContainsAny(token, " \t\r\n")
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
