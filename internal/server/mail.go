package server

import (
	"errors"
	"net/http"

	"github.com/iivankin/platformd/internal/mailer"
	"github.com/iivankin/platformd/internal/state"
)

func registerMailRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/settings/mail", getMailSettings(config))
	mux.HandleFunc("PUT /api/v1/settings/mail/smtp", putSMTPSettings(config))
	mux.HandleFunc("POST /api/v1/settings/mail/test", testSMTPSettings(config))
	mux.HandleFunc("POST /api/v1/settings/mail/error-alerts", createMailErrorAlert(config))
	mux.HandleFunc("PUT /api/v1/settings/mail/error-alerts/{alertID}", updateMailErrorAlert(config))
	mux.HandleFunc("DELETE /api/v1/settings/mail/error-alerts/{alertID}", deleteMailErrorAlert(config))
	mux.HandleFunc("POST /api/v1/settings/mail/metric-alerts", createMailMetricAlert(config))
	mux.HandleFunc("PUT /api/v1/settings/mail/metric-alerts/{alertID}", updateMailMetricAlert(config))
	mux.HandleFunc("DELETE /api/v1/settings/mail/metric-alerts/{alertID}", deleteMailMetricAlert(config))
}

func getMailSettings(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		settings, err := config.mailer.Settings(request.Context())
		if writeMailError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicMailSettings(settings))
	}
}

func putSMTPSettings(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		FromAddress string `json:"fromAddress"`
		FromName    string `json:"fromName"`
		Encryption  string `json:"encryption"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		var body requestBody
		if !decodeInstallationSettingsJSON(response, request, &body) {
			return
		}
		password := []byte(body.Password)
		body.Password = ""
		defer clear(password)
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs()
		if err != nil {
			writeAPIError(response, http.StatusInternalServerError, "smtp_configure_failed", "Unable to allocate request IDs")
			return
		}
		settings, err := config.mailer.ConfigureSMTP(request.Context(), mailer.SMTPInput{
			Host: body.Host, Port: body.Port, Username: body.Username, Password: password,
			FromAddress: body.FromAddress, FromName: body.FromName, Encryption: body.Encryption,
		}, mailer.Mutation{
			AuditEventID: auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
			CorrelationID: requestID, UpdatedAtMillis: timestamp.UnixMilli(),
		})
		if writeMailError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, settings)
	}
}

func testSMTPSettings(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		To string `json:"to"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeInstallationSettingsJSON(response, request, &body) {
			return
		}
		if err := config.mailer.TestSMTP(request.Context(), body.To); writeMailError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"sent": true})
	}
}

func createMailErrorAlert(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		mutation, ok := mailMutation(response, request, config)
		if !ok {
			return
		}
		alert, ok := decodeMailErrorAlert(response, request)
		if !ok {
			return
		}
		created, err := config.mailer.CreateErrorAlert(request.Context(), alert, mutation)
		if writeMailError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/settings/mail/error-alerts/"+created.ID)
		writeJSON(response, http.StatusCreated, publicMailErrorAlert(created))
	}
}

func updateMailErrorAlert(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		mutation, ok := mailMutation(response, request, config)
		if !ok {
			return
		}
		alert, ok := decodeMailErrorAlert(response, request)
		if !ok {
			return
		}
		alert.ID = request.PathValue("alertID")
		updated, err := config.mailer.UpdateErrorAlert(request.Context(), alert, mutation)
		if writeMailError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicMailErrorAlert(updated))
	}
}

func deleteMailErrorAlert(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		mutation, ok := mailMutation(response, request, config)
		if !ok {
			return
		}
		if err := config.mailer.DeleteErrorAlert(request.Context(), request.PathValue("alertID"), mutation); writeMailError(response, err) {
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func createMailMetricAlert(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		mutation, ok := mailMutation(response, request, config)
		if !ok {
			return
		}
		alert, ok := decodeMailMetricAlert(response, request)
		if !ok {
			return
		}
		created, err := config.mailer.CreateMetricAlert(request.Context(), alert, mutation)
		if writeMailError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/settings/mail/metric-alerts/"+created.ID)
		writeJSON(response, http.StatusCreated, publicMailMetricAlert(created))
	}
}

func updateMailMetricAlert(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		mutation, ok := mailMutation(response, request, config)
		if !ok {
			return
		}
		alert, ok := decodeMailMetricAlert(response, request)
		if !ok {
			return
		}
		alert.ID = request.PathValue("alertID")
		updated, err := config.mailer.UpdateMetricAlert(request.Context(), alert, mutation)
		if writeMailError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicMailMetricAlert(updated))
	}
}

func deleteMailMetricAlert(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		mutation, ok := mailMutation(response, request, config)
		if !ok {
			return
		}
		if err := config.mailer.DeleteMetricAlert(request.Context(), request.PathValue("alertID"), mutation); writeMailError(response, err) {
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

type mailErrorAlertBody struct {
	Name       string   `json:"name"`
	Enabled    bool     `json:"enabled"`
	Recipients []string `json:"recipients"`
	EventTypes []string `json:"eventTypes"`
	ServiceIDs []string `json:"serviceIds"`
}

type mailMetricAlertBody struct {
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	Recipients    []string `json:"recipients"`
	Scope         string   `json:"scope"`
	ProjectID     string   `json:"projectId"`
	ServiceID     string   `json:"serviceId"`
	SQL           string   `json:"sql"`
	Operator      string   `json:"operator"`
	Threshold     float64  `json:"threshold"`
	WindowSeconds int      `json:"windowSeconds"`
}

func decodeMailErrorAlert(response http.ResponseWriter, request *http.Request) (state.MailErrorAlert, bool) {
	var body mailErrorAlertBody
	if !decodeInstallationSettingsJSON(response, request, &body) {
		return state.MailErrorAlert{}, false
	}
	return state.MailErrorAlert{
		Name: body.Name, Enabled: body.Enabled, Recipients: body.Recipients,
		EventTypes: body.EventTypes, ServiceIDs: body.ServiceIDs,
	}, true
}

func decodeMailMetricAlert(response http.ResponseWriter, request *http.Request) (state.MailMetricAlert, bool) {
	var body mailMetricAlertBody
	if !decodeInstallationSettingsJSON(response, request, &body) {
		return state.MailMetricAlert{}, false
	}
	return state.MailMetricAlert{
		Name: body.Name, Enabled: body.Enabled, Recipients: body.Recipients,
		Scope:         state.MetricScope{Kind: body.Scope, ProjectID: body.ProjectID, ServiceID: body.ServiceID},
		SQL:           body.SQL,
		Operator:      body.Operator,
		Threshold:     body.Threshold,
		WindowSeconds: body.WindowSeconds,
	}, true
}

func mailMutation(response http.ResponseWriter, request *http.Request, config handlerConfig) (mailer.Mutation, bool) {
	identity, ok := requireAccessIdentity(response, request)
	if !ok {
		return mailer.Mutation{}, false
	}
	_, auditID, requestID, err := createRequestIDs()
	if err != nil {
		writeAPIError(response, http.StatusInternalServerError, "mail_alert_failed", "Unable to allocate request IDs")
		return mailer.Mutation{}, false
	}
	response.Header().Set("X-Request-ID", requestID)
	return mailer.Mutation{
		AuditEventID: auditID, ActorID: identity.Subject, ActorEmail: identity.Email,
		CorrelationID: requestID, UpdatedAtMillis: config.now().UnixMilli(),
	}, true
}

func publicMailSettings(settings mailer.Settings) map[string]any {
	errorAlerts := make([]map[string]any, 0, len(settings.ErrorAlerts))
	for _, alert := range settings.ErrorAlerts {
		errorAlerts = append(errorAlerts, publicMailErrorAlert(alert))
	}
	metricAlerts := make([]map[string]any, 0, len(settings.MetricAlerts))
	for _, alert := range settings.MetricAlerts {
		metricAlerts = append(metricAlerts, publicMailMetricAlert(alert))
	}
	services := make([]map[string]any, 0, len(settings.Services))
	for _, service := range settings.Services {
		services = append(services, map[string]any{
			"id": service.ID, "name": service.Name, "projectId": service.ProjectID, "projectName": service.ProjectName,
		})
	}
	return map[string]any{
		"smtp": settings.SMTP, "errorAlerts": errorAlerts, "metricAlerts": metricAlerts, "services": services,
	}
}

func publicMailErrorAlert(alert state.MailErrorAlert) map[string]any {
	return map[string]any{
		"id": alert.ID, "name": alert.Name, "enabled": alert.Enabled, "recipients": alert.Recipients,
		"eventTypes": alert.EventTypes, "serviceIds": alert.ServiceIDs,
		"createdAt": alert.CreatedAtMillis, "updatedAt": alert.UpdatedAtMillis,
	}
}

func publicMailMetricAlert(alert state.MailMetricAlert) map[string]any {
	payload := map[string]any{
		"id": alert.ID, "name": alert.Name, "enabled": alert.Enabled, "recipients": alert.Recipients,
		"scope": alert.Scope.Kind, "sql": alert.SQL, "operator": alert.Operator, "threshold": alert.Threshold,
		"windowSeconds": alert.WindowSeconds, "firing": alert.Firing,
		"createdAt": alert.CreatedAtMillis, "updatedAt": alert.UpdatedAtMillis,
	}
	if alert.Scope.ProjectID != "" {
		payload["projectId"] = alert.Scope.ProjectID
	}
	if alert.Scope.ServiceID != "" {
		payload["serviceId"] = alert.Scope.ServiceID
	}
	if alert.LastValue != nil {
		payload["lastValue"] = *alert.LastValue
	}
	if alert.LastEvaluatedAt > 0 {
		payload["lastEvaluatedAt"] = alert.LastEvaluatedAt
	}
	return payload
}

func writeMailError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, state.ErrSMTPNotConfigured):
		writeAPIError(response, http.StatusBadRequest, "smtp_not_configured", "SMTP is not configured")
	case errors.Is(err, state.ErrMailAlertNotFound):
		writeAPIError(response, http.StatusNotFound, "mail_alert_not_found", "Mail alert not found")
	case errors.Is(err, state.ErrMailAlertLimit):
		writeAPIError(response, http.StatusConflict, "mail_alert_limit", "Mail alert limit reached")
	case errors.Is(err, state.ErrMailAlertServiceMissing):
		writeAPIError(response, http.StatusBadRequest, "mail_alert_service_missing", "Mail alert references a missing service")
	case errors.Is(err, state.ErrMetricScopeNotFound):
		writeAPIError(response, http.StatusNotFound, "metric_scope_not_found", "Metric scope not found")
	default:
		writeAPIError(response, http.StatusBadRequest, "mail_settings_failed", err.Error())
	}
	return true
}
