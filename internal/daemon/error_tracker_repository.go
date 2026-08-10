package daemon

import (
	"context"

	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/state"
)

type liveErrorTrackerRepository struct {
	store  *state.Store
	router *ingress.Router
}

func (repository *liveErrorTrackerRepository) CreateErrorTracker(ctx context.Context, input state.CreateErrorTracker) (state.ErrorTracker, error) {
	created, err := repository.store.CreateErrorTracker(ctx, input)
	if err == nil {
		_ = repository.reloadPublicRoutes(ctx)
	}
	return created, err
}

func (repository *liveErrorTrackerRepository) ErrorTrackerInProject(ctx context.Context, projectID, trackerID string) (state.ErrorTracker, error) {
	return repository.store.ErrorTrackerInProject(ctx, projectID, trackerID)
}

func (repository *liveErrorTrackerRepository) ErrorTrackersByProject(ctx context.Context, projectID string) ([]state.ErrorTracker, error) {
	return repository.store.ErrorTrackersByProject(ctx, projectID)
}

func (repository *liveErrorTrackerRepository) UpdateErrorTrackerPublicAccess(ctx context.Context, input state.UpdateErrorTrackerPublicAccess) (state.ErrorTracker, error) {
	updated, err := repository.store.UpdateErrorTrackerPublicAccess(ctx, input)
	if err == nil {
		_ = repository.reloadPublicRoutes(ctx)
	}
	return updated, err
}

func (repository *liveErrorTrackerRepository) ErrorTrackerByHostname(ctx context.Context, hostname string) (state.ErrorTracker, error) {
	return repository.store.ErrorTrackerByHostname(ctx, hostname)
}

func (repository *liveErrorTrackerRepository) reloadPublicRoutes(ctx context.Context) error {
	if repository.router == nil {
		return nil
	}
	trackers, err := repository.store.ErrorTrackers(ctx)
	if err != nil {
		return err
	}
	hostnames := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		if tracker.PublicHostname != "" {
			hostnames = append(hostnames, tracker.PublicHostname)
		}
	}
	repository.router.ReloadErrorTrackers(hostnames)
	return nil
}
