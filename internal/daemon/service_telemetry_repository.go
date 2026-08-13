package daemon

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	createdNew, err := repository.ensureDNS(ctx, input.PublicHostname)
	if err != nil {
		return telemetry.ServiceConfiguration{}, err
	}
	deletedPrevious := false
	if service.SentryPublicHostname != input.PublicHostname {
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
	repository.manager.ServeService(response, request, service.ID)
}

func (repository *liveServiceTelemetryRepository) reloadPublicRoutes(ctx context.Context) error {
	if repository.router == nil {
		return nil
	}
	services, err := repository.store.Services(ctx)
	if err != nil {
		return err
	}
	hostnames := make([]string, 0, len(services))
	for _, service := range services {
		if service.SentryPublicHostname != "" {
			hostnames = append(hostnames, service.SentryPublicHostname)
		}
	}
	repository.router.ReloadServiceTelemetry(hostnames)
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
