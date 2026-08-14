package preview

import (
	"context"
	"io"
	"net/netip"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type previewEngine struct {
	spec       containerengine.ContainerSpec
	attachedID string
}

func (*previewEngine) Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error) {
	return containerengine.Image{}, nil
}

func (*previewEngine) InspectImage(context.Context, string) (containerengine.Image, error) {
	return containerengine.Image{}, nil
}

func (engine *previewEngine) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	engine.spec = spec
	return containerengine.Container{ID: "container"}, nil
}

func (engine *previewEngine) StartContainerAttached(_ context.Context, id string, stdout, stderr io.WriteCloser) (<-chan error, error) {
	engine.attachedID = id
	_ = stdout.Close()
	_ = stderr.Close()
	done := make(chan error)
	close(done)
	return done, nil
}
func (*previewEngine) StopContainer(string, uint) error                    { return nil }
func (*previewEngine) RemoveContainer(context.Context, string, bool) error { return nil }
func (*previewEngine) InspectContainer(string) (containerengine.Container, error) {
	return containerengine.Container{}, nil
}

type previewLogSink struct {
	resourceID   string
	resourceName string
	deploymentID string
	attemptID    string
	streams      []string
}

func (sink *previewLogSink) ContainerWriter(resourceID, resourceName, deploymentID, attemptID, stream string) io.WriteCloser {
	sink.resourceID = resourceID
	sink.resourceName = resourceName
	sink.deploymentID = deploymentID
	sink.attemptID = attemptID
	sink.streams = append(sink.streams, stream)
	return nopWriteCloser{Writer: io.Discard}
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

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
	logs := &previewLogSink{}
	application := &Application{
		engine:      engine,
		environment: previewEnvironmentResolver{values: map[string]string{"APP_ENV": "preview"}},
		placement: func(state.ServiceDesired) (Placement, error) {
			return Placement{
				NetworkName: "platformd-project", Gateway: netip.MustParseAddr("10.42.0.1"),
				DNSSearch: "storefront.internal", CgroupParent: "platformd.slice",
			}, nil
		},
		containerLogs: logs,
		now:           func() time.Time { return time.Unix(100, 0) },
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
			PreviewURL: "https://preview-abcdef.example.com",
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
	if engine.spec.LogDriver != containerengine.ContainerLogNone || engine.spec.LogPath != "" {
		t.Fatalf("preview log configuration = %#v", engine.spec)
	}
	if err := application.startContainer(context.Background(), state.ServiceDesired{ID: "service", Name: "storefront"}, "preview", "container"); err != nil {
		t.Fatal(err)
	}
	if engine.attachedID != "container" || logs.resourceID != "service" || logs.resourceName != "storefront" ||
		logs.deploymentID != "preview" || logs.attemptID != "container" || len(logs.streams) != 2 {
		t.Fatalf("preview attached logs = engine %q, sink %#v", engine.attachedID, logs)
	}
}
