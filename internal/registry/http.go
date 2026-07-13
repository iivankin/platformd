package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/registryname"
	"github.com/iivankin/platformd/internal/state"
)

const distributionAPIVersion = "registry/2.0"

type HTTPHandler struct {
	application *Application
	limiter     FailureLimiter
	admission   *admission.Gate
}

type FailureLimiter interface {
	Permit(string, string) (bool, time.Duration)
	Failed(string, string)
	Succeeded(string, string)
}

func NewHTTPHandler(application *Application, limiter FailureLimiter, gate *admission.Gate) (*HTTPHandler, error) {
	if application == nil || limiter == nil || gate == nil {
		return nil, errors.New("registry HTTP dependencies are incomplete")
	}
	return &HTTPHandler{application: application, limiter: limiter, admission: gate}, nil
}

func (handler *HTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Docker-Distribution-API-Version", distributionAPIVersion)
	if request.URL.Path == "/v2/token" {
		handler.token(response, request)
		return
	}
	if request.URL.Path == "/v2/" {
		handler.ping(response, request)
		return
	}
	route, err := parseRegistryRoute(request.URL.Path)
	if err != nil {
		writeDistributionError(response, http.StatusNotFound, "NAME_UNKNOWN", "repository route is unknown")
		return
	}
	repository, err := handler.application.RepositoryByName(request.Context(), route.repository)
	if err != nil {
		writeDistributionError(response, http.StatusNotFound, "NAME_UNKNOWN", "repository is unknown")
		return
	}
	finishRequest, err := handler.application.BeginRepositoryRequest(repository.ID)
	if err != nil {
		writeDistributionError(response, http.StatusServiceUnavailable, "UNAVAILABLE", "repository is temporarily unavailable")
		return
	}
	defer finishRequest()
	write := route.kind == routeUploadCollection || route.kind == routeUpload || (route.kind == routeManifest && request.Method == http.MethodPut)
	authentication, authorized := handler.authorize(response, request, repository, write)
	if !authorized {
		return
	}
	if write {
		lease, err := handler.admission.Begin("registry_mutation", repository.ID)
		if err != nil {
			writeDistributionError(response, http.StatusConflict, "UNAVAILABLE", "platform update is in progress")
			return
		}
		defer lease.Release()
	}
	switch route.kind {
	case routeBlob:
		handler.blob(response, request, repository, route.reference)
	case routeManifest:
		handler.manifest(response, request, authentication, route.reference)
	case routeTags:
		handler.tags(response, request, repository)
	case routeUploadCollection:
		handler.beginUpload(response, request, authentication)
	case routeUpload:
		handler.upload(response, request, authentication, route.reference)
	default:
		writeDistributionError(response, http.StatusNotFound, "UNSUPPORTED", "operation is not supported")
	}
}

func (handler *HTTPHandler) ping(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeDistributionError(response, http.StatusMethodNotAllowed, "UNSUPPORTED", "operation is not supported")
		return
	}
	token, ok := bearerAuthorization(request.Header.Values("Authorization"))
	if !ok {
		registryUnauthorized(response, request, "", false)
		return
	}
	claims, err := handler.application.tokens.verify(token, request.Host, handler.application.now())
	if err != nil || claims.CredentialID == "" {
		registryUnauthorized(response, request, "", false)
		return
	}
	repository, err := handler.application.Repository(request.Context(), claims.RepositoryID)
	if err != nil || repository.Name != claims.Repository {
		registryUnauthorized(response, request, "", false)
		return
	}
	if _, err := handler.application.authenticationForCredentialID(request.Context(), repository, claims.CredentialID, false); err != nil {
		registryUnauthorized(response, request, "", false)
		return
	}
	setRegistryCache(response, false, false)
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte("{}"))
}

func (handler *HTTPHandler) authorize(response http.ResponseWriter, request *http.Request, repository state.RegistryRepository, write bool) (Authentication, bool) {
	authorizationValues := request.Header.Values("Authorization")
	if !write && repository.PublicPull && len(authorizationValues) == 0 {
		setRegistryCache(response, true, false)
		return Authentication{Repository: repository}, true
	}
	token, ok := bearerAuthorization(authorizationValues)
	if !ok {
		registryUnauthorized(response, request, repository.Name, write)
		return Authentication{}, false
	}
	claims, err := handler.application.tokens.verify(token, request.Host, handler.application.now())
	if err != nil || claims.RepositoryID != repository.ID || claims.Repository != repository.Name || !tokenAllows(claims.Actions, write) {
		registryUnauthorized(response, request, repository.Name, write)
		return Authentication{}, false
	}
	if claims.CredentialID == "" {
		if write || !repository.PublicPull {
			registryUnauthorized(response, request, repository.Name, write)
			return Authentication{}, false
		}
		setRegistryCache(response, true, false)
		return Authentication{Repository: repository}, true
	}
	authentication, err := handler.application.authenticationForCredentialID(request.Context(), repository, claims.CredentialID, write)
	if err != nil {
		registryUnauthorized(response, request, repository.Name, write)
		return Authentication{}, false
	}
	setRegistryCache(response, repository.PublicPull && !write, false)
	return authentication, true
}

func bearerAuthorization(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	scheme, value, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || value == "" || strings.ContainsAny(value, " \t\r\n") {
		return "", false
	}
	return value, true
}

func tokenAllows(actions []string, write bool) bool {
	required := "pull"
	if write {
		required = "push"
	}
	for _, action := range actions {
		if action == required {
			return true
		}
	}
	return false
}

func registrySourceAddress(request *http.Request) string {
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

func registryUnauthorized(response http.ResponseWriter, request *http.Request, repository string, write bool) {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	challenge := fmt.Sprintf(`Bearer realm=%q,service=%q`, scheme+"://"+request.Host+"/v2/token", request.Host)
	if repository != "" {
		actions := "pull"
		if write {
			actions = "pull,push"
		}
		challenge += fmt.Sprintf(`,scope=%q`, "repository:"+repository+":"+actions)
	}
	response.Header().Set("WWW-Authenticate", challenge)
	writeDistributionError(response, http.StatusUnauthorized, "UNAUTHORIZED", "authentication is required")
}

func setRegistryCache(response http.ResponseWriter, public, immutable bool) {
	if public && immutable {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
}

type distributionErrorEnvelope struct {
	Errors []distributionError `json:"errors"`
}

type distributionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeDistributionError(response http.ResponseWriter, status int, code, message string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(distributionErrorEnvelope{
		Errors: []distributionError{{Code: code, Message: message}},
	})
}

type registryRouteKind uint8

const (
	routeBlob registryRouteKind = iota + 1
	routeManifest
	routeTags
	routeUploadCollection
	routeUpload
)

type registryRoute struct {
	repository string
	kind       registryRouteKind
	reference  string
}

func parseRegistryRoute(path string) (registryRoute, error) {
	if !strings.HasPrefix(path, "/v2/") {
		return registryRoute{}, errors.New("registry route prefix is invalid")
	}
	tail := strings.TrimPrefix(path, "/v2/")
	if strings.HasSuffix(tail, "/blobs/uploads/") {
		repository := strings.TrimSuffix(tail, "/blobs/uploads/")
		if registryname.ValidateRepository(repository) == nil {
			return registryRoute{repository: repository, kind: routeUploadCollection}, nil
		}
	}
	markers := []struct {
		value string
		kind  registryRouteKind
	}{
		{value: "/blobs/uploads/", kind: routeUpload},
		{value: "/blobs/", kind: routeBlob},
		{value: "/manifests/", kind: routeManifest},
	}
	for _, marker := range markers {
		if index := strings.LastIndex(tail, marker.value); index > 0 {
			repository := tail[:index]
			reference := tail[index+len(marker.value):]
			if reference == "" || registryname.ValidateRepository(repository) != nil {
				return registryRoute{}, errors.New("registry route values are invalid")
			}
			return registryRoute{repository: repository, kind: marker.kind, reference: reference}, nil
		}
	}
	if strings.HasSuffix(tail, "/tags/list") {
		repository := strings.TrimSuffix(tail, "/tags/list")
		if registryname.ValidateRepository(repository) == nil {
			return registryRoute{repository: repository, kind: routeTags}, nil
		}
	}
	return registryRoute{}, errors.New("registry route is unsupported")
}

func positiveQueryInteger(request *http.Request, name string, fallback, maximum int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, errors.New("query integer is out of range")
	}
	return parsed, nil
}
