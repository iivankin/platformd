package registry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/registryauth"
	"github.com/iivankin/platformd/internal/registryname"
)

type registryTokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	IssuedAt    string `json:"issued_at"`
}

func (handler *HTTPHandler) token(response http.ResponseWriter, request *http.Request) {
	setRegistryCache(response, false, false)
	if request.Method != http.MethodGet {
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "token operation is not supported")
		return
	}
	serviceValues, serviceExists := request.URL.Query()["service"]
	if !serviceExists || len(serviceValues) != 1 || serviceValues[0] != request.Host {
		writeDistributionError(response, http.StatusBadRequest, "UNAUTHORIZED", "token service is invalid")
		return
	}
	scopeValues, scopeExists := request.URL.Query()["scope"]
	if !scopeExists {
		authentication, ok := handler.tokenBasicAuthentication(response, request, "", false)
		if !ok {
			return
		}
		handler.issueToken(request.Context(), response, request.Host, authentication, nil)
		return
	}
	if len(scopeValues) != 1 {
		writeDistributionError(response, http.StatusBadRequest, "DENIED", "exactly one repository scope is supported")
		return
	}
	repositoryName, actions, err := parseTokenScope(scopeValues[0])
	if err != nil {
		writeDistributionError(response, http.StatusBadRequest, "DENIED", err.Error())
		return
	}
	repository, err := handler.application.RepositoryByName(request.Context(), repositoryName)
	if err != nil {
		writeDistributionError(response, http.StatusNotFound, "NAME_UNKNOWN", "repository is unknown")
		return
	}
	write := slicesContain(actions, "push")
	if len(request.Header.Values("Authorization")) == 0 {
		if !repository.PublicPull || write {
			tokenBasicUnauthorized(response)
			return
		}
		handler.issueToken(request.Context(), response, request.Host, Authentication{Repository: repository}, actions)
		return
	}
	authentication, ok := handler.tokenBasicAuthentication(response, request, repository.Name, write)
	if !ok {
		return
	}
	handler.issueToken(request.Context(), response, request.Host, authentication, actions)
}

func (handler *HTTPHandler) tokenBasicAuthentication(response http.ResponseWriter, request *http.Request, repository string, write bool) (Authentication, bool) {
	values := request.Header.Values("Authorization")
	username, secret, basic := request.BasicAuth()
	credentialID, credentialIDErr := registryauth.CredentialID(username)
	if credentialIDErr != nil {
		credentialID = ""
	}
	source := registrySourceAddress(request)
	if allowed, retryAfter := handler.limiter.Permit(credentialID, source); !allowed {
		seconds := max(1, int((retryAfter+time.Second-1)/time.Second))
		response.Header().Set("Retry-After", strconv.Itoa(seconds))
		writeDistributionError(response, http.StatusTooManyRequests, "TOOMANYREQUESTS", "authentication is rate limited")
		return Authentication{}, false
	}
	if len(values) != 1 || !basic || credentialID == "" {
		handler.limiter.Failed(credentialID, source)
		tokenBasicUnauthorized(response)
		return Authentication{}, false
	}
	var authentication Authentication
	var err error
	if repository == "" {
		authentication, err = handler.application.AuthenticateCredential(request.Context(), username, secret)
	} else {
		authentication, err = handler.application.Authenticate(request.Context(), repository, username, secret, write)
	}
	if err != nil {
		if errors.Is(err, ErrDenied) {
			handler.limiter.Succeeded(credentialID, source)
			writeDistributionError(response, http.StatusForbidden, "DENIED", "credential does not grant requested actions")
			return Authentication{}, false
		}
		handler.limiter.Failed(credentialID, source)
		tokenBasicUnauthorized(response)
		return Authentication{}, false
	}
	handler.limiter.Succeeded(credentialID, source)
	return authentication, true
}

func (handler *HTTPHandler) issueToken(ctx context.Context, response http.ResponseWriter, service string, authentication Authentication, actions []string) {
	credentialID := authentication.Credential.ID
	now := handler.application.now()
	if credentialID != "" {
		if err := handler.application.MarkCredentialUsed(ctx, credentialID); err != nil {
			writeDistributionError(response, http.StatusInternalServerError, "UNKNOWN", "unable to record credential use")
			return
		}
	}
	value, err := handler.application.tokens.issue(
		service, authentication.Repository.ID, authentication.Repository.Name,
		credentialID, actions, now,
	)
	if err != nil {
		writeDistributionError(response, http.StatusInternalServerError, "UNKNOWN", "unable to issue token")
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(registryTokenResponse{
		Token: value, AccessToken: value, ExpiresIn: int(RegistryTokenLifetime / time.Second),
		IssuedAt: now.UTC().Format(time.RFC3339),
	})
}

func tokenBasicUnauthorized(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", `Basic realm="platformd registry token"`)
	writeDistributionError(response, http.StatusUnauthorized, "UNAUTHORIZED", "robot credential is required")
}

func parseTokenScope(value string) (string, []string, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 3 || parts[0] != "repository" || registryname.ValidateRepository(parts[1]) != nil {
		return "", nil, errors.New("repository token scope is invalid")
	}
	requested := strings.Split(parts[2], ",")
	if len(requested) == 0 || len(requested) > 2 {
		return "", nil, errors.New("repository token actions are invalid")
	}
	actions := make([]string, 0, len(requested))
	for _, action := range []string{"pull", "push"} {
		if slicesContain(requested, action) {
			actions = append(actions, action)
		}
	}
	if len(actions) != len(requested) {
		return "", nil, errors.New("repository token action is unsupported or duplicated")
	}
	return parts[1], actions, nil
}

func slicesContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
