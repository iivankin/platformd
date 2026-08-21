package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/firewall"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
)

const maximumAnalyticsRequestBytes = 64 << 10

type AnalyticsRepository interface {
	Project(context.Context, string) (state.ProjectSummary, error)
	ProjectServiceHostnames(context.Context, string) ([]string, error)
	AnalyticsTrackers(context.Context, string) ([]state.AnalyticsTracker, error)
	AnalyticsTracker(context.Context, string, string) (state.AnalyticsTracker, error)
	CreateAnalyticsTracker(context.Context, state.AnalyticsTracker) (state.AnalyticsTracker, error)
	UpdateAnalyticsTracker(context.Context, state.AnalyticsTracker, int64) (state.AnalyticsTracker, error)
	DeleteAnalyticsTracker(context.Context, string, string) error
	AnalyticsGoals(context.Context, string, string) ([]state.AnalyticsGoal, error)
	CreateAnalyticsGoal(context.Context, string, state.AnalyticsGoal) (state.AnalyticsGoal, error)
	UpdateAnalyticsGoal(context.Context, string, state.AnalyticsGoal, int64) (state.AnalyticsGoal, error)
	DeleteAnalyticsGoal(context.Context, string, string, string) error
	AnalyticsFunnels(context.Context, string, string) ([]state.AnalyticsFunnel, error)
	CreateAnalyticsFunnel(context.Context, string, state.AnalyticsFunnel) (state.AnalyticsFunnel, error)
	UpdateAnalyticsFunnel(context.Context, string, state.AnalyticsFunnel, int64) (state.AnalyticsFunnel, error)
	DeleteAnalyticsFunnel(context.Context, string, string, string) error
	AnalyticsFlags(context.Context, string, string) ([]state.AnalyticsFlag, error)
	CreateAnalyticsFlag(context.Context, string, state.AnalyticsFlag) (state.AnalyticsFlag, error)
	UpdateAnalyticsFlag(context.Context, string, state.AnalyticsFlag, int64) (state.AnalyticsFlag, error)
	DeleteAnalyticsFlag(context.Context, string, string, string) error
	AnalyticsExperiments(context.Context, string, string) ([]state.AnalyticsExperiment, error)
	CreateAnalyticsExperiment(context.Context, string, state.AnalyticsExperiment) (state.AnalyticsExperiment, error)
	StopAnalyticsExperiment(context.Context, string, string, string, int64) (state.AnalyticsExperiment, error)
	ShipAnalyticsExperiment(context.Context, string, string, string, string, int64) (state.AnalyticsExperiment, error)
	AnalyticsCharts(context.Context, string, string) ([]state.AnalyticsChart, error)
	CreateAnalyticsChart(context.Context, string, state.AnalyticsChart) (state.AnalyticsChart, error)
	UpdateAnalyticsChart(context.Context, string, state.AnalyticsChart, int64) (state.AnalyticsChart, error)
	DeleteAnalyticsChart(context.Context, string, string, string) error
	QueryAnalytics(context.Context, string, string, json.RawMessage) (int, string, []byte, error)
	TrackersMatchingService(context.Context, string, string) ([]state.AnalyticsTracker, error)
}

type analyticsTrackerResponse struct {
	ID                string   `json:"id"`
	ProjectID         string   `json:"projectId"`
	Name              string   `json:"name"`
	RootDomain        string   `json:"rootDomain"`
	InternalHostname  string   `json:"internalHostname"`
	InternalOFREPURL  string   `json:"internalOfrepUrl"`
	MatchingHostnames []string `json:"matchingHostnames"`
	CreatedAt         int64    `json:"createdAt"`
	UpdatedAt         int64    `json:"updatedAt"`
}

type analyticsGoalResponse struct {
	ID        string `json:"id"`
	TrackerID string `json:"trackerId"`
	Name      string `json:"name"`
	Action    string `json:"actionType"`
	Value     string `json:"actionValue"`
	Hostname  string `json:"hostname,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type analyticsFunnelResponse struct {
	ID          string                      `json:"id"`
	TrackerID   string                      `json:"trackerId"`
	Name        string                      `json:"name"`
	WindowValue int                         `json:"windowValue"`
	WindowUnit  string                      `json:"windowUnit"`
	Steps       []state.AnalyticsFunnelStep `json:"steps"`
	CreatedAt   int64                       `json:"createdAt"`
	UpdatedAt   int64                       `json:"updatedAt"`
}

type analyticsFlagResponse struct {
	ID          string                       `json:"id"`
	TrackerID   string                       `json:"trackerId"`
	Key         string                       `json:"key"`
	Description string                       `json:"description,omitempty"`
	Type        string                       `json:"type"`
	Enabled     bool                         `json:"enabled"`
	Variants    []state.AnalyticsFlagVariant `json:"variants"`
	Payload     json.RawMessage              `json:"payload"`
	Targeting   json.RawMessage              `json:"targeting"`
	CreatedAt   int64                        `json:"createdAt"`
	UpdatedAt   int64                        `json:"updatedAt"`
}

type analyticsExperimentResponse struct {
	ID             string                          `json:"id"`
	FlagID         string                          `json:"flagId"`
	TrackerID      string                          `json:"trackerId"`
	ControlVariant string                          `json:"controlVariant"`
	Metric         state.AnalyticsExperimentMetric `json:"metric"`
	WindowValue    int                             `json:"windowValue"`
	WindowUnit     string                          `json:"windowUnit"`
	StartedAt      int64                           `json:"startedAt"`
	EndedAt        int64                           `json:"endedAt,omitempty"`
	CreatedAt      int64                           `json:"createdAt"`
	UpdatedAt      int64                           `json:"updatedAt"`
}

type analyticsChartResponse struct {
	ID            string `json:"id"`
	TrackerID     string `json:"trackerId"`
	Title         string `json:"title"`
	SQL           string `json:"sql"`
	Visualization string `json:"visualization"`
	Legend        string `json:"legend"`
	Unit          string `json:"unit,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

func WithAnalytics(repository AnalyticsRepository) Option {
	return func(config *handlerConfig) {
		config.analytics = repository
	}
}

func registerAnalyticsRoutes(mux *http.ServeMux, repository AnalyticsRepository) {
	base := "/api/v1/projects/{projectID}/telemetry/analytics"
	mux.HandleFunc("GET "+base+"/trackers", listAnalyticsTrackers(repository))
	mux.HandleFunc("POST "+base+"/trackers", createAnalyticsTracker(repository))
	mux.HandleFunc("GET "+base+"/trackers/{trackerID}", getAnalyticsTracker(repository))
	mux.HandleFunc("PUT "+base+"/trackers/{trackerID}", updateAnalyticsTracker(repository))
	mux.HandleFunc("DELETE "+base+"/trackers/{trackerID}", deleteAnalyticsTracker(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/query", queryAnalytics(repository))
	mux.HandleFunc("GET "+base+"/trackers/{trackerID}/goals", listAnalyticsGoals(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/goals", createAnalyticsGoal(repository))
	mux.HandleFunc("PUT "+base+"/trackers/{trackerID}/goals/{goalID}", updateAnalyticsGoal(repository))
	mux.HandleFunc("DELETE "+base+"/trackers/{trackerID}/goals/{goalID}", deleteAnalyticsGoal(repository))
	mux.HandleFunc("GET "+base+"/trackers/{trackerID}/funnels", listAnalyticsFunnels(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/funnels", createAnalyticsFunnel(repository))
	mux.HandleFunc("PUT "+base+"/trackers/{trackerID}/funnels/{funnelID}", updateAnalyticsFunnel(repository))
	mux.HandleFunc("DELETE "+base+"/trackers/{trackerID}/funnels/{funnelID}", deleteAnalyticsFunnel(repository))
	mux.HandleFunc("GET "+base+"/trackers/{trackerID}/flags", listAnalyticsFlags(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/flags", createAnalyticsFlag(repository))
	mux.HandleFunc("PUT "+base+"/trackers/{trackerID}/flags/{flagID}", updateAnalyticsFlag(repository))
	mux.HandleFunc("DELETE "+base+"/trackers/{trackerID}/flags/{flagID}", deleteAnalyticsFlag(repository))
	mux.HandleFunc("GET "+base+"/trackers/{trackerID}/experiments", listAnalyticsExperiments(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/experiments", createAnalyticsExperiment(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/experiments/{experimentID}/stop", stopAnalyticsExperiment(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/experiments/{experimentID}/ship", shipAnalyticsExperiment(repository))
	mux.HandleFunc("GET "+base+"/trackers/{trackerID}/charts", listAnalyticsCharts(repository))
	mux.HandleFunc("POST "+base+"/trackers/{trackerID}/charts", createAnalyticsChart(repository))
	mux.HandleFunc("PUT "+base+"/trackers/{trackerID}/charts/{chartID}", updateAnalyticsChart(repository))
	mux.HandleFunc("DELETE "+base+"/trackers/{trackerID}/charts/{chartID}", deleteAnalyticsChart(repository))
}

func listAnalyticsTrackers(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		projectID := request.PathValue("projectID")
		trackers, err := repository.AnalyticsTrackers(request.Context(), projectID)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload, err := publicTrackers(request.Context(), repository, projectID, trackers)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func getAnalyticsTracker(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		tracker, err := repository.AnalyticsTracker(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload, err := publicTrackers(request.Context(), repository, tracker.ProjectID, []state.AnalyticsTracker{tracker})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, payload[0])
	}
}

func createAnalyticsTracker(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Name       string `json:"name"`
		RootDomain string `json:"rootDomain"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		tracker, err := repository.CreateAnalyticsTracker(request.Context(), state.AnalyticsTracker{
			ProjectID: request.PathValue("projectID"), Name: body.Name, RootDomain: body.RootDomain,
		})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload, err := publicTrackers(request.Context(), repository, tracker.ProjectID, []state.AnalyticsTracker{tracker})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, payload[0])
	}
}

func updateAnalyticsTracker(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Name              string `json:"name"`
		RootDomain        string `json:"rootDomain"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		tracker, err := repository.UpdateAnalyticsTracker(request.Context(), state.AnalyticsTracker{
			ID: request.PathValue("trackerID"), ProjectID: request.PathValue("projectID"),
			Name: body.Name, RootDomain: body.RootDomain,
		}, body.ExpectedUpdatedAt)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload, err := publicTrackers(request.Context(), repository, tracker.ProjectID, []state.AnalyticsTracker{tracker})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, payload[0])
	}
}

func deleteAnalyticsTracker(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteAnalyticsTracker(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID")); err != nil {
			writeAnalyticsError(response, err)
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusNoContent)
	}
}

func queryAnalytics(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, maximumAnalyticsRequestBytes)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		status, contentType, payload, err := repository.QueryAnalytics(
			request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"), body,
		)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		if contentType == "" {
			contentType = "application/json; charset=utf-8"
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Type", contentType)
		response.WriteHeader(status)
		_, _ = response.Write(payload)
	}
}

func listAnalyticsGoals(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		goals, err := repository.AnalyticsGoals(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload := make([]analyticsGoalResponse, 0, len(goals))
		for _, goal := range goals {
			payload = append(payload, publicGoal(goal))
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func createAnalyticsGoal(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Name        string `json:"name"`
		ActionType  string `json:"actionType"`
		ActionValue string `json:"actionValue"`
		Hostname    string `json:"hostname"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		goal, err := repository.CreateAnalyticsGoal(request.Context(), request.PathValue("projectID"), state.AnalyticsGoal{
			TrackerID: request.PathValue("trackerID"), Name: body.Name, ActionType: body.ActionType,
			ActionValue: body.ActionValue, Hostname: body.Hostname,
		})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, publicGoal(goal))
	}
}

func updateAnalyticsGoal(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Name              string `json:"name"`
		ActionType        string `json:"actionType"`
		ActionValue       string `json:"actionValue"`
		Hostname          string `json:"hostname"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		goal, err := repository.UpdateAnalyticsGoal(request.Context(), request.PathValue("projectID"), state.AnalyticsGoal{
			ID: request.PathValue("goalID"), TrackerID: request.PathValue("trackerID"),
			Name: body.Name, ActionType: body.ActionType, ActionValue: body.ActionValue, Hostname: body.Hostname,
		}, body.ExpectedUpdatedAt)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicGoal(goal))
	}
}

func deleteAnalyticsGoal(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteAnalyticsGoal(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"), request.PathValue("goalID")); err != nil {
			writeAnalyticsError(response, err)
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusNoContent)
	}
}

func listAnalyticsFunnels(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		funnels, err := repository.AnalyticsFunnels(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload := make([]analyticsFunnelResponse, 0, len(funnels))
		for _, funnel := range funnels {
			payload = append(payload, publicFunnel(funnel))
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func createAnalyticsFunnel(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Name        string                      `json:"name"`
		WindowValue int                         `json:"windowValue"`
		WindowUnit  string                      `json:"windowUnit"`
		Steps       []state.AnalyticsFunnelStep `json:"steps"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.WindowValue == 0 {
			body.WindowValue = 7
		}
		if body.WindowUnit == "" {
			body.WindowUnit = "day"
		}
		funnel, err := repository.CreateAnalyticsFunnel(request.Context(), request.PathValue("projectID"), state.AnalyticsFunnel{
			TrackerID: request.PathValue("trackerID"), Name: body.Name,
			WindowValue: body.WindowValue, WindowUnit: body.WindowUnit, Steps: body.Steps,
		})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, publicFunnel(funnel))
	}
}

func updateAnalyticsFunnel(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Name              string                      `json:"name"`
		WindowValue       int                         `json:"windowValue"`
		WindowUnit        string                      `json:"windowUnit"`
		Steps             []state.AnalyticsFunnelStep `json:"steps"`
		ExpectedUpdatedAt int64                       `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		if body.WindowValue == 0 {
			body.WindowValue = 7
		}
		if body.WindowUnit == "" {
			body.WindowUnit = "day"
		}
		funnel, err := repository.UpdateAnalyticsFunnel(request.Context(), request.PathValue("projectID"), state.AnalyticsFunnel{
			ID: request.PathValue("funnelID"), TrackerID: request.PathValue("trackerID"),
			Name: body.Name, WindowValue: body.WindowValue, WindowUnit: body.WindowUnit, Steps: body.Steps,
		}, body.ExpectedUpdatedAt)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicFunnel(funnel))
	}
}

func deleteAnalyticsFunnel(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteAnalyticsFunnel(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"), request.PathValue("funnelID")); err != nil {
			writeAnalyticsError(response, err)
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusNoContent)
	}
}

func listAnalyticsFlags(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		flags, err := repository.AnalyticsFlags(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload := make([]analyticsFlagResponse, 0, len(flags))
		for _, flag := range flags {
			payload = append(payload, publicFlag(flag))
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func createAnalyticsFlag(repository AnalyticsRepository) http.HandlerFunc {
	return mutateAnalyticsFlag(repository, true)
}

func updateAnalyticsFlag(repository AnalyticsRepository) http.HandlerFunc {
	return mutateAnalyticsFlag(repository, false)
}

func mutateAnalyticsFlag(repository AnalyticsRepository, create bool) http.HandlerFunc {
	type requestBody struct {
		Key               string                       `json:"key"`
		Description       string                       `json:"description"`
		Type              string                       `json:"type"`
		Enabled           bool                         `json:"enabled"`
		Variants          []state.AnalyticsFlagVariant `json:"variants"`
		Payload           json.RawMessage              `json:"payload"`
		Targeting         json.RawMessage              `json:"targeting"`
		ExpectedUpdatedAt int64                        `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		flag := state.AnalyticsFlag{
			TrackerID: request.PathValue("trackerID"), Key: strings.TrimSpace(body.Key),
			Description: body.Description, Type: body.Type, Enabled: body.Enabled, Variants: body.Variants,
			PayloadJSON: string(body.Payload), TargetingJSON: string(body.Targeting),
		}
		var err error
		if create {
			flag, err = repository.CreateAnalyticsFlag(request.Context(), request.PathValue("projectID"), flag)
			if err != nil {
				writeAnalyticsError(response, err)
				return
			}
			writeJSON(response, http.StatusCreated, publicFlag(flag))
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		flag.ID = request.PathValue("flagID")
		flag, err = repository.UpdateAnalyticsFlag(request.Context(), request.PathValue("projectID"), flag, body.ExpectedUpdatedAt)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicFlag(flag))
	}
}

func deleteAnalyticsFlag(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteAnalyticsFlag(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"), request.PathValue("flagID")); err != nil {
			writeAnalyticsError(response, err)
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusNoContent)
	}
}

func listAnalyticsExperiments(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		experiments, err := repository.AnalyticsExperiments(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload := make([]analyticsExperimentResponse, 0, len(experiments))
		for _, experiment := range experiments {
			payload = append(payload, publicExperiment(experiment))
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func createAnalyticsExperiment(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		FlagID         string                          `json:"flagId"`
		ControlVariant string                          `json:"controlVariant"`
		Metric         state.AnalyticsExperimentMetric `json:"metric"`
		WindowValue    int                             `json:"windowValue"`
		WindowUnit     string                          `json:"windowUnit"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.WindowValue == 0 {
			body.WindowValue = 14
		}
		if body.WindowUnit == "" {
			body.WindowUnit = "day"
		}
		experiment, err := repository.CreateAnalyticsExperiment(request.Context(), request.PathValue("projectID"), state.AnalyticsExperiment{
			FlagID: body.FlagID, TrackerID: request.PathValue("trackerID"),
			ControlVariant: body.ControlVariant, Metric: body.Metric,
			WindowValue: body.WindowValue, WindowUnit: body.WindowUnit,
		})
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, publicExperiment(experiment))
	}
}

func stopAnalyticsExperiment(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		ExpectedUpdatedAt int64 `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		experiment, err := repository.StopAnalyticsExperiment(
			request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"),
			request.PathValue("experimentID"), body.ExpectedUpdatedAt,
		)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicExperiment(experiment))
	}
}

func shipAnalyticsExperiment(repository AnalyticsRepository) http.HandlerFunc {
	type requestBody struct {
		Variant           string `json:"variant"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		if body.ExpectedUpdatedAt <= 0 || body.Variant == "" {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		experiment, err := repository.ShipAnalyticsExperiment(
			request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"),
			request.PathValue("experimentID"), body.Variant, body.ExpectedUpdatedAt,
		)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicExperiment(experiment))
	}
}

func listAnalyticsCharts(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		charts, err := repository.AnalyticsCharts(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"))
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		payload := make([]analyticsChartResponse, 0, len(charts))
		for _, chart := range charts {
			payload = append(payload, publicAnalyticsChart(chart))
		}
		writeJSON(response, http.StatusOK, payload)
	}
}

func createAnalyticsChart(repository AnalyticsRepository) http.HandlerFunc {
	return mutateAnalyticsChart(repository, true)
}

func updateAnalyticsChart(repository AnalyticsRepository) http.HandlerFunc {
	return mutateAnalyticsChart(repository, false)
}

func mutateAnalyticsChart(repository AnalyticsRepository, create bool) http.HandlerFunc {
	type requestBody struct {
		Title             string `json:"title"`
		SQL               string `json:"sql"`
		Visualization     string `json:"visualization"`
		Legend            string `json:"legend"`
		Unit              string `json:"unit"`
		ExpectedUpdatedAt int64  `json:"expectedUpdatedAt"`
	}
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		var body requestBody
		if !decodeAnalyticsBody(response, request, &body) {
			return
		}
		chart := state.AnalyticsChart{
			TrackerID: request.PathValue("trackerID"), Title: body.Title, SQL: body.SQL,
			Visualization: body.Visualization, Legend: body.Legend, Unit: body.Unit,
		}
		var err error
		if create {
			chart, err = repository.CreateAnalyticsChart(request.Context(), request.PathValue("projectID"), chart)
			if err != nil {
				writeAnalyticsError(response, err)
				return
			}
			writeJSON(response, http.StatusCreated, publicAnalyticsChart(chart))
			return
		}
		if body.ExpectedUpdatedAt <= 0 {
			writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
			return
		}
		chart.ID = request.PathValue("chartID")
		chart, err = repository.UpdateAnalyticsChart(request.Context(), request.PathValue("projectID"), chart, body.ExpectedUpdatedAt)
		if err != nil {
			writeAnalyticsError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, publicAnalyticsChart(chart))
	}
}

func deleteAnalyticsChart(repository AnalyticsRepository) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if _, ok := requireAccessIdentity(response, request); !ok {
			return
		}
		if err := repository.DeleteAnalyticsChart(request.Context(), request.PathValue("projectID"), request.PathValue("trackerID"), request.PathValue("chartID")); err != nil {
			writeAnalyticsError(response, err)
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusNoContent)
	}
}

func publicTrackers(ctx context.Context, repository AnalyticsRepository, projectID string, trackers []state.AnalyticsTracker) ([]analyticsTrackerResponse, error) {
	project, err := repository.Project(ctx, projectID)
	if err != nil {
		return nil, err
	}
	hostnames, err := repository.ProjectServiceHostnames(ctx, projectID)
	if err != nil {
		return nil, err
	}
	payload := make([]analyticsTrackerResponse, 0, len(trackers))
	for _, tracker := range trackers {
		matching := make([]string, 0)
		for _, hostname := range hostnames {
			if state.HostMatchesTracker(hostname, tracker.RootDomain) {
				matching = append(matching, hostname)
			}
		}
		internal := telemetry.InternalAnalyticsHostname(project.Name, state.TrackerSlug(tracker.RootDomain))
		payload = append(payload, analyticsTrackerResponse{
			ID: tracker.ID, ProjectID: tracker.ProjectID, Name: tracker.Name, RootDomain: tracker.RootDomain,
			InternalHostname:  internal,
			InternalOFREPURL:  "http://" + internal + ":" + strconv.Itoa(firewall.ServiceTelemetryPort),
			MatchingHostnames: matching, CreatedAt: tracker.CreatedAtMillis, UpdatedAt: tracker.UpdatedAtMillis,
		})
	}
	return payload, nil
}

func publicGoal(goal state.AnalyticsGoal) analyticsGoalResponse {
	return analyticsGoalResponse{
		ID: goal.ID, TrackerID: goal.TrackerID, Name: goal.Name, Action: goal.ActionType,
		Value: goal.ActionValue, Hostname: goal.Hostname, CreatedAt: goal.CreatedAtMillis, UpdatedAt: goal.UpdatedAtMillis,
	}
}

func publicFunnel(funnel state.AnalyticsFunnel) analyticsFunnelResponse {
	return analyticsFunnelResponse{
		ID: funnel.ID, TrackerID: funnel.TrackerID, Name: funnel.Name,
		WindowValue: funnel.WindowValue, WindowUnit: funnel.WindowUnit, Steps: funnel.Steps,
		CreatedAt: funnel.CreatedAtMillis, UpdatedAt: funnel.UpdatedAtMillis,
	}
}

func publicFlag(flag state.AnalyticsFlag) analyticsFlagResponse {
	payload := json.RawMessage(flag.PayloadJSON)
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	targeting := json.RawMessage(flag.TargetingJSON)
	if len(targeting) == 0 {
		targeting = json.RawMessage(`{"groups":[]}`)
	}
	return analyticsFlagResponse{
		ID: flag.ID, TrackerID: flag.TrackerID, Key: flag.Key, Description: flag.Description,
		Type: flag.Type, Enabled: flag.Enabled, Variants: flag.Variants, Payload: payload, Targeting: targeting,
		CreatedAt: flag.CreatedAtMillis, UpdatedAt: flag.UpdatedAtMillis,
	}
}

func publicExperiment(experiment state.AnalyticsExperiment) analyticsExperimentResponse {
	return analyticsExperimentResponse{
		ID: experiment.ID, FlagID: experiment.FlagID, TrackerID: experiment.TrackerID,
		ControlVariant: experiment.ControlVariant, Metric: experiment.Metric,
		WindowValue: experiment.WindowValue, WindowUnit: experiment.WindowUnit,
		StartedAt: experiment.StartedAtMillis, EndedAt: experiment.EndedAtMillis,
		CreatedAt: experiment.CreatedAtMillis, UpdatedAt: experiment.UpdatedAtMillis,
	}
}

func publicAnalyticsChart(chart state.AnalyticsChart) analyticsChartResponse {
	return analyticsChartResponse{
		ID: chart.ID, TrackerID: chart.TrackerID, Title: chart.Title, SQL: chart.SQL,
		Visualization: chart.Visualization, Legend: chart.Legend, Unit: chart.Unit,
		CreatedAt: chart.CreatedAtMillis, UpdatedAt: chart.UpdatedAtMillis,
	}
}

func decodeAnalyticsBody(response http.ResponseWriter, request *http.Request, dest any) bool {
	if !requireJSONContentType(response, request) {
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, maximumAnalyticsRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil || requireJSONEnd(decoder) != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
		return false
	}
	return true
}

func writeAnalyticsError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrProjectNotFound):
		writeAPIError(response, http.StatusNotFound, "project_not_found", "Project not found")
	case errors.Is(err, state.ErrAnalyticsTrackerNotFound):
		writeAPIError(response, http.StatusNotFound, "analytics_tracker_not_found", "Analytics tracker was not found")
	case errors.Is(err, state.ErrAnalyticsGoalNotFound):
		writeAPIError(response, http.StatusNotFound, "analytics_goal_not_found", "Analytics goal was not found")
	case errors.Is(err, state.ErrAnalyticsFunnelNotFound):
		writeAPIError(response, http.StatusNotFound, "analytics_funnel_not_found", "Analytics funnel was not found")
	case errors.Is(err, state.ErrAnalyticsFlagNotFound):
		writeAPIError(response, http.StatusNotFound, "analytics_flag_not_found", "Analytics flag was not found")
	case errors.Is(err, state.ErrAnalyticsExperimentNotFound):
		writeAPIError(response, http.StatusNotFound, "analytics_experiment_not_found", "Analytics experiment was not found")
	case errors.Is(err, state.ErrAnalyticsChartNotFound):
		writeAPIError(response, http.StatusNotFound, "analytics_chart_not_found", "Analytics chart was not found")
	case errors.Is(err, state.ErrAnalyticsTrackerChanged), errors.Is(err, state.ErrAnalyticsChanged):
		writeAPIError(response, http.StatusConflict, "analytics_changed", "Analytics object changed")
	case errors.Is(err, state.ErrAnalyticsTrackerConflict), errors.Is(err, state.ErrAnalyticsFlagConflict), errors.Is(err, state.ErrAnalyticsExperimentConflict):
		writeAPIError(response, http.StatusConflict, "analytics_conflict", err.Error())
	case errors.Is(err, state.ErrAnalyticsInvalid):
		writeAPIError(response, http.StatusBadRequest, "invalid_analytics", "Analytics fields are invalid")
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "Unable to manage analytics")
	}
}
