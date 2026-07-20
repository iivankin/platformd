package postgresextension

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/state"
)

type builderEngine struct {
	images       map[string]containerengine.Image
	created      []containerengine.ContainerSpec
	commits      []containerengine.DerivedImageRequest
	removedImage []string
}

func (engine *builderEngine) InspectImage(_ context.Context, name string) (containerengine.Image, error) {
	image, ok := engine.images[name]
	if !ok {
		return containerengine.Image{}, errors.New("not found")
	}
	return image, nil
}

func (engine *builderEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.created = append(engine.created, spec)
	return containerengine.Container{ID: "builder"}, nil
}

func (*builderEngine) StartContainer(context.Context, string) error         { return nil }
func (*builderEngine) WaitContainer(context.Context, string) (int32, error) { return 0, nil }
func (*builderEngine) RemoveContainer(context.Context, string, bool) error  { return nil }

func (engine *builderEngine) CommitDerivedImage(_ context.Context, request containerengine.DerivedImageRequest) (containerengine.Image, error) {
	engine.commits = append(engine.commits, request)
	image := containerengine.Image{
		ID: "derived", Architecture: "amd64", OS: "linux", Labels: request.Labels,
	}
	engine.images[request.Reference] = image
	return image, nil
}

func (engine *builderEngine) ImagesByLabel(context.Context, string) ([]containerengine.Image, error) {
	result := make([]containerengine.Image, 0)
	for _, image := range engine.images {
		if image.Labels[OwnerLabel] == DerivedOwner {
			result = append(result, image)
		}
	}
	return result, nil
}

func (engine *builderEngine) RemoveImage(_ context.Context, id string) error {
	engine.removedImage = append(engine.removedImage, id)
	return nil
}

type builderGrowth struct{ calls int }

func (growth *builderGrowth) PermitGrowth(context.Context) error {
	growth.calls++
	return nil
}

func TestBuilderCachesDerivedImageWithoutDatabaseVolume(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "vector.tar.gz")
	if err := os.WriteFile(source, []byte("verified by the source cache in production"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := &builderEngine{images: make(map[string]containerengine.Image)}
	growth := &builderGrowth{}
	builder, err := New(Config{
		Engine: engine, Growth: growth, CacheRoot: root, LogRoot: root,
		LogSizeBytes: 1 << 20, LogMaxFiles: 2,
		ResolveSource: func(_ context.Context, recipe Recipe) (string, error) {
			if recipe != VectorRecipe() {
				t.Fatalf("recipe = %+v", recipe)
			}
			return source, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	recipe := VectorRecipe()
	extensions := []state.ManagedPostgresExtension{{
		PostgresID: "postgres", Name: recipe.Name, Version: recipe.Version, RecipeDigest: recipe.Digest,
	}}
	base := containerengine.Image{ID: "base", Digest: "sha256:base", Architecture: "amd64", OS: "linux"}
	request := BuildRequest{
		Base: base, Extensions: extensions, ProjectID: "project", PostgresID: "postgres",
		Network: "project-network", DNSServers: []string{"10.90.0.1"}, CgroupParent: "platformd/postgres",
	}
	first, err := builder.Ensure(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "derived" || len(engine.created) != 1 || len(engine.commits) != 1 || growth.calls != 1 {
		t.Fatalf("first build = %+v, creates=%d commits=%d growth=%d", first, len(engine.created), len(engine.commits), growth.calls)
	}
	spec := engine.created[0]
	if len(spec.Mounts) != 1 || !spec.Mounts[0].ReadOnly || spec.Mounts[0].Destination != "/platformd/vector.tar.gz" {
		t.Fatalf("builder mounts = %+v", spec.Mounts)
	}
	for _, mount := range spec.Mounts {
		if mount.Destination == "/var/lib/postgresql/data" {
			t.Fatal("database volume was mounted into the extension builder")
		}
	}
	if len(spec.DNSServers) != 1 || spec.DNSServers[0] != "10.90.0.1" || len(spec.DNSSearch) != 0 {
		t.Fatalf("builder DNS = servers=%v search=%v", spec.DNSServers, spec.DNSSearch)
	}
	second, err := builder.Ensure(context.Background(), request)
	if err != nil || second.ID != first.ID || len(engine.created) != 1 || growth.calls != 1 {
		t.Fatalf("cached build = %+v, %v creates=%d growth=%d", second, err, len(engine.created), growth.calls)
	}
	cacheKey := first.Labels[CacheKeyLabel]
	if err := builder.GarbageCollect(context.Background(), map[string]struct{}{cacheKey: {}}); err != nil {
		t.Fatal(err)
	}
	if len(engine.removedImage) != 0 {
		t.Fatalf("required image was removed: %v", engine.removedImage)
	}
	if err := builder.GarbageCollect(context.Background(), map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if len(engine.removedImage) != 1 || engine.removedImage[0] != "derived" {
		t.Fatalf("unused image removal = %v", engine.removedImage)
	}
}
