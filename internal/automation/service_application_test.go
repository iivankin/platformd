package automation

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type mutationRepository struct {
	createCalls int
	created     state.CreateService
	updated     state.UpdateServiceInput
}

func (repository *mutationRepository) CreateService(_ context.Context, input state.CreateService) (state.ServiceDesired, error) {
	repository.createCalls++
	repository.created = input
	return state.ServiceDesired{ID: input.ID, ProjectID: input.ProjectID, Name: input.Name, Enabled: input.Enabled, Snapshot: input.Snapshot}, nil
}

func (repository *mutationRepository) UpdateService(_ context.Context, input state.UpdateServiceInput) (state.ServiceDesired, error) {
	repository.updated = input
	return state.ServiceDesired{ID: input.ID, ProjectID: input.ProjectID, Enabled: input.Enabled, Snapshot: input.Snapshot}, nil
}

func (*mutationRepository) RollbackService(context.Context, state.RollbackServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func (*mutationRepository) RedeployService(context.Context, state.RedeployServiceInput) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

func TestServiceApplicationAuthorizesBeforeRepositoryAndCreatesTokenAuditInput(t *testing.T) {
	repository := &mutationRepository{}
	application, err := NewServiceApplication(repository, bytes.NewReader(make([]byte, 96)), func() time.Time {
		return time.UnixMilli(1700000000000)
	})
	if err != nil {
		t.Fatal(err)
	}
	projectID := "project"
	input := CreateServiceInput{
		ProjectID: projectID, Name: "api", Enabled: true,
		Configuration: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22"),},
	}
	if _, err := application.Create(context.Background(), Identity{TokenID: "read", Role: "read"}, input); !errors.Is(err, ErrAdminRequired) {
		t.Fatalf("read token error = %v", err)
	}
	otherProject := "other"
	if _, err := application.Create(context.Background(), Identity{TokenID: "admin", Role: "admin", ProjectID: &otherProject}, input); !errors.Is(err, ErrProjectBoundary) {
		t.Fatalf("bound token error = %v", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("repository called %d times before authorization", repository.createCalls)
	}

	result, err := application.Create(context.Background(), Identity{TokenID: "admin-token", Role: "admin", ProjectID: &projectID}, input)
	if err != nil {
		t.Fatal(err)
	}
	if repository.createCalls != 1 || repository.created.ActorKind != "token" || repository.created.ActorID != "admin-token" || repository.created.ActorEmail != "" {
		t.Fatalf("create audit actor = %+v", repository.created)
	}
	if repository.created.Snapshot.Source.Image.Reference != "docker.io/library/alpine:3.22" || result.RequestID == "" || result.Service.ID == "" {
		t.Fatalf("create result = %+v, input = %+v", result, repository.created)
	}
}

func TestServiceApplicationRejectsInvalidUpdateBeforeRepository(t *testing.T) {
	repository := &mutationRepository{}
	application, err := NewServiceApplication(repository, bytes.NewReader(make([]byte, 64)), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.Update(context.Background(), Identity{TokenID: "admin", Role: "admin"}, UpdateServiceInput{
		ProjectID: "project", ServiceID: "service", ExpectedUpdatedAt: 0,
		Configuration: serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine"),},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("update error = %v", err)
	}
	if repository.updated.ID != "" {
		t.Fatal("repository was called for invalid update")
	}
}
