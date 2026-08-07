package automation

import (
	"context"
	"errors"

	"github.com/iivankin/platformd/internal/state"
)

type AuditRepository interface {
	AuditEvents(context.Context, state.AuditQuery) (state.AuditPage, error)
}

type AuditApplication struct {
	repository AuditRepository
}

type ListAuditEventsInput struct {
	ProjectID  string
	ActorKind  string
	Action     string
	Result     string
	TargetKind string
	TargetID   string
	Cursor     string
	Limit      int
}

type AuditEventDTO struct {
	ID                   string         `json:"id"`
	ProjectID            string         `json:"projectId,omitempty"`
	ActorKind            string         `json:"actorKind"`
	ActorID              string         `json:"actorId"`
	Action               string         `json:"action"`
	TargetKind           string         `json:"targetKind"`
	TargetID             string         `json:"targetId"`
	RequestCorrelationID string         `json:"requestCorrelationId,omitempty"`
	Result               string         `json:"result"`
	Metadata             map[string]any `json:"metadata"`
	CreatedAt            int64          `json:"createdAt"`
}

type AuditPageDTO struct {
	Events     []AuditEventDTO `json:"events"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

func NewAuditApplication(repository AuditRepository) (*AuditApplication, error) {
	if repository == nil {
		return nil, errors.New("audit repository is required")
	}
	return &AuditApplication{repository: repository}, nil
}

func (application *AuditApplication) List(ctx context.Context, identity Identity, input ListAuditEventsInput) (AuditPageDTO, error) {
	if err := requireReadIdentity(identity); err != nil {
		return AuditPageDTO{}, err
	}
	projectID := input.ProjectID
	if identity.ProjectID != nil {
		if projectID == "" {
			projectID = *identity.ProjectID
		} else if !identity.AllowsProject(projectID) {
			return AuditPageDTO{}, ErrProjectBoundary
		}
	}
	page, err := application.repository.AuditEvents(ctx, state.AuditQuery{
		ProjectID: projectID, ActorKind: input.ActorKind, Action: input.Action, Result: input.Result,
		TargetKind: input.TargetKind, TargetID: input.TargetID, Cursor: input.Cursor, Limit: input.Limit,
	})
	if err != nil {
		return AuditPageDTO{}, err
	}
	events := make([]AuditEventDTO, 0, len(page.Events))
	for _, event := range page.Events {
		events = append(events, AuditEventDTO{
			ID: event.ID, ProjectID: event.ProjectID, ActorKind: event.ActorKind, ActorID: event.ActorID,
			Action: event.Action, TargetKind: event.TargetKind, TargetID: event.TargetID,
			RequestCorrelationID: event.RequestCorrelationID, Result: event.Result,
			Metadata: event.Metadata, CreatedAt: event.CreatedAtMillis,
		})
	}
	return AuditPageDTO{Events: events, NextCursor: page.NextCursor}, nil
}
