package automation_test

import (
	"context"
	"testing"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type automationVolumeRepository struct {
	created state.CreateVolume
}

func (repository *automationVolumeRepository) CreateVolume(_ context.Context, input state.CreateVolume) (state.Volume, error) {
	repository.created = input
	return input.Volume, nil
}

func (*automationVolumeRepository) VolumesByService(context.Context, string, string) ([]state.Volume, error) {
	return []state.Volume{{ID: "volume", ProjectID: "project", ServiceID: "service"}}, nil
}

func (*automationVolumeRepository) DeleteVolume(_ context.Context, input state.DeleteVolume) (state.Volume, error) {
	return state.Volume{ID: input.VolumeID, ProjectID: input.ProjectID, ServiceID: input.ServiceID}, nil
}

func (*automationVolumeRepository) Service(context.Context, string, string) (state.ServiceDesired, error) {
	return state.ServiceDesired{}, nil
}

type automationVolumeFilesystem struct{}

func (automationVolumeFilesystem) Ensure(state.PersistentVolumeReference) error { return nil }
func (automationVolumeFilesystem) Remove(string, string) error                  { return nil }

type automationVolumeImages struct{}

func (automationVolumeImages) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{}, nil
}

func TestVolumeAutomationEnforcesRoleAndProjectBoundary(t *testing.T) {
	t.Parallel()

	repository := &automationVolumeRepository{}
	domain, err := volume.New(volume.Config{
		Repository: repository, Filesystem: automationVolumeFilesystem{}, Images: automationVolumeImages{},
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := automation.NewVolumeApplication(domain)
	if err != nil {
		t.Fatal(err)
	}
	bound := "project"
	read := automation.Identity{TokenID: "read", Role: "read", ProjectID: &bound}
	if listed, err := application.List(context.Background(), read, "project", "service"); err != nil || len(listed) != 1 {
		t.Fatalf("read list = %+v/%v", listed, err)
	}
	if _, err := application.Create(context.Background(), read, automation.CreateVolumeInput{
		ProjectID: "project", ServiceID: "service", Name: "data",
	}); err != automation.ErrAdminRequired {
		t.Fatalf("read create error = %v", err)
	}
	admin := automation.Identity{TokenID: "admin", Role: "admin", ProjectID: &bound}
	if _, err := application.Create(context.Background(), admin, automation.CreateVolumeInput{
		ProjectID: "other", ServiceID: "service", Name: "data",
	}); err != automation.ErrProjectBoundary {
		t.Fatalf("cross-project create error = %v", err)
	}
	if _, err := application.Create(context.Background(), admin, automation.CreateVolumeInput{
		ProjectID: "project", ServiceID: "service", Name: "data",
	}); err != nil {
		t.Fatal(err)
	}
	if repository.created.ActorKind != "token" || repository.created.ActorID != "admin" || repository.created.ActorEmail != "" {
		t.Fatalf("automation actor = %+v", repository.created)
	}
}
