package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/sentry"
	"github.com/iivankin/platformd/internal/state"
)

type ServiceRuntime interface {
	ServiceTelemetryGateway(string) (netip.Addr, error)
	PublishServiceTelemetry(string, string) error
	UnpublishServiceTelemetry(string, string) error
}

type ServiceManager struct {
	process     *Process
	runtime     ServiceRuntime
	proxy       *sentry.Proxy
	credentials *Credentials
	mu          sync.Mutex
	hosts       map[string]serviceHostnames
	gateways    map[string]*serviceGateway
}

type serviceHostnames struct {
	projectID string
	sentry    string
	otlp      string
}

type ServiceConfiguration struct {
	ServiceID            string
	InternalHostname     string
	InternalDSN          string
	InternalOTLPEndpoint string
	PublicHostname       string
	PublicDSN            string
	BrowserTunnelPath    string
	UpdatedAt            int64
}

type MetricScopeResponse struct {
	Body        []byte
	ContentType string
	StatusCode  int
}

type staticTarget struct{ process *Process }

func (target staticTarget) Target(string) (*url.URL, bool) {
	value := target.process.Target()
	return value, value != nil
}

func NewServiceManager(
	process *Process,
	runtime ServiceRuntime,
	credentials *Credentials,
	notifications func(string, []byte),
) (*ServiceManager, error) {
	if process == nil || process.Target() == nil || runtime == nil || credentials == nil || notifications == nil {
		return nil, errors.New("telemetry service manager dependencies are incomplete")
	}
	proxy, err := sentry.NewProxy(staticTarget{process: process}, credentials.VerifyArtifactToken, notifications)
	if err != nil {
		return nil, err
	}
	return &ServiceManager{
		process: process, runtime: runtime, proxy: proxy, credentials: credentials,
		hosts: make(map[string]serviceHostnames), gateways: make(map[string]*serviceGateway),
	}, nil
}

func (manager *ServiceManager) RotateArtifactToken(ctx context.Context, serviceID string) (string, error) {
	return manager.credentials.RotateArtifactToken(ctx, serviceID)
}

func (manager *ServiceManager) ServeService(response http.ResponseWriter, request *http.Request, serviceID string) {
	forwarded := request.Clone(request.Context())
	forwarded.URL.Path = "/internal/services/" + url.PathEscape(serviceID) + request.URL.Path
	forwarded.URL.RawPath = ""
	manager.proxy.Serve(response, forwarded, serviceID)
}

func (manager *ServiceManager) ServeTraceScope(response http.ResponseWriter, request *http.Request, traceID string, anchorServiceID string, serviceIDs []string) {
	var anchor *string
	if anchorServiceID != "" {
		anchor = &anchorServiceID
	}
	manager.serveJSONScope(response, request, "/internal/trace-scopes/"+url.PathEscape(traceID), struct {
		AnchorServiceID *string  `json:"anchorServiceId"`
		ServiceIDs      []string `json:"serviceIds"`
	}{AnchorServiceID: anchor, ServiceIDs: serviceIDs})
}

func (manager *ServiceManager) ServeTraceListScope(response http.ResponseWriter, request *http.Request, anchorServiceID string, serviceIDs []string) {
	var anchor *string
	if anchorServiceID != "" {
		anchor = &anchorServiceID
	}
	manager.serveJSONScope(response, request, "/internal/trace-scopes", struct {
		AnchorServiceID *string  `json:"anchorServiceId"`
		ServiceIDs      []string `json:"serviceIds"`
	}{AnchorServiceID: anchor, ServiceIDs: serviceIDs})
}

func (manager *ServiceManager) ServeIssueListScope(response http.ResponseWriter, request *http.Request, serviceIDs []string) {
	manager.serveJSONScope(response, request, "/internal/issue-scopes", struct {
		ServiceIDs []string `json:"serviceIds"`
	}{ServiceIDs: serviceIDs})
}

func (manager *ServiceManager) serveJSONScope(response http.ResponseWriter, request *http.Request, path string, scope any) {
	encoded, err := json.Marshal(scope)
	if err != nil {
		http.Error(response, "Unable to encode telemetry scope", http.StatusInternalServerError)
		return
	}
	forwarded, err := http.NewRequestWithContext(request.Context(), http.MethodPost,
		manager.process.Target().String()+path, bytes.NewReader(encoded))
	if err != nil {
		http.Error(response, "Unable to create telemetry request", http.StatusInternalServerError)
		return
	}
	forwarded.URL.RawQuery = request.URL.RawQuery
	forwarded.Header.Set("Accept", "application/json")
	forwarded.Header.Set("Content-Type", "application/json")
	upstream, err := manager.process.client.Do(forwarded)
	if err != nil {
		http.Error(response, "Telemetry is unavailable", http.StatusBadGateway)
		return
	}
	defer upstream.Body.Close()
	if contentType := upstream.Header.Get("Content-Type"); contentType != "" {
		response.Header().Set("Content-Type", contentType)
	}
	response.WriteHeader(upstream.StatusCode)
	_, _ = io.Copy(response, upstream.Body)
}

func (manager *ServiceManager) QueryService(ctx context.Context, serviceID, method, path string, body any) (any, error) {
	if serviceID == "" || (method != http.MethodGet && method != http.MethodPatch) || !strings.HasPrefix(path, "/") {
		return nil, errors.New("service telemetry query is invalid")
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method,
		manager.process.Target().String()+"/internal/services/"+url.PathEscape(serviceID)+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := manager.process.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if readErr != nil {
			return nil, errors.Join(readErr, fmt.Errorf("telemetry returned HTTP %d", response.StatusCode))
		}
		return nil, fmt.Errorf("telemetry returned HTTP %d: %s", response.StatusCode, serviceErrorMessage(body))
	}
	var result any
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func serviceErrorMessage(body []byte) string {
	var value struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &value) == nil && value.Message != "" {
		if value.Code != "" {
			return value.Code + ": " + value.Message
		}
		return value.Message
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return "empty response"
	}
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func (manager *ServiceManager) QueryMetricScope(
	ctx context.Context,
	serviceIDs []string,
	operation string,
	query json.RawMessage,
) (MetricScopeResponse, error) {
	if operation != "catalog" && operation != "query" {
		return MetricScopeResponse{}, errors.New("metric scope operation is invalid")
	}
	payload := struct {
		ServiceIDs []string        `json:"serviceIds"`
		Query      json.RawMessage `json:"query,omitempty"`
	}{ServiceIDs: serviceIDs}
	if operation == "query" {
		if len(query) == 0 || !json.Valid(query) {
			return MetricScopeResponse{}, errors.New("metric scope query is invalid")
		}
		payload.Query = query
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return MetricScopeResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		manager.process.Target().String()+"/internal/metric-scopes/"+operation, bytes.NewReader(encoded))
	if err != nil {
		return MetricScopeResponse{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := manager.process.client.Do(request)
	if err != nil {
		return MetricScopeResponse{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return MetricScopeResponse{}, err
	}
	return MetricScopeResponse{
		Body: body, ContentType: response.Header.Get("Content-Type"), StatusCode: response.StatusCode,
	}, nil
}

func (manager *ServiceManager) DeleteServiceData(ctx context.Context, serviceID string) error {
	if serviceID == "" {
		return errors.New("telemetry service ID is empty")
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		manager.process.Target().String()+"/internal/services/"+url.PathEscape(serviceID),
		nil,
	)
	if err != nil {
		return err
	}
	client := *manager.process.client
	client.Timeout = 0
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("delete service telemetry returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (manager *ServiceManager) Ensure(ctx context.Context, service state.ServiceDesired) error {
	if service.ID == "" || service.Name == "" || service.ProjectID == "" || service.ProjectName == "" {
		return errors.New("service telemetry identity is incomplete")
	}
	hostnames := serviceHostnames{
		projectID: service.ProjectID,
		sentry:    InternalSentryHostname(service),
		otlp:      InternalOTLPHostname(service),
	}
	if err := manager.runtime.PublishServiceTelemetry(service.ProjectID, hostnames.sentry); err != nil {
		return err
	}
	if err := manager.runtime.PublishServiceTelemetry(service.ProjectID, hostnames.otlp); err != nil {
		_ = manager.runtime.UnpublishServiceTelemetry(service.ProjectID, hostnames.sentry)
		return err
	}
	manager.mu.Lock()
	gateway := manager.gateways[service.ProjectID]
	if gateway == nil {
		address, err := manager.runtime.ServiceTelemetryGateway(service.ProjectID)
		if err != nil {
			manager.mu.Unlock()
			_ = manager.unpublish(service.ProjectID, hostnames)
			return err
		}
		created, err := startServiceGateway(address, manager.proxy)
		if err != nil {
			manager.mu.Unlock()
			_ = manager.unpublish(service.ProjectID, hostnames)
			return fmt.Errorf("start service Sentry gateway: %w", err)
		}
		manager.gateways[service.ProjectID] = created
		gateway = created
	}
	gateway.SetSentry(hostnames.sentry, service.ID)
	gateway.SetOTLP(hostnames.otlp, service.ID)
	previousHostnames := manager.hosts[service.ID]
	manager.hosts[service.ID] = hostnames
	manager.mu.Unlock()
	if previousHostnames.sentry != "" && previousHostnames != hostnames {
		gateway.DeleteSentry(previousHostnames.sentry)
		gateway.DeleteOTLP(previousHostnames.otlp)
		if err := manager.unpublish(service.ProjectID, previousHostnames); err != nil {
			return err
		}
	}
	return nil
}

func (manager *ServiceManager) Forget(service state.ServiceDesired) error {
	hostnames := serviceHostnames{
		projectID: service.ProjectID,
		sentry:    InternalSentryHostname(service),
		otlp:      InternalOTLPHostname(service),
	}
	manager.mu.Lock()
	if current := manager.hosts[service.ID]; current.sentry != "" {
		hostnames = current
	}
	delete(manager.hosts, service.ID)
	gateway := manager.gateways[hostnames.projectID]
	projectHasServices := false
	for _, current := range manager.hosts {
		if current.projectID == hostnames.projectID {
			projectHasServices = true
			break
		}
	}
	if !projectHasServices {
		delete(manager.gateways, hostnames.projectID)
	}
	if gateway != nil {
		gateway.DeleteSentry(hostnames.sentry)
		gateway.DeleteOTLP(hostnames.otlp)
	}
	var closeErr error
	if gateway != nil && !projectHasServices {
		// Keep Ensure blocked until both listeners release their ports. Otherwise a
		// concurrent create in the same project can race the last-service cleanup.
		closeErr = gateway.Close()
	}
	manager.mu.Unlock()
	return errors.Join(manager.unpublish(hostnames.projectID, hostnames), closeErr)
}

func (manager *ServiceManager) Configuration(service state.ServiceDesired) (ServiceConfiguration, error) {
	return ServiceConfiguration{
		ServiceID: service.ID, InternalHostname: InternalSentryHostname(service),
		InternalDSN:          manager.InternalDSN(service),
		InternalOTLPEndpoint: manager.InternalOTLPEndpoint(service),
		PublicHostname:       service.SentryPublicHostname,
		PublicDSN:            manager.PublicDSN(service),
		BrowserTunnelPath:    service.SentryTunnelPath,
		UpdatedAt:            service.UpdatedAtMillis,
	}, nil
}

func (manager *ServiceManager) ServePublic(response http.ResponseWriter, request *http.Request, serviceID string) {
	manager.proxy.ServePublic(response, request, serviceID)
}

func (manager *ServiceManager) InternalDSN(service state.ServiceDesired) string {
	return InternalDSN(service)
}

func (manager *ServiceManager) InternalOTLPEndpoint(service state.ServiceDesired) string {
	return InternalOTLPEndpoint(service)
}

func (manager *ServiceManager) PublicDSN(service state.ServiceDesired) string {
	if service.SentryPublicHostname == "" {
		return ""
	}
	return SentryDSN("https", service.SentryPublicHostname, service.ID)
}

func (manager *ServiceManager) Close() error {
	manager.mu.Lock()
	gateways := manager.gateways
	manager.gateways = make(map[string]*serviceGateway)
	manager.mu.Unlock()
	var failures []error
	for _, gateway := range gateways {
		failures = append(failures, gateway.Close())
	}
	return errors.Join(failures...)
}

func InternalSentryHostname(service state.ServiceDesired) string {
	return "errors-" + service.Name + "." + service.ProjectName + ".internal"
}

func InternalOTLPHostname(service state.ServiceDesired) string {
	return "otel-" + service.Name + "." + service.ProjectName + ".internal"
}

func InternalDSN(service state.ServiceDesired) string {
	return SentryDSN("http", InternalSentryHostname(service)+":"+strconv.Itoa(firewall.ServiceTelemetryPort), service.ID)
}

func InternalOTLPEndpoint(service state.ServiceDesired) string {
	return "http://" + InternalOTLPHostname(service) + ":" + strconv.Itoa(firewall.OTLPHTTPPort)
}

func (manager *ServiceManager) unpublish(projectID string, hostnames serviceHostnames) error {
	return errors.Join(
		manager.runtime.UnpublishServiceTelemetry(projectID, hostnames.sentry),
		manager.runtime.UnpublishServiceTelemetry(projectID, hostnames.otlp),
	)
}

// SentryDSN derives the transport credential from SQLite's service identity.
func SentryDSN(scheme, hostname, serviceID string) string {
	return scheme + "://" + serviceID + "@" + hostname + "/" + sentry.ProtocolProjectID
}
