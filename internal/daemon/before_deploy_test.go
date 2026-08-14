package daemon

import (
	"context"
	"io"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

type beforeDeployEngineStub struct {
	actions  *[]string
	exitCode int
	spec     containerengine.ContainerSpec
}

func (stub *beforeDeployEngineStub) CreateContainer(_ context.Context, spec containerengine.ContainerSpec) (containerengine.Container, error) {
	*stub.actions = append(*stub.actions, "create")
	stub.spec = spec
	return containerengine.Container{ID: "before-container"}, nil
}

func (stub *beforeDeployEngineStub) StartContainer(context.Context, string) error {
	*stub.actions = append(*stub.actions, "start")
	return nil
}

func (stub *beforeDeployEngineStub) ExecContainer(_ context.Context, _ string, request containerengine.ExecRequest) (int, error) {
	*stub.actions = append(*stub.actions, "command")
	_, _ = io.WriteString(request.Stdout, "migration output\n")
	return stub.exitCode, nil
}

func (stub *beforeDeployEngineStub) RemoveContainer(context.Context, string, bool) error {
	*stub.actions = append(*stub.actions, "remove")
	return nil
}

type beforeDeployEnvironmentStub struct{}

func (beforeDeployEnvironmentStub) Resolve(context.Context, state.ServiceDesired, deployment.EnvironmentContext) (map[string]string, error) {
	return map[string]string{"DATABASE_URL": "postgres://database/app"}, nil
}

type beforeDeployCloudflareStub struct {
	actions   *[]string
	hostnames []string
}

type beforeDeployLogSink struct {
	output strings.Builder
}

type beforeDeployWriteCloser struct{ io.Writer }

func (beforeDeployWriteCloser) Close() error { return nil }

func (sink *beforeDeployLogSink) ContainerWriter(_, _, _, _, _ string) io.WriteCloser {
	return beforeDeployWriteCloser{Writer: io.Discard}
}

func (sink *beforeDeployLogSink) BeforeDeployWriter(_, _, _, _, _ string) io.WriteCloser {
	return beforeDeployWriteCloser{Writer: &sink.output}
}

func (stub *beforeDeployCloudflareStub) PurgeHostnames(_ context.Context, hostnames []string) error {
	*stub.actions = append(*stub.actions, "cloudflare")
	stub.hostnames = append([]string(nil), hostnames...)
	return nil
}

func beforeDeployTestRequest(t *testing.T) deployment.BeforeDeployRequest {
	t.Helper()
	return deployment.BeforeDeployRequest{
		Desired: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api",
			Snapshot: serviceconfig.Snapshot{
				Source: serviceconfig.PublicImageSource("alpine"),
				BeforeDeploy: &serviceconfig.BeforeDeploy{
					Command:             "bun run migrate",
					CloudflareHostnames: []string{"api.example.com"},
				},
			},
		},
		EnvironmentContext: deployment.EnvironmentContext{DeploymentID: "deployment", Kind: deployment.EnvironmentProduction},
		ImageID:            "new-image",
	}
}

func TestBeforeDeployExecutorRunsConfiguredActionsInOrder(t *testing.T) {
	actions := []string{}
	engine := &beforeDeployEngineStub{actions: &actions}
	cloudflare := &beforeDeployCloudflareStub{actions: &actions}
	logs := &beforeDeployLogSink{}
	executor := beforeDeployExecutor{
		engine: engine, environment: beforeDeployEnvironmentStub{}, cloudflare: cloudflare,
		placement: func(state.ServiceDesired) (deployment.Placement, error) {
			return deployment.Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.24.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/service-service",
			}, nil
		},
		logs: logs,
	}
	if err := executor.Execute(context.Background(), beforeDeployTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"create", "start", "command", "remove", "cloudflare"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("action order = %#v, want %#v", actions, want)
	}
	if engine.spec.ImageID != "new-image" || engine.spec.Network != "project-network" || engine.spec.Environment["DATABASE_URL"] == "" || len(engine.spec.Mounts) != 0 || len(engine.spec.ManagedVolumes) != 0 {
		t.Fatalf("command container spec = %+v", engine.spec)
	}
	if !reflect.DeepEqual(engine.spec.Entrypoint, []string{"/bin/sh", "-c"}) || !reflect.DeepEqual(cloudflare.hostnames, []string{"api.example.com"}) {
		t.Fatalf("executor inputs = entrypoint %#v, Cloudflare %#v", engine.spec.Entrypoint, cloudflare.hostnames)
	}
	if engine.spec.LogDriver != containerengine.ContainerLogNone || !strings.Contains(logs.output.String(), "migration output") ||
		!strings.Contains(logs.output.String(), "Cloudflare cache purge completed") {
		t.Fatalf("before-deploy logging = driver %q output %q", engine.spec.LogDriver, logs.output.String())
	}
}

func TestBeforeDeployExecutorStopsAfterCommandFailure(t *testing.T) {
	actions := []string{}
	executor := beforeDeployExecutor{
		engine:      &beforeDeployEngineStub{actions: &actions, exitCode: 17},
		environment: beforeDeployEnvironmentStub{},
		logs:        &beforeDeployLogSink{},
		placement: func(state.ServiceDesired) (deployment.Placement, error) {
			return deployment.Placement{NetworkName: "network", Gateway: netip.MustParseAddr("10.24.0.1")}, nil
		},
		cloudflare: &beforeDeployCloudflareStub{actions: &actions},
	}
	if err := executor.Execute(context.Background(), beforeDeployTestRequest(t)); err == nil {
		t.Fatal("failing command did not fail before-deploy execution")
	}
	if want := []string{"create", "start", "command", "remove"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions after command failure = %#v, want %#v", actions, want)
	}
}
