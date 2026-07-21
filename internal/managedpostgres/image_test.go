package managedpostgres

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/postgresextension"
	"github.com/iivankin/platformd/internal/state"
)

type extensionImageEngine struct{ image containerengine.Image }

func (*extensionImageEngine) Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error) {
	return containerengine.Image{}, errors.New("unexpected pull")
}
func (engine *extensionImageEngine) InspectImage(context.Context, string) (containerengine.Image, error) {
	return engine.image, nil
}
func (*extensionImageEngine) CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error) {
	return containerengine.Container{}, errors.New("unexpected create")
}
func (*extensionImageEngine) StartContainer(context.Context, string) error {
	return errors.New("unexpected start")
}
func (*extensionImageEngine) StopContainer(string, uint) error { return errors.New("unexpected stop") }
func (*extensionImageEngine) RemoveContainer(context.Context, string, bool) error {
	return errors.New("unexpected remove")
}
func (*extensionImageEngine) InspectContainer(string) (containerengine.Container, error) {
	return containerengine.Container{}, errors.New("unexpected inspect")
}
func (*extensionImageEngine) ExecContainer(context.Context, string, containerengine.ExecRequest) (int, error) {
	return -1, errors.New("unexpected exec")
}

type extensionRecipeStore struct {
	desired []state.ManagedPostgresExtension
}

func (store *extensionRecipeStore) ManagedPostgresExtensions(context.Context, string) ([]state.ManagedPostgresExtension, error) {
	return append([]state.ManagedPostgresExtension(nil), store.desired...), nil
}
func (*extensionRecipeStore) PutManagedPostgresExtension(context.Context, state.PutManagedPostgresExtension) error {
	return nil
}
func (*extensionRecipeStore) DeleteManagedPostgresExtension(context.Context, string, string) error {
	return nil
}

type extensionImageBuilder struct {
	requests []postgresextension.BuildRequest
	image    containerengine.Image
}

func (builder *extensionImageBuilder) Ensure(_ context.Context, request postgresextension.BuildRequest) (containerengine.Image, error) {
	builder.requests = append(builder.requests, request)
	return builder.image, nil
}

func TestResolveImageRebuildsDesiredExtensionLayer(t *testing.T) {
	recipe := postgresextension.VectorRecipe()
	desired := []state.ManagedPostgresExtension{{
		PostgresID: "postgres", Name: recipe.Name, Version: recipe.Version, RecipeDigest: recipe.Digest,
	}}
	base := containerengine.Image{
		ID: "base", Digest: restoreTestImageDigest, Architecture: "amd64", OS: "linux",
	}
	builder := &extensionImageBuilder{image: containerengine.Image{ID: "derived"}}
	controller := &Controller{
		engine: &extensionImageEngine{image: base}, growth: allowGrowthGate{},
		extensions: &extensionRecipeStore{desired: desired}, extensionBuilder: builder,
		placement: func(state.ManagedPostgres) (Placement, error) {
			return Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.90.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/postgres",
			}, nil
		},
	}
	resource := state.ManagedPostgres{
		ID: "postgres", ProjectID: "project", ProjectName: "shop",
		ImageTag: "18.4-alpine3.23", ImageDigest: restoreTestImageDigest,
	}
	resolved, err := controller.resolveImage(context.Background(), resource)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != "derived" || len(builder.requests) != 1 {
		t.Fatalf("resolved image = %+v, requests=%d", resolved, len(builder.requests))
	}
	request := builder.requests[0]
	if request.Base.ID != base.ID || len(request.Extensions) != 1 || request.Extensions[0].RecipeDigest != recipe.Digest || request.Network != "project-network" || len(request.DNSServers) != 1 || request.DNSServers[0] != "10.90.0.1" {
		t.Fatalf("extension build request = %+v", request)
	}
}
