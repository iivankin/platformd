package preview

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type previewEngine struct {
	spec containerengine.ContainerSpec
}

func (*previewEngine) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{}, nil
}

func (engine *previewEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.spec = spec
	return containerengine.Container{ID: "container"}, nil
}

func (*previewEngine) StartContainer(context.Context, string) error        { return nil }
func (*previewEngine) StopContainer(string, uint) error                    { return nil }
func (*previewEngine) RemoveContainer(context.Context, string, bool) error { return nil }
func (*previewEngine) InspectContainer(string) (containerengine.Container, error) {
	return containerengine.Container{}, nil
}

type previewEnvironmentResolver struct {
	values map[string]string
}

func (environment previewEnvironmentResolver) Resolve(
	context.Context,
	state.ServiceDesired,
	deployment.EnvironmentContext,
) (map[string]string, error) {
	values := make(map[string]string, len(environment.values))
	for name, value := range environment.values {
		values[name] = value
	}
	return values, nil
}

func TestCreateContainerNeverMountsProductionVolumes(t *testing.T) {
	engine := &previewEngine{}
	application := &Application{
		engine:      engine,
		environment: previewEnvironmentResolver{values: map[string]string{"APP_ENV": "preview"}},
		placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{
				NetworkName: "platformd-project", Gateway: netip.MustParseAddr("10.42.0.1"),
				DNSSearch: "storefront.internal", CgroupParent: "platformd.slice",
			}, nil
		},
		logRoot: filepath.Join(t.TempDir(), "logs"), logSizeBytes: 1 << 20, logMaxFiles: 2,
		now:   func() time.Time { return time.Unix(100, 0) },
		newID: func() (string, error) { return "attempt", nil },
	}
	desired := state.ServiceDesired{
		ID: "service", ProjectID: "project",
		Snapshot: serviceconfig.Snapshot{
			Command: []string{"/app/server"}, Args: []string{"--serve"},
			CPUMillicores: 500, MemoryMaxBytes: 256 << 20,
			VolumeMounts: []serviceconfig.VolumeMount{{
				VolumeID: "production-data", ContainerPath: "/var/lib/app",
			}},
		},
	}
	if _, _, err := application.createContainer(
		context.Background(),
		desired,
		deployment.EnvironmentContext{
			DeploymentID: "preview", Kind: deployment.EnvironmentPreview,
			PreviewURL: "https://preview-abcdef.example.com", PullRequestNumber: 42,
		},
		"image",
	); err != nil {
		t.Fatal(err)
	}
	if len(engine.spec.Mounts) != 0 {
		t.Fatalf("preview inherited production mounts: %#v", engine.spec.Mounts)
	}
	if engine.spec.Network != "platformd-project" || len(engine.spec.DNSSearch) != 1 || engine.spec.DNSSearch[0] != "storefront.internal" {
		t.Fatalf("preview project network placement = %#v", engine.spec)
	}
	if engine.spec.Labels["io.platformd.owner"] != "preview" || engine.spec.Labels["io.platformd.preview-id"] != "preview" {
		t.Fatalf("preview labels = %#v", engine.spec.Labels)
	}
}
