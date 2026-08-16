package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cloudflaredns"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
)

type liveServiceTelemetryRepository struct {
	store         *state.Store
	manager       *telemetry.ServiceManager
	certificates  *origin.Selector
	cloudflare    *cloudflaredns.Application
	router        *ingress.Router
	publicMu      *sync.Mutex
	adminHostname string
	master        cryptobox.MasterKey
}

const (
	serviceTelemetryCleanupTimeout     = 2 * time.Minute
	serviceTelemetryRouteReloadTimeout = 5 * time.Second
)

func cleanupServiceTelemetry(manager *telemetry.ServiceManager, service state.ServiceDesired) error {
	ctx, cancel := context.WithTimeout(context.Background(), serviceTelemetryCleanupTimeout)
	defer cancel()
	return errors.Join(manager.DeleteServiceData(ctx, service.ID), manager.Forget(service))
}

func (repository *liveServiceTelemetryRepository) QueryService(ctx context.Context, serviceID, method, path string, body any) (any, error) {
	return repository.manager.QueryService(ctx, serviceID, method, path, body)
}

func (repository *liveServiceTelemetryRepository) RotateServiceArtifactToken(ctx context.Context, projectID, serviceID string) (string, error) {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return "", err
	}
	return repository.manager.RotateArtifactToken(ctx, serviceID)
}

func (repository *liveServiceTelemetryRepository) ServiceTelemetryWebhooks(ctx context.Context, projectID, serviceID string) ([]state.ServiceTelemetryWebhook, error) {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return nil, err
	}
	return repository.store.ServiceTelemetryWebhooks(ctx, serviceID)
}

func (repository *liveServiceTelemetryRepository) CreateServiceTelemetryWebhook(ctx context.Context, projectID, serviceID, value string, events []string) (state.ServiceTelemetryWebhook, string, error) {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return state.ServiceTelemetryWebhook{}, "", err
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return state.ServiceTelemetryWebhook{}, "", fmt.Errorf("%w: URL is invalid", state.ErrServiceTelemetryWebhookInvalid)
	}
	allowed := []string{"event_received", "issue_created", "issue_regressed", "issue_resolved"}
	if len(events) == 0 {
		return state.ServiceTelemetryWebhook{}, "", fmt.Errorf("%w: events are required", state.ErrServiceTelemetryWebhookInvalid)
	}
	slices.Sort(events)
	events = slices.Compact(events)
	if slices.ContainsFunc(events, func(event string) bool { return !slices.Contains(allowed, event) }) {
		return state.ServiceTelemetryWebhook{}, "", fmt.Errorf("%w: event is invalid", state.ErrServiceTelemetryWebhookInvalid)
	}
	webhookID, err := id.New()
	if err != nil {
		return state.ServiceTelemetryWebhook{}, "", err
	}
	rawSecret := make([]byte, 32)
	if _, err := rand.Read(rawSecret); err != nil {
		return state.ServiceTelemetryWebhook{}, "", err
	}
	secret := "ptel_whsec_" + base64.RawURLEncoding.EncodeToString(rawSecret)
	box, err := cryptobox.NewBox(repository.master, []byte(webhookID), "platformd/sqlite/service-telemetry-webhook/v1")
	if err != nil {
		return state.ServiceTelemetryWebhook{}, "", err
	}
	encrypted, err := box.Seal([]byte(secret), []byte(serviceID))
	if err != nil {
		return state.ServiceTelemetryWebhook{}, "", err
	}
	now := time.Now().UnixMilli()
	webhook := state.ServiceTelemetryWebhook{ID: webhookID, ServiceID: serviceID, URL: parsed.String(), EventTypes: events,
		SecretEncrypted: encrypted, Enabled: true, CreatedAtMillis: now, UpdatedAtMillis: now}
	if err := repository.store.CreateServiceTelemetryWebhook(ctx, webhook); err != nil {
		return state.ServiceTelemetryWebhook{}, "", err
	}
	return webhook, secret, nil
}

func (repository *liveServiceTelemetryRepository) DeleteServiceTelemetryWebhook(ctx context.Context, projectID, serviceID, webhookID string) error {
	if _, err := repository.store.Service(ctx, projectID, serviceID); err != nil {
		return err
	}
	return repository.store.DeleteServiceTelemetryWebhook(ctx, serviceID, webhookID)
}

func (repository *liveServiceTelemetryRepository) MetricCharts(
	ctx context.Context,
	scope state.MetricScope,
) ([]state.MetricChart, error) {
	return repository.store.MetricCharts(ctx, scope)
}

func (repository *liveServiceTelemetryRepository) CreateMetricChart(
	ctx context.Context,
	chart state.MetricChart,
) (state.MetricChart, error) {
	chartID, err := id.New()
	if err != nil {
		return state.MetricChart{}, err
	}
	now := time.Now().UnixMilli()
	chart.ID = chartID
	chart.Title = strings.TrimSpace(chart.Title)
	chart.SQL = strings.TrimSpace(chart.SQL)
	chart.CreatedAtMillis = now
	chart.UpdatedAtMillis = now
	if err := repository.store.CreateMetricChart(ctx, chart); err != nil {
		return state.MetricChart{}, err
	}
	return chart, nil
}

func (repository *liveServiceTelemetryRepository) UpdateMetricChart(
	ctx context.Context,
	chart state.MetricChart,
	expectedUpdatedAt int64,
) (state.MetricChart, error) {
	chart.Title = strings.TrimSpace(chart.Title)
	chart.SQL = strings.TrimSpace(chart.SQL)
	chart.UpdatedAtMillis = time.Now().UnixMilli()
	if chart.UpdatedAtMillis <= expectedUpdatedAt {
		chart.UpdatedAtMillis = expectedUpdatedAt + 1
	}
	if err := repository.store.UpdateMetricChart(ctx, chart, expectedUpdatedAt); err != nil {
		return state.MetricChart{}, err
	}
	charts, err := repository.store.MetricCharts(ctx, chart.Scope)
	if err != nil {
		return state.MetricChart{}, err
	}
	for _, updated := range charts {
		if updated.ID == chart.ID {
			return updated, nil
		}
	}
	return state.MetricChart{}, state.ErrMetricChartNotFound
}

func (repository *liveServiceTelemetryRepository) DeleteMetricChart(
	ctx context.Context,
	scope state.MetricScope,
	chartID string,
) error {
	return repository.store.DeleteMetricChart(ctx, scope, chartID)
}

func (repository *liveServiceTelemetryRepository) QueryMetricScope(
	ctx context.Context,
	scope state.MetricScope,
	operation string,
	query json.RawMessage,
) (telemetry.MetricScopeResponse, error) {
	serviceIDs, err := repository.store.MetricScopeServiceIDs(ctx, scope)
	if err != nil {
		return telemetry.MetricScopeResponse{}, err
	}
	return repository.manager.QueryMetricScope(ctx, serviceIDs, operation, query)
}

func (repository *liveServiceTelemetryRepository) ServiceTelemetry(
	ctx context.Context,
	projectID string,
	serviceID string,
) (telemetry.ServiceConfiguration, error) {
	service, err := repository.store.Service(ctx, projectID, serviceID)
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	return repository.manager.Configuration(service)
}

func (repository *liveServiceTelemetryRepository) UpdateServiceTelemetryPublicAccess(
	ctx context.Context,
	input state.UpdateServiceSentryPublicAccess,
) (telemetry.ServiceConfiguration, error) {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	service, err := repository.store.Service(ctx, input.ProjectID, input.ID)
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	if service.UpdatedAtMillis != input.ExpectedUpdatedMillis {
		return telemetry.ServiceConfiguration{}, state.ErrServiceChanged
	}
	if input.PublicHostname != "" {
		input.PublicHostname, err = publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return telemetry.ServiceConfiguration{}, err
		}
		if !repository.certificates.Covers(input.PublicHostname) {
			return telemetry.ServiceConfiguration{}, state.ErrCertificateCoverage
		}
	}
	domains, err := repository.store.ServiceDomains(ctx, input.ProjectID, input.ID)
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	applicationHostnames := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		applicationHostnames[domain.Hostname] = struct{}{}
	}
	_, nextUsesApplicationDNS := applicationHostnames[input.PublicHostname]
	createdNew := false
	if !nextUsesApplicationDNS {
		createdNew, err = repository.ensureDNS(ctx, input.PublicHostname)
	}
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	deletedPrevious := false
	_, previousUsesApplicationDNS := applicationHostnames[service.SentryPublicHostname]
	if service.SentryPublicHostname != input.PublicHostname && !previousUsesApplicationDNS {
		deletedPrevious, err = repository.deleteDNS(ctx, service.SentryPublicHostname)
		if err != nil {
			if createdNew {
				_, cleanupErr := repository.deleteDNS(ctx, input.PublicHostname)
				err = errors.Join(err, cleanupErr)
			}
			return telemetry.ServiceConfiguration{}, err
		}
	}
	updated, err := repository.store.UpdateServiceSentryPublicAccess(ctx, input)
	if err != nil {
		if deletedPrevious {
			_, restoreErr := repository.ensureDNS(ctx, service.SentryPublicHostname)
			err = errors.Join(err, restoreErr)
		}
		if createdNew {
			_, cleanupErr := repository.deleteDNS(ctx, input.PublicHostname)
			err = errors.Join(err, cleanupErr)
		}
		return telemetry.ServiceConfiguration{}, err
	}
	// SQLite has already committed the hostname. Finish publishing the new
	// in-memory route even if the client disconnects after the commit.
	reloadContext, cancelReload := context.WithTimeout(context.WithoutCancel(ctx), serviceTelemetryRouteReloadTimeout)
	defer cancelReload()
	if err := repository.reloadPublicRoutes(reloadContext); err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	return repository.manager.Configuration(updated)
}

func (repository *liveServiceTelemetryRepository) UpdateServiceTelemetryTunnel(
	ctx context.Context,
	input state.UpdateServiceTelemetryTunnel,
) (telemetry.ServiceConfiguration, error) {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	service, err := repository.store.Service(ctx, input.ProjectID, input.ID)
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	if service.UpdatedAtMillis != input.ExpectedUpdatedMillis {
		return telemetry.ServiceConfiguration{}, state.ErrServiceChanged
	}
	updated, err := repository.store.UpdateServiceTelemetryTunnel(ctx, input)
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	// The committed path must become visible even if the API client disconnects
	// while ingress is rebuilding its immutable route snapshot.
	reloadContext, cancelReload := context.WithTimeout(context.WithoutCancel(ctx), serviceTelemetryRouteReloadTimeout)
	defer cancelReload()
	if err := repository.reloadPublicRoutes(reloadContext); err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	return repository.manager.Configuration(updated)
}

func (repository *liveServiceTelemetryRepository) ServeServiceTelemetry(
	response http.ResponseWriter,
	request *http.Request,
	projectID string,
	serviceID string,
) {
	service, err := repository.store.Service(request.Context(), projectID, serviceID)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	if request.Method == http.MethodGet {
		traceID, isTrace := strings.CutPrefix(request.URL.Path, "/traces/")
		isTraceList := request.URL.Path == "/traces"
		isTraceDetail := isTrace && traceID != "" && !strings.Contains(traceID, "/")
		if isTraceList || isTraceDetail {
			services, listErr := repository.store.Services(request.Context())
			if listErr != nil {
				http.Error(response, "Unable to load project telemetry scope", http.StatusInternalServerError)
				return
			}
			serviceIDs := make([]string, 0, len(services))
			for _, candidate := range services {
				if candidate.ProjectID == projectID {
					serviceIDs = append(serviceIDs, candidate.ID)
				}
			}
			if isTraceList {
				repository.manager.ServeTraceListScope(response, request, service.ID, serviceIDs)
			} else {
				repository.manager.ServeTraceScope(response, request, traceID, service.ID, serviceIDs)
			}
			return
		}
	}
	repository.manager.ServeService(response, request, service.ID)
}

func (repository *liveServiceTelemetryRepository) ServeTelemetryScope(
	response http.ResponseWriter,
	request *http.Request,
	scope state.MetricScope,
) {
	serviceIDs, err := repository.store.MetricScopeServiceIDs(request.Context(), scope)
	if err != nil {
		if errors.Is(err, state.ErrMetricScopeNotFound) {
			http.NotFound(response, request)
			return
		}
		http.Error(response, "Unable to load telemetry scope", http.StatusInternalServerError)
		return
	}
	if request.Method != http.MethodGet {
		http.NotFound(response, request)
		return
	}
	traceID, isTrace := strings.CutPrefix(request.URL.Path, "/traces/")
	switch {
	case request.URL.Path == "/traces":
		repository.manager.ServeTraceListScope(response, request, "", serviceIDs)
	case isTrace && traceID != "" && !strings.Contains(traceID, "/"):
		repository.manager.ServeTraceScope(response, request, traceID, "", serviceIDs)
	case request.URL.Path == "/errors/issues":
		repository.serveScopedIssues(response, request, serviceIDs)
	default:
		http.NotFound(response, request)
	}
}

func (repository *liveServiceTelemetryRepository) serveScopedIssues(
	response http.ResponseWriter,
	request *http.Request,
	serviceIDs []string,
) {
	recorder := httptest.NewRecorder()
	repository.manager.ServeIssueListScope(recorder, request, serviceIDs)
	upstream := recorder.Result()
	body, err := io.ReadAll(upstream.Body)
	_ = upstream.Body.Close()
	if err != nil {
		http.Error(response, "Unable to read scoped issues", http.StatusBadGateway)
		return
	}
	if upstream.StatusCode == http.StatusOK {
		services, listErr := repository.store.Services(request.Context())
		if listErr != nil {
			http.Error(response, "Unable to load project telemetry scope", http.StatusInternalServerError)
			return
		}
		projectByService := make(map[string]string, len(services))
		for _, service := range services {
			projectByService[service.ID] = service.ProjectID
		}
		enriched, enrichErr := attachIssueProjectIDs(body, projectByService)
		if enrichErr != nil {
			http.Error(response, "Unable to annotate scoped issues", http.StatusBadGateway)
			return
		}
		body = enriched
	}
	for key, values := range upstream.Header {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			response.Header().Add(key, value)
		}
	}
	response.WriteHeader(upstream.StatusCode)
	_, _ = response.Write(body)
}

func attachIssueProjectIDs(body []byte, projectByService map[string]string) ([]byte, error) {
	var envelope struct {
		Data  []map[string]any `json:"data"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	for _, issue := range envelope.Data {
		serviceID, _ := issue["serviceId"].(string)
		if projectID := projectByService[serviceID]; projectID != "" {
			issue["projectId"] = projectID
		}
	}
	return json.Marshal(envelope)
}

func (repository *liveServiceTelemetryRepository) reloadPublicRoutes(ctx context.Context) error {
	if repository.router == nil {
		return nil
	}
	services, err := repository.store.Services(ctx)
	if err != nil {
		return err
	}
	routes := make(map[string]ingress.ServiceTelemetryRoute, len(services))
	for _, service := range services {
		if service.SentryPublicHostname != "" {
			routes[service.SentryPublicHostname] = ingress.ServiceTelemetryRoute{
				BrowserTunnelPath: service.SentryTunnelPath,
			}
		}
	}
	repository.router.ReloadServiceTelemetry(routes)
	return nil
}

func (repository *liveServiceTelemetryRepository) reconcileDNS(ctx context.Context) error {
	if repository.cloudflare == nil {
		return nil
	}
	services, err := repository.store.Services(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, service := range services {
		if service.SentryPublicHostname == "" {
			continue
		}
		if _, err := repository.ensureDNS(ctx, service.SentryPublicHostname); err != nil {
			result = errors.Join(result, fmt.Errorf("reconcile Cloudflare DNS for service %s telemetry: %w", service.ID, err))
		}
	}
	return result
}

func (repository *liveServiceTelemetryRepository) ensureDNS(ctx context.Context, hostname string) (bool, error) {
	if hostname == "" || repository.cloudflare == nil {
		return false, nil
	}
	created, err := repository.cloudflare.EnsureServiceHostname(ctx, hostname, repository.adminHostname)
	if err != nil {
		return false, fmt.Errorf("configure Cloudflare DNS for %s: %w", hostname, err)
	}
	return created, nil
}

func (repository *liveServiceTelemetryRepository) deleteDNS(ctx context.Context, hostname string) (bool, error) {
	if hostname == "" || repository.cloudflare == nil {
		return false, nil
	}
	deleted, err := repository.cloudflare.DeleteServiceHostname(ctx, hostname)
	if err != nil {
		return false, fmt.Errorf("remove Cloudflare DNS for %s: %w", hostname, err)
	}
	return deleted, nil
}

func (repository *liveServiceTelemetryRepository) deleteProjectDNS(
	ctx context.Context,
	services []state.ServiceDesired,
) ([]string, error) {
	deleted := make([]string, 0, len(services))
	for _, service := range services {
		removed, err := repository.deleteDNS(ctx, service.SentryPublicHostname)
		if err != nil {
			return nil, errors.Join(err, repository.restoreDNS(ctx, deleted))
		}
		if removed {
			deleted = append(deleted, service.SentryPublicHostname)
		}
	}
	return deleted, nil
}

func (repository *liveServiceTelemetryRepository) restoreDNS(ctx context.Context, hostnames []string) error {
	var result error
	for _, hostname := range hostnames {
		if _, err := repository.ensureDNS(ctx, hostname); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (repository *liveServiceTelemetryRepository) withdrawServiceDNS(
	ctx context.Context,
	service state.ServiceDesired,
) (bool, error) {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	return repository.deleteDNS(ctx, service.SentryPublicHostname)
}

func (repository *liveServiceTelemetryRepository) restoreServiceDNS(
	ctx context.Context,
	service state.ServiceDesired,
) error {
	repository.publicMu.Lock()
	defer repository.publicMu.Unlock()
	_, err := repository.ensureDNS(ctx, service.SentryPublicHostname)
	return err
}
