package errortracker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

var ErrInvalidInput = errors.New("invalid error tracker input")

type Repository interface {
	CreateErrorTracker(context.Context, state.CreateErrorTracker) (state.ErrorTracker, error)
	ErrorTrackerInProject(context.Context, string, string) (state.ErrorTracker, error)
	ErrorTrackersByProject(context.Context, string) ([]state.ErrorTracker, error)
	UpdateErrorTrackerPublicAccess(context.Context, state.UpdateErrorTrackerPublicAccess) (state.ErrorTracker, error)
}

type Actor struct {
	Kind  string
	ID    string
	Email string
}

type CreateInput struct {
	ProjectID      string
	Name           string
	PublicHostname string
	BackupPolicy   state.InitialBackupPolicy
	Actor          Actor
}

type Application struct {
	repository Repository
	runtime    *Manager
	covers     func(string) bool
	publicMu   *sync.Mutex
	now        func() time.Time
	newID      func() (string, error)
}

func NewApplication(repository Repository, runtime *Manager, covers func(string) bool, publicMu *sync.Mutex) (*Application, error) {
	if repository == nil || runtime == nil || covers == nil || publicMu == nil {
		return nil, errors.New("error tracker application dependencies are incomplete")
	}
	return &Application{
		repository: repository, runtime: runtime, covers: covers, publicMu: publicMu,
		now: time.Now, newID: id.New,
	}, nil
}

func (application *Application) Create(ctx context.Context, input CreateInput) (state.ErrorTracker, string, error) {
	if input.ProjectID == "" || input.Actor.ID == "" ||
		(input.Actor.Kind != "access" && input.Actor.Kind != "token") ||
		(input.Actor.Kind == "access" && input.Actor.Email == "") {
		return state.ErrorTracker{}, "", ErrInvalidInput
	}
	if err := resourcename.Validate(input.Name); err != nil {
		return state.ErrorTracker{}, "", errors.Join(ErrInvalidInput, err)
	}
	application.publicMu.Lock()
	defer application.publicMu.Unlock()
	if input.PublicHostname != "" {
		hostname, err := publichostname.Normalize(input.PublicHostname)
		if err != nil {
			return state.ErrorTracker{}, "", errors.Join(ErrInvalidInput, err)
		}
		if !application.covers(hostname) {
			return state.ErrorTracker{}, "", state.ErrCertificateCoverage
		}
		input.PublicHostname = hostname
	}
	identifiers := make([]string, 4)
	for index := range identifiers {
		value, err := application.newID()
		if err != nil {
			return state.ErrorTracker{}, "", err
		}
		identifiers[index] = value
	}
	now := application.now().UnixMilli()
	created, err := application.repository.CreateErrorTracker(ctx, state.CreateErrorTracker{
		ID: identifiers[0], ProjectID: input.ProjectID, Name: input.Name,
		VolumeID: identifiers[1], PublicHostname: input.PublicHostname, BackupPolicy: input.BackupPolicy,
		AuditEventID: identifiers[2], ActorKind: input.Actor.Kind, ActorID: input.Actor.ID,
		ActorEmail: input.Actor.Email, RequestCorrelationID: identifiers[3], CreatedAtMillis: now,
	})
	if err != nil {
		return state.ErrorTracker{}, "", err
	}
	if err := application.runtime.Enable(ctx, created); err != nil {
		application.runtime.recordFailure(created.ID, err)
	}
	return created, identifiers[3], nil
}

func (application *Application) Tracker(ctx context.Context, projectID, trackerID string) (state.ErrorTracker, error) {
	return application.repository.ErrorTrackerInProject(ctx, projectID, trackerID)
}

func (application *Application) Trackers(ctx context.Context, projectID string) ([]state.ErrorTracker, error) {
	return application.repository.ErrorTrackersByProject(ctx, projectID)
}

func (application *Application) UpdatePublicAccess(
	ctx context.Context,
	projectID, trackerID, hostname string,
	expectedUpdatedAt int64,
) (state.ErrorTracker, error) {
	application.publicMu.Lock()
	defer application.publicMu.Unlock()
	if hostname != "" {
		normalized, err := publichostname.Normalize(hostname)
		if err != nil {
			return state.ErrorTracker{}, err
		}
		if !application.covers(normalized) {
			return state.ErrorTracker{}, state.ErrCertificateCoverage
		}
		hostname = normalized
	}
	updated, err := application.repository.UpdateErrorTrackerPublicAccess(ctx, state.UpdateErrorTrackerPublicAccess{
		ID: trackerID, ProjectID: projectID, PublicHostname: hostname,
		ExpectedUpdatedMillis: expectedUpdatedAt, UpdatedAtMillis: application.now().UnixMilli(),
	})
	if err != nil {
		return state.ErrorTracker{}, err
	}
	if err := application.runtime.Enable(ctx, updated); err != nil {
		application.runtime.recordFailure(updated.ID, err)
	}
	return updated, nil
}

func (application *Application) Status(trackerID string) (string, string) {
	return application.runtime.Status(trackerID)
}
