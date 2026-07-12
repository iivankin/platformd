package access

import (
	"context"
	"net"
	"net/http"
	"strings"
)

const assertionHeader = "Cf-Access-Jwt-Assertion"

type TokenVerifier interface {
	Verify(context.Context, string) (Identity, error)
}

type identityContextKey struct{}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}

// ProtectAdmin enforces the exact public host/SNI boundary before Access auth.
// The loopback health exception is intentionally narrow so init can verify the
// configured TLS route without obtaining a browser Access token.
func ProtectAdmin(hostname string, verifier TokenVerifier, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if requestHostname(request.Host) != hostname || request.TLS == nil || strings.ToLower(request.TLS.ServerName) != hostname {
			writeDenied(response, http.StatusMisdirectedRequest)
			return
		}
		if isLoopbackHealth(request) {
			next.ServeHTTP(response, request)
			return
		}
		assertions := request.Header.Values(assertionHeader)
		if len(assertions) != 1 || assertions[0] == "" {
			writeDenied(response, http.StatusForbidden)
			return
		}
		identity, err := verifier.Verify(request.Context(), assertions[0])
		if err != nil {
			writeDenied(response, http.StatusForbidden)
			return
		}
		if mutationMethod(request.Method) && request.Header.Get("Origin") != "https://"+hostname {
			writeDenied(response, http.StatusForbidden)
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), identityContextKey{}, identity)))
	})
}

func requestHostname(value string) string {
	host := strings.ToLower(value)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	if strings.HasSuffix(host, ".") {
		return ""
	}
	return host
}

func isLoopbackHealth(request *http.Request) bool {
	if request.Method != http.MethodGet || request.URL.Path != "/healthz" {
		return false
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return false
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func mutationMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

func writeDenied(response http.ResponseWriter, status int) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.Error(response, http.StatusText(status), status)
}
