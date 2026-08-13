package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const maximumMetricCharts = 20

const (
	MetricScopeInstallation = "installation"
	MetricScopeProject      = "project"
	MetricScopeService      = "service"
)

var (
	ErrMetricChartChanged  = errors.New("metric chart changed")
	ErrMetricChartLimit    = errors.New("metric chart limit reached")
	ErrMetricChartNotFound = errors.New("metric chart not found")
	ErrMetricScopeNotFound = errors.New("metric scope not found")
)

type MetricScope struct {
	Kind      string
	ProjectID string
	ServiceID string
}

type MetricChart struct {
	ID              string
	Scope           MetricScope
	Title           string
	SQL             string
	Visualization   string
	Legend          string
	Unit            string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

func (store *Store) MetricCharts(ctx context.Context, scope MetricScope) ([]MetricChart, error) {
	if err := ensureMetricScope(ctx, store.database, scope); err != nil {
		return nil, err
	}
	projectID, serviceID := scopeDatabaseIDs(scope)
	rows, err := store.database.QueryContext(ctx, `
SELECT id, title, sql, visualization, legend, IFNULL(unit, ''), created_at, updated_at
FROM metric_charts
WHERE scope_kind = ? AND project_id IS ? AND service_id IS ?
ORDER BY created_at, id`, scope.Kind, projectID, serviceID)
	if err != nil {
		return nil, fmt.Errorf("list metric charts: %w", err)
	}
	defer rows.Close()
	charts := make([]MetricChart, 0)
	for rows.Next() {
		chart := MetricChart{Scope: scope}
		if err := rows.Scan(&chart.ID, &chart.Title, &chart.SQL, &chart.Visualization,
			&chart.Legend, &chart.Unit, &chart.CreatedAtMillis, &chart.UpdatedAtMillis); err != nil {
			return nil, fmt.Errorf("scan metric chart: %w", err)
		}
		charts = append(charts, chart)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metric charts: %w", err)
	}
	return charts, nil
}

func (store *Store) CreateMetricChart(ctx context.Context, chart MetricChart) error {
	if err := validateMetricChart(chart, true); err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMetricScope(ctx, transaction, chart.Scope); err != nil {
			return err
		}
		projectID, serviceID := scopeDatabaseIDs(chart.Scope)
		var chartCount int
		if err := transaction.QueryRowContext(ctx, `
SELECT COUNT(*) FROM metric_charts
WHERE scope_kind = ? AND project_id IS ? AND service_id IS ?`,
			chart.Scope.Kind, projectID, serviceID).Scan(&chartCount); err != nil {
			return fmt.Errorf("count metric charts: %w", err)
		}
		if chartCount >= maximumMetricCharts {
			return ErrMetricChartLimit
		}
		_, err := transaction.ExecContext(ctx, `
INSERT INTO metric_charts(
  id, scope_kind, project_id, service_id, title, sql, visualization, legend, unit, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
			chart.ID, chart.Scope.Kind, projectID, serviceID, chart.Title, chart.SQL,
			chart.Visualization, chart.Legend, chart.Unit, chart.CreatedAtMillis, chart.UpdatedAtMillis)
		if err != nil {
			return fmt.Errorf("create metric chart: %w", err)
		}
		return nil
	})
}

func (store *Store) UpdateMetricChart(ctx context.Context, chart MetricChart, expectedUpdatedAt int64) error {
	if err := validateMetricChart(chart, false); err != nil || expectedUpdatedAt <= 0 || chart.UpdatedAtMillis <= expectedUpdatedAt {
		if err != nil {
			return err
		}
		return errors.New("metric chart update is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMetricScope(ctx, transaction, chart.Scope); err != nil {
			return err
		}
		projectID, serviceID := scopeDatabaseIDs(chart.Scope)
		result, err := transaction.ExecContext(ctx, `
UPDATE metric_charts SET
  title = ?, sql = ?, visualization = ?, legend = ?, unit = NULLIF(?, ''), updated_at = ?
WHERE id = ? AND scope_kind = ? AND project_id IS ? AND service_id IS ? AND updated_at = ?`,
			chart.Title, chart.SQL, chart.Visualization, chart.Legend, chart.Unit, chart.UpdatedAtMillis,
			chart.ID, chart.Scope.Kind, projectID, serviceID, expectedUpdatedAt)
		if err != nil {
			return fmt.Errorf("update metric chart: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed == 1 {
			return nil
		}
		var exists int
		if err := transaction.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM metric_charts
  WHERE id = ? AND scope_kind = ? AND project_id IS ? AND service_id IS ?
)`, chart.ID, chart.Scope.Kind, projectID, serviceID).Scan(&exists); err != nil {
			return err
		}
		if exists == 1 {
			return ErrMetricChartChanged
		}
		return ErrMetricChartNotFound
	})
}

func (store *Store) DeleteMetricChart(ctx context.Context, scope MetricScope, chartID string) error {
	if err := validateMetricScope(scope); err != nil || chartID == "" {
		return errors.New("delete metric chart input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMetricScope(ctx, transaction, scope); err != nil {
			return err
		}
		projectID, serviceID := scopeDatabaseIDs(scope)
		result, err := transaction.ExecContext(ctx, `
DELETE FROM metric_charts
WHERE id = ? AND scope_kind = ? AND project_id IS ? AND service_id IS ?`,
			chartID, scope.Kind, projectID, serviceID)
		if err != nil {
			return fmt.Errorf("delete metric chart: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrMetricChartNotFound
		}
		return nil
	})
}

func (store *Store) MetricScopeServiceIDs(ctx context.Context, scope MetricScope) ([]string, error) {
	if err := ensureMetricScope(ctx, store.database, scope); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error
	switch scope.Kind {
	case MetricScopeService:
		return []string{scope.ServiceID}, nil
	case MetricScopeProject:
		rows, err = store.database.QueryContext(ctx, "SELECT id FROM services WHERE project_id = ? ORDER BY id", scope.ProjectID)
	case MetricScopeInstallation:
		rows, err = store.database.QueryContext(ctx, "SELECT id FROM services ORDER BY id")
	}
	if err != nil {
		return nil, fmt.Errorf("query metric scope services: %w", err)
	}
	defer rows.Close()
	serviceIDs := make([]string, 0)
	for rows.Next() {
		var serviceID string
		if err := rows.Scan(&serviceID); err != nil {
			return nil, fmt.Errorf("scan metric scope service: %w", err)
		}
		serviceIDs = append(serviceIDs, serviceID)
	}
	return serviceIDs, rows.Err()
}

type metricScopeQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func ensureMetricScope(ctx context.Context, queryer metricScopeQueryer, scope MetricScope) error {
	if err := validateMetricScope(scope); err != nil {
		return err
	}
	if scope.Kind == MetricScopeInstallation {
		return nil
	}
	query := "SELECT EXISTS(SELECT 1 FROM projects WHERE id = ?)"
	arguments := []any{scope.ProjectID}
	if scope.Kind == MetricScopeService {
		query = "SELECT EXISTS(SELECT 1 FROM services WHERE id = ? AND project_id = ?)"
		arguments = []any{scope.ServiceID, scope.ProjectID}
	}
	var exists int
	if err := queryer.QueryRowContext(ctx, query, arguments...).Scan(&exists); err != nil {
		return fmt.Errorf("check metric scope: %w", err)
	}
	if exists != 1 {
		return ErrMetricScopeNotFound
	}
	return nil
}

func validateMetricChart(chart MetricChart, creating bool) error {
	if err := validateMetricScope(chart.Scope); err != nil {
		return err
	}
	if chart.ID == "" || chart.Title == "" || len(chart.Title) > 80 || chart.SQL == "" || len(chart.SQL) > 16<<10 ||
		!validMetricChartVisualization(chart.Visualization) || len(chart.Legend) > 80 || len(chart.Unit) > 32 ||
		chart.UpdatedAtMillis <= 0 || (creating && (chart.CreatedAtMillis <= 0 || chart.CreatedAtMillis != chart.UpdatedAtMillis)) {
		return errors.New("metric chart input is incomplete")
	}
	return nil
}

func validateMetricScope(scope MetricScope) error {
	valid := false
	switch scope.Kind {
	case MetricScopeInstallation:
		valid = scope.ProjectID == "" && scope.ServiceID == ""
	case MetricScopeProject:
		valid = scope.ProjectID != "" && scope.ServiceID == ""
	case MetricScopeService:
		valid = scope.ProjectID != "" && scope.ServiceID != ""
	}
	if !valid {
		return errors.New("metric scope is invalid")
	}
	return nil
}

func scopeDatabaseIDs(scope MetricScope) (any, any) {
	var projectID, serviceID any
	if scope.Kind == MetricScopeProject {
		projectID = scope.ProjectID
	}
	if scope.Kind == MetricScopeService {
		serviceID = scope.ServiceID
	}
	return projectID, serviceID
}

func validMetricChartVisualization(value string) bool {
	return value == "line" || value == "area" || value == "bar" || value == "value"
}
