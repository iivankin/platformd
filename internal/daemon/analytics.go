//go:build !platformd_worker

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/analytics"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
)

type liveAnalyticsRepository struct {
	store   *state.Store
	manager *telemetry.ServiceManager
	query   *analytics.Handler
}

func (repository *liveAnalyticsRepository) Project(ctx context.Context, projectID string) (state.ProjectSummary, error) {
	return repository.store.Project(ctx, projectID)
}

func (repository *liveAnalyticsRepository) ProjectServiceHostnames(ctx context.Context, projectID string) ([]string, error) {
	return repository.store.ProjectServiceHostnames(ctx, projectID)
}

func (repository *liveAnalyticsRepository) AnalyticsTrackers(ctx context.Context, projectID string) ([]state.AnalyticsTracker, error) {
	return repository.store.AnalyticsTrackers(ctx, projectID)
}

func (repository *liveAnalyticsRepository) AnalyticsTracker(ctx context.Context, projectID, trackerID string) (state.AnalyticsTracker, error) {
	return repository.store.AnalyticsTracker(ctx, projectID, trackerID)
}

func (repository *liveAnalyticsRepository) CreateAnalyticsTracker(ctx context.Context, tracker state.AnalyticsTracker) (state.AnalyticsTracker, error) {
	trackerID, err := id.New()
	if err != nil {
		return state.AnalyticsTracker{}, err
	}
	now := time.Now().UnixMilli()
	tracker.ID = trackerID
	tracker.Name = strings.TrimSpace(tracker.Name)
	tracker.RootDomain = state.NormalizeTrackerRoot(tracker.RootDomain)
	if tracker.Mode == "" {
		tracker.Mode = state.AnalyticsModeOptOut
	}
	tracker.CreatedAtMillis = now
	tracker.UpdatedAtMillis = now
	if err := repository.store.CreateAnalyticsTracker(ctx, tracker); err != nil {
		return state.AnalyticsTracker{}, err
	}
	if err := repository.ensureTracker(ctx, tracker); err != nil {
		_ = repository.store.DeleteAnalyticsTracker(ctx, tracker.ProjectID, tracker.ID)
		_ = repository.manager.ForgetAnalytics(tracker.ProjectID, tracker.ID)
		return state.AnalyticsTracker{}, err
	}
	return tracker, nil
}

func (repository *liveAnalyticsRepository) UpdateAnalyticsTracker(ctx context.Context, tracker state.AnalyticsTracker, expectedUpdatedAt int64) (state.AnalyticsTracker, error) {
	previous, err := repository.store.AnalyticsTracker(ctx, tracker.ProjectID, tracker.ID)
	if err != nil {
		return state.AnalyticsTracker{}, err
	}
	tracker.Name = strings.TrimSpace(tracker.Name)
	tracker.RootDomain = state.NormalizeTrackerRoot(tracker.RootDomain)
	tracker.UpdatedAtMillis = time.Now().UnixMilli()
	if tracker.UpdatedAtMillis <= expectedUpdatedAt {
		tracker.UpdatedAtMillis = expectedUpdatedAt + 1
	}
	if err := repository.store.UpdateAnalyticsTracker(ctx, tracker, expectedUpdatedAt); err != nil {
		return state.AnalyticsTracker{}, err
	}
	if err := repository.ensureTracker(ctx, tracker); err != nil {
		restored := previous
		restored.UpdatedAtMillis = tracker.UpdatedAtMillis + 1
		_ = repository.store.UpdateAnalyticsTracker(ctx, restored, tracker.UpdatedAtMillis)
		return state.AnalyticsTracker{}, err
	}
	return tracker, nil
}

func (repository *liveAnalyticsRepository) DeleteAnalyticsTracker(ctx context.Context, projectID, trackerID string) error {
	if err := repository.store.DeleteAnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return err
	}
	return repository.manager.ForgetAnalytics(projectID, trackerID)
}

func (repository *liveAnalyticsRepository) AnalyticsGoals(ctx context.Context, projectID, trackerID string) ([]state.AnalyticsGoal, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return nil, err
	}
	return repository.store.AnalyticsGoals(ctx, trackerID)
}

func (repository *liveAnalyticsRepository) CreateAnalyticsGoal(ctx context.Context, projectID string, goal state.AnalyticsGoal) (state.AnalyticsGoal, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, goal.TrackerID); err != nil {
		return state.AnalyticsGoal{}, err
	}
	goalID, err := id.New()
	if err != nil {
		return state.AnalyticsGoal{}, err
	}
	now := time.Now().UnixMilli()
	goal.ID = goalID
	goal.CreatedAtMillis = now
	goal.UpdatedAtMillis = now
	if err := repository.store.CreateAnalyticsGoal(ctx, goal); err != nil {
		return state.AnalyticsGoal{}, err
	}
	return goal, nil
}

func (repository *liveAnalyticsRepository) UpdateAnalyticsGoal(ctx context.Context, projectID string, goal state.AnalyticsGoal, expectedUpdatedAt int64) (state.AnalyticsGoal, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, goal.TrackerID); err != nil {
		return state.AnalyticsGoal{}, err
	}
	goal.UpdatedAtMillis = time.Now().UnixMilli()
	if goal.UpdatedAtMillis <= expectedUpdatedAt {
		goal.UpdatedAtMillis = expectedUpdatedAt + 1
	}
	if err := repository.store.UpdateAnalyticsGoal(ctx, goal, expectedUpdatedAt); err != nil {
		return state.AnalyticsGoal{}, err
	}
	return goal, nil
}

func (repository *liveAnalyticsRepository) DeleteAnalyticsGoal(ctx context.Context, projectID, trackerID, goalID string) error {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return err
	}
	return repository.store.DeleteAnalyticsGoal(ctx, trackerID, goalID)
}

func (repository *liveAnalyticsRepository) AnalyticsFunnels(ctx context.Context, projectID, trackerID string) ([]state.AnalyticsFunnel, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return nil, err
	}
	return repository.store.AnalyticsFunnels(ctx, trackerID)
}

func (repository *liveAnalyticsRepository) CreateAnalyticsFunnel(ctx context.Context, projectID string, funnel state.AnalyticsFunnel) (state.AnalyticsFunnel, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, funnel.TrackerID); err != nil {
		return state.AnalyticsFunnel{}, err
	}
	funnelID, err := id.New()
	if err != nil {
		return state.AnalyticsFunnel{}, err
	}
	now := time.Now().UnixMilli()
	funnel.ID = funnelID
	funnel.CreatedAtMillis = now
	funnel.UpdatedAtMillis = now
	if err := repository.store.CreateAnalyticsFunnel(ctx, funnel); err != nil {
		return state.AnalyticsFunnel{}, err
	}
	return funnel, nil
}

func (repository *liveAnalyticsRepository) UpdateAnalyticsFunnel(ctx context.Context, projectID string, funnel state.AnalyticsFunnel, expectedUpdatedAt int64) (state.AnalyticsFunnel, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, funnel.TrackerID); err != nil {
		return state.AnalyticsFunnel{}, err
	}
	funnel.UpdatedAtMillis = time.Now().UnixMilli()
	if funnel.UpdatedAtMillis <= expectedUpdatedAt {
		funnel.UpdatedAtMillis = expectedUpdatedAt + 1
	}
	if err := repository.store.UpdateAnalyticsFunnel(ctx, funnel, expectedUpdatedAt); err != nil {
		return state.AnalyticsFunnel{}, err
	}
	return funnel, nil
}

func (repository *liveAnalyticsRepository) DeleteAnalyticsFunnel(ctx context.Context, projectID, trackerID, funnelID string) error {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return err
	}
	return repository.store.DeleteAnalyticsFunnel(ctx, trackerID, funnelID)
}

func (repository *liveAnalyticsRepository) AnalyticsFlags(ctx context.Context, projectID, trackerID string) ([]state.AnalyticsFlag, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return nil, err
	}
	return repository.store.AnalyticsFlags(ctx, trackerID)
}

func (repository *liveAnalyticsRepository) CreateAnalyticsFlag(ctx context.Context, projectID string, flag state.AnalyticsFlag) (state.AnalyticsFlag, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, flag.TrackerID); err != nil {
		return state.AnalyticsFlag{}, err
	}
	flagID, err := id.New()
	if err != nil {
		return state.AnalyticsFlag{}, err
	}
	now := time.Now().UnixMilli()
	flag.ID = flagID
	flag.CreatedAtMillis = now
	flag.UpdatedAtMillis = now
	if err := repository.store.CreateAnalyticsFlag(ctx, flag); err != nil {
		return state.AnalyticsFlag{}, err
	}
	return flag, nil
}

func (repository *liveAnalyticsRepository) UpdateAnalyticsFlag(ctx context.Context, projectID string, flag state.AnalyticsFlag, expectedUpdatedAt int64) (state.AnalyticsFlag, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, flag.TrackerID); err != nil {
		return state.AnalyticsFlag{}, err
	}
	flag.UpdatedAtMillis = time.Now().UnixMilli()
	if flag.UpdatedAtMillis <= expectedUpdatedAt {
		flag.UpdatedAtMillis = expectedUpdatedAt + 1
	}
	if err := repository.store.UpdateAnalyticsFlag(ctx, flag, expectedUpdatedAt); err != nil {
		return state.AnalyticsFlag{}, err
	}
	return flag, nil
}

func (repository *liveAnalyticsRepository) DeleteAnalyticsFlag(ctx context.Context, projectID, trackerID, flagID string) error {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return err
	}
	return repository.store.DeleteAnalyticsFlag(ctx, trackerID, flagID)
}

func (repository *liveAnalyticsRepository) AnalyticsExperiments(ctx context.Context, projectID, trackerID string) ([]state.AnalyticsExperiment, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return nil, err
	}
	return repository.store.AnalyticsExperiments(ctx, trackerID)
}

func (repository *liveAnalyticsRepository) CreateAnalyticsExperiment(ctx context.Context, projectID string, experiment state.AnalyticsExperiment) (state.AnalyticsExperiment, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, experiment.TrackerID); err != nil {
		return state.AnalyticsExperiment{}, err
	}
	experimentID, err := id.New()
	if err != nil {
		return state.AnalyticsExperiment{}, err
	}
	now := time.Now().UnixMilli()
	experiment.ID = experimentID
	if experiment.StartedAtMillis == 0 {
		experiment.StartedAtMillis = now
	}
	experiment.CreatedAtMillis = now
	experiment.UpdatedAtMillis = now
	if err := repository.store.CreateAnalyticsExperiment(ctx, experiment); err != nil {
		return state.AnalyticsExperiment{}, err
	}
	return experiment, nil
}

func (repository *liveAnalyticsRepository) StopAnalyticsExperiment(ctx context.Context, projectID, trackerID, experimentID string, expectedUpdatedAt int64) (state.AnalyticsExperiment, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return state.AnalyticsExperiment{}, err
	}
	endedAt := time.Now().UnixMilli()
	if err := repository.store.StopAnalyticsExperiment(ctx, trackerID, experimentID, endedAt, expectedUpdatedAt); err != nil {
		return state.AnalyticsExperiment{}, err
	}
	experiments, err := repository.store.AnalyticsExperiments(ctx, trackerID)
	if err != nil {
		return state.AnalyticsExperiment{}, err
	}
	for _, experiment := range experiments {
		if experiment.ID == experimentID {
			return experiment, nil
		}
	}
	return state.AnalyticsExperiment{}, state.ErrAnalyticsExperimentNotFound
}

func (repository *liveAnalyticsRepository) ShipAnalyticsExperiment(ctx context.Context, projectID, trackerID, experimentID, variant string, expectedUpdatedAt int64) (state.AnalyticsExperiment, error) {
	experiments, err := repository.AnalyticsExperiments(ctx, projectID, trackerID)
	if err != nil {
		return state.AnalyticsExperiment{}, err
	}
	var experiment state.AnalyticsExperiment
	for _, candidate := range experiments {
		if candidate.ID == experimentID {
			experiment = candidate
			break
		}
	}
	if experiment.ID == "" {
		return state.AnalyticsExperiment{}, state.ErrAnalyticsExperimentNotFound
	}
	now := time.Now().UnixMilli()
	if err := repository.store.ShipAnalyticsExperiment(ctx, trackerID, experimentID, variant, expectedUpdatedAt, now); err != nil {
		return state.AnalyticsExperiment{}, err
	}
	experiments, err = repository.store.AnalyticsExperiments(ctx, trackerID)
	if err != nil {
		return state.AnalyticsExperiment{}, err
	}
	for _, saved := range experiments {
		if saved.ID == experimentID {
			return saved, nil
		}
	}
	return state.AnalyticsExperiment{}, state.ErrAnalyticsExperimentNotFound
}

func (repository *liveAnalyticsRepository) AnalyticsCharts(ctx context.Context, projectID, trackerID string) ([]state.AnalyticsChart, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return nil, err
	}
	return repository.store.AnalyticsCharts(ctx, trackerID)
}

func (repository *liveAnalyticsRepository) CreateAnalyticsChart(ctx context.Context, projectID string, chart state.AnalyticsChart) (state.AnalyticsChart, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, chart.TrackerID); err != nil {
		return state.AnalyticsChart{}, err
	}
	chartID, err := id.New()
	if err != nil {
		return state.AnalyticsChart{}, err
	}
	now := time.Now().UnixMilli()
	chart.ID = chartID
	chart.CreatedAtMillis = now
	chart.UpdatedAtMillis = now
	if err := repository.store.CreateAnalyticsChart(ctx, chart); err != nil {
		return state.AnalyticsChart{}, err
	}
	return chart, nil
}

func (repository *liveAnalyticsRepository) UpdateAnalyticsChart(ctx context.Context, projectID string, chart state.AnalyticsChart, expectedUpdatedAt int64) (state.AnalyticsChart, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, chart.TrackerID); err != nil {
		return state.AnalyticsChart{}, err
	}
	chart.UpdatedAtMillis = time.Now().UnixMilli()
	if chart.UpdatedAtMillis <= expectedUpdatedAt {
		chart.UpdatedAtMillis = expectedUpdatedAt + 1
	}
	if err := repository.store.UpdateAnalyticsChart(ctx, chart, expectedUpdatedAt); err != nil {
		return state.AnalyticsChart{}, err
	}
	return chart, nil
}

func (repository *liveAnalyticsRepository) DeleteAnalyticsChart(ctx context.Context, projectID, trackerID, chartID string) error {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return err
	}
	return repository.store.DeleteAnalyticsChart(ctx, trackerID, chartID)
}

func (repository *liveAnalyticsRepository) QueryAnalytics(ctx context.Context, projectID, trackerID string, body json.RawMessage) (int, string, []byte, error) {
	if _, err := repository.store.AnalyticsTracker(ctx, projectID, trackerID); err != nil {
		return 0, "", nil, err
	}
	enriched, err := repository.enrichQuery(ctx, trackerID, body)
	if err != nil {
		return 0, "", nil, err
	}
	return repository.query.Query(ctx, trackerID, enriched)
}

func (repository *liveAnalyticsRepository) TrackersMatchingService(ctx context.Context, projectID, serviceID string) ([]state.AnalyticsTracker, error) {
	domains, err := repository.store.ServiceDomains(ctx, projectID, serviceID)
	if err != nil {
		return nil, err
	}
	trackers, err := repository.store.AnalyticsTrackers(ctx, projectID)
	if err != nil {
		return nil, err
	}
	matched := make([]state.AnalyticsTracker, 0)
	for _, tracker := range trackers {
		for _, domain := range domains {
			if state.HostMatchesTracker(domain.Hostname, tracker.RootDomain) {
				matched = append(matched, tracker)
				break
			}
		}
	}
	return matched, nil
}

func (repository *liveAnalyticsRepository) ensureTracker(ctx context.Context, tracker state.AnalyticsTracker) error {
	project, err := repository.store.Project(ctx, tracker.ProjectID)
	if err != nil {
		return err
	}
	return repository.manager.EnsureAnalytics(
		tracker.ProjectID, project.Name, state.TrackerSlug(tracker.RootDomain), tracker.ID,
	)
}

func (repository *liveAnalyticsRepository) enrichQuery(ctx context.Context, trackerID string, body json.RawMessage) (json.RawMessage, error) {
	if len(body) == 0 {
		return body, nil
	}
	var query map[string]any
	if err := json.Unmarshal(body, &query); err != nil {
		return nil, err
	}
	report, _ := query["report"].(string)
	switch report {
	case "funnel":
		funnelID, _ := query["funnelId"].(string)
		if funnelID == "" {
			break
		}
		funnel, err := repository.store.AnalyticsFunnel(ctx, trackerID, funnelID)
		if err != nil {
			return nil, err
		}
		query["steps"] = funnel.Steps
		query["windowSeconds"] = windowSeconds(funnel.WindowValue, funnel.WindowUnit)
	case "experiment":
		experimentID, _ := query["experimentId"].(string)
		if experimentID == "" {
			break
		}
		experiments, err := repository.store.AnalyticsExperiments(ctx, trackerID)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, experiment := range experiments {
			if experiment.ID != experimentID {
				continue
			}
			matched = true
			flag, err := repository.store.AnalyticsFlag(ctx, trackerID, experiment.FlagID)
			if err != nil {
				return nil, err
			}
			delete(query, "metricEvent")
			delete(query, "metricPath")
			delete(query, "metricHostname")
			query["flag"] = flag.Key
			query["windowSeconds"] = windowSeconds(experiment.WindowValue, experiment.WindowUnit)
			if experiment.Metric.EventName != "" {
				query["metricEvent"] = experiment.Metric.EventName
			}
			if experiment.Metric.GoalID == "" {
				break
			}
			goals, err := repository.store.AnalyticsGoals(ctx, trackerID)
			if err != nil {
				return nil, err
			}
			matchedGoal := false
			for _, goal := range goals {
				if goal.ID != experiment.Metric.GoalID {
					continue
				}
				matchedGoal = true
				delete(query, "metricEvent")
				if goal.ActionType == "event" {
					query["metricEvent"] = goal.ActionValue
				} else {
					query["metricPath"] = goal.ActionValue
				}
				if goal.Hostname != "" {
					query["metricHostname"] = goal.Hostname
				}
			}
			if !matchedGoal {
				return nil, state.ErrAnalyticsGoalNotFound
			}
		}
		if !matched {
			return nil, state.ErrAnalyticsExperimentNotFound
		}
	}
	encoded, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("encode analytics query: %w", err)
	}
	return encoded, nil
}

func windowSeconds(value int, unit string) int64 {
	switch unit {
	case "minute":
		return int64(value) * 60
	case "hour":
		return int64(value) * 3600
	default:
		return int64(value) * 86400
	}
}
