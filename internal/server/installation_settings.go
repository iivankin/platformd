package server

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/iivankin/platformd/internal/installationsettings"
	"github.com/iivankin/platformd/internal/state"
)

const maximumInstallationSettingsBytes = 1 << 20

type installationSettingsResponse struct {
	InstallationID     string                            `json:"installationId"`
	AdminHostname      string                            `json:"adminHostname"`
	AutomationHostname string                            `json:"automationHostname"`
	AccessTeamDomain   string                            `json:"accessTeamDomain"`
	AccessAudience     string                            `json:"accessAudience"`
	Certificates       []installationCertificateResponse `json:"certificates"`
}

type installationCertificateResponse struct {
	ID        string   `json:"id"`
	DNSNames  []string `json:"dnsNames"`
	CreatedAt int64    `json:"createdAt"`
}

func registerInstallationSettingsRoutes(mux *http.ServeMux, config handlerConfig) {
	mux.HandleFunc("GET /api/v1/settings", getInstallationSettings(config))
	mux.HandleFunc("PUT /api/v1/settings/automation-hostname", setAutomationHostname(config))
	mux.HandleFunc("POST /api/v1/settings/origin-certificates", addOriginCertificate(config))
	mux.HandleFunc("PUT /api/v1/settings/origin-certificates/{certificateID}", replaceOriginCertificate(config))
	mux.HandleFunc("DELETE /api/v1/settings/origin-certificates/{certificateID}", deleteOriginCertificate(config))
}

func getInstallationSettings(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		settings, err := config.installationSettings.Settings(request.Context())
		if writeInstallationSettingsError(response, err) {
			return
		}
		writeJSON(response, http.StatusOK, publicInstallationSettings(settings))
	}
}

func setAutomationHostname(config handlerConfig) http.HandlerFunc {
	type requestBody struct {
		Hostname string `json:"hostname"`
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
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeInstallationSettingsError(response, err)
			return
		}
		settings, err := config.installationSettings.SetAutomationHostname(request.Context(), body.Hostname,
			settingsMutation(identity.Subject, identity.Email, auditID, requestID, timestamp))
		if writeInstallationSettingsError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicInstallationSettings(settings))
	}
}

func addOriginCertificate(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		certificatePEM, privateKey, ok := decodeCertificateBody(response, request)
		if !ok {
			return
		}
		defer clear(privateKey)
		timestamp := config.now()
		resourceID, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeInstallationSettingsError(response, err)
			return
		}
		settings, err := config.installationSettings.AddCertificate(request.Context(), installationsettings.CertificateMutation{
			Mutation:       settingsMutationWithResource(identity.Subject, identity.Email, resourceID, auditID, requestID, timestamp),
			CertificatePEM: certificatePEM, PrivateKeyPEM: privateKey,
		})
		if writeInstallationSettingsError(response, err) {
			return
		}
		response.Header().Set("Location", "/api/v1/settings/origin-certificates/"+resourceID)
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusCreated, publicInstallationSettings(settings))
	}
}

func replaceOriginCertificate(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		certificatePEM, privateKey, ok := decodeCertificateBody(response, request)
		if !ok {
			return
		}
		defer clear(privateKey)
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeInstallationSettingsError(response, err)
			return
		}
		settings, err := config.installationSettings.ReplaceCertificate(request.Context(), request.PathValue("certificateID"),
			installationsettings.CertificateMutation{
				Mutation:       settingsMutation(identity.Subject, identity.Email, auditID, requestID, timestamp),
				CertificatePEM: certificatePEM, PrivateKeyPEM: privateKey,
			})
		if writeInstallationSettingsError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicInstallationSettings(settings))
	}
}

func deleteOriginCertificate(config handlerConfig) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		identity, ok := requireAccessIdentity(response, request)
		if !ok {
			return
		}
		timestamp := config.now()
		_, auditID, requestID, err := createRequestIDs(timestamp, config.random)
		if err != nil {
			writeInstallationSettingsError(response, err)
			return
		}
		settings, err := config.installationSettings.DeleteCertificate(request.Context(), request.PathValue("certificateID"),
			settingsMutation(identity.Subject, identity.Email, auditID, requestID, timestamp))
		if writeInstallationSettingsError(response, err) {
			return
		}
		response.Header().Set("X-Request-ID", requestID)
		writeJSON(response, http.StatusOK, publicInstallationSettings(settings))
	}
}

func decodeCertificateBody(response http.ResponseWriter, request *http.Request) (string, []byte, bool) {
	type requestBody struct {
		CertificatePEM string `json:"certificatePem"`
		PrivateKeyPEM  string `json:"privateKeyPem"`
	}
	var body requestBody
	if !decodeInstallationSettingsJSON(response, request, &body) {
		return "", nil, false
	}
	privateKey := []byte(body.PrivateKeyPEM)
	body.PrivateKeyPEM = ""
	return body.CertificatePEM, privateKey, true
}

func decodeInstallationSettingsJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumInstallationSettingsBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "Request body contains invalid installation settings fields")
		return false
	}
	return true
}

func settingsMutation(subject, email, auditID, requestID string, timestamp time.Time) installationsettings.Mutation {
	return settingsMutationWithResource(subject, email, "", auditID, requestID, timestamp)
}

func settingsMutationWithResource(subject, email, resourceID, auditID, requestID string, timestamp time.Time) installationsettings.Mutation {
	return installationsettings.Mutation{
		ResourceID: resourceID, AuditEventID: auditID, CorrelationID: requestID,
		Actor: installationsettings.Actor{ID: subject, Email: email}, Timestamp: timestamp,
	}
}

func publicInstallationSettings(settings installationsettings.Settings) installationSettingsResponse {
	certificates := make([]installationCertificateResponse, 0, len(settings.Certificates))
	for _, certificate := range settings.Certificates {
		certificates = append(certificates, installationCertificateResponse{
			ID: certificate.ID, DNSNames: certificate.DNSNames, CreatedAt: certificate.CreatedAtMillis,
		})
	}
	return installationSettingsResponse{
		InstallationID: settings.InstallationID, AdminHostname: settings.AdminHostname,
		AutomationHostname: settings.AutomationHostname, AccessTeamDomain: settings.AccessTeamDomain,
		AccessAudience: settings.AccessAudience, Certificates: certificates,
	}
}

func writeInstallationSettingsError(response http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var coverage *state.OriginCertificateCoverageError
	switch {
	case errors.Is(err, state.ErrOriginCertificateNotFound):
		writeAPIError(response, http.StatusNotFound, "origin_certificate_not_found", "Origin certificate not found")
	case errors.As(err, &coverage):
		writeJSON(response, http.StatusConflict, map[string]any{"error": map[string]any{
			"code": "certificate_coverage", "message": coverage.Error(), "dependentHostnames": coverage.Hostnames,
		}})
	case errors.Is(err, state.ErrHostnameInUse):
		writeAPIError(response, http.StatusConflict, "hostname_in_use", "Hostname is already assigned to another public role")
	default:
		writeAPIError(response, http.StatusBadRequest, "installation_settings_failed", err.Error())
	}
	return true
}
