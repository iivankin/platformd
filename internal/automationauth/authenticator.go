package automationauth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/apitoken"
	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type CredentialStore interface {
	APITokenCredential(context.Context, string) (state.APIToken, error)
}

type FailureLimiter interface {
	Permit(string, string) (bool, time.Duration)
	Failed(string, string)
	Succeeded(string, string)
}

type Config struct {
	Store    CredentialStore
	Verifier apitoken.Verifier
	Limiter  FailureLimiter
}

type Authenticator struct {
	store    CredentialStore
	verifier apitoken.Verifier
	limiter  FailureLimiter
}

func New(config Config) (*Authenticator, error) {
	if config.Store == nil || config.Limiter == nil {
		return nil, errors.New("automation authenticator dependencies are incomplete")
	}
	return &Authenticator{store: config.Store, verifier: config.Verifier, limiter: config.Limiter}, nil
}

func (authenticator *Authenticator) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
		identity, retryAfter, err := authenticator.authenticate(request)
		if retryAfter > 0 {
			seconds := max(1, int((retryAfter+time.Second-1)/time.Second))
			response.Header().Set("Retry-After", strconv.Itoa(seconds))
			http.Error(response, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
			return
		}
		if err != nil {
			response.Header().Set("WWW-Authenticate", `Bearer realm="platformd automation"`)
			http.Error(response, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(response, request.WithContext(automation.WithIdentity(request.Context(), identity)))
	})
}

func (authenticator *Authenticator) authenticate(request *http.Request) (automation.Identity, time.Duration, error) {
	source := sourceAddress(request)
	publicID, secret, parseErr := bearerCredential(request.Header.Values("Authorization"))
	if allowed, retryAfter := authenticator.limiter.Permit(publicID, source); !allowed {
		return automation.Identity{}, retryAfter, errors.New("automation authentication rate limited")
	}
	if parseErr != nil {
		authenticator.limiter.Failed(publicID, source)
		return automation.Identity{}, 0, parseErr
	}
	credential, err := authenticator.store.APITokenCredential(request.Context(), publicID)
	if err != nil || credential.ID != publicID || credential.RevokedAtMillis != nil || !authenticator.verifier.Verify(publicID, secret, credential.SecretHMAC) {
		authenticator.limiter.Failed(publicID, source)
		return automation.Identity{}, 0, errors.New("invalid API token")
	}
	authenticator.limiter.Succeeded(publicID, source)
	return automation.Identity{TokenID: credential.ID, Role: credential.Role, ProjectID: credential.ProjectID}, 0, nil
}

func bearerCredential(values []string) (string, string, error) {
	if len(values) != 1 {
		return "", "", errors.New("exactly one Authorization header is required")
	}
	scheme, value, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", "", errors.New("Authorization must contain one Bearer token")
	}
	publicID, secret, err := apitoken.Parse(value)
	if err != nil {
		return "", "", fmt.Errorf("parse Bearer token: %w", err)
	}
	return publicID, secret, nil
}

func sourceAddress(request *http.Request) string {
	if values := request.Header.Values("CF-Connecting-IP"); len(values) == 1 {
		if address, err := netip.ParseAddr(strings.TrimSpace(values[0])); err == nil {
			return address.Unmap().String()
		}
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	return address.Unmap().String()
}
