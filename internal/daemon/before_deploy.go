package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/buildlog"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/state"
)

const (
	beforeDeployActionTimeout  = 30 * time.Minute
	beforeDeployCleanupTimeout = 30 * time.Second
	beforeDeployIdleCommand    = "trap 'exit 0' TERM INT; while :; do sleep 3600; done"
)

type beforeDeployEngine interface {
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainer(context.Context, string) error
	ExecContainer(context.Context, string, containerengine.ExecRequest) (int, error)
	RemoveContainer(context.Context, string, bool) error
}

type beforeDeployGitHub interface {
	DispatchWorkflowAndWait(context.Context, int64, string, string, map[string]any) (githubapp.WorkflowRun, error)
}

type beforeDeployCloudflare interface {
	PurgeHostnames(context.Context, []string) error
}

type beforeDeployExecutor struct {
	engine       beforeDeployEngine
	environment  deployment.EnvironmentResolver
	placement    func(state.ServiceDesired) (deployment.Placement, error)
	github       beforeDeployGitHub
	cloudflare   beforeDeployCloudflare
	logSizeBytes int64
	logMaxFiles  uint
}

func (executor beforeDeployExecutor) Execute(ctx context.Context, request deployment.BeforeDeployRequest) error {
	configuration := request.Desired.Snapshot.BeforeDeploy
	if configuration == nil {
		return nil
	}
	if configuration.Command != "" {
		if err := executor.runCommand(ctx, request, configuration.Command); err != nil {
			return fmt.Errorf("run before-deploy command: %w", err)
		}
	}
	if workflow := configuration.GitHubWorkflow; workflow != nil {
		if err := executor.runWorkflow(ctx, request, workflow.Path, workflow.Name, workflow.Inputs); err != nil {
			return fmt.Errorf("run before-deploy GitHub workflow: %w", err)
		}
	}
	if len(configuration.CloudflareHostnames) > 0 {
		if executor.cloudflare == nil {
			return errors.New("purge before-deploy Cloudflare cache: Cloudflare integration is unavailable")
		}
		if err := appendBeforeDeployLog(request.BuildLogPath, "Purging Cloudflare cache for "+strings.Join(configuration.CloudflareHostnames, ", ")); err != nil {
			return err
		}
		if err := executor.cloudflare.PurgeHostnames(ctx, configuration.CloudflareHostnames); err != nil {
			return fmt.Errorf("purge before-deploy Cloudflare cache: %w", err)
		}
		if err := appendBeforeDeployLog(request.BuildLogPath, "Cloudflare cache purge completed"); err != nil {
			return err
		}
	}
	return nil
}

func (executor beforeDeployExecutor) runCommand(
	ctx context.Context,
	request deployment.BeforeDeployRequest,
	command string,
) (result error) {
	if executor.engine == nil || executor.environment == nil || executor.placement == nil {
		return errors.New("command executor is unavailable")
	}
	placement, err := executor.placement(request.Desired)
	if err != nil {
		return fmt.Errorf("place command container: %w", err)
	}
	environment, err := executor.environment.Resolve(ctx, request.Desired, request.EnvironmentContext)
	if err != nil {
		return fmt.Errorf("resolve command environment: %w", err)
	}
	container, err := executor.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: request.ImageID, Name: "platformd-before-deploy-" + request.EnvironmentContext.DeploymentID,
		Entrypoint: []string{"/bin/sh", "-c"}, Command: []string{beforeDeployIdleCommand},
		Environment: environment,
		Labels: map[string]string{
			"io.platformd.owner": "before-deploy", "io.platformd.project-id": request.Desired.ProjectID,
			"io.platformd.service-id":    request.Desired.ID,
			"io.platformd.deployment-id": request.EnvironmentContext.DeploymentID,
		},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()},
		DNSSearch:    []string{placement.DNSSearch},
		LogPath:      filepath.Join(filepath.Dir(request.BuildLogPath), "before-deploy-container.log"),
		LogSizeBytes: executor.logSizeBytes, LogMaxFiles: executor.logMaxFiles,
		CgroupParent: placement.CgroupParent, CPUMillicores: request.Desired.Snapshot.CPUMillicores,
		MemoryMaxBytes: request.Desired.Snapshot.MemoryMaxBytes,
	})
	if err != nil {
		return err
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), beforeDeployCleanupTimeout)
		defer cancel()
		result = errors.Join(result, executor.engine.RemoveContainer(cleanupContext, container.ID, true))
	}()
	if err := executor.engine.StartContainer(ctx, container.ID); err != nil {
		return err
	}
	if err := appendBeforeDeployLog(request.BuildLogPath, "Running command: "+command); err != nil {
		return err
	}
	writer, err := buildlog.OpenAppend(request.BuildLogPath)
	if err != nil {
		return err
	}
	commandContext, cancel := context.WithTimeout(ctx, beforeDeployActionTimeout)
	exitCode, execErr := executor.engine.ExecContainer(commandContext, container.ID, containerengine.ExecRequest{
		Command: []string{"/bin/sh", "-lc", command}, Stdout: writer, Stderr: writer,
	})
	cancel()
	closeErr := writer.Close()
	if execErr != nil {
		return errors.Join(execErr, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}
	if exitCode != 0 {
		return fmt.Errorf("command exited with code %d", exitCode)
	}
	return appendBeforeDeployLog(request.BuildLogPath, "Before-deploy command completed")
}

func (executor beforeDeployExecutor) runWorkflow(
	ctx context.Context,
	request deployment.BeforeDeployRequest,
	workflowPath string,
	workflowName string,
	inputs map[string]any,
) error {
	githubSource := request.Desired.Snapshot.Source.GitHub
	if executor.github == nil || githubSource == nil {
		return errors.New("GitHub workflow executor is unavailable")
	}
	if err := appendBeforeDeployLog(request.BuildLogPath, "Dispatching GitHub workflow: "+workflowName); err != nil {
		return err
	}
	workflowContext, cancel := context.WithTimeout(ctx, beforeDeployActionTimeout)
	defer cancel()
	run, err := executor.github.DispatchWorkflowAndWait(
		workflowContext, githubSource.RepositoryID, workflowPath, githubSource.Branch, inputs,
	)
	if err != nil {
		return err
	}
	message := "GitHub workflow completed: " + workflowName
	if run.HTMLURL != "" {
		message += " (" + run.HTMLURL + ")"
	}
	return appendBeforeDeployLog(request.BuildLogPath, message)
}

func appendBeforeDeployLog(logPath, message string) error {
	return buildlog.Append(logPath, fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), message))
}
