package volume_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/volume"
)

type repositoryStub struct {
	created     state.CreateVolume
	createError error
	deleted     state.Volume
	deleteInput state.DeleteVolume
	service     state.ServiceDesired
	volumes     []state.Volume
}

func (repository *repositoryStub) CreateVolume(_ context.Context, input state.CreateVolume) (state.Volume, error) {
	repository.created = input
	return input.Volume, repository.createError
}

func (repository *repositoryStub) VolumesByService(context.Context, string, string) ([]state.Volume, error) {
	return repository.volumes, nil
}

func (repository *repositoryStub) DeleteVolume(_ context.Context, input state.DeleteVolume) (state.Volume, error) {
	repository.deleteInput = input
	return repository.deleted, nil
}

func (repository *repositoryStub) Service(context.Context, string, string) (state.ServiceDesired, error) {
	return repository.service, nil
}

type filesystemStub struct {
	ensured     []state.PersistentVolumeReference
	removed     [][2]string
	removeError error
}

func (filesystem *filesystemStub) Ensure(reference state.PersistentVolumeReference) error {
	filesystem.ensured = append(filesystem.ensured, reference)
	return nil
}

func (filesystem *filesystemStub) Remove(projectID, volumeID string) error {
	filesystem.removed = append(filesystem.removed, [2]string{projectID, volumeID})
	return filesystem.removeError
}

type imageInspectorStub struct {
	image containerengine.Image
}

func (inspector imageInspectorStub) InspectImage(context.Context, string) (containerengine.Image, error) {
	return inspector.image, nil
}

func TestCreateVolumePublishesDirectoryBeforeState(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{}
	filesystem := &filesystemStub{}
	application := newApplication(t, repository, filesystem, imageInspectorStub{})
	result, err := application.Create(context.Background(), volume.CreateInput{
		ProjectID: "project", ServiceID: "service", Name: "data", OwnerUID: 1000, OwnerGID: 1001,
		Actor: volume.Actor{Kind: "access", ID: "subject", Email: "user@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filesystem.ensured) != 1 || filesystem.ensured[0].VolumeID != result.Volume.ID ||
		filesystem.ensured[0].OwnerUID != 1000 || filesystem.ensured[0].OwnerGID != 1001 {
		t.Fatalf("ensured references = %+v", filesystem.ensured)
	}
	if repository.created.ID != result.Volume.ID || repository.created.AuditEventID == "" ||
		repository.created.RequestCorrelationID != result.RequestID || repository.created.ActorEmail != "user@example.com" {
		t.Fatalf("state create = %+v, result = %+v", repository.created, result)
	}
}

func TestCreateVolumeRemovesDirectoryWhenStateRejectsMutation(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{createError: state.ErrVolumeNameConflict}
	filesystem := &filesystemStub{}
	application := newApplication(t, repository, filesystem, imageInspectorStub{})
	_, err := application.Create(context.Background(), volume.CreateInput{
		ProjectID: "project", ServiceID: "service", Name: "data", OwnerUID: 0, OwnerGID: 0,
		Actor: volume.Actor{Kind: "token", ID: "token"},
	})
	if !errors.Is(err, state.ErrVolumeNameConflict) {
		t.Fatalf("create error = %v", err)
	}
	if len(filesystem.removed) != 1 || filesystem.removed[0][0] != "project" || filesystem.removed[0][1] == "" {
		t.Fatalf("removed paths = %+v", filesystem.removed)
	}
}

func TestDeleteVolumeCommitsStateAndReportsDeferredFilesystemCleanup(t *testing.T) {
	t.Parallel()

	repository := &repositoryStub{deleted: state.Volume{ID: "volume", ProjectID: "project", ServiceID: "service"}}
	filesystem := &filesystemStub{removeError: errors.New("filesystem busy")}
	var cleanupError error
	application, err := volume.New(volume.Config{
		Repository: repository, Filesystem: filesystem, Images: imageInspectorStub{},
		Now:            func() time.Time { return time.Unix(1_900_000_000, 0) },
		OnCleanupError: func(err error) { cleanupError = err },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := application.Delete(context.Background(), volume.DeleteInput{
		ProjectID: "project", ServiceID: "service", VolumeID: "volume",
		Actor: volume.Actor{Kind: "token", ID: "token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Volume.ID != "volume" || cleanupError == nil || repository.deleteInput.VolumeID != "volume" {
		t.Fatalf("delete result=%+v cleanup=%v state=%+v", result, cleanupError, repository.deleteInput)
	}
}

func TestOwnerSuggestionRequiresExactNumericUIDAndGID(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		user  string
		uid   int
		gid   int
		exact bool
	}{
		{name: "pair", user: "1000:1001", uid: 1000, gid: 1001, exact: true},
		{name: "single uid", user: "1000"},
		{name: "symbolic", user: "app:app"},
		{name: "root pair", user: "0:0", exact: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &repositoryStub{service: state.ServiceDesired{ActiveImageDigest: "sha256:image"}}
			application := newApplication(t, repository, &filesystemStub{}, imageInspectorStub{
				image: containerengine.Image{User: test.user},
			})
			suggestion, err := application.SuggestOwner(context.Background(), "project", "service")
			if err != nil {
				t.Fatal(err)
			}
			if suggestion.OwnerUID != test.uid || suggestion.OwnerGID != test.gid ||
				suggestion.ExactNumeric != test.exact || suggestion.ImageUser != test.user {
				t.Fatalf("suggestion = %+v", suggestion)
			}
		})
	}
}

func newApplication(
	t *testing.T,
	repository volume.Repository,
	filesystem volume.Filesystem,
	images volume.ImageInspector,
) *volume.Application {
	t.Helper()
	application, err := volume.New(volume.Config{
		Repository: repository, Filesystem: filesystem, Images: images,
		Now: func() time.Time { return time.Unix(1_900_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return application
}
