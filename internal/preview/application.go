package preview

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/buildlog"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/githubapp"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

const (
	Retention           = 14 * 24 * time.Hour
	cleanupInterval     = time.Hour
	processStartupGrace = 3 * time.Second
	probeInterval       = 250 * time.Millisecond
	probeTimeout        = 2 * time.Second
	stopTimeoutSeconds  = 10
	reportTimeout       = 10 * time.Second
)

type Store interface {
	DesiredService(context.Context, string) (state.ServiceDesired, error)
	ServiceDomains(context.Context, string, string) ([]state.ServiceDomain, error)
	BeginPreviewDeployment(context.Context, state.BeginPreviewDeployment) error
	SetPreviewGitHubDeployment(context.Context, string, int64) error
	SetPreviewGitHubComment(context.Context, string, int64) error
	SetPreviewBuild(context.Context, string, string, string, string) error
	SetPreviewDNSRecords(context.Context, string, []string) error
	ActivatePreviewDeployment(context.Context, string, string, []string, int64) error
	FinishPreviewDeployment(context.Context, string, string, string, string, int64) error
	StopPreviewDeployment(context.Context, string, int64) error
	ActivePreviewDeployment(context.Context, string, int) (state.PreviewDeployment, error)
	ActivePreviewDeployments(context.Context) ([]state.PreviewDeployment, error)
	LatestPreviewCommentID(context.Context, string, int) (int64, error)
	ExpiredActivePreviewDeployments(context.Context, int64) ([]state.PreviewDeployment, error)
	FinishedPreviewDeploymentsWithDNS(context.Context) ([]state.PreviewDeployment, error)
	ClearPreviewDNSRecords(context.Context, string) error
	DeleteFinishedPreviewDeployments(context.Context, int64) ([]state.PreviewDeployment, error)
}

type Engine interface {
	InspectImage(context.Context, string) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainer(context.Context, string) error
	StopContainer(string, uint) error
	RemoveContainer(context.Context, string, bool) error
	InspectContainer(string) (containerengine.Container, error)
}

type EnvironmentResolver interface {
	Resolve(context.Context, state.ServiceDesired, string) (map[string]string, error)
}

type GitHub interface {
	CreateDeployment(context.Context, githubapp.CreateDeploymentInput) (githubapp.Deployment, error)
	CreateDeploymentStatus(context.Context, githubapp.CreateDeploymentStatusInput) error
	CreateIssueComment(context.Context, int64, int, string) (githubapp.IssueComment, error)
	UpdateIssueComment(context.Context, int64, int64, string) error
}

type DNS interface {
	EnsurePreviewHostname(context.Context, string, string, string) ([]string, error)
	DeletePreviewHostname(context.Context, string, []string) error
}

type Placement struct {
	NetworkName  string
	Gateway      netip.Addr
	DNSSearch    string
	CgroupParent string
}

type Config struct {
	Store             Store
	Engine            Engine
	Environment       EnvironmentResolver
	Sources           deployment.SourceResolver
	GitHub            GitHub
	DNS               DNS
	Growth            deployment.GrowthGate
	Admission         *admission.Gate
	Placement         func(state.ServiceDesired) (Placement, error)
	RoutesChanged     func(context.Context) error
	CertificateCovers func(string) bool
	AdminHostname     string
	LogRoot           string
	LogSizeBytes      int64
	LogMaxFiles       uint
	Now               func() time.Time
	NewID             func(time.Time) (string, error)
}

type activeContainer struct {
	container   containerengine.Container
	networkName string
}

type Application struct {
	store             Store
	engine            Engine
	environment       EnvironmentResolver
	sources           deployment.SourceResolver
	github            GitHub
	dns               DNS
	growth            deployment.GrowthGate
	admission         *admission.Gate
	placement         func(state.ServiceDesired) (Placement, error)
	routesChanged     func(context.Context) error
	certificateCovers func(string) bool
	adminHostname     string
	logRoot           string
	logSizeBytes      int64
	logMaxFiles       uint
	now               func() time.Time
	newID             func(time.Time) (string, error)
	httpClient        *http.Client

	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	active map[string]activeContainer
}

func New(config Config) (*Application, error) {
	if config.Store == nil || config.Engine == nil || config.Environment == nil || config.Sources == nil ||
		config.GitHub == nil || config.DNS == nil || config.Growth == nil || config.Admission == nil ||
		config.Placement == nil || config.RoutesChanged == nil || config.CertificateCovers == nil ||
		config.AdminHostname == "" || !filepath.IsAbs(config.LogRoot) || config.LogRoot == "/" ||
		config.LogSizeBytes <= 0 || config.LogMaxFiles == 0 {
		return nil, errors.New("PR preview dependencies are incomplete")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = func(timestamp time.Time) (string, error) { return id.NewWith(timestamp, rand.Reader) }
	}
	return &Application{
		store: config.Store, engine: config.Engine, environment: config.Environment,
		sources: config.Sources, github: config.GitHub, dns: config.DNS,
		growth: config.Growth, admission: config.Admission, placement: config.Placement,
		routesChanged: config.RoutesChanged, certificateCovers: config.CertificateCovers,
		adminHostname: config.AdminHostname, logRoot: config.LogRoot,
		logSizeBytes: config.LogSizeBytes, logMaxFiles: config.LogMaxFiles,
		now: now, newID: newID,
		httpClient: &http.Client{
			Timeout: probeTimeout,
			Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true,
				DialContext: (&net.Dialer{Timeout: probeTimeout}).DialContext},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		locks: make(map[string]*sync.Mutex), active: make(map[string]activeContainer),
	}, nil
}

func (application *Application) Deploy(ctx context.Context, serviceID string, event githubapp.PullRequestEvent) error {
	if event.Action != "deploy" || event.Number <= 0 || event.Revision == "" {
		return errors.New("PR preview event is invalid")
	}
	lease, err := application.admission.Begin("service_pr_preview", serviceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := application.previewLock(serviceID, event.Number)
	lock.Lock()
	defer lock.Unlock()

	desired, domains, preview, err := application.desiredPreview(ctx, serviceID, event)
	if err != nil {
		return err
	}
	current, currentErr := application.store.ActivePreviewDeployment(ctx, serviceID, event.Number)
	if currentErr == nil && current.SourceRevision == event.Revision {
		return nil
	}
	if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
		return currentErr
	}

	normalized, snapshotJSON, configHash, err := serviceconfig.Canonical(desired.Snapshot)
	if err != nil {
		return err
	}
	desired.Snapshot = normalized
	startedAt := application.now()
	previewID, err := application.newID(startedAt)
	if err != nil {
		return err
	}
	hostname, err := servicesource.PreviewHostname(preview.HostnameTemplate, event.Revision)
	if err != nil {
		return err
	}
	if err := application.growth.PermitGrowth(ctx); err != nil {
		return err
	}
	if err := application.store.BeginPreviewDeployment(ctx, state.BeginPreviewDeployment{
		ID: previewID, ServiceID: serviceID, PullRequestNumber: event.Number,
		SourceRevision: event.Revision, Hostname: hostname, TargetPort: domains[0].TargetPort,
		ConfigHash: configHash, SnapshotJSON: snapshotJSON,
		CreatedAtMillis: startedAt.UnixMilli(), ExpiresAtMillis: startedAt.Add(Retention).UnixMilli(),
	}); err != nil {
		return err
	}
	logPath := application.buildLogPath(serviceID, previewID)
	commentID, _ := application.store.LatestPreviewCommentID(ctx, serviceID, event.Number)
	githubDeploymentID := application.startGitHubReport(ctx, desired, previewID, event, logPath)
	if githubDeploymentID > 0 {
		_ = application.store.SetPreviewGitHubDeployment(ctx, previewID, githubDeploymentID)
	}
	commentID = application.reportComment(ctx, desired, event, previewID, commentID, "building", hostname, "")
	if commentID > 0 {
		_ = application.store.SetPreviewGitHubComment(ctx, previewID, commentID)
	}
	if !application.certificateCovers(hostname) {
		return application.fail(
			ctx, desired, event, previewID, githubDeploymentID, commentID, hostname,
			"certificate_not_configured",
			fmt.Errorf("origin certificate does not cover PR preview hostname %s", hostname),
		)
	}

	logFile, err := buildlog.OpenAppend(logPath)
	if err != nil {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "build_log_failed", err)
	}
	resolution, resolveErr := application.sources.Resolve(ctx, desired, previewID, event.Revision, logFile, false)
	closeErr := logFile.Close()
	if closeErr != nil && resolveErr == nil {
		resolveErr = closeErr
	}
	if resolveErr != nil {
		var skipped *deployment.SourceSkippedError
		if errors.Is(resolveErr, deployment.ErrSourceChecksPending) {
			return application.skip(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "Waiting for GitHub CI checks")
		}
		if errors.As(resolveErr, &skipped) {
			return application.skip(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, skipped.Reason)
		}
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "source_resolution_failed", resolveErr)
	}
	if resolution.Image.ID == "" || resolution.Image.Digest == "" || resolution.ImageReference == "" {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "source_resolution_failed", errors.New("built preview image is incomplete"))
	}
	if err := application.store.SetPreviewBuild(ctx, previewID, resolution.Image.Digest, resolution.ImageReference, resolution.CommitMessage); err != nil {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "state_update_failed", err)
	}
	candidate, placement, err := application.createContainer(ctx, desired, previewID, event, hostname, resolution.Image.ID)
	if err != nil {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "candidate_create_failed", err)
	}
	candidateOwned := true
	defer func() {
		if candidateOwned {
			_ = application.engine.RemoveContainer(context.Background(), candidate.ID, true)
		}
	}()
	if err := application.engine.StartContainer(ctx, candidate.ID); err != nil {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "candidate_start_failed", err)
	}
	ready, err := application.waitReady(ctx, desired, candidate.ID, placement.NetworkName)
	if err != nil {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "readiness_failed", err)
	}
	recordIDs, err := application.dns.EnsurePreviewHostname(ctx, domains[0].Hostname, hostname, previewID)
	if err != nil {
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "cloudflare_dns_failed", err)
	}
	previewState := state.PreviewDeployment{
		ID: previewID, ServiceID: serviceID, PullRequestNumber: event.Number,
		SourceRevision: event.Revision, Hostname: hostname,
		GitHubDeploymentID: githubDeploymentID, GitHubCommentID: commentID,
		CloudflareRecordIDs: recordIDs, Snapshot: desired.Snapshot,
	}
	if err := application.store.SetPreviewDNSRecords(ctx, previewID, recordIDs); err != nil {
		cleanupErr := application.dns.DeletePreviewHostname(context.Background(), hostname, recordIDs)
		return application.fail(
			ctx, desired, event, previewID, githubDeploymentID, commentID, hostname,
			"state_update_failed", errors.Join(err, cleanupErr),
		)
	}
	expectedActiveID := current.ID
	application.setActive(previewID, activeContainer{container: ready, networkName: placement.NetworkName})
	if err := application.store.ActivatePreviewDeployment(ctx, previewID, expectedActiveID, recordIDs, application.now().UnixMilli()); err != nil {
		application.clearActive(previewID)
		cleanupErr := application.deleteDNS(context.Background(), previewState)
		return application.fail(ctx, desired, event, previewID, githubDeploymentID, commentID, hostname, "publication_commit_failed", errors.Join(err, cleanupErr))
	}
	if err := application.routesChanged(ctx); err != nil {
		withdrawErr := application.stop(ctx, previewState, "Preview route publication failed")
		application.reportComment(ctx, desired, event, previewID, commentID, "failed", hostname, err.Error())
		return errors.Join(fmt.Errorf("publish PR preview route: %w", err), withdrawErr)
	}
	candidateOwned = false
	if current.ID != "" {
		application.removeRuntime(current.ID)
		if err := application.deleteDNS(context.Background(), current); err != nil {
			// The stopped row retains record IDs for the periodic retry.
			_ = appendBuildLog(logPath, "Cloudflare DNS cleanup warning: "+err.Error())
		}
		application.finishGitHubReport(current, githubapp.DeploymentInactive, "Preview superseded", "")
	}
	application.finishGitHubReport(state.PreviewDeployment{
		ID: previewID, ServiceID: serviceID, PullRequestNumber: event.Number, GitHubDeploymentID: githubDeploymentID,
		Hostname: hostname, Snapshot: desired.Snapshot,
	}, githubapp.DeploymentSuccess, "Preview ready", "https://"+hostname)
	application.reportComment(ctx, desired, event, previewID, commentID, "ready", hostname, "")
	return nil
}

// ClosePullRequest withdraws traffic and DNS immediately. The immutable
// deployment row and logs remain available until the 14-day preview GC runs.
func (application *Application) ClosePullRequest(ctx context.Context, serviceID string, event githubapp.PullRequestEvent) error {
	lock := application.previewLock(serviceID, event.Number)
	lock.Lock()
	defer lock.Unlock()
	active, err := application.store.ActivePreviewDeployment(ctx, serviceID, event.Number)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	stopErr := application.stop(ctx, active, "Pull request closed")
	desired, loadErr := application.store.DesiredService(ctx, serviceID)
	if loadErr == nil {
		application.reportComment(ctx, desired, event, active.ID, active.GitHubCommentID, "stopped", active.Hostname, "Pull request closed")
	}
	return stopErr
}

// StopService withdraws every preview before the parent service is disabled,
// reconfigured away from GitHub previews, or deleted.
func (application *Application) StopService(ctx context.Context, serviceID, reason string) error {
	previews, err := application.store.ActivePreviewDeployments(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range previews {
		if item.ServiceID != serviceID {
			continue
		}
		lock := application.previewLock(item.ServiceID, item.PullRequestNumber)
		lock.Lock()
		failures = append(failures, application.stop(ctx, item, reason))
		lock.Unlock()
	}
	return errors.Join(failures...)
}

func (application *Application) Restore(ctx context.Context) error {
	previews, err := application.store.ActivePreviewDeployments(ctx)
	if err != nil {
		return err
	}
	for _, preview := range previews {
		if preview.ExpiresAtMillis <= application.now().UnixMilli() {
			_ = application.stop(ctx, preview, "Preview expired")
			continue
		}
		desired, err := application.store.DesiredService(ctx, preview.ServiceID)
		if err != nil {
			return err
		}
		desired.Snapshot = preview.Snapshot
		image, err := application.engine.InspectImage(ctx, preview.ImageDigest)
		if err != nil {
			// Control disaster recovery restores durable preview state, but image
			// layers are intentionally reconstructed from the pinned Git revision.
			if stopErr := application.stop(ctx, preview, "Rebuilding preview after recovery"); stopErr != nil {
				return errors.Join(fmt.Errorf("restore PR preview %s image: %w", preview.ID, err), stopErr)
			}
			source := desired.Snapshot.Source.GitHub
			if source == nil {
				continue
			}
			redeployEvent := githubapp.PullRequestEvent{
				Action: "deploy", RepositoryID: source.RepositoryID,
				Number: preview.PullRequestNumber, BaseBranch: source.Branch,
				Revision: preview.SourceRevision, ChecksEvent: source.WaitForCI,
			}
			if deployErr := application.Deploy(ctx, preview.ServiceID, redeployEvent); deployErr != nil {
				return fmt.Errorf("rebuild PR preview %s after recovery: %w", preview.ID, deployErr)
			}
			continue
		}
		event := githubapp.PullRequestEvent{Number: preview.PullRequestNumber, Revision: preview.SourceRevision}
		container, placement, err := application.createContainer(ctx, desired, preview.ID, event, preview.Hostname, image.ID)
		if err != nil {
			return err
		}
		if err := application.engine.StartContainer(ctx, container.ID); err != nil {
			_ = application.engine.RemoveContainer(context.Background(), container.ID, true)
			return err
		}
		ready, err := application.waitReady(ctx, desired, container.ID, placement.NetworkName)
		if err != nil {
			_ = application.engine.RemoveContainer(context.Background(), container.ID, true)
			return err
		}
		application.reconcileDNS(ctx, desired, preview)
		application.setActive(preview.ID, activeContainer{container: ready, networkName: placement.NetworkName})
	}
	return application.routesChanged(ctx)
}

func (application *Application) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		application.cleanup(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (application *Application) cleanup(ctx context.Context) {
	now := application.now()
	active, err := application.store.ActivePreviewDeployments(ctx)
	if err == nil {
		for _, item := range active {
			lock := application.previewLock(item.ServiceID, item.PullRequestNumber)
			lock.Lock()
			current, currentErr := application.store.ActivePreviewDeployment(ctx, item.ServiceID, item.PullRequestNumber)
			if currentErr == nil && current.ID == item.ID {
				desired, loadErr := application.store.DesiredService(ctx, item.ServiceID)
				if loadErr == nil {
					application.reconcileDNS(ctx, desired, item)
				}
			}
			lock.Unlock()
		}
	}
	expired, err := application.store.ExpiredActivePreviewDeployments(ctx, now.UnixMilli())
	if err == nil {
		for _, preview := range expired {
			_ = application.stop(ctx, preview, "Preview expired after 14 days")
		}
	}
	finishedWithDNS, err := application.store.FinishedPreviewDeploymentsWithDNS(ctx)
	if err == nil {
		for _, item := range finishedWithDNS {
			_ = application.deleteDNS(ctx, item)
		}
	}
	removed, err := application.store.DeleteFinishedPreviewDeployments(ctx, now.Add(-Retention).UnixMilli())
	if err != nil {
		return
	}
	for _, preview := range removed {
		_ = os.RemoveAll(filepath.Join(application.logRoot, "services", preview.ServiceID, preview.ID))
	}
}

func (application *Application) reconcileDNS(ctx context.Context, desired state.ServiceDesired, item state.PreviewDeployment) {
	domains, err := application.store.ServiceDomains(ctx, desired.ProjectID, desired.ID)
	if err == nil && len(domains) != 1 {
		err = state.ErrPreviewDomainCount
	}
	var recordIDs []string
	if err == nil {
		recordIDs, err = application.dns.EnsurePreviewHostname(ctx, domains[0].Hostname, item.Hostname, item.ID)
	}
	if err == nil {
		err = application.store.SetPreviewDNSRecords(ctx, item.ID, recordIDs)
	}
	if err != nil {
		_ = appendBuildLog(application.buildLogPath(item.ServiceID, item.ID), "Cloudflare DNS reconcile warning: "+err.Error())
	}
}

func (application *Application) stop(ctx context.Context, preview state.PreviewDeployment, reason string) error {
	stateErr := application.store.StopPreviewDeployment(ctx, preview.ID, application.now().UnixMilli())
	routeErr := application.routesChanged(ctx)
	application.removeRuntime(preview.ID)
	dnsErr := application.deleteDNS(context.Background(), preview)
	application.finishGitHubReport(preview, githubapp.DeploymentInactive, reason, "")
	return errors.Join(stateErr, routeErr, dnsErr)
}

func (application *Application) deleteDNS(ctx context.Context, preview state.PreviewDeployment) error {
	if len(preview.CloudflareRecordIDs) == 0 {
		return nil
	}
	if err := application.dns.DeletePreviewHostname(ctx, preview.Hostname, preview.CloudflareRecordIDs); err != nil {
		return err
	}
	return application.store.ClearPreviewDNSRecords(ctx, preview.ID)
}

func (application *Application) Backend(previewID string, targetPort int) (deployment.Backend, bool, error) {
	application.mu.Lock()
	active, ok := application.active[previewID]
	application.mu.Unlock()
	if !ok || targetPort < 1 || targetPort > 65535 {
		return deployment.Backend{}, false, nil
	}
	container, err := application.engine.InspectContainer(active.container.ID)
	if err != nil {
		return deployment.Backend{}, true, err
	}
	if container.State != "running" {
		return deployment.Backend{}, false, nil
	}
	addresses := container.IPs[active.networkName]
	if len(addresses) != 1 {
		return deployment.Backend{}, true, fmt.Errorf("PR preview container has %d backend addresses, want one", len(addresses))
	}
	return deployment.Backend{DeploymentID: previewID, Address: addresses[0], Port: targetPort}, true, nil
}

func (application *Application) desiredPreview(ctx context.Context, serviceID string, event githubapp.PullRequestEvent) (state.ServiceDesired, []state.ServiceDomain, servicesource.PullRequestPreview, error) {
	desired, err := application.store.DesiredService(ctx, serviceID)
	if err != nil {
		return state.ServiceDesired{}, nil, servicesource.PullRequestPreview{}, err
	}
	github := desired.Snapshot.Source.GitHub
	if !desired.Enabled || github == nil || github.PullRequestPreview == nil || github.RepositoryID != event.RepositoryID || github.Branch != event.BaseBranch {
		return state.ServiceDesired{}, nil, servicesource.PullRequestPreview{}, errors.New("service is not configured for this PR preview")
	}
	domains, err := application.store.ServiceDomains(ctx, desired.ProjectID, serviceID)
	if err != nil {
		return state.ServiceDesired{}, nil, servicesource.PullRequestPreview{}, err
	}
	if len(domains) != 1 {
		return state.ServiceDesired{}, nil, servicesource.PullRequestPreview{}, state.ErrPreviewDomainCount
	}
	return desired, domains, *github.PullRequestPreview, nil
}

func (application *Application) createContainer(ctx context.Context, desired state.ServiceDesired, previewID string, event githubapp.PullRequestEvent, hostname, imageID string) (containerengine.Container, Placement, error) {
	placement, err := application.placement(desired)
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	attemptID, err := application.newID(application.now())
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	logPath := filepath.Join(application.logRoot, "services", desired.ID, previewID, attemptID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	environment, err := application.environment.Resolve(ctx, desired, previewID)
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	environment["PLATFORMD_PREVIEW"] = "true"
	environment["PLATFORMD_PREVIEW_URL"] = "https://" + hostname
	environment["PLATFORMD_PULL_REQUEST_NUMBER"] = strconv.Itoa(event.Number)
	container, err := application.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-preview-" + previewID,
		Entrypoint: desired.Snapshot.Command, Command: desired.Snapshot.Args, Environment: environment,
		Labels: map[string]string{
			"io.platformd.owner": "preview", "io.platformd.project-id": desired.ProjectID,
			"io.platformd.service-id": desired.ID, "io.platformd.preview-id": previewID,
		},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()},
		DNSSearch: []string{placement.DNSSearch}, LogPath: logPath,
		LogSizeBytes: application.logSizeBytes, LogMaxFiles: application.logMaxFiles,
		CgroupParent: placement.CgroupParent, CPUMillicores: desired.Snapshot.CPUMillicores,
		MemoryMaxBytes: desired.Snapshot.MemoryMaxBytes,
	})
	return container, placement, err
}

func (application *Application) waitReady(ctx context.Context, desired state.ServiceDesired, containerID, networkName string) (containerengine.Container, error) {
	health := desired.Snapshot.HealthCheck
	timeout := time.Duration(serviceconfig.DefaultHealthTimeoutSeconds) * time.Second
	if health != nil {
		timeout = time.Duration(health.TimeoutSeconds) * time.Second
	}
	deadline := application.now().Add(timeout)
	if health == nil {
		deadline = application.now().Add(processStartupGrace)
	}
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	for {
		container, err := application.engine.InspectContainer(containerID)
		if err != nil {
			return containerengine.Container{}, err
		}
		if container.State != "running" {
			return containerengine.Container{}, fmt.Errorf("container state is %s", container.State)
		}
		if health == nil && !application.now().Before(deadline) {
			return container, nil
		}
		if health != nil {
			addresses := container.IPs[networkName]
			if len(addresses) == 1 {
				request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(addresses[0], strconv.Itoa(health.Port))+health.Path, nil)
				response, probeErr := application.httpClient.Do(request)
				if probeErr == nil {
					_ = response.Body.Close()
					if response.StatusCode >= 200 && response.StatusCode < 400 {
						return container, nil
					}
				}
			}
			if !application.now().Before(deadline) {
				return containerengine.Container{}, errors.New("HTTP readiness timed out")
			}
		}
		select {
		case <-ctx.Done():
			return containerengine.Container{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (application *Application) fail(ctx context.Context, desired state.ServiceDesired, event githubapp.PullRequestEvent, previewID string, githubDeploymentID, commentID int64, hostname, code string, cause error) error {
	_ = appendBuildLog(application.buildLogPath(desired.ID, previewID), "Preview failed: "+cause.Error())
	_ = application.store.FinishPreviewDeployment(ctx, previewID, "failed", code, cause.Error(), application.now().UnixMilli())
	application.finishGitHubReport(state.PreviewDeployment{
		ID: previewID, ServiceID: desired.ID, PullRequestNumber: event.Number, GitHubDeploymentID: githubDeploymentID,
		Hostname: hostname, Snapshot: desired.Snapshot,
	}, githubapp.DeploymentFailure, "Preview failed", "")
	application.reportComment(ctx, desired, event, previewID, commentID, "failed", hostname, cause.Error())
	return cause
}

func (application *Application) skip(ctx context.Context, desired state.ServiceDesired, event githubapp.PullRequestEvent, previewID string, githubDeploymentID, commentID int64, hostname, reason string) error {
	_ = application.store.FinishPreviewDeployment(ctx, previewID, "skipped", "source_skipped", reason, application.now().UnixMilli())
	application.finishGitHubReport(state.PreviewDeployment{
		ID: previewID, ServiceID: desired.ID, PullRequestNumber: event.Number, GitHubDeploymentID: githubDeploymentID,
		Hostname: hostname, Snapshot: desired.Snapshot,
	}, githubapp.DeploymentFailure, reason, "")
	application.reportComment(ctx, desired, event, previewID, commentID, "waiting", hostname, reason)
	return nil
}

func (application *Application) startGitHubReport(ctx context.Context, desired state.ServiceDesired, previewID string, event githubapp.PullRequestEvent, logPath string) int64 {
	environment := previewEnvironment(desired, event.Number)
	created, err := application.github.CreateDeployment(ctx, githubapp.CreateDeploymentInput{
		RepositoryID: event.RepositoryID, Ref: event.Revision, Environment: environment,
		Description:           "PR preview for " + desired.ProjectName + "/" + desired.Name,
		PlatformdDeploymentID: previewID, TransientEnvironment: true, ProductionEnvironment: false,
	})
	if err != nil {
		_ = appendBuildLog(logPath, "GitHub deployment reporting warning: "+err.Error())
		return 0
	}
	err = application.github.CreateDeploymentStatus(ctx, githubapp.CreateDeploymentStatusInput{
		RepositoryID: event.RepositoryID, DeploymentID: created.ID, State: githubapp.DeploymentInProgress,
		Environment: environment, Description: "Building preview", LogURL: application.logURL(desired, previewID),
	})
	if err != nil {
		_ = appendBuildLog(logPath, "GitHub deployment reporting warning: "+err.Error())
	}
	return created.ID
}

func (application *Application) finishGitHubReport(preview state.PreviewDeployment, status githubapp.DeploymentStatusState, description, environmentURL string) {
	github := preview.Snapshot.Source.GitHub
	if github == nil || preview.GitHubDeploymentID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	logURL := application.logURLForIDs(preview.ServiceID, preview.ID)
	if desired, err := application.store.DesiredService(ctx, preview.ServiceID); err == nil {
		logURL = application.logURL(desired, preview.ID)
	}
	_ = application.github.CreateDeploymentStatus(ctx, githubapp.CreateDeploymentStatusInput{
		RepositoryID: github.RepositoryID, DeploymentID: preview.GitHubDeploymentID, State: status,
		Environment: previewEnvironmentFromSnapshot(preview), Description: description,
		LogURL: logURL, EnvironmentURL: environmentURL,
	})
}

func (application *Application) reportComment(ctx context.Context, desired state.ServiceDesired, event githubapp.PullRequestEvent, previewID string, commentID int64, status, hostname, detail string) int64 {
	source := desired.Snapshot.Source.GitHub
	if source == nil {
		return commentID
	}
	body := previewComment(desired, event, status, hostname, detail)
	reportContext, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	if commentID > 0 {
		if err := application.github.UpdateIssueComment(reportContext, source.RepositoryID, commentID, body); err == nil {
			return commentID
		}
	}
	created, err := application.github.CreateIssueComment(reportContext, source.RepositoryID, event.Number, body)
	if err != nil {
		_ = appendBuildLog(application.buildLogPath(desired.ID, previewID), "GitHub PR comment warning: "+err.Error())
		return commentID
	}
	return created.ID
}

func previewComment(desired state.ServiceDesired, event githubapp.PullRequestEvent, status, hostname, detail string) string {
	body := "<!-- platformd-preview:" + desired.ID + " -->\n### platformd preview — `" + desired.Name + "`\n\n"
	switch status {
	case "ready":
		body += "✅ Ready: [https://" + hostname + "](https://" + hostname + ")"
	case "building":
		revision := event.Revision
		if len(revision) > servicesource.PreviewHashLength {
			revision = revision[:servicesource.PreviewHashLength]
		}
		body += "⏳ Building `" + revision + "`"
	case "waiting":
		body += "⏸ " + detail
	case "stopped":
		body += "🛑 Preview stopped — " + detail
	default:
		body += "❌ Preview failed: " + detail
	}
	body += "\n\nThis preview expires 14 days after it is created."
	return body
}

func previewEnvironment(desired state.ServiceDesired, pullRequest int) string {
	return "platformd/preview/" + desired.ID + "/pr-" + strconv.Itoa(pullRequest)
}

func previewEnvironmentFromSnapshot(preview state.PreviewDeployment) string {
	return "platformd/preview/" + preview.ServiceID + "/pr-" + strconv.Itoa(preview.PullRequestNumber)
}

func (application *Application) logURL(desired state.ServiceDesired, previewID string) string {
	value, err := url.JoinPath(
		"https://"+application.adminHostname,
		"projects", desired.ProjectID, "services", desired.ID,
		"deployments", previewID, "build-logs",
	)
	if err != nil {
		return application.logURLForIDs(desired.ID, previewID)
	}
	return value
}

func (application *Application) logURLForIDs(_, _ string) string {
	return "https://" + application.adminHostname
}

func (application *Application) buildLogPath(serviceID, previewID string) string {
	return filepath.Join(application.logRoot, "services", serviceID, previewID, "build.log")
}

func appendBuildLog(path, message string) error {
	return buildlog.Append(path, time.Now().UTC().Format(time.RFC3339)+" "+message+"\n")
}

func (application *Application) previewLock(serviceID string, pullRequest int) *sync.Mutex {
	key := serviceID + ":" + strconv.Itoa(pullRequest)
	application.mu.Lock()
	defer application.mu.Unlock()
	lock := application.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		application.locks[key] = lock
	}
	return lock
}

func (application *Application) setActive(previewID string, active activeContainer) {
	application.mu.Lock()
	application.active[previewID] = active
	application.mu.Unlock()
}

func (application *Application) clearActive(previewID string) {
	application.mu.Lock()
	delete(application.active, previewID)
	application.mu.Unlock()
}

func (application *Application) removeRuntime(previewID string) {
	application.mu.Lock()
	active, ok := application.active[previewID]
	delete(application.active, previewID)
	application.mu.Unlock()
	if !ok {
		return
	}
	_ = application.engine.StopContainer(active.container.ID, stopTimeoutSeconds)
	_ = application.engine.RemoveContainer(context.Background(), active.container.ID, true)
}
