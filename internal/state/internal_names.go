package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/iivankin/platformd/internal/internaldns"
)

type InternalName struct {
	Found     bool
	ProjectID string
	HostID    string
}

func (store *Store) LookupInternalName(ctx context.Context, hostname string) (InternalName, error) {
	resource, projectName, ok := internaldns.ParseInternalName(hostname)
	if !ok {
		return InternalName{}, nil
	}
	var projectID string
	err := store.database.QueryRowContext(ctx, `SELECT id FROM projects WHERE name = ?`, projectName).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return InternalName{}, nil
	}
	if err != nil {
		return InternalName{}, err
	}
	if name, ok := strings.CutPrefix(resource, "errors-"); ok {
		return store.lookupServiceInternal(ctx, projectID, name, true)
	}
	if name, ok := strings.CutPrefix(resource, "otel-"); ok {
		return store.lookupServiceInternal(ctx, projectID, name, true)
	}
	if slug, ok := strings.CutPrefix(resource, "analytics-"); ok {
		return store.lookupAnalyticsInternal(ctx, projectID, slug)
	}
	if found, err := store.lookupServiceInternal(ctx, projectID, resource, false); err != nil || found.Found {
		return found, err
	}
	return store.lookupPrimaryResourceInternal(ctx, projectID, resource)
}

func (store *Store) lookupServiceInternal(ctx context.Context, projectID, name string, primary bool) (InternalName, error) {
	var hostID sql.NullString
	err := store.database.QueryRowContext(ctx, `
SELECT host_id FROM services WHERE project_id = ? AND name = ?`, projectID, name).Scan(&hostID)
	if errors.Is(err, sql.ErrNoRows) {
		return InternalName{}, nil
	}
	if err != nil {
		return InternalName{}, err
	}
	result := InternalName{Found: true, ProjectID: projectID}
	if !primary {
		result.HostID = hostID.String
	}
	return result, nil
}

func (store *Store) lookupAnalyticsInternal(ctx context.Context, projectID, slug string) (InternalName, error) {
	var exists int
	err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM analytics_trackers WHERE project_id = ?)`, projectID).Scan(&exists)
	if err != nil {
		return InternalName{}, err
	}
	if exists != 1 {
		return InternalName{}, nil
	}
	return InternalName{Found: true, ProjectID: projectID}, nil
}

func (store *Store) lookupPrimaryResourceInternal(ctx context.Context, projectID, name string) (InternalName, error) {
	var exists int
	err := store.database.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM managed_postgres WHERE project_id = ? AND name = ?
  UNION ALL
  SELECT 1 FROM managed_redis WHERE project_id = ? AND name = ?
  UNION ALL
  SELECT 1 FROM object_stores WHERE project_id = ? AND name = ?
  UNION ALL
  SELECT 1 FROM network_gateways WHERE project_id = ? AND name = ?
)`, projectID, name, projectID, name, projectID, name, projectID, name).Scan(&exists)
	if err != nil {
		return InternalName{}, err
	}
	if exists != 1 {
		return InternalName{}, nil
	}
	return InternalName{Found: true, ProjectID: projectID}, nil
}
