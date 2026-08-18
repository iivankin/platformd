package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	AnalyticsModeCookieless = "cookieless"
	AnalyticsModeOptOut     = "opt-out"
	AnalyticsModeOptIn      = "opt-in"
)

var (
	ErrAnalyticsTrackerNotFound    = errors.New("analytics tracker not found")
	ErrAnalyticsTrackerChanged     = errors.New("analytics tracker changed")
	ErrAnalyticsTrackerConflict    = errors.New("analytics tracker conflict")
	ErrAnalyticsGoalNotFound       = errors.New("analytics goal not found")
	ErrAnalyticsFunnelNotFound     = errors.New("analytics funnel not found")
	ErrAnalyticsChartNotFound      = errors.New("analytics chart not found")
	ErrAnalyticsFlagNotFound       = errors.New("analytics flag not found")
	ErrAnalyticsFlagConflict       = errors.New("analytics flag conflict")
	ErrAnalyticsExperimentNotFound = errors.New("analytics experiment not found")
	ErrAnalyticsExperimentConflict = errors.New("analytics experiment conflict")
	ErrAnalyticsChanged            = errors.New("analytics object changed")
	ErrAnalyticsInvalid            = errors.New("analytics fields are invalid")
)

type AnalyticsTracker struct {
	ID              string
	ProjectID       string
	Name            string
	RootDomain      string
	Mode            string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type AnalyticsGoal struct {
	ID              string
	TrackerID       string
	Name            string
	ActionType      string
	ActionValue     string
	Hostname        string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type AnalyticsFunnelStep struct {
	Type     string `json:"type"`
	Value    string `json:"value"`
	Hostname string `json:"hostname,omitempty"`
}

type AnalyticsFunnel struct {
	ID              string
	TrackerID       string
	Name            string
	WindowValue     int
	WindowUnit      string
	Steps           []AnalyticsFunnelStep
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type AnalyticsChart struct {
	ID              string
	TrackerID       string
	Title           string
	SQL             string
	Visualization   string
	Legend          string
	Unit            string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type AnalyticsFlagVariant struct {
	Key        string  `json:"key"`
	Percentage float64 `json:"percentage"`
}

type AnalyticsFlag struct {
	ID              string
	TrackerID       string
	Key             string
	Description     string
	Type            string
	Enabled         bool
	Variants        []AnalyticsFlagVariant
	PayloadJSON     string
	TargetingJSON   string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type AnalyticsExperimentMetric struct {
	GoalID    string `json:"goalId,omitempty"`
	EventName string `json:"eventName,omitempty"`
}

type AnalyticsExperiment struct {
	ID              string
	FlagID          string
	TrackerID       string
	ControlVariant  string
	Metric          AnalyticsExperimentMetric
	WindowValue     int
	WindowUnit      string
	StartedAtMillis int64
	EndedAtMillis   int64
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

func TrackerSlug(rootDomain string) string {
	return strings.ReplaceAll(NormalizeTrackerRoot(rootDomain), ".", "--")
}

func NormalizeTrackerRoot(value string) string {
	host := strings.TrimRight(strings.ToLower(strings.TrimSpace(value)), ".")
	if strings.HasPrefix(host, "[") {
		if end := strings.IndexByte(host, ']'); end > 0 {
			return host[1:end]
		}
	}
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		return strings.TrimRight(hostname, ".")
	}
	return host
}

func HostMatchesTracker(host, root string) bool {
	host = NormalizeTrackerRoot(host)
	root = NormalizeTrackerRoot(root)
	return host != "" && root != "" && (host == root || strings.HasSuffix(host, "."+root))
}

func RootsOverlap(left, right string) bool {
	left = NormalizeTrackerRoot(left)
	right = NormalizeTrackerRoot(right)
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasSuffix(left, "."+right) || strings.HasSuffix(right, "."+left)
}

func (store *Store) AnalyticsTrackers(ctx context.Context, projectID string) ([]AnalyticsTracker, error) {
	if _, err := store.Project(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT id, project_id, name, root_domain, mode, created_at, updated_at
FROM analytics_trackers WHERE project_id = ? ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list analytics trackers: %w", err)
	}
	defer rows.Close()
	trackers := make([]AnalyticsTracker, 0)
	for rows.Next() {
		var tracker AnalyticsTracker
		if err := rows.Scan(&tracker.ID, &tracker.ProjectID, &tracker.Name, &tracker.RootDomain,
			&tracker.Mode, &tracker.CreatedAtMillis, &tracker.UpdatedAtMillis); err != nil {
			return nil, fmt.Errorf("scan analytics tracker: %w", err)
		}
		trackers = append(trackers, tracker)
	}
	return trackers, rows.Err()
}

func (store *Store) AllAnalyticsTrackers(ctx context.Context) ([]AnalyticsTracker, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, project_id, name, root_domain, mode, created_at, updated_at
FROM analytics_trackers ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list all analytics trackers: %w", err)
	}
	defer rows.Close()
	trackers := make([]AnalyticsTracker, 0)
	for rows.Next() {
		var tracker AnalyticsTracker
		if err := rows.Scan(&tracker.ID, &tracker.ProjectID, &tracker.Name, &tracker.RootDomain,
			&tracker.Mode, &tracker.CreatedAtMillis, &tracker.UpdatedAtMillis); err != nil {
			return nil, fmt.Errorf("scan analytics tracker: %w", err)
		}
		trackers = append(trackers, tracker)
	}
	return trackers, rows.Err()
}

func (store *Store) AnalyticsTracker(ctx context.Context, projectID, trackerID string) (AnalyticsTracker, error) {
	var tracker AnalyticsTracker
	err := store.database.QueryRowContext(ctx, `
SELECT id, project_id, name, root_domain, mode, created_at, updated_at
FROM analytics_trackers WHERE id = ? AND project_id = ?`, trackerID, projectID).Scan(
		&tracker.ID, &tracker.ProjectID, &tracker.Name, &tracker.RootDomain,
		&tracker.Mode, &tracker.CreatedAtMillis, &tracker.UpdatedAtMillis)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsTracker{}, ErrAnalyticsTrackerNotFound
	}
	if err != nil {
		return AnalyticsTracker{}, fmt.Errorf("get analytics tracker: %w", err)
	}
	return tracker, nil
}

func (store *Store) AnalyticsTrackerByID(ctx context.Context, trackerID string) (AnalyticsTracker, error) {
	var tracker AnalyticsTracker
	err := store.database.QueryRowContext(ctx, `
SELECT id, project_id, name, root_domain, mode, created_at, updated_at
FROM analytics_trackers WHERE id = ?`, trackerID).Scan(
		&tracker.ID, &tracker.ProjectID, &tracker.Name, &tracker.RootDomain,
		&tracker.Mode, &tracker.CreatedAtMillis, &tracker.UpdatedAtMillis)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsTracker{}, ErrAnalyticsTrackerNotFound
	}
	if err != nil {
		return AnalyticsTracker{}, fmt.Errorf("get analytics tracker: %w", err)
	}
	return tracker, nil
}

func (store *Store) CreateAnalyticsTracker(ctx context.Context, tracker AnalyticsTracker) error {
	tracker.RootDomain = NormalizeTrackerRoot(tracker.RootDomain)
	if err := validateAnalyticsTracker(tracker); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureProjectExists(ctx, transaction, tracker.ProjectID); err != nil {
			return err
		}
		if err := rejectOverlappingTrackerRoot(ctx, transaction, tracker.RootDomain, ""); err != nil {
			return err
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO analytics_trackers(id, project_id, name, root_domain, mode, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			tracker.ID, tracker.ProjectID, tracker.Name, tracker.RootDomain, tracker.Mode,
			tracker.CreatedAtMillis, tracker.UpdatedAtMillis)
		if err != nil {
			if isUniqueConstraint(err) {
				return ErrAnalyticsTrackerConflict
			}
			return fmt.Errorf("create analytics tracker: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateAnalyticsTracker(ctx context.Context, tracker AnalyticsTracker, expectedUpdatedAt int64) error {
	tracker.RootDomain = NormalizeTrackerRoot(tracker.RootDomain)
	if err := validateAnalyticsTracker(tracker); err != nil || expectedUpdatedAt <= 0 || tracker.UpdatedAtMillis <= expectedUpdatedAt {
		if err != nil {
			return err
		}
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := rejectOverlappingTrackerRoot(ctx, transaction, tracker.RootDomain, tracker.ID); err != nil {
			return err
		}
		if err := requireAnalyticsObject(ctx, transaction, ErrAnalyticsTrackerNotFound,
			`SELECT 1 FROM analytics_trackers WHERE id = ? AND project_id = ?`, tracker.ID, tracker.ProjectID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_trackers SET name = ?, root_domain = ?, mode = ?, updated_at = ?
WHERE id = ? AND project_id = ? AND updated_at = ?`,
			tracker.Name, tracker.RootDomain, tracker.Mode, tracker.UpdatedAtMillis,
			tracker.ID, tracker.ProjectID, expectedUpdatedAt)
		if err != nil {
			if isUniqueConstraint(err) {
				return ErrAnalyticsTrackerConflict
			}
			return fmt.Errorf("update analytics tracker: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsTrackerChanged)
	})
}

func (store *Store) DeleteAnalyticsTracker(ctx context.Context, projectID, trackerID string) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM analytics_trackers WHERE id = ? AND project_id = ?`, trackerID, projectID)
		if err != nil {
			return fmt.Errorf("delete analytics tracker: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsTrackerNotFound)
	})
}

func (store *Store) ServiceIDByHostname(ctx context.Context, hostname string) (string, error) {
	var serviceID string
	err := store.database.QueryRowContext(ctx, `
SELECT service_id FROM service_domains WHERE hostname = ?`, strings.ToLower(hostname)).Scan(&serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup service hostname: %w", err)
	}
	return serviceID, nil
}

func (store *Store) ProjectServiceHostnames(ctx context.Context, projectID string) ([]string, error) {
	if _, err := store.Project(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := store.database.QueryContext(ctx, `
SELECT d.hostname FROM service_domains d
JOIN services s ON s.id = d.service_id
WHERE s.project_id = ?
ORDER BY d.hostname`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list project service hostnames: %w", err)
	}
	defer rows.Close()
	hostnames := make([]string, 0)
	for rows.Next() {
		var hostname string
		if err := rows.Scan(&hostname); err != nil {
			return nil, fmt.Errorf("scan service hostname: %w", err)
		}
		hostnames = append(hostnames, hostname)
	}
	return hostnames, rows.Err()
}

func (store *Store) AnalyticsGoals(ctx context.Context, trackerID string) ([]AnalyticsGoal, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, tracker_id, name, action_type, action_value, IFNULL(hostname, ''), created_at, updated_at
FROM analytics_goals WHERE tracker_id = ? ORDER BY created_at, id`, trackerID)
	if err != nil {
		return nil, fmt.Errorf("list analytics goals: %w", err)
	}
	defer rows.Close()
	goals := make([]AnalyticsGoal, 0)
	for rows.Next() {
		var goal AnalyticsGoal
		if err := rows.Scan(&goal.ID, &goal.TrackerID, &goal.Name, &goal.ActionType, &goal.ActionValue,
			&goal.Hostname, &goal.CreatedAtMillis, &goal.UpdatedAtMillis); err != nil {
			return nil, fmt.Errorf("scan analytics goal: %w", err)
		}
		goals = append(goals, goal)
	}
	return goals, rows.Err()
}

func (store *Store) CreateAnalyticsGoal(ctx context.Context, goal AnalyticsGoal) error {
	if err := validateAnalyticsGoal(goal); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO analytics_goals(id, tracker_id, name, action_type, action_value, hostname, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
			goal.ID, goal.TrackerID, goal.Name, goal.ActionType, goal.ActionValue, goal.Hostname,
			goal.CreatedAtMillis, goal.UpdatedAtMillis)
		if err != nil {
			return fmt.Errorf("create analytics goal: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateAnalyticsGoal(ctx context.Context, goal AnalyticsGoal, expectedUpdatedAt int64) error {
	if err := validateAnalyticsGoal(goal); err != nil || expectedUpdatedAt <= 0 {
		if err != nil {
			return err
		}
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := requireAnalyticsObject(ctx, transaction, ErrAnalyticsGoalNotFound,
			`SELECT 1 FROM analytics_goals WHERE id = ? AND tracker_id = ?`, goal.ID, goal.TrackerID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_goals SET name = ?, action_type = ?, action_value = ?, hostname = NULLIF(?, ''), updated_at = ?
WHERE id = ? AND tracker_id = ? AND updated_at = ?`,
			goal.Name, goal.ActionType, goal.ActionValue, goal.Hostname, goal.UpdatedAtMillis,
			goal.ID, goal.TrackerID, expectedUpdatedAt)
		if err != nil {
			return fmt.Errorf("update analytics goal: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChanged)
	})
}

func (store *Store) DeleteAnalyticsGoal(ctx context.Context, trackerID, goalID string) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var inUse int
		if err := transaction.QueryRowContext(ctx, `
SELECT COUNT(*) FROM analytics_experiments e
JOIN analytics_flags f ON f.id = e.flag_id
WHERE f.tracker_id = ? AND json_extract(e.metric_json, '$.goalId') = ?`, trackerID, goalID).Scan(&inUse); err != nil {
			return fmt.Errorf("count analytics goal experiments: %w", err)
		}
		if inUse > 0 {
			return ErrAnalyticsExperimentConflict
		}
		result, err := transaction.ExecContext(ctx, `
DELETE FROM analytics_goals WHERE id = ? AND tracker_id = ?`, goalID, trackerID)
		if err != nil {
			return fmt.Errorf("delete analytics goal: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsGoalNotFound)
	})
}

func (store *Store) AnalyticsFunnels(ctx context.Context, trackerID string) ([]AnalyticsFunnel, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, tracker_id, name, window_value, window_unit, steps_json, created_at, updated_at
FROM analytics_funnels WHERE tracker_id = ? ORDER BY created_at, id`, trackerID)
	if err != nil {
		return nil, fmt.Errorf("list analytics funnels: %w", err)
	}
	defer rows.Close()
	funnels := make([]AnalyticsFunnel, 0)
	for rows.Next() {
		funnel, err := scanAnalyticsFunnel(rows)
		if err != nil {
			return nil, err
		}
		funnels = append(funnels, funnel)
	}
	return funnels, rows.Err()
}

func (store *Store) AnalyticsFunnel(ctx context.Context, trackerID, funnelID string) (AnalyticsFunnel, error) {
	row := store.database.QueryRowContext(ctx, `
SELECT id, tracker_id, name, window_value, window_unit, steps_json, created_at, updated_at
FROM analytics_funnels WHERE id = ? AND tracker_id = ?`, funnelID, trackerID)
	funnel, err := scanAnalyticsFunnel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsFunnel{}, ErrAnalyticsFunnelNotFound
	}
	return funnel, err
}

func (store *Store) CreateAnalyticsFunnel(ctx context.Context, funnel AnalyticsFunnel) error {
	encoded, err := marshalAnalyticsFunnel(funnel)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO analytics_funnels(id, tracker_id, name, window_value, window_unit, steps_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			funnel.ID, funnel.TrackerID, funnel.Name, funnel.WindowValue, funnel.WindowUnit, encoded,
			funnel.CreatedAtMillis, funnel.UpdatedAtMillis)
		if err != nil {
			return fmt.Errorf("create analytics funnel: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateAnalyticsFunnel(ctx context.Context, funnel AnalyticsFunnel, expectedUpdatedAt int64) error {
	encoded, err := marshalAnalyticsFunnel(funnel)
	if err != nil || expectedUpdatedAt <= 0 {
		if err != nil {
			return err
		}
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := requireAnalyticsObject(ctx, transaction, ErrAnalyticsFunnelNotFound,
			`SELECT 1 FROM analytics_funnels WHERE id = ? AND tracker_id = ?`, funnel.ID, funnel.TrackerID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_funnels SET name = ?, window_value = ?, window_unit = ?, steps_json = ?, updated_at = ?
WHERE id = ? AND tracker_id = ? AND updated_at = ?`,
			funnel.Name, funnel.WindowValue, funnel.WindowUnit, encoded, funnel.UpdatedAtMillis,
			funnel.ID, funnel.TrackerID, expectedUpdatedAt)
		if err != nil {
			return fmt.Errorf("update analytics funnel: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChanged)
	})
}

func (store *Store) DeleteAnalyticsFunnel(ctx context.Context, trackerID, funnelID string) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM analytics_funnels WHERE id = ? AND tracker_id = ?`, funnelID, trackerID)
		if err != nil {
			return fmt.Errorf("delete analytics funnel: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsFunnelNotFound)
	})
}

func (store *Store) AnalyticsCharts(ctx context.Context, trackerID string) ([]AnalyticsChart, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, tracker_id, title, sql, visualization, legend, IFNULL(unit, ''), created_at, updated_at
FROM analytics_charts WHERE tracker_id = ? ORDER BY created_at, id`, trackerID)
	if err != nil {
		return nil, fmt.Errorf("list analytics charts: %w", err)
	}
	defer rows.Close()
	charts := make([]AnalyticsChart, 0)
	for rows.Next() {
		var chart AnalyticsChart
		if err := rows.Scan(&chart.ID, &chart.TrackerID, &chart.Title, &chart.SQL, &chart.Visualization,
			&chart.Legend, &chart.Unit, &chart.CreatedAtMillis, &chart.UpdatedAtMillis); err != nil {
			return nil, fmt.Errorf("scan analytics chart: %w", err)
		}
		charts = append(charts, chart)
	}
	return charts, rows.Err()
}

func (store *Store) CreateAnalyticsChart(ctx context.Context, chart AnalyticsChart) error {
	if err := validateAnalyticsChart(chart); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO analytics_charts(id, tracker_id, title, sql, visualization, legend, unit, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
			chart.ID, chart.TrackerID, chart.Title, chart.SQL, chart.Visualization, chart.Legend, chart.Unit,
			chart.CreatedAtMillis, chart.UpdatedAtMillis)
		if err != nil {
			return fmt.Errorf("create analytics chart: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateAnalyticsChart(ctx context.Context, chart AnalyticsChart, expectedUpdatedAt int64) error {
	if err := validateAnalyticsChart(chart); err != nil || expectedUpdatedAt <= 0 {
		if err != nil {
			return err
		}
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := requireAnalyticsObject(ctx, transaction, ErrAnalyticsChartNotFound,
			`SELECT 1 FROM analytics_charts WHERE id = ? AND tracker_id = ?`, chart.ID, chart.TrackerID); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_charts SET title = ?, sql = ?, visualization = ?, legend = ?, unit = NULLIF(?, ''), updated_at = ?
WHERE id = ? AND tracker_id = ? AND updated_at = ?`,
			chart.Title, chart.SQL, chart.Visualization, chart.Legend, chart.Unit, chart.UpdatedAtMillis,
			chart.ID, chart.TrackerID, expectedUpdatedAt)
		if err != nil {
			return fmt.Errorf("update analytics chart: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChanged)
	})
}

func (store *Store) DeleteAnalyticsChart(ctx context.Context, trackerID, chartID string) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
DELETE FROM analytics_charts WHERE id = ? AND tracker_id = ?`, chartID, trackerID)
		if err != nil {
			return fmt.Errorf("delete analytics chart: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChartNotFound)
	})
}

func (store *Store) AnalyticsFlags(ctx context.Context, trackerID string) ([]AnalyticsFlag, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at
FROM analytics_flags WHERE tracker_id = ? ORDER BY created_at, id`, trackerID)
	if err != nil {
		return nil, fmt.Errorf("list analytics flags: %w", err)
	}
	defer rows.Close()
	flags := make([]AnalyticsFlag, 0)
	for rows.Next() {
		flag, err := scanAnalyticsFlag(rows)
		if err != nil {
			return nil, err
		}
		flags = append(flags, flag)
	}
	return flags, rows.Err()
}

func (store *Store) AnalyticsFlag(ctx context.Context, trackerID, flagID string) (AnalyticsFlag, error) {
	row := store.database.QueryRowContext(ctx, `
SELECT id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at
FROM analytics_flags WHERE id = ? AND tracker_id = ?`, flagID, trackerID)
	flag, err := scanAnalyticsFlag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsFlag{}, ErrAnalyticsFlagNotFound
	}
	return flag, err
}

func (store *Store) AnalyticsFlagByKey(ctx context.Context, trackerID, key string) (AnalyticsFlag, error) {
	row := store.database.QueryRowContext(ctx, `
SELECT id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at
FROM analytics_flags WHERE tracker_id = ? AND key = ?`, trackerID, key)
	flag, err := scanAnalyticsFlag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsFlag{}, ErrAnalyticsFlagNotFound
	}
	return flag, err
}

func (store *Store) CreateAnalyticsFlag(ctx context.Context, flag AnalyticsFlag) error {
	encoded, err := marshalAnalyticsFlag(flag)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO analytics_flags(id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			flag.ID, flag.TrackerID, flag.Key, flag.Description, flag.Type, boolToInt(flag.Enabled),
			encoded.variants, encoded.payload, encoded.targeting, flag.CreatedAtMillis, flag.UpdatedAtMillis)
		if err != nil {
			if isUniqueConstraint(err) {
				return ErrAnalyticsFlagConflict
			}
			return fmt.Errorf("create analytics flag: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateAnalyticsFlag(ctx context.Context, flag AnalyticsFlag, expectedUpdatedAt int64) error {
	encoded, err := marshalAnalyticsFlag(flag)
	if err != nil || expectedUpdatedAt <= 0 {
		if err != nil {
			return err
		}
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		current, err := scanAnalyticsFlag(transaction.QueryRowContext(ctx, `
SELECT id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at
FROM analytics_flags WHERE id = ? AND tracker_id = ?`, flag.ID, flag.TrackerID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAnalyticsFlagNotFound
		}
		if err != nil {
			return err
		}
		if current.Key != flag.Key || current.Type != flag.Type || !sameAnalyticsVariants(current.Variants, flag.Variants) {
			var running int
			if err := transaction.QueryRowContext(ctx, `
SELECT COUNT(*) FROM analytics_experiments WHERE flag_id = ? AND ended_at IS NULL`, flag.ID).Scan(&running); err != nil {
				return fmt.Errorf("count running analytics experiments: %w", err)
			}
			if running > 0 {
				return ErrAnalyticsInvalid
			}
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_flags SET key = ?, description = ?, flag_type = ?, enabled = ?, variants_json = ?, payload_json = ?, targeting_json = ?, updated_at = ?
WHERE id = ? AND tracker_id = ? AND updated_at = ?`,
			flag.Key, flag.Description, flag.Type, boolToInt(flag.Enabled), encoded.variants, encoded.payload, encoded.targeting,
			flag.UpdatedAtMillis, flag.ID, flag.TrackerID, expectedUpdatedAt)
		if err != nil {
			if isUniqueConstraint(err) {
				return ErrAnalyticsFlagConflict
			}
			return fmt.Errorf("update analytics flag: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChanged)
	})
}

func (store *Store) DeleteAnalyticsFlag(ctx context.Context, trackerID, flagID string) error {
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var running int
		if err := transaction.QueryRowContext(ctx, `
SELECT COUNT(*) FROM analytics_experiments WHERE flag_id = ? AND ended_at IS NULL`, flagID).Scan(&running); err != nil {
			return fmt.Errorf("count running analytics experiments: %w", err)
		}
		if running > 0 {
			return ErrAnalyticsExperimentConflict
		}
		result, err := transaction.ExecContext(ctx, `
DELETE FROM analytics_flags WHERE id = ? AND tracker_id = ?`, flagID, trackerID)
		if err != nil {
			return fmt.Errorf("delete analytics flag: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsFlagNotFound)
	})
}

func (store *Store) AnalyticsExperiments(ctx context.Context, trackerID string) ([]AnalyticsExperiment, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT e.id, e.flag_id, f.tracker_id, e.control_variant, e.metric_json, e.window_value, e.window_unit,
  e.started_at, IFNULL(e.ended_at, 0), e.created_at, e.updated_at
FROM analytics_experiments e
JOIN analytics_flags f ON f.id = e.flag_id
WHERE f.tracker_id = ?
ORDER BY e.created_at, e.id`, trackerID)
	if err != nil {
		return nil, fmt.Errorf("list analytics experiments: %w", err)
	}
	defer rows.Close()
	experiments := make([]AnalyticsExperiment, 0)
	for rows.Next() {
		experiment, err := scanAnalyticsExperiment(rows)
		if err != nil {
			return nil, err
		}
		experiments = append(experiments, experiment)
	}
	return experiments, rows.Err()
}

func (store *Store) RunningAnalyticsExperiment(ctx context.Context, flagID string, atMillis int64) (AnalyticsExperiment, error) {
	row := store.database.QueryRowContext(ctx, `
SELECT e.id, e.flag_id, f.tracker_id, e.control_variant, e.metric_json, e.window_value, e.window_unit,
  e.started_at, IFNULL(e.ended_at, 0), e.created_at, e.updated_at
FROM analytics_experiments e
JOIN analytics_flags f ON f.id = e.flag_id
WHERE e.flag_id = ? AND e.started_at <= ? AND (e.ended_at IS NULL OR e.ended_at >= ?)
ORDER BY e.started_at DESC LIMIT 1`, flagID, atMillis, atMillis)
	experiment, err := scanAnalyticsExperiment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsExperiment{}, ErrAnalyticsExperimentNotFound
	}
	return experiment, err
}

func (store *Store) CreateAnalyticsExperiment(ctx context.Context, experiment AnalyticsExperiment) error {
	encoded, err := marshalAnalyticsExperiment(experiment)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		flag, err := scanAnalyticsFlag(transaction.QueryRowContext(ctx, `
SELECT id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at
FROM analytics_flags WHERE id = ?`, experiment.FlagID))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAnalyticsFlagNotFound
		}
		if err != nil {
			return err
		}
		if flag.TrackerID != experiment.TrackerID {
			return ErrAnalyticsFlagNotFound
		}
		knownVariant := false
		for _, variant := range flag.Variants {
			if variant.Key == experiment.ControlVariant {
				knownVariant = true
				break
			}
		}
		if !knownVariant {
			return ErrAnalyticsInvalid
		}
		if experiment.Metric.GoalID != "" {
			if err := requireAnalyticsObject(ctx, transaction, ErrAnalyticsGoalNotFound,
				`SELECT 1 FROM analytics_goals WHERE id = ? AND tracker_id = ?`, experiment.Metric.GoalID, experiment.TrackerID); err != nil {
				return err
			}
		}
		var running int
		if err := transaction.QueryRowContext(ctx, `
SELECT COUNT(*) FROM analytics_experiments
WHERE flag_id = ? AND ended_at IS NULL`, experiment.FlagID).Scan(&running); err != nil {
			return fmt.Errorf("count running analytics experiments: %w", err)
		}
		if running > 0 {
			return ErrAnalyticsExperimentConflict
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO analytics_experiments(id, flag_id, control_variant, metric_json, window_value, window_unit, started_at, ended_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
			experiment.ID, experiment.FlagID, experiment.ControlVariant, encoded,
			experiment.WindowValue, experiment.WindowUnit, experiment.StartedAtMillis,
			experiment.CreatedAtMillis, experiment.UpdatedAtMillis)
		if err != nil {
			return fmt.Errorf("create analytics experiment: %w", err)
		}
		return nil
	})
}

func (store *Store) StopAnalyticsExperiment(ctx context.Context, trackerID, experimentID string, endedAt, expectedUpdatedAt int64) error {
	if endedAt <= 0 || expectedUpdatedAt <= 0 {
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := requireRunningAnalyticsExperiment(ctx, transaction, trackerID, experimentID, expectedUpdatedAt); err != nil {
			return err
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_experiments SET ended_at = ?, updated_at = ?
WHERE id = ? AND updated_at = ? AND ended_at IS NULL AND flag_id IN (
  SELECT id FROM analytics_flags WHERE tracker_id = ?
)`, endedAt, endedAt, experimentID, expectedUpdatedAt, trackerID)
		if err != nil {
			return fmt.Errorf("stop analytics experiment: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChanged)
	})
}

func (store *Store) ShipAnalyticsExperiment(ctx context.Context, trackerID, experimentID, variant string, expectedUpdatedAt, now int64) error {
	if now <= 0 || expectedUpdatedAt <= 0 || strings.TrimSpace(variant) == "" {
		return ErrAnalyticsInvalid
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		flag, experiment, err := scanRunningExperimentFlag(ctx, transaction, trackerID, experimentID)
		if err != nil {
			return err
		}
		if experiment.UpdatedAtMillis != expectedUpdatedAt {
			return ErrAnalyticsChanged
		}
		if experiment.EndedAtMillis != 0 {
			return ErrAnalyticsExperimentConflict
		}
		updated := false
		for index := range flag.Variants {
			if flag.Variants[index].Key == variant {
				flag.Variants[index].Percentage = 100
				updated = true
			} else {
				flag.Variants[index].Percentage = 0
			}
		}
		if !updated {
			return ErrAnalyticsInvalid
		}
		targeting, err := pinAnalyticsWinner(flag.TargetingJSON, variant)
		if err != nil {
			return err
		}
		flag.TargetingJSON = targeting
		flag.UpdatedAtMillis = now
		encoded, err := marshalAnalyticsFlag(flag)
		if err != nil {
			return err
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE analytics_flags SET variants_json = ?, targeting_json = ?, updated_at = ? WHERE id = ? AND tracker_id = ?`,
			encoded.variants, encoded.targeting, flag.UpdatedAtMillis, flag.ID, trackerID); err != nil {
			return fmt.Errorf("ship analytics flag: %w", err)
		}
		result, err := transaction.ExecContext(ctx, `
UPDATE analytics_experiments SET ended_at = ?, updated_at = ?
WHERE id = ? AND updated_at = ? AND ended_at IS NULL`, now, now, experimentID, expectedUpdatedAt)
		if err != nil {
			return fmt.Errorf("ship analytics experiment: %w", err)
		}
		return requireOneAnalyticsChange(result, ErrAnalyticsChanged)
	})
}

func scanRunningExperimentFlag(ctx context.Context, transaction *sql.Tx, trackerID, experimentID string) (AnalyticsFlag, AnalyticsExperiment, error) {
	row := transaction.QueryRowContext(ctx, `
SELECT e.id, e.flag_id, f.tracker_id, e.control_variant, e.metric_json, e.window_value, e.window_unit,
  e.started_at, IFNULL(e.ended_at, 0), e.created_at, e.updated_at
FROM analytics_experiments e
JOIN analytics_flags f ON f.id = e.flag_id
WHERE e.id = ? AND f.tracker_id = ?`, experimentID, trackerID)
	experiment, err := scanAnalyticsExperiment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsFlag{}, AnalyticsExperiment{}, ErrAnalyticsExperimentNotFound
	}
	if err != nil {
		return AnalyticsFlag{}, AnalyticsExperiment{}, err
	}
	flag, err := scanAnalyticsFlag(transaction.QueryRowContext(ctx, `
SELECT id, tracker_id, key, description, flag_type, enabled, variants_json, payload_json, targeting_json, created_at, updated_at
FROM analytics_flags WHERE id = ? AND tracker_id = ?`, experiment.FlagID, trackerID))
	if errors.Is(err, sql.ErrNoRows) {
		return AnalyticsFlag{}, AnalyticsExperiment{}, ErrAnalyticsFlagNotFound
	}
	return flag, experiment, err
}

func pinAnalyticsWinner(targetingJSON, variant string) (string, error) {
	if strings.TrimSpace(targetingJSON) != "" && !json.Valid([]byte(targetingJSON)) {
		return "", ErrAnalyticsInvalid
	}
	encoded, err := json.Marshal(map[string]any{
		"groups": []map[string]any{{
			"properties":         []any{},
			"rollout_percentage": 100,
			"variant":            variant,
		}},
	})
	if err != nil {
		return "", fmt.Errorf("pin analytics winner: %w", err)
	}
	return string(encoded), nil
}

func rejectOverlappingTrackerRoot(ctx context.Context, transaction *sql.Tx, root, exceptID string) error {
	rows, err := transaction.QueryContext(ctx, `SELECT id, root_domain FROM analytics_trackers`)
	if err != nil {
		return fmt.Errorf("list tracker roots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, existing string
		if err := rows.Scan(&id, &existing); err != nil {
			return fmt.Errorf("scan tracker root: %w", err)
		}
		if exceptID != "" && id == exceptID {
			continue
		}
		if RootsOverlap(root, existing) {
			return ErrAnalyticsTrackerConflict
		}
	}
	return rows.Err()
}

func validateAnalyticsTracker(tracker AnalyticsTracker) error {
	if tracker.ID == "" || tracker.ProjectID == "" || strings.TrimSpace(tracker.Name) == "" {
		return ErrAnalyticsInvalid
	}
	root := NormalizeTrackerRoot(tracker.RootDomain)
	if root == "" || strings.Contains(root, "/") || strings.Contains(root, " ") {
		return ErrAnalyticsInvalid
	}
	switch tracker.Mode {
	case AnalyticsModeCookieless, AnalyticsModeOptOut, AnalyticsModeOptIn:
		return nil
	default:
		return ErrAnalyticsInvalid
	}
}

func validateAnalyticsGoal(goal AnalyticsGoal) error {
	if goal.ID == "" || goal.TrackerID == "" || strings.TrimSpace(goal.Name) == "" || strings.TrimSpace(goal.ActionValue) == "" {
		return ErrAnalyticsInvalid
	}
	if goal.ActionType != "path" && goal.ActionType != "event" {
		return ErrAnalyticsInvalid
	}
	return nil
}

func validateAnalyticsChart(chart AnalyticsChart) error {
	if chart.ID == "" || chart.TrackerID == "" || strings.TrimSpace(chart.Title) == "" || strings.TrimSpace(chart.SQL) == "" {
		return ErrAnalyticsInvalid
	}
	switch chart.Visualization {
	case "line", "area", "bar", "value", "table":
		return nil
	default:
		return ErrAnalyticsInvalid
	}
}

type funnelScanner interface {
	Scan(dest ...any) error
}

func scanAnalyticsFunnel(scanner funnelScanner) (AnalyticsFunnel, error) {
	var funnel AnalyticsFunnel
	var encoded string
	if err := scanner.Scan(&funnel.ID, &funnel.TrackerID, &funnel.Name, &funnel.WindowValue, &funnel.WindowUnit,
		&encoded, &funnel.CreatedAtMillis, &funnel.UpdatedAtMillis); err != nil {
		return AnalyticsFunnel{}, err
	}
	if err := json.Unmarshal([]byte(encoded), &funnel.Steps); err != nil {
		return AnalyticsFunnel{}, fmt.Errorf("decode analytics funnel steps: %w", err)
	}
	return funnel, nil
}

func marshalAnalyticsFunnel(funnel AnalyticsFunnel) (string, error) {
	if funnel.ID == "" || funnel.TrackerID == "" || strings.TrimSpace(funnel.Name) == "" {
		return "", ErrAnalyticsInvalid
	}
	if funnel.WindowValue < 1 || (funnel.WindowUnit != "minute" && funnel.WindowUnit != "hour" && funnel.WindowUnit != "day") {
		return "", ErrAnalyticsInvalid
	}
	if len(funnel.Steps) < 2 || len(funnel.Steps) > 8 {
		return "", ErrAnalyticsInvalid
	}
	for _, step := range funnel.Steps {
		if (step.Type != "path" && step.Type != "event") || strings.TrimSpace(step.Value) == "" {
			return "", ErrAnalyticsInvalid
		}
	}
	encoded, err := json.Marshal(funnel.Steps)
	if err != nil {
		return "", fmt.Errorf("encode analytics funnel steps: %w", err)
	}
	return string(encoded), nil
}

type flagScanner interface {
	Scan(dest ...any) error
}

func scanAnalyticsFlag(scanner flagScanner) (AnalyticsFlag, error) {
	var flag AnalyticsFlag
	var enabled int
	var variants, payload, targeting string
	if err := scanner.Scan(&flag.ID, &flag.TrackerID, &flag.Key, &flag.Description, &flag.Type, &enabled,
		&variants, &payload, &targeting, &flag.CreatedAtMillis, &flag.UpdatedAtMillis); err != nil {
		return AnalyticsFlag{}, err
	}
	flag.Enabled = enabled == 1
	flag.PayloadJSON = payload
	flag.TargetingJSON = targeting
	if err := json.Unmarshal([]byte(variants), &flag.Variants); err != nil {
		return AnalyticsFlag{}, fmt.Errorf("decode analytics flag variants: %w", err)
	}
	return flag, nil
}

type encodedFlag struct {
	variants, payload, targeting string
}

func marshalAnalyticsFlag(flag AnalyticsFlag) (encodedFlag, error) {
	if flag.ID == "" || flag.TrackerID == "" || strings.TrimSpace(flag.Key) == "" {
		return encodedFlag{}, ErrAnalyticsInvalid
	}
	if flag.Type != "boolean" && flag.Type != "multivariate" {
		return encodedFlag{}, ErrAnalyticsInvalid
	}
	if len(flag.Variants) == 0 {
		return encodedFlag{}, ErrAnalyticsInvalid
	}
	if flag.Type == "boolean" {
		hasTrue, hasFalse := false, false
		for _, variant := range flag.Variants {
			hasTrue = hasTrue || variant.Key == "true"
			hasFalse = hasFalse || variant.Key == "false"
		}
		if !hasTrue || !hasFalse {
			return encodedFlag{}, ErrAnalyticsInvalid
		}
	}
	keys := make(map[string]struct{}, len(flag.Variants))
	for _, variant := range flag.Variants {
		if strings.TrimSpace(variant.Key) == "" {
			return encodedFlag{}, ErrAnalyticsInvalid
		}
		if _, exists := keys[variant.Key]; exists {
			return encodedFlag{}, ErrAnalyticsInvalid
		}
		keys[variant.Key] = struct{}{}
	}
	variants, err := json.Marshal(flag.Variants)
	if err != nil {
		return encodedFlag{}, fmt.Errorf("encode analytics flag variants: %w", err)
	}
	payload := flag.PayloadJSON
	if payload == "" {
		payload = "null"
	}
	if !json.Valid([]byte(payload)) {
		return encodedFlag{}, ErrAnalyticsInvalid
	}
	targeting := flag.TargetingJSON
	if targeting == "" {
		targeting = `{"groups":[]}`
	}
	if !json.Valid([]byte(targeting)) {
		return encodedFlag{}, ErrAnalyticsInvalid
	}
	var groups struct {
		Groups []struct {
			Variant *string `json:"variant"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(targeting), &groups); err != nil {
		return encodedFlag{}, ErrAnalyticsInvalid
	}
	for _, group := range groups.Groups {
		if group.Variant == nil || *group.Variant == "" {
			continue
		}
		if _, ok := keys[*group.Variant]; !ok {
			return encodedFlag{}, ErrAnalyticsInvalid
		}
	}
	return encodedFlag{variants: string(variants), payload: payload, targeting: targeting}, nil
}

func sameAnalyticsVariants(left, right []AnalyticsFlagVariant) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Key != right[index].Key || left[index].Percentage != right[index].Percentage {
			return false
		}
	}
	return true
}

type experimentScanner interface {
	Scan(dest ...any) error
}

func scanAnalyticsExperiment(scanner experimentScanner) (AnalyticsExperiment, error) {
	var experiment AnalyticsExperiment
	var encoded string
	if err := scanner.Scan(&experiment.ID, &experiment.FlagID, &experiment.TrackerID, &experiment.ControlVariant,
		&encoded, &experiment.WindowValue, &experiment.WindowUnit, &experiment.StartedAtMillis, &experiment.EndedAtMillis,
		&experiment.CreatedAtMillis, &experiment.UpdatedAtMillis); err != nil {
		return AnalyticsExperiment{}, err
	}
	if err := json.Unmarshal([]byte(encoded), &experiment.Metric); err != nil {
		return AnalyticsExperiment{}, fmt.Errorf("decode analytics experiment metric: %w", err)
	}
	return experiment, nil
}

func marshalAnalyticsExperiment(experiment AnalyticsExperiment) (string, error) {
	if experiment.ID == "" || experiment.FlagID == "" || strings.TrimSpace(experiment.ControlVariant) == "" {
		return "", ErrAnalyticsInvalid
	}
	if experiment.WindowValue < 1 || (experiment.WindowUnit != "minute" && experiment.WindowUnit != "hour" && experiment.WindowUnit != "day") {
		return "", ErrAnalyticsInvalid
	}
	if experiment.Metric.GoalID == "" && strings.TrimSpace(experiment.Metric.EventName) == "" {
		return "", ErrAnalyticsInvalid
	}
	encoded, err := json.Marshal(experiment.Metric)
	if err != nil {
		return "", fmt.Errorf("encode analytics experiment metric: %w", err)
	}
	return string(encoded), nil
}

func requireRunningAnalyticsExperiment(ctx context.Context, transaction *sql.Tx, trackerID, experimentID string, expectedUpdatedAt int64) error {
	var updatedAt int64
	var endedAt sql.NullInt64
	err := transaction.QueryRowContext(ctx, `
SELECT e.updated_at, e.ended_at
FROM analytics_experiments e
JOIN analytics_flags f ON f.id = e.flag_id
WHERE e.id = ? AND f.tracker_id = ?`, experimentID, trackerID).Scan(&updatedAt, &endedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAnalyticsExperimentNotFound
	}
	if err != nil {
		return err
	}
	if updatedAt != expectedUpdatedAt {
		return ErrAnalyticsChanged
	}
	if endedAt.Valid {
		return ErrAnalyticsExperimentConflict
	}
	return nil
}

func requireAnalyticsObject(ctx context.Context, transaction *sql.Tx, notFound error, query string, args ...any) error {
	var exists int
	err := transaction.QueryRowContext(ctx, query, args...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound
	}
	return err
}

func requireOneAnalyticsChange(result sql.Result, missing error) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return missing
	}
	return nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func isUniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}

func ensureProjectExists(ctx context.Context, querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, projectID string) error {
	var exists int
	if err := querier.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)`, projectID).Scan(&exists); err != nil {
		return fmt.Errorf("check project: %w", err)
	}
	if exists == 0 {
		return ErrProjectNotFound
	}
	return nil
}
