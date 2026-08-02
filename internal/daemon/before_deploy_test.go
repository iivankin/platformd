package daemon

import (
	"context"
	"io"
	"net/netip"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
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

type beforeDeployGitHubStub struct {
	actions *[]string
	inputs  map[string]any
}

func (stub *beforeDeployGitHubStub) DispatchWorkflowAndWait(_ context.Context, repositoryID int64, workflowPath, ref string, inputs map[string]any) (githubapp.WorkflowRun, error) {
	*stub.actions = append(*stub.actions, "github")
	if repositoryID != 23 || workflowPath != ".github/workflows/migrate.yml" || ref != "main" {
		return githubapp.WorkflowRun{}, &unexpectedBeforeDeployCall{value: "GitHub workflow dispatch"}
	}
	stub.inputs = inputs
	return githubapp.WorkflowRun{ID: 91, Conclusion: "success", HTMLURL: "https://github.example/run/91"}, nil
}

type beforeDeployCloudflareStub struct {
	actions   *[]string
	hostnames []string
}

func (stub *beforeDeployCloudflareStub) PurgeHostnames(_ context.Context, hostnames []string) error {
	*stub.actions = append(*stub.actions, "cloudflare")
	stub.hostnames = append([]string(nil), hostnames...)
	return nil
}

type unexpectedBeforeDeployCall struct{ value string }

func (err *unexpectedBeforeDeployCall) Error() string { return "unexpected " + err.value }

func beforeDeployTestRequest(t *testing.T) deployment.BeforeDeployRequest {
	t.Helper()
	return deployment.BeforeDeployRequest{
		Desired: state.ServiceDesired{
			ID: "service", ProjectID: "project", ProjectName: "shop", Name: "api",
			Snapshot: serviceconfig.Snapshot{
				Source: servicesource.Source{Type: servicesource.GitHubImage, GitHub: &servicesource.GitHub{
					RepositoryID: 23, Repository: "acme/api", Branch: "main",
					DockerfilePath: "Dockerfile", ContextPath: ".",
				}},
				BeforeDeploy: &serviceconfig.BeforeDeploy{
					Command: "bun run migrate",
					GitHubWorkflow: &serviceconfig.GitHubWorkflow{
						Path: ".github/workflows/migrate.yml", Name: "Migrate",
						Inputs: map[string]any{"dryRun": false, "batch": float64(20)},
					},
					CloudflareHostnames: []string{"api.example.com"},
				},
			},
		},
		EnvironmentContext: deployment.EnvironmentContext{DeploymentID: "deployment", Kind: deployment.EnvironmentProduction},
		ImageID:            "new-image",
		BuildLogPath:       filepath.Join(t.TempDir(), "build.log"),
	}
}

func TestBeforeDeployExecutorRunsConfiguredActionsInOrder(t *testing.T) {
	actions := []string{}
	engine := &beforeDeployEngineStub{actions: &actions}
	github := &beforeDeployGitHubStub{actions: &actions}
	cloudflare := &beforeDeployCloudflareStub{actions: &actions}
	executor := beforeDeployExecutor{
		engine: engine, environment: beforeDeployEnvironmentStub{}, github: github, cloudflare: cloudflare,
		placement: func(state.ServiceDesired) (deployment.Placement, error) {
			return deployment.Placement{
				NetworkName: "project-network", Gateway: netip.MustParseAddr("10.24.0.1"),
				DNSSearch: "shop.internal", CgroupParent: "/platformd/service-service",
			}, nil
		},
		logSizeBytes: 1024, logMaxFiles: 2,
	}
	if err := executor.Execute(context.Background(), beforeDeployTestRequest(t)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"create", "start", "command", "remove", "github", "cloudflare"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("action order = %#v, want %#v", actions, want)
	}
	if engine.spec.ImageID != "new-image" || engine.spec.Network != "project-network" || engine.spec.Environment["DATABASE_URL"] == "" || len(engine.spec.Mounts) != 0 || len(engine.spec.ManagedVolumes) != 0 {
		t.Fatalf("command container spec = %+v", engine.spec)
	}
	if !reflect.DeepEqual(engine.spec.Entrypoint, []string{"/bin/sh", "-c"}) || !reflect.DeepEqual(github.inputs, map[string]any{"dryRun": false, "batch": float64(20)}) || !reflect.DeepEqual(cloudflare.hostnames, []string{"api.example.com"}) {
		t.Fatalf("executor inputs = entrypoint %#v, GitHub %#v, Cloudflare %#v", engine.spec.Entrypoint, github.inputs, cloudflare.hostnames)
	}
}

func TestBeforeDeployExecutorStopsAfterCommandFailure(t *testing.T) {
	actions := []string{}
	executor := beforeDeployExecutor{
		engine:      &beforeDeployEngineStub{actions: &actions, exitCode: 17},
		environment: beforeDeployEnvironmentStub{},
		placement: func(state.ServiceDesired) (deployment.Placement, error) {
			return deployment.Placement{NetworkName: "network", Gateway: netip.MustParseAddr("10.24.0.1")}, nil
		},
		github: &beforeDeployGitHubStub{actions: &actions}, cloudflare: &beforeDeployCloudflareStub{actions: &actions},
		logSizeBytes: 1024, logMaxFiles: 1,
	}
	if err := executor.Execute(context.Background(), beforeDeployTestRequest(t)); err == nil {
		t.Fatal("failing command did not fail before-deploy execution")
	}
	if want := []string{"create", "start", "command", "remove"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions after command failure = %#v, want %#v", actions, want)
	}
}
