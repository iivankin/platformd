package daemon

import (
	"context"
	"errors"
	"log"

	"github.com/iivankin/platformd/internal/cloudflaredns"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/preview"
	"github.com/iivankin/platformd/internal/state"
)

func (stack *runtimeStack) ConfigurePreviews(
	ctx context.Context,
	store *state.Store,
	master cryptobox.MasterKey,
	github *githubapp.Application,
	dns *cloudflaredns.Application,
	domains *liveDomainRepository,
	adminHostname string,
	certificateCovers func(string) bool,
) error {
	application, err := preview.New(preview.Config{
		Store: store, Engine: stack.engine,
		Environment: resourceVariableResolver{store: store, master: master},
		Sources: githubSourceResolver{
			github: github, engine: stack.engine, generatedRoot: stack.paths.GeneratedRoot,
		},
		GitHub: github, DNS: dns, Growth: stack.growth, Admission: stack.admission,
		Placement: stack.previewPlacement, RoutesChanged: domains.reload,
		CertificateCovers: certificateCovers, AdminHostname: adminHostname,
		LogRoot: stack.paths.LogsRoot, LogSizeBytes: serviceLogSegmentBytes,
		LogMaxFiles: serviceLogMaxFiles,
	})
	if err != nil {
		return err
	}
	stack.mu.Lock()
	if stack.closed {
		stack.mu.Unlock()
		return errors.New("container runtime is closed")
	}
	stack.previews = application
	stack.mu.Unlock()
	if err := application.Restore(ctx); err != nil {
		return err
	}
	go application.RunCleanup(ctx)
	return nil
}

func (stack *runtimeStack) NotifyGitHubPullRequest(
	ctx context.Context,
	store *state.Store,
	github *githubapp.Application,
	event githubapp.PullRequestEvent,
) {
	serviceIDs, err := store.EnabledServiceIDs(ctx)
	if err != nil {
		return
	}
	for _, serviceID := range serviceIDs {
		desired, loadErr := store.DesiredService(ctx, serviceID)
		if loadErr != nil || desired.Snapshot.Source.GitHub == nil ||
			desired.Snapshot.Source.GitHub.PullRequestPreview == nil {
			continue
		}
		source := desired.Snapshot.Source.GitHub
		if source.RepositoryID != event.RepositoryID || source.Branch != event.BaseBranch {
			continue
		}
		stack.mu.Lock()
		application := stack.previews
		stack.mu.Unlock()
		if application == nil {
			stack.recordServiceFailure(serviceID, errors.New("PR preview runtime is not configured"))
			continue
		}
		if event.Action == "close" {
			go func(serviceID string) {
				if closeErr := application.ClosePullRequest(ctx, serviceID, event); closeErr != nil {
					log.Printf("close PR preview for service %s: %v", serviceID, closeErr)
				}
			}(serviceID)
			continue
		}
		// As with production builds, services that wait for CI are driven only
		// by completed-check events. The initial PR event remains visible as a
		// skipped deployment instead of racing check creation.
		if source.WaitForCI != event.ChecksEvent {
			if source.WaitForCI && !event.ChecksEvent {
				go func(serviceID string) {
					if deployErr := application.Deploy(ctx, serviceID, event); deployErr != nil {
						log.Printf("record waiting PR preview for service %s: %v", serviceID, deployErr)
					}
				}(serviceID)
			}
			continue
		}
		if len(source.TriggerPaths) > 0 {
			commit, commitErr := github.Commit(ctx, event.RepositoryID, event.Revision)
			if commitErr != nil {
				stack.recordServiceFailure(serviceID, commitErr)
				continue
			}
			if !githubPathsMatch(source.TriggerPaths, commit.ChangedPaths) {
				continue
			}
		}
		go func(serviceID string) {
			if deployErr := application.Deploy(ctx, serviceID, event); deployErr != nil {
				log.Printf("deploy PR preview for service %s: %v", serviceID, deployErr)
			}
		}(serviceID)
	}
}

func (stack *runtimeStack) stopServicePreviews(ctx context.Context, serviceID, reason string) error {
	stack.mu.Lock()
	application := stack.previews
	stack.mu.Unlock()
	if application == nil {
		return nil
	}
	return application.StopService(ctx, serviceID, reason)
}
