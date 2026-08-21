package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/state"
)

type analyticsStub struct {
	trackers    []state.AnalyticsTracker
	goals       []state.AnalyticsGoal
	funnels     []state.AnalyticsFunnel
	flags       []state.AnalyticsFlag
	experiments []state.AnalyticsExperiment
	charts      []state.AnalyticsChart
	hostnames   []string
	lastQuery   json.RawMessage
	shipVariant string
}

func (stub *analyticsStub) Project(_ context.Context, projectID string) (state.ProjectSummary, error) {
	return state.ProjectSummary{ID: projectID, Name: "alpha"}, nil
}

func (stub *analyticsStub) ProjectServiceHostnames(context.Context, string) ([]string, error) {
	if stub.hostnames == nil {
		return []string{"www.example.com"}, nil
	}
	return stub.hostnames, nil
}

func (stub *analyticsStub) AnalyticsTrackers(context.Context, string) ([]state.AnalyticsTracker, error) {
	return stub.defaultTrackers(), nil
}

func (stub *analyticsStub) AnalyticsTracker(context.Context, string, string) (state.AnalyticsTracker, error) {
	trackers := stub.defaultTrackers()
	return trackers[0], nil
}

func (stub *analyticsStub) CreateAnalyticsTracker(_ context.Context, tracker state.AnalyticsTracker) (state.AnalyticsTracker, error) {
	tracker.ID, tracker.CreatedAtMillis, tracker.UpdatedAtMillis = "created-tracker", 2, 2
	return tracker, nil
}

func (stub *analyticsStub) UpdateAnalyticsTracker(_ context.Context, tracker state.AnalyticsTracker, expected int64) (state.AnalyticsTracker, error) {
	tracker.CreatedAtMillis, tracker.UpdatedAtMillis = 1, expected+1
	return tracker, nil
}

func (*analyticsStub) DeleteAnalyticsTracker(context.Context, string, string) error {
	return nil
}

func (stub *analyticsStub) AnalyticsGoals(context.Context, string, string) ([]state.AnalyticsGoal, error) {
	return stub.goals, nil
}

func (stub *analyticsStub) CreateAnalyticsGoal(_ context.Context, _ string, goal state.AnalyticsGoal) (state.AnalyticsGoal, error) {
	goal.ID, goal.CreatedAtMillis, goal.UpdatedAtMillis = "created-goal", 2, 2
	return goal, nil
}

func (stub *analyticsStub) UpdateAnalyticsGoal(_ context.Context, _ string, goal state.AnalyticsGoal, expected int64) (state.AnalyticsGoal, error) {
	goal.CreatedAtMillis, goal.UpdatedAtMillis = 1, expected+1
	return goal, nil
}

func (*analyticsStub) DeleteAnalyticsGoal(context.Context, string, string, string) error {
	return nil
}

func (stub *analyticsStub) AnalyticsFunnels(context.Context, string, string) ([]state.AnalyticsFunnel, error) {
	return stub.funnels, nil
}

func (stub *analyticsStub) CreateAnalyticsFunnel(_ context.Context, _ string, funnel state.AnalyticsFunnel) (state.AnalyticsFunnel, error) {
	funnel.ID, funnel.CreatedAtMillis, funnel.UpdatedAtMillis = "created-funnel", 2, 2
	return funnel, nil
}

func (stub *analyticsStub) UpdateAnalyticsFunnel(_ context.Context, _ string, funnel state.AnalyticsFunnel, expected int64) (state.AnalyticsFunnel, error) {
	funnel.CreatedAtMillis, funnel.UpdatedAtMillis = 1, expected+1
	return funnel, nil
}

func (*analyticsStub) DeleteAnalyticsFunnel(context.Context, string, string, string) error {
	return nil
}

func (stub *analyticsStub) AnalyticsFlags(context.Context, string, string) ([]state.AnalyticsFlag, error) {
	return stub.flags, nil
}

func (stub *analyticsStub) CreateAnalyticsFlag(_ context.Context, _ string, flag state.AnalyticsFlag) (state.AnalyticsFlag, error) {
	flag.ID, flag.CreatedAtMillis, flag.UpdatedAtMillis = "created-flag", 2, 2
	return flag, nil
}

func (stub *analyticsStub) UpdateAnalyticsFlag(_ context.Context, _ string, flag state.AnalyticsFlag, expected int64) (state.AnalyticsFlag, error) {
	flag.CreatedAtMillis, flag.UpdatedAtMillis = 1, expected+1
	return flag, nil
}

func (*analyticsStub) DeleteAnalyticsFlag(context.Context, string, string, string) error {
	return nil
}

func (stub *analyticsStub) AnalyticsExperiments(context.Context, string, string) ([]state.AnalyticsExperiment, error) {
	return stub.experiments, nil
}

func (stub *analyticsStub) CreateAnalyticsExperiment(_ context.Context, _ string, experiment state.AnalyticsExperiment) (state.AnalyticsExperiment, error) {
	experiment.ID, experiment.CreatedAtMillis, experiment.UpdatedAtMillis = "created-experiment", 2, 2
	return experiment, nil
}

func (stub *analyticsStub) StopAnalyticsExperiment(_ context.Context, _, _, experimentID string, expected int64) (state.AnalyticsExperiment, error) {
	return state.AnalyticsExperiment{ID: experimentID, UpdatedAtMillis: expected + 1}, nil
}

func (stub *analyticsStub) ShipAnalyticsExperiment(_ context.Context, _, _, experimentID, variant string, expected int64) (state.AnalyticsExperiment, error) {
	stub.shipVariant = variant
	return state.AnalyticsExperiment{ID: experimentID, UpdatedAtMillis: expected + 1}, nil
}

func (stub *analyticsStub) AnalyticsCharts(context.Context, string, string) ([]state.AnalyticsChart, error) {
	return stub.charts, nil
}

func (stub *analyticsStub) CreateAnalyticsChart(_ context.Context, _ string, chart state.AnalyticsChart) (state.AnalyticsChart, error) {
	chart.ID, chart.CreatedAtMillis, chart.UpdatedAtMillis = "created-chart", 2, 2
	return chart, nil
}

func (stub *analyticsStub) UpdateAnalyticsChart(_ context.Context, _ string, chart state.AnalyticsChart, expected int64) (state.AnalyticsChart, error) {
	chart.CreatedAtMillis, chart.UpdatedAtMillis = 1, expected+1
	return chart, nil
}

func (*analyticsStub) DeleteAnalyticsChart(context.Context, string, string, string) error {
	return nil
}

func (stub *analyticsStub) QueryAnalytics(_ context.Context, _, _ string, query json.RawMessage) (int, string, []byte, error) {
	stub.lastQuery = append(json.RawMessage(nil), query...)
	return http.StatusOK, "application/json", []byte(`{"report":"overview","visitors":12}`), nil
}

func (stub *analyticsStub) defaultTrackers() []state.AnalyticsTracker {
	if len(stub.trackers) > 0 {
		return stub.trackers
	}
	return []state.AnalyticsTracker{{
		ID: "tracker", ProjectID: "project-a", Name: "site", RootDomain: "example.com",
		CreatedAtMillis: 1, UpdatedAtMillis: 1,
	}}
}

func TestMCPAnalyticsCoversQueriesAndMutations(t *testing.T) {
	stub := &analyticsStub{
		goals: []state.AnalyticsGoal{{ID: "goal", Name: "Signup", ActionType: "event", ActionValue: "signup", UpdatedAtMillis: 4}},
		funnels: []state.AnalyticsFunnel{{
			ID: "funnel", Name: "Checkout", WindowValue: 7, WindowUnit: "day",
			Steps:           []state.AnalyticsFunnelStep{{Type: "path", Value: "/"}},
			UpdatedAtMillis: 5,
		}},
		experiments: []state.AnalyticsExperiment{{ID: "experiment", FlagID: "flag", ControlVariant: "false", UpdatedAtMillis: 6}},
	}
	handler := newTestHandler(t, &repositoryStub{projects: []state.ProjectSummary{{ID: "project-a", Name: "alpha"}}})
	handler.analytics = stub
	bound := "project-a"
	read := automation.Identity{TokenID: "read", Role: "read", ProjectID: &bound}
	admin := automation.Identity{TokenID: "admin", Role: "admin", ProjectID: &bound}

	list := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`), read)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	for _, name := range []string{"list_analytics_trackers", "get_analytics_tracker", "query_analytics"} {
		if !strings.Contains(response.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("read analytics tools omit %s: %s", name, response.Body)
		}
	}
	if strings.Contains(response.Body.String(), `"name":"ship_analytics_experiment"`) {
		t.Fatalf("read token sees analytics mutation: %s", response.Body)
	}

	list = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`), admin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	for _, name := range []string{
		"update_analytics_goal", "update_analytics_funnel", "update_analytics_chart", "ship_analytics_experiment",
	} {
		if !strings.Contains(response.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("admin analytics tools omit %s: %s", name, response.Body)
		}
	}

	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_analytics_trackers","arguments":{"projectId":"project-a"}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `internalOfrepUrl`) || !strings.Contains(response.Body.String(), `www.example.com`) || strings.Contains(response.Body.String(), `UpdatedAtMillis`) || !strings.Contains(response.Body.String(), `updatedAt`) {
		t.Fatalf("list trackers = %s", response.Body)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"query_analytics","arguments":{"projectId":"project-a","trackerId":"tracker","report":"overview","from":1000,"to":2000,"filters":[{"dimension":"country","operator":"is","value":"US"}]}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `visitors`) {
		t.Fatalf("query analytics = %s", response.Body)
	}
	var query map[string]any
	if err := json.Unmarshal(stub.lastQuery, &query); err != nil {
		t.Fatal(err)
	}
	if query["report"] != "overview" || query["from"] != float64(1000) || query["to"] != float64(2000) {
		t.Fatalf("flattened query = %s", stub.lastQuery)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ship_analytics_experiment","arguments":{"projectId":"project-a","trackerId":"tracker","experimentId":"experiment","variant":"true","expectedUpdatedAt":6}}}`), read)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if !strings.Contains(response.Body.String(), "admin token is required") || stub.shipVariant != "" {
		t.Fatalf("read ship = %s variant=%q", response.Body, stub.shipVariant)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"ship_analytics_experiment","arguments":{"projectId":"project-a","trackerId":"tracker","experimentId":"experiment","variant":"true","expectedUpdatedAt":6}}}`), admin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || stub.shipVariant != "true" {
		t.Fatalf("admin ship = %s variant=%q", response.Body, stub.shipVariant)
	}

	call = withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"update_analytics_goal","arguments":{"projectId":"project-a","trackerId":"tracker","goalId":"goal","name":"Signup","actionType":"event","actionValue":"signup_completed","expectedUpdatedAt":4}}}`), admin)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if strings.Contains(response.Body.String(), `"isError":true`) || !strings.Contains(response.Body.String(), `signup_completed`) {
		t.Fatalf("update goal = %s", response.Body)
	}
}

func TestMCPAnalyticsQueryRejectsNestedQueryObject(t *testing.T) {
	handler := newTestHandler(t, &repositoryStub{})
	bound := "project-a"
	call := withMCPIdentity(mcpRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query_analytics","arguments":{"projectId":"project-a","trackerId":"tracker","query":{"report":"overview","from":1,"to":2}}}}`), automation.Identity{TokenID: "read", Role: "read", ProjectID: &bound})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, call)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "invalid tool arguments") {
		t.Fatalf("nested query object = %d/%s", response.Code, response.Body)
	}
}
