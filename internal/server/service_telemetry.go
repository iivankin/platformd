package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
)

const maximumServiceTelemetryRequestBytes = 32 << 10

type ServiceTelemetryRepository interface {
	ServiceTelemetry(context.Context, string, string) (telemetry.ServiceConfiguration, error)
	RotateServiceArtifactToken(context.Context, string, string) (string, error)
	ServiceTelemetryWebhooks(context.Context, string, string) ([]state.ServiceTelemetryWebhook, error)
	CreateServiceTelemetryWebhook(context.Context, string, string, string, []string) (state.ServiceTelemetryWebhook, string, error)
	DeleteServiceTelemetryWebhook(context.Context, string, string, string) error
	MetricCharts(context.Context, state.MetricScope) ([]state.MetricChart, error)
	CreateMetricChart(context.Context, state.MetricChart) (state.MetricChart, error)
	UpdateMetricChart(context.Context, state.MetricChart, int64) (state.MetricChart, error)
	DeleteMetricChart(context.Context, state.MetricScope, string) error
	QueryMetricScope(context.Context, state.MetricScope, string, json.RawMessage) (telemetry.MetricScopeResponse, error)
	UpdateServiceTelemetryPublicAccess(context.Context, state.UpdateServiceSentryPublicAccess) (telemetry.ServiceConfiguration, error)
	UpdateServiceTelemetryTunnel(context.Context, state.UpdateServiceTelemetryTunnel) (telemetry.ServiceConfiguration, error)
	ServeServiceTelemetry(http.ResponseWriter, *http.Request, string, string)
	ServeTelemetryScope(http.ResponseWriter, *http.Request, state.MetricScope)
}

type serviceTelemetryResponse struct {
	ServiceID            string                            `json:"serviceId"`
	InternalHostname     string                            `json:"internalHostname"`
	InternalDSN          string                            `json:"internalDsn"`
	InternalOTLPEndpoint string                            `json:"internalOtlpEndpoint"`
	PublicHostname       string                            `json:"publicHostname,omitempty"`
	PublicDSN            string                            `json:"publicDsn,omitempty"`
	BrowserTunnelPath    string                            `json:"browserTunnelPath,omitempty"`
	UpdatedAt            int64                             `json:"updatedAt"`
	Webhooks             []serviceTelemetryWebhookResponse `json:"webhooks"`
}

type serviceTelemetryWebhookResponse struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Enabled   bool     `json:"enabled"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

func registerServiceTelemetryRoutes(mux *http.ServeMux, config handlerConfig) {
	pattern := "/api/v1/projects/{projectID}/services/{serviceID}/telemetry"
	mux.HandleFunc("GET "+pattern, getServiceTelemetry(config.serviceTelemetry))
	mux.HandleFunc("PUT "+pattern+"/public-access", updateServiceTelemetryPublicAccess(config))
	mux.HandleFunc("PUT "+pattern+"/browser-tunnel", updateServiceTelemetryTunnel(config))
	mux.HandleFunc("POST "+pattern+"/artifact-token", rotateServiceArtifactToken(config.serviceTelemetry))
	mux.HandleFunc("POST "+pattern+"/webhooks", createServiceTelemetryWebhook(config.serviceTelemetry))
	mux.HandleFunc("DELETE "+pattern+"/webhooks/{webhookID}", deleteServiceTelemetryWebhook(config.serviceTelemetry))
	registerMetricScopeRoutes(mux, config.serviceTelemetry, pattern, serviceMetricScope)
	registerMetricScopeRoutes(mux, config.serviceTelemetry, "/api/v1/projects/{projectID}/telemetry", projectMetricScope)
	registerMetricScopeRoutes(mux, config.serviceTelemetry, "/api/v1/telemetry", installationMetricScope)
	mux.Handle("/api/v1/projects/{projectID}/telemetry/{path...}", telemetryScopeConsole(config.serviceTelemetry, projectMetricScope))
	mux.Handle("/api/v1/telemetry/{path...}", telemetryScopeConsole(config.serviceTelemetry, installationMetricScope))
	mux.Handle(pattern+"/{path...}", serviceTelemetryConsole(config.serviceTelemetry))
	mux.Handle("/api/v1/projects/{projectID}/services/{serviceID}/errors/{path...}", serviceTelemetryConsole(config.serviceTelemetry))
}

func telemetryScopeConsole(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		forwarded := request.Clone(request.Context())
		forwarded.URL.Path = "/" + request.PathValue("path")
		forwarded.URL.RawPath = ""
		forwarded.Header = request.Header.Clone()
		forwarded.Header.Del("Authorization")
		forwarded.Header.Del("Cookie")
		forwarded.Header.Del("Cf-Access-Jwt-Assertion")
		repository.ServeTelemetryScope(response, forwarded, scope(request))
	})
}

func updateServiceTelemetryTunnel(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		BrowserTunnelPath string `json:"browserTunnelPath"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		if !requireJSONContentType(response, request) {
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumServiceTelemetryRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil || body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_telemetry", "Browser tunnel fields are invalid")
			return
		}
		_, auditID, correlationID, err := createRequestIDs()
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate telemetry mutation identifiers")
			return
		}
		configuration, err := config.serviceTelemetry.UpdateServiceTelemetryTunnel(request.Context(), state.UpdateServiceTelemetryTunnel{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"), Path: body.BrowserTunnelPath,
			ExpectedUpdatedMillis: body.ExpectedUpdatedAt, AuditEventID: auditID,
			ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: config.now().UnixMilli(),
		})
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		webhooks, err := config.serviceTelemetry.ServiceTelemetryWebhooks(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicServiceTelemetry(configuration, webhooks))
	}
}

func getServiceTelemetry(repository ServiceTelemetryRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		configuration, err := repository.ServiceTelemetry(
			request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"),
		)
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		webhooks, err := repository.ServiceTelemetryWebhooks(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicServiceTelemetry(configuration, webhooks))
	}
}

func updateServiceTelemetryPublicAccess(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
		PublicHostname    string `json:"publicHostname"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := access.IdentityFromContext(request.Context())
		if !ok {
			writeAPIError(response, http.StatusForbidden, "access_identity_required", "Cloudflare Access identity is required")
			return
		}
		if !requireJSONContentType(response, request) {
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumServiceTelemetryRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var body requestBody
		if err := decoder.Decode(&body); err != nil || requireJSONEnd(decoder) != nil || body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_service_telemetry", "Service telemetry public access fields are invalid")
			return
		}
		if body.PublicHostname != "" {
			normalized, err := publichostname.Normalize(body.PublicHostname)
			if err != nil {
				writeAPIError(response, http.StatusBadRequest, "invalid_public_hostname", err.Error())
				return
			}
			body.PublicHostname = normalized
		}
		_, auditID, correlationID, err := createRequestIDs()
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to allocate telemetry mutation identifiers")
			return
		}
		configuration, err := config.serviceTelemetry.UpdateServiceTelemetryPublicAccess(request.Context(), state.UpdateServiceSentryPublicAccess{
			ID: request.PathValue("serviceID"), ProjectID: request.PathValue("projectID"),
			PublicHostname: body.PublicHostname, ExpectedUpdatedMillis: body.ExpectedUpdatedAt,
			AuditEventID: auditID, ActorKind: "access", ActorID: identity.Subject, ActorEmail: identity.Email,
			RequestCorrelationID: correlationID, UpdatedAtMillis: config.now().UnixMilli(),
		})
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		webhooks, err := config.serviceTelemetry.ServiceTelemetryWebhooks(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicServiceTelemetry(configuration, webhooks))
	}
}

func rotateServiceArtifactToken(repository ServiceTelemetryRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		token, err := repository.RotateServiceArtifactToken(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"))
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]string{"authToken": token})
	}
}

func createServiceTelemetryWebhook(repository ServiceTelemetryRepository) http.HandlerFunc {
	type body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if !requireJSONContentType(response, request) {
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumServiceTelemetryRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var input body
		if decoder.Decode(&input) != nil || requireJSONEnd(decoder) != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_webhook", "Webhook fields are invalid")
			return
		}
		webhook, secret, err := repository.CreateServiceTelemetryWebhook(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"), input.URL, input.Events)
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		value := publicServiceTelemetryWebhook(webhook)
		writeJSON(response, http.StatusCreated, struct {
			serviceTelemetryWebhookResponse
			Secret string `json:"secret"`
		}{value, secret})
	}
}

func deleteServiceTelemetryWebhook(repository ServiceTelemetryRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteServiceTelemetryWebhook(request.Context(), request.PathValue("projectID"), request.PathValue("serviceID"), request.PathValue("webhookID")); err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func serviceTelemetryConsole(repository ServiceTelemetryRepository) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		forwarded := request.Clone(request.Context())
		forwarded.URL.Path = "/" + request.PathValue("path")
		forwarded.URL.RawPath = ""
		forwarded.Header = request.Header.Clone()
		forwarded.Header.Del("Authorization")
		forwarded.Header.Del("Cookie")
		forwarded.Header.Del("Cf-Access-Jwt-Assertion")
		repository.ServeServiceTelemetry(response, forwarded, request.PathValue("projectID"), request.PathValue("serviceID"))
	})
}

func publicServiceTelemetry(configuration telemetry.ServiceConfiguration, webhooks []state.ServiceTelemetryWebhook) serviceTelemetryResponse {
	items := make([]serviceTelemetryWebhookResponse, 0, len(webhooks))
	for _, webhook := range webhooks {
		items = append(items, publicServiceTelemetryWebhook(webhook))
	}
	return serviceTelemetryResponse{
		ServiceID: configuration.ServiceID, InternalHostname: configuration.InternalHostname,
		InternalDSN: configuration.InternalDSN, InternalOTLPEndpoint: configuration.InternalOTLPEndpoint,
		PublicHostname: configuration.PublicHostname, PublicDSN: configuration.PublicDSN,
		BrowserTunnelPath: configuration.BrowserTunnelPath,
		UpdatedAt:         configuration.UpdatedAt,
		Webhooks:          items,
	}
}

func publicServiceTelemetryWebhook(webhook state.ServiceTelemetryWebhook) serviceTelemetryWebhookResponse {
	return serviceTelemetryWebhookResponse{ID: webhook.ID, URL: webhook.URL, Events: webhook.EventTypes, Enabled: webhook.Enabled, CreatedAt: webhook.CreatedAtMillis, UpdatedAt: webhook.UpdatedAtMillis}
}

func writeServiceTelemetryError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrServiceNotFound):
		writeAPIError(response, http.StatusNotFound, "service_not_found", "Service was not found")
	case errors.Is(err, state.ErrServiceChanged), errors.Is(err, state.ErrHostnameInUse), errors.Is(err, state.ErrMetricChartChanged):
		writeAPIError(response, http.StatusConflict, "service_telemetry_conflict", err.Error())
	case errors.Is(err, state.ErrCertificateCoverage):
		writeAPIError(response, http.StatusUnprocessableEntity, "certificate_coverage", err.Error())
	case errors.Is(err, state.ErrMetricChartLimit):
		writeAPIError(response, http.StatusConflict, "metric_chart_limit", err.Error())
	case errors.Is(err, state.ErrMetricChartNotFound):
		writeAPIError(response, http.StatusNotFound, "metric_chart_not_found", err.Error())
	case errors.Is(err, state.ErrMetricScopeNotFound):
		writeAPIError(response, http.StatusNotFound, "metric_scope_not_found", err.Error())
	case errors.Is(err, state.ErrServiceTelemetryWebhookInvalid):
		writeAPIError(response, http.StatusBadRequest, "invalid_webhook", err.Error())
	case errors.Is(err, state.ErrServiceTelemetryTunnelPathInvalid):
		writeAPIError(response, http.StatusBadRequest, "invalid_browser_tunnel", err.Error())
	case errors.Is(err, state.ErrServiceTelemetryTunnelNeedsDomain):
		writeAPIError(response, http.StatusBadRequest, "public_telemetry_required", err.Error())
	case errors.Is(err, state.ErrServiceTelemetryWebhookNotFound):
		writeAPIError(response, http.StatusNotFound, "webhook_not_found", err.Error())
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage service telemetry")
	}
}
