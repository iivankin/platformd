package automation

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

type ProjectCreator interface {
	CreateProjectByToken(context.Context, state.CreateProjectByToken) (state.ProjectSummary, error)
}

type ProjectApplication struct {
	creator ProjectCreator
	random  io.Reader
	now     func() time.Time
}

type ProjectMutationResult struct {
	Project   state.ProjectSummary
	RequestID string
}

func NewProjectApplication(creator ProjectCreator, random io.Reader, now func() time.Time) (*ProjectApplication, error) {
	if creator == nil {
		return nil, errors.New("project automation creator is required")
	}
	if random == nil {
		random = rand.Reader
	}
	if now == nil {
		now = time.Now
	}
	return &ProjectApplication{creator: creator, random: random, now: now}, nil
}

func (application *ProjectApplication) Create(ctx context.Context, identity Identity, name string) (ProjectMutationResult, error) {
	if identity.TokenID == "" || !identity.IsAdmin() {
		return ProjectMutationResult{}, ErrAdminRequired
	}
	if identity.ProjectID != nil {
		return ProjectMutationResult{}, ErrProjectBoundary
	}
	if err := resourcename.Validate(name); err != nil {
		return ProjectMutationResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	timestamp := application.now()
	identifiers := make([]string, 3)
	for index := range identifiers {
		value, err := id.NewWith(timestamp, application.random)
		if err != nil {
			return ProjectMutationResult{}, fmt.Errorf("allocate project mutation identifiers: %w", err)
		}
		identifiers[index] = value
	}
	project, err := application.creator.CreateProjectByToken(ctx, state.CreateProjectByToken{
		ID: identifiers[0], Name: name, AuditEventID: identifiers[1],
		ActorTokenID: identity.TokenID, RequestCorrelationID: identifiers[2],
		CreatedAtMillis: timestamp.UnixMilli(),
	})
	return ProjectMutationResult{Project: project, RequestID: identifiers[2]}, err
}
