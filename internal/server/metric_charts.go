package server

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

type metricScopeFromRequest func(*http.Request) state.MetricScope

type metricChartResponse struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	SQL           string `json:"sql"`
	Visualization string `json:"visualization"`
	Legend        string `json:"legend"`
	Unit          string `json:"unit,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type metricChartRequest struct {
	Title             string `json:"title"`
	SQL               string `json:"sql"`
	Visualization     string `json:"visualization"`
	Legend            string `json:"legend"`
	Unit              string `json:"unit"`
	ExpectedUpdatedAt int64  `json:"expectedUpdatedAt,omitempty"`
}

func registerMetricScopeRoutes(
	mux *http.ServeMux,
	repository ServiceTelemetryRepository,
	base string,
	scope metricScopeFromRequest,
) {
	mux.HandleFunc("GET "+base+"/metrics/catalog", getMetricCatalog(repository, scope))
	mux.HandleFunc("POST "+base+"/metrics/query", queryMetrics(repository, scope))
	mux.HandleFunc("GET "+base+"/metric-charts", getMetricCharts(repository, scope))
	mux.HandleFunc("POST "+base+"/metric-charts", createMetricChart(repository, scope))
	mux.HandleFunc("PUT "+base+"/metric-charts/{chartID}", updateMetricChart(repository, scope))
	mux.HandleFunc("DELETE "+base+"/metric-charts/{chartID}", deleteMetricChart(repository, scope))
}

func serviceMetricScope(request *http.Request) state.MetricScope {
	return state.MetricScope{
		Kind: state.MetricScopeService, ProjectID: request.PathValue("projectID"), ServiceID: request.PathValue("serviceID"),
	}
}

func projectMetricScope(request *http.Request) state.MetricScope {
	return state.MetricScope{Kind: state.MetricScopeProject, ProjectID: request.PathValue("projectID")}
}

func installationMetricScope(*http.Request) state.MetricScope {
	return state.MetricScope{Kind: state.MetricScopeInstallation}
}

func getMetricCatalog(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		result, err := repository.QueryMetricScope(request.Context(), scope(request), "catalog", nil)
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeMetricScopeResponse(response, result.StatusCode, result.ContentType, result.Body)
	}
}

func queryMetrics(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if !requireJSONContentType(response, request) {
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumServiceTelemetryRequestBytes)
		body, err := io.ReadAll(request.Body)
		if err != nil || !json.Valid(body) {
			writeAPIError(response, http.StatusBadRequest, "invalid_metric_query", "Metric SQL query is invalid")
			return
		}
		result, err := repository.QueryMetricScope(request.Context(), scope(request), "query", json.RawMessage(body))
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeMetricScopeResponse(response, result.StatusCode, result.ContentType, result.Body)
	}
}

func getMetricCharts(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		charts, err := repository.MetricCharts(request.Context(), scope(request))
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		items := make([]metricChartResponse, 0, len(charts))
		for _, chart := range charts {
			items = append(items, publicMetricChart(chart))
		}
		writeJSON(response, http.StatusOK, items)
	}
}

func createMetricChart(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		body, ok := decodeMetricChartRequest(response, request)
		if !ok {
			return
		}
		chart, valid := metricChartFromRequest(body)
		if !valid || body.ExpectedUpdatedAt != 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_metric_chart", "Metric chart fields are invalid")
			return
		}
		chart.Scope = scope(request)
		chart, err := repository.CreateMetricChart(request.Context(), chart)
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, publicMetricChart(chart))
	}
}

func updateMetricChart(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		body, ok := decodeMetricChartRequest(response, request)
		if !ok {
			return
		}
		chart, valid := metricChartFromRequest(body)
		if !valid || body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_metric_chart", "Metric chart fields are invalid")
			return
		}
		chart.ID = request.PathValue("chartID")
		chart.Scope = scope(request)
		chart, err := repository.UpdateMetricChart(request.Context(), chart, body.ExpectedUpdatedAt)
		if err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicMetricChart(chart))
	}
}

func deleteMetricChart(repository ServiceTelemetryRepository, scope metricScopeFromRequest) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteMetricChart(request.Context(), scope(request), request.PathValue("chartID")); err != nil {
			writeServiceTelemetryError(response, err)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
}

func decodeMetricChartRequest(response http.ResponseWriter, request *http.Request) (metricChartRequest, bool) {
	if !requireJSONContentType(response, request) {
		return metricChartRequest{}, false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumServiceTelemetryRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body metricChartRequest
	if decoder.Decode(&body) != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_metric_chart", "Metric chart fields are invalid")
		return metricChartRequest{}, false
	}
	return body, true
}

func requireJSONContentType(response http.ResponseWriter, request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json")
		return false
	}
	return true
}

func metricChartFromRequest(body metricChartRequest) (state.MetricChart, bool) {
	chart := state.MetricChart{
		Title: strings.TrimSpace(body.Title), SQL: strings.TrimSpace(body.SQL), Visualization: body.Visualization,
		Legend: strings.TrimSpace(body.Legend), Unit: strings.TrimSpace(body.Unit),
	}
	valid := chart.Title != "" && len(chart.Title) <= 80 && chart.SQL != "" && len(chart.SQL) <= 16<<10 &&
		validMetricChartVisualization(chart.Visualization) && len(chart.Legend) <= 80 && len(chart.Unit) <= 32
	return chart, valid
}

func validMetricChartVisualization(value string) bool {
	return value == "line" || value == "area" || value == "bar" || value == "value"
}

func publicMetricChart(chart state.MetricChart) metricChartResponse {
	return metricChartResponse{
		ID: chart.ID, Title: chart.Title, SQL: chart.SQL, Visualization: chart.Visualization,
		Legend: chart.Legend, Unit: chart.Unit, CreatedAt: chart.CreatedAtMillis, UpdatedAt: chart.UpdatedAtMillis,
	}
}

func writeMetricScopeResponse(response http.ResponseWriter, status int, contentType string, body []byte) {
	if contentType == "" {
		contentType = "application/json"
	}
	response.Header().Set("Content-Type", contentType)
	response.WriteHeader(status)
	_, _ = response.Write(body)
}
