package deployment

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/buildlog"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/diskpressure"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
)

const (
	processStartupGrace = 3 * time.Second
	probeInterval       = 250 * time.Millisecond
	probeTimeout        = 2 * time.Second
	reportTimeout       = 10 * time.Second
	stopTimeoutSeconds  = 10
)

var ErrBlockedPair = errors.New("deployment pair is blocked by an earlier failure")
var ErrSourceChecksPending = errors.New("source checks are still pending")

type SourceSkippedError struct {
	Reason string
}

func (err *SourceSkippedError) Error() string {
	return err.Reason
}

type Store interface {
	DesiredService(context.Context, string) (state.ServiceDesired, error)
	BeginDeployment(context.Context, state.BeginDeployment) error
	UpdateDeploymentSource(context.Context, string, string, string, string, string) error
	FinishDeployment(context.Context, string, string, string, string, int64) error
	ActivateDeployment(context.Context, string, string, string, int64) error
	FailDeployment(context.Context, string, string, string, int64) error
	LatestFailedDeployment(context.Context, string, string, string) (bool, error)
	Deployment(context.Context, string) (state.DeploymentRecord, error)
	VolumeInitialized(context.Context, string, string, string) (bool, error)
	RecordVolumeInitialization(context.Context, string, string, string, int64) error
}

type Engine interface {
	Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error)
	InspectImage(context.Context, string) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainer(context.Context, string) error
	StopContainer(string, uint) error
	RemoveContainer(context.Context, string, bool) error
	InspectContainer(string) (containerengine.Container, error)
	ExecContainer(context.Context, string, containerengine.ExecRequest) (int, error)
	ExecTerminalContainer(context.Context, string, containerengine.TerminalExecRequest) (int, error)
}

type Placement struct {
	NetworkName  string
	Gateway      netip.Addr
	DNSSearch    string
	CgroupParent string
}

type Publisher interface {
	Publish(state.ServiceDesired, containerengine.Container) error
	Withdraw(state.ServiceDesired) error
}

type ImageCredential struct {
	Username string
	Password string
}

type RuntimeStatus struct {
	DeploymentID string
	State        string
	ExitCode     int32
}

type quiescedService struct {
	desired state.ServiceDesired
	active  activeContainer
}

type Backend struct {
	DeploymentID string
	Address      string
	Port         int
}

type TerminalTarget struct {
	DeploymentID string
	ContainerID  string
}

type CredentialResolver interface {
	Resolve(context.Context, state.ServiceDesired) (ImageCredential, error)
}

type EnvironmentResolver interface {
	Resolve(context.Context, state.ServiceDesired, string) (map[string]string, error)
}

type ImageSourceResolver interface {
	Resolve(context.Context, string) (reference string, close func(), handled bool, err error)
}

type SourceResolution struct {
	Image          containerengine.Image
	ImageReference string
	Revision       string
	CommitMessage  string
}

type SourceBuildStarted func(SourceResolution) error

type SourceResolver interface {
	Resolve(context.Context, state.ServiceDesired, string, string, io.Writer, bool, SourceBuildStarted) (SourceResolution, error)
}

type ReportStatus string

const (
	ReportSucceeded ReportStatus = "succeeded"
	ReportFailed    ReportStatus = "failed"
)

// Reporter mirrors a local deployment into an external source provider. The
// local deployment record remains authoritative and reporting errors never
// change whether a workload is deployed.
type Reporter interface {
	Start(context.Context, state.ServiceDesired, string, string) (string, error)
	Finish(context.Context, state.ServiceDesired, string, string, ReportStatus) error
}

type GrowthGate interface {
	PermitGrowth(context.Context) error
}

type Config struct {
	Store        Store
	Engine       Engine
	Publisher    Publisher
	Credentials  CredentialResolver
	Environment  EnvironmentResolver
	ImageSources ImageSourceResolver
	Sources      SourceResolver
	Reporter     Reporter
	Growth       GrowthGate
	Admission    *admission.Gate
	Placement    func(state.ServiceDesired) (Placement, error)
	LogRoot      string
	VolumeRoot   string
	LogSizeBytes int64
	LogMaxFiles  uint
	Now          func() time.Time
	NewID        func(time.Time) (string, error)
	HTTPClient   *http.Client
}

type activeContainer struct {
	deploymentID string
	container    containerengine.Container
	networkName  string
}

type Controller struct {
	store        Store
	engine       Engine
	publisher    Publisher
	credentials  CredentialResolver
	environment  EnvironmentResolver
	imageSources ImageSourceResolver
	sources      SourceResolver
	reporter     Reporter
	growth       GrowthGate
	admission    *admission.Gate
	placement    func(state.ServiceDesired) (Placement, error)
	logRoot      string
	volumeRoot   string
	logSizeBytes int64
	logMaxFiles  uint
	now          func() time.Time
	newID        func(time.Time) (string, error)
	httpClient   *http.Client

	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	active map[string]activeContainer
}

func New(config Config) (*Controller, error) {
	if config.Store == nil || config.Engine == nil || config.Publisher == nil || config.Growth == nil || config.Admission == nil || config.Placement == nil {
		return nil, errors.New("deployment controller dependencies are incomplete")
	}
	if !safeRoot(config.LogRoot) || !safeRoot(config.VolumeRoot) {
		return nil, errors.New("deployment controller roots must be canonical absolute non-root paths")
	}
	if config.LogSizeBytes <= 0 || config.LogMaxFiles == 0 {
		return nil, errors.New("deployment controller log rotation must be positive")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = func(timestamp time.Time) (string, error) {
			return id.NewWith(timestamp, rand.Reader)
		}
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: probeTimeout,
			Transport: &http.Transport{
				Proxy:             nil,
				DisableKeepAlives: true,
				DialContext:       (&net.Dialer{Timeout: probeTimeout}).DialContext,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Controller{
		store: config.Store, engine: config.Engine, publisher: config.Publisher, credentials: config.Credentials,
		environment:  config.Environment,
		imageSources: config.ImageSources,
		sources:      config.Sources,
		reporter:     config.Reporter,
		growth:       config.Growth,
		admission:    config.Admission,
		placement:    config.Placement, logRoot: config.LogRoot, volumeRoot: config.VolumeRoot,
		logSizeBytes: config.LogSizeBytes, logMaxFiles: config.LogMaxFiles,
		now: now, newID: newID, httpClient: httpClient,
		locks: make(map[string]*sync.Mutex), active: make(map[string]activeContainer),
	}, nil
}

func (controller *Controller) Deploy(ctx context.Context, serviceID string, force bool) error {
	return controller.deploy(ctx, serviceID, "", force)
}

// DeployRevision pins a webhook-triggered build to the commit that produced
// the event without writing that transient revision into desired service state.
func (controller *Controller) DeployRevision(ctx context.Context, serviceID, revision string, force bool) error {
	if revision == "" {
		return errors.New("source revision is required")
	}
	return controller.deploy(ctx, serviceID, revision, force)
}

func (controller *Controller) deploy(ctx context.Context, serviceID, sourceRevisionOverride string, force bool) error {
	lease, err := controller.admission.Begin("service_deploy", serviceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()

	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	if !desired.Enabled {
		return controller.stopDisabled(ctx, desired)
	}
	normalized, snapshotJSON, configHash, err := serviceconfig.Canonical(desired.Snapshot)
	if err != nil {
		return err
	}
	desired.Snapshot = normalized
	startedAt := controller.now()
	deploymentID, err := controller.newID(startedAt)
	if err != nil {
		return fmt.Errorf("allocate deployment ID: %w", err)
	}
	buildLogPath := controller.buildLogPath(serviceID, deploymentID)
	if err := controller.growth.PermitGrowth(ctx); err != nil {
		active, activeExists := controller.activeContainer(serviceID)
		if !force && errors.Is(err, diskpressure.ErrGrowthDenied) && activeExists &&
			active.deploymentID == desired.ActiveDeploymentID && desired.ActiveConfigHash == configHash {
			return controller.publisher.Publish(desired, active.container)
		}
		return err
	}
	imageReference := servicesource.ImageReference(normalized.Source)
	var image containerengine.Image
	var sourceRevision string
	var commitMessage string
	var reportID string
	deploymentStarted := false
	beginDeployment := func(resolution SourceResolution) error {
		if deploymentStarted {
			return nil
		}
		imageReference = resolution.ImageReference
		sourceRevision = resolution.Revision
		commitMessage = resolution.CommitMessage
		if err := controller.store.BeginDeployment(ctx, state.BeginDeployment{
			ID: deploymentID, ServiceID: serviceID, ImageDigest: resolution.Image.Digest,
			ImageReference: imageReference, SourceRevision: sourceRevision, CommitMessage: commitMessage,
			ConfigHash: configHash, SnapshotJSON: snapshotJSON, CreatedAtMillis: startedAt.UnixMilli(),
		}); err != nil {
			return err
		}
		deploymentStarted = true
		reportID = controller.startReport(ctx, desired, deploymentID, sourceRevision, buildLogPath)
		return nil
	}
	failDeployment := func(code string, failure error) error {
		if !deploymentStarted {
			if beginErr := beginDeployment(SourceResolution{
				Image: image, ImageReference: imageReference, Revision: sourceRevision, CommitMessage: commitMessage,
			}); beginErr != nil {
				return errors.Join(failure, beginErr)
			}
		}
		finishErr := controller.store.FinishDeployment(
			ctx, deploymentID, "failed", code, failure.Error(), controller.now().UnixMilli(),
		)
		controller.finishReport(desired, deploymentID, reportID, ReportFailed, buildLogPath)
		return errors.Join(failure, finishErr)
	}
	finishAttempt := func(status, code, message string) error {
		if !deploymentStarted {
			return nil
		}
		finishErr := controller.store.FinishDeployment(ctx, deploymentID, status, code, message, controller.now().UnixMilli())
		reportStatus := ReportSucceeded
		if status == "interrupted" {
			reportStatus = ReportFailed
		}
		controller.finishReport(desired, deploymentID, reportID, reportStatus, buildLogPath)
		return finishErr
	}
	if normalized.Source.Type == servicesource.GitHubImage {
		if controller.sources == nil {
			return errors.New("GitHub source resolution is not configured")
		}
		logFile, openErr := openBuildLog(buildLogPath)
		if openErr != nil {
			return openErr
		}
		resolution, resolveErr := controller.sources.Resolve(
			ctx, desired, deploymentID, sourceRevisionOverride, logFile, force, beginDeployment,
		)
		closeErr := logFile.Close()
		if closeErr != nil && resolveErr == nil {
			resolveErr = closeErr
		}
		imageReference = resolution.ImageReference
		sourceRevision = resolution.Revision
		commitMessage = resolution.CommitMessage
		var skipped *SourceSkippedError
		if errors.As(resolveErr, &skipped) {
			if deploymentStarted {
				if err := finishAttempt("skipped", "source_checks_failed", skipped.Reason); err != nil {
					return err
				}
			} else {
				finishedAt := controller.now()
				if err := controller.store.BeginDeployment(ctx, state.BeginDeployment{
					ID: deploymentID, ServiceID: serviceID, ImageReference: imageReference,
					SourceRevision: sourceRevision, CommitMessage: commitMessage,
					ConfigHash: configHash, SnapshotJSON: snapshotJSON,
					Status: "skipped", CreatedAtMillis: startedAt.UnixMilli(), FinishedAtMillis: finishedAt.UnixMilli(),
				}); err != nil {
					return err
				}
			}
			_ = appendBuildLog(buildLogPath, "Deployment skipped: "+skipped.Reason)
			return nil
		}
		if resolveErr != nil {
			if errors.Is(resolveErr, ErrSourceChecksPending) && !deploymentStarted {
				return resolveErr
			}
			return failDeployment("source_resolution_failed", resolveErr)
		}
		image = resolution.Image
		if !deploymentStarted {
			if err := beginDeployment(resolution); err != nil {
				return err
			}
		}
	} else {
		if imageReference == "" {
			return errors.New("image source reference is empty")
		}
		if err := beginDeployment(SourceResolution{ImageReference: imageReference}); err != nil {
			return err
		}
	}
	credential := ImageCredential{}
	if normalized.Source.Type == servicesource.PrivateImage {
		if controller.credentials == nil {
			return failDeployment("source_resolution_failed", errors.New("image credential resolution is not configured"))
		}
		credential, err = controller.credentials.Resolve(ctx, desired)
		if err != nil {
			return failDeployment("source_resolution_failed", fmt.Errorf("resolve image credential: %w", err))
		}
	}

	if normalized.Source.Type != servicesource.GitHubImage {
		if err := appendBuildLog(buildLogPath, "Resolving "+imageReference); err != nil {
			return failDeployment("build_log_failed", err)
		}
		image, err = controller.pull(ctx, containerengine.PullRequest{
			Reference: imageReference,
			Username:  credential.Username,
			Password:  credential.Password,
			Refresh:   !serviceconfig.IsDigestReference(imageReference),
		})
		if err != nil {
			_ = appendBuildLog(buildLogPath, "Source resolution failed: "+err.Error())
			return failDeployment("source_resolution_failed", fmt.Errorf("resolve and pull service image: %w", err))
		}
		if err := appendBuildLog(buildLogPath, "Resolved "+image.Digest); err != nil {
			return failDeployment("build_log_failed", err)
		}
	}
	if image.ID == "" || image.Digest == "" {
		return failDeployment("source_resolution_failed", errors.New("pulled image has no ID or digest"))
	}
	if servicesource.IsImage(normalized.Source) {
		if _, err := serviceconfig.PinnedReference(imageReference, image.Digest); err != nil {
			return failDeployment("source_resolution_failed", err)
		}
	}
	if err := controller.store.UpdateDeploymentSource(
		ctx, deploymentID, image.Digest, imageReference, sourceRevision, commitMessage,
	); err != nil {
		return failDeployment("state_update_failed", err)
	}

	current, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return failDeployment("state_load_failed", err)
	}
	currentNormalized, _, currentHash, err := serviceconfig.Canonical(current.Snapshot)
	if err != nil {
		return failDeployment("state_load_failed", err)
	}
	if !current.Enabled || currentHash != configHash {
		_ = finishAttempt("interrupted", "service_changed", state.ErrServiceChanged.Error())
		return state.ErrServiceChanged
	}
	desired = current
	desired.Snapshot = currentNormalized
	if !force && desired.ActiveImageDigest == image.Digest && desired.ActiveConfigHash == configHash {
		active, ok := controller.activeContainer(serviceID)
		if !ok || active.deploymentID != desired.ActiveDeploymentID {
			return failDeployment("runtime_state_missing", errors.New("active deployment has no matching runtime container"))
		}
		if err := finishAttempt("skipped", "no_changes", "The resolved source and service configuration are already active"); err != nil {
			return err
		}
		return controller.publisher.Publish(desired, active.container)
	}
	if !force {
		blocked, err := controller.store.LatestFailedDeployment(ctx, serviceID, configHash, image.Digest)
		if err != nil {
			return failDeployment("state_load_failed", err)
		}
		if blocked {
			_ = finishAttempt("skipped", "blocked_pair", ErrBlockedPair.Error())
			return ErrBlockedPair
		}
	}
	if err := appendBuildLog(buildLogPath, "Image ready; starting deployment"); err != nil {
		return failDeployment("build_log_failed", err)
	}
	deployErr := controller.runDeployment(ctx, desired, deploymentID, image.ID)
	if deployErr != nil {
		controller.finishReport(desired, deploymentID, reportID, ReportFailed, buildLogPath)
		return deployErr
	}
	controller.finishReport(desired, deploymentID, reportID, ReportSucceeded, buildLogPath)
	return nil
}

func openBuildLog(logPath string) (io.WriteCloser, error) {
	return buildlog.OpenAppend(logPath)
}

func (controller *Controller) buildLogPath(serviceID, deploymentID string) string {
	return filepath.Join(controller.logRoot, "services", serviceID, deploymentID, "build.log")
}

func appendBuildLog(logPath, message string) error {
	return buildlog.Append(logPath, fmt.Sprintf("%s %s\n", time.Now().UTC().Format(time.RFC3339), message))
}

func (controller *Controller) startReport(
	ctx context.Context,
	desired state.ServiceDesired,
	deploymentID string,
	revision string,
	buildLogPath string,
) string {
	if controller.reporter == nil || revision == "" {
		return ""
	}
	reportContext, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()
	reportID, err := controller.reporter.Start(reportContext, desired, deploymentID, revision)
	if err != nil {
		_ = appendBuildLog(buildLogPath, "External deployment reporting warning: "+err.Error())
	}
	return reportID
}

func (controller *Controller) finishReport(
	desired state.ServiceDesired,
	deploymentID string,
	reportID string,
	status ReportStatus,
	buildLogPath string,
) {
	if controller.reporter == nil || reportID == "" {
		return
	}
	reportContext, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	if err := controller.reporter.Finish(reportContext, desired, deploymentID, reportID, status); err != nil {
		_ = appendBuildLog(buildLogPath, "External deployment reporting warning: "+err.Error())
	}
}

func (controller *Controller) Restore(ctx context.Context, serviceID string) error {
	lease, err := controller.admission.Begin("service_reconcile", serviceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	_, err = controller.restoreCurrentLocked(ctx, serviceID, "")
	return err
}

// RestoreCurrent recreates the exact logical deployment only while it is
// still current. The expected pointer prevents a delayed crash-loop attempt
// from reviving an older deployment after a user action has replaced it.
func (controller *Controller) RestoreCurrent(ctx context.Context, serviceID, expectedDeploymentID string) (bool, error) {
	lease, err := controller.admission.Begin("service_restart", serviceID)
	if err != nil {
		return false, err
	}
	defer lease.Release()
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	return controller.restoreCurrentLocked(ctx, serviceID, expectedDeploymentID)
}

// RestartCurrent replaces only the runtime attempt. The logical deployment ID,
// immutable snapshot, and deployment history remain unchanged.
func (controller *Controller) RestartCurrent(ctx context.Context, serviceID, expectedDeploymentID string) error {
	lease, err := controller.admission.Begin("service_restart", serviceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	if !desired.Enabled || desired.ActiveDeploymentID == "" || desired.ActiveDeploymentID != expectedDeploymentID {
		return state.ErrDeploymentNotFound
	}
	if active, exists := controller.activeContainer(serviceID); exists {
		if active.deploymentID != expectedDeploymentID {
			return state.ErrServiceChanged
		}
		if err := controller.publisher.Withdraw(desired); err != nil {
			return err
		}
		if err := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds); err != nil {
			return errors.Join(err, controller.publisher.Publish(desired, active.container))
		}
		if err := controller.engine.RemoveContainer(ctx, active.container.ID, true); err != nil {
			return err
		}
		controller.clearActive(serviceID)
	}
	restored, err := controller.restoreCurrentLocked(ctx, serviceID, expectedDeploymentID)
	if err != nil {
		return err
	}
	if !restored {
		return errors.New("active service deployment was not restarted")
	}
	return nil
}

func (controller *Controller) DeleteDeploymentLogs(serviceID, deploymentID string) error {
	if serviceID == "" || deploymentID == "" || filepath.Base(serviceID) != serviceID || filepath.Base(deploymentID) != deploymentID {
		return errors.New("service deployment log identity is invalid")
	}
	return os.RemoveAll(filepath.Join(controller.logRoot, "services", serviceID, deploymentID))
}

// DeleteService removes the live container. State deletion is committed by the
// caller only after this runtime cutover succeeds, so a deleted service can
// never keep serving traffic.
func (controller *Controller) DeleteService(ctx context.Context, desired state.ServiceDesired) error {
	lease, err := controller.admission.Begin("service_delete", desired.ID)
	if err != nil {
		return err
	}
	defer lease.Release()
	lock := controller.serviceLock(desired.ID)
	lock.Lock()
	defer lock.Unlock()
	return controller.stopDisabled(ctx, desired)
}

// DeleteServiceDuringProjectDeletion skips the per-service admission lease
// because the project DELETE request already owns the platform-wide exclusive
// mutation lease. Taking a nested lease would reject its own cleanup.
func (controller *Controller) DeleteServiceDuringProjectDeletion(ctx context.Context, desired state.ServiceDesired) error {
	lock := controller.serviceLock(desired.ID)
	lock.Lock()
	defer lock.Unlock()
	return controller.stopDisabled(ctx, desired)
}

func (controller *Controller) DeleteServiceLogs(serviceID string) error {
	if serviceID == "" || filepath.Base(serviceID) != serviceID {
		return errors.New("service log identity is invalid")
	}
	return os.RemoveAll(filepath.Join(controller.logRoot, "services", serviceID))
}

// QuiesceAll stops active service containers without deleting their libpod
// records. The returned closure recreates the exact active pointers if update
// cutover aborts after this point.
func (controller *Controller) QuiesceAll(ctx context.Context) (func(context.Context) error, error) {
	controller.mu.Lock()
	serviceIDs := make([]string, 0, len(controller.active))
	for serviceID := range controller.active {
		serviceIDs = append(serviceIDs, serviceID)
	}
	controller.mu.Unlock()
	sort.Strings(serviceIDs)
	stopped := make([]quiescedService, 0, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		if err := ctx.Err(); err != nil {
			return controller.resumeServices(stopped), err
		}
		quiesced, err := controller.quiesceService(ctx, serviceID)
		if quiesced != nil {
			stopped = append(stopped, *quiesced)
		}
		if err != nil {
			return controller.resumeServices(stopped), err
		}
	}
	return controller.resumeServices(stopped), nil
}

// WithServiceQuiesced is used for destructive filesystem replacement. Backup
// reads never call it; only restore briefly stops the container so it cannot
// keep file descriptors into the directory being atomically replaced.
func (controller *Controller) WithServiceQuiesced(ctx context.Context, serviceID string, action func() error) error {
	if serviceID == "" || action == nil {
		return errors.New("service quiesce request is incomplete")
	}
	quiesced, err := controller.quiesceService(ctx, serviceID)
	if err != nil {
		return err
	}
	actionErr := action()
	if quiesced == nil {
		return actionErr
	}
	return errors.Join(actionErr, controller.resumeService(ctx, *quiesced))
}

func (controller *Controller) quiesceService(ctx context.Context, serviceID string) (*quiescedService, error) {
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	active, ok := controller.activeContainer(serviceID)
	if !ok {
		return nil, nil
	}
	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if err := controller.publisher.Withdraw(desired); err != nil {
		return nil, fmt.Errorf("withdraw service %s before update: %w", serviceID, err)
	}
	if err := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds); err != nil {
		return nil, errors.Join(
			fmt.Errorf("stop service %s before update: %w", serviceID, err),
			controller.publisher.Publish(desired, active.container),
		)
	}
	controller.clearActive(serviceID)
	return &quiescedService{desired: desired, active: active}, nil
}

func (controller *Controller) resumeServices(services []quiescedService) func(context.Context) error {
	return func(ctx context.Context) error {
		var failures []error
		for _, service := range services {
			if err := controller.resumeService(ctx, service); err != nil {
				failures = append(failures, fmt.Errorf("resume service %s: %w", service.desired.ID, err))
			}
		}
		return errors.Join(failures...)
	}
}

func (controller *Controller) resumeService(ctx context.Context, service quiescedService) error {
	lock := controller.serviceLock(service.desired.ID)
	lock.Lock()
	defer lock.Unlock()
	if _, active := controller.activeContainer(service.desired.ID); active {
		return nil
	}
	if err := controller.engine.StartContainer(ctx, service.active.container.ID); err != nil {
		return err
	}
	ready, err := controller.waitReady(ctx, service.desired, service.active.container.ID, service.active.networkName)
	if err != nil {
		return err
	}
	service.active.container = ready
	controller.setActive(service.desired.ID, service.active)
	return controller.publisher.Publish(service.desired, ready)
}

func (controller *Controller) restoreCurrentLocked(ctx context.Context, serviceID, expectedDeploymentID string) (bool, error) {
	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return false, err
	}
	if !desired.Enabled || desired.ActiveDeploymentID == "" {
		return false, nil
	}
	if expectedDeploymentID != "" && desired.ActiveDeploymentID != expectedDeploymentID {
		return false, nil
	}
	if _, exists := controller.activeContainer(serviceID); exists {
		return false, nil
	}
	activeDeployment, err := controller.store.Deployment(ctx, desired.ActiveDeploymentID)
	if err != nil {
		return false, fmt.Errorf("load active deployment: %w", err)
	}
	desired.Snapshot = activeDeployment.Snapshot
	image, inspectErr := controller.engine.InspectImage(ctx, activeDeployment.ImageDigest)
	if inspectErr != nil {
		if err := controller.growth.PermitGrowth(ctx); err != nil {
			return false, fmt.Errorf("active service image is not cached: %w", err)
		}
		if desired.Snapshot.Source.Type == servicesource.GitHubImage {
			image, err = controller.rebuildGitHubDeployment(ctx, desired, activeDeployment)
		} else {
			image, err = controller.pullActiveImage(ctx, desired, activeDeployment)
		}
		if err != nil {
			return false, err
		}
	}
	if image.Digest != activeDeployment.ImageDigest {
		return false, fmt.Errorf("active image digest = %s, want %s", image.Digest, activeDeployment.ImageDigest)
	}
	container, placement, err := controller.createRuntimeContainer(ctx, desired, activeDeployment.ID, image.ID)
	if err != nil {
		return false, err
	}
	remove := true
	defer func() {
		if remove {
			_ = controller.engine.RemoveContainer(context.Background(), container.ID, true)
		}
	}()
	if err := controller.engine.StartContainer(ctx, container.ID); err != nil {
		return false, fmt.Errorf("start active service container: %w", err)
	}
	if err := controller.recordVolumeInitializations(ctx, desired); err != nil {
		return false, err
	}
	ready, err := controller.waitReady(ctx, desired, container.ID, placement.NetworkName)
	if err != nil {
		return false, fmt.Errorf("restore active service readiness: %w", err)
	}
	controller.setActive(desired.ID, activeContainer{
		deploymentID: activeDeployment.ID, container: ready,
		networkName: placement.NetworkName,
	})
	remove = false
	if err := controller.publisher.Publish(desired, ready); err != nil {
		return false, fmt.Errorf("publish restored service: %w", err)
	}
	return true, nil
}

func (controller *Controller) rebuildGitHubDeployment(
	ctx context.Context,
	desired state.ServiceDesired,
	active state.DeploymentRecord,
) (containerengine.Image, error) {
	if controller.sources == nil || desired.Snapshot.Source.GitHub == nil || active.SourceRevision == "" {
		return containerengine.Image{}, errors.New("active GitHub deployment cannot be rebuilt from its exact revision")
	}
	github := *desired.Snapshot.Source.GitHub
	github.Revision = active.SourceRevision
	github.WaitForCI = false
	desired.Snapshot.Source.GitHub = &github
	logFile, err := openBuildLog(controller.buildLogPath(desired.ID, active.ID))
	if err != nil {
		return containerengine.Image{}, err
	}
	_, _ = io.WriteString(logFile, "\nRebuilding the active GitHub revision because its local image is missing\n")
	resolution, resolveErr := controller.sources.Resolve(
		ctx, desired, active.ID, active.SourceRevision, logFile, true, nil,
	)
	closeErr := logFile.Close()
	if err := errors.Join(resolveErr, closeErr); err != nil {
		return containerengine.Image{}, fmt.Errorf("rebuild active GitHub image: %w", err)
	}
	return resolution.Image, nil
}

func (controller *Controller) pullActiveImage(
	ctx context.Context,
	desired state.ServiceDesired,
	active state.DeploymentRecord,
) (containerengine.Image, error) {
	pinnedReference, err := serviceconfig.PinnedReference(active.ImageReference, active.ImageDigest)
	if err != nil {
		return containerengine.Image{}, err
	}
	credential := ImageCredential{}
	if desired.Snapshot.Source.Type == servicesource.PrivateImage {
		if controller.credentials == nil {
			return containerengine.Image{}, errors.New("image credential resolution is not configured")
		}
		credential, err = controller.credentials.Resolve(ctx, desired)
		if err != nil {
			return containerengine.Image{}, fmt.Errorf("resolve active image credential: %w", err)
		}
	}
	image, err := controller.pull(ctx, containerengine.PullRequest{
		Reference: pinnedReference, Username: credential.Username, Password: credential.Password,
	})
	if err != nil {
		return containerengine.Image{}, fmt.Errorf("pull active service image: %w", err)
	}
	return image, nil
}

func (controller *Controller) pull(ctx context.Context, request containerengine.PullRequest) (containerengine.Image, error) {
	if controller.imageSources == nil {
		return controller.engine.Pull(ctx, request)
	}
	reference, closeSource, handled, err := controller.imageSources.Resolve(ctx, request.Reference)
	if err != nil {
		return containerengine.Image{}, err
	}
	if !handled {
		return controller.engine.Pull(ctx, request)
	}
	if closeSource == nil || reference == "" {
		return containerengine.Image{}, errors.New("resolved image source is incomplete")
	}
	if request.Username != "" || request.Password != "" {
		closeSource()
		return containerengine.Image{}, errors.New("embedded registry image cannot use a remote image credential")
	}
	defer closeSource()
	request.Reference = reference
	request.Refresh = false
	return controller.engine.Pull(ctx, request)
}

// PrepareUnexpectedExit removes publication and the exited runtime attempt.
// Product state and the logical active deployment pointer remain unchanged.
func (controller *Controller) PrepareUnexpectedExit(ctx context.Context, serviceID, deploymentID, containerID string) (bool, error) {
	lock := controller.serviceLock(serviceID)
	lock.Lock()
	defer lock.Unlock()
	desired, err := controller.store.DesiredService(ctx, serviceID)
	if err != nil {
		return false, err
	}
	active, exists := controller.activeContainer(serviceID)
	if !desired.Enabled || desired.ActiveDeploymentID != deploymentID || !exists || active.deploymentID != deploymentID || active.container.ID != containerID {
		return false, nil
	}
	withdrawErr := controller.publisher.Withdraw(desired)
	controller.clearActive(serviceID)
	removeErr := controller.engine.RemoveContainer(ctx, containerID, true)
	return true, errors.Join(withdrawErr, removeErr)
}

func (controller *Controller) Status(serviceID string) (RuntimeStatus, bool, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok {
		return RuntimeStatus{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return RuntimeStatus{DeploymentID: active.deploymentID}, true, err
	}
	return RuntimeStatus{
		DeploymentID: active.deploymentID,
		State:        container.State,
		ExitCode:     container.ExitCode,
	}, true, nil
}

func (controller *Controller) Container(serviceID string) (containerengine.Container, bool, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok {
		return containerengine.Container{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return containerengine.Container{}, true, err
	}
	if container.State != "running" {
		return containerengine.Container{}, false, nil
	}
	return container, true, nil
}

func (controller *Controller) Backend(serviceID string, targetPort int) (Backend, bool, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok || targetPort < 1 || targetPort > 65535 {
		return Backend{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return Backend{}, true, err
	}
	if container.State != "running" {
		return Backend{}, false, nil
	}
	addresses := container.IPs[active.networkName]
	if len(addresses) != 1 {
		return Backend{}, true, fmt.Errorf("service container has %d backend addresses, want one", len(addresses))
	}
	return Backend{
		DeploymentID: active.deploymentID, Address: addresses[0], Port: targetPort,
	}, true, nil
}

func (controller *Controller) TerminalTarget(serviceID string) (TerminalTarget, bool, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok {
		return TerminalTarget{}, false, nil
	}
	container, err := controller.engine.InspectContainer(active.container.ID)
	if err != nil {
		return TerminalTarget{DeploymentID: active.deploymentID, ContainerID: active.container.ID}, true, err
	}
	if container.State != "running" {
		return TerminalTarget{}, false, nil
	}
	return TerminalTarget{DeploymentID: active.deploymentID, ContainerID: container.ID}, true, nil
}

func (controller *Controller) ExecTerminal(ctx context.Context, serviceID, expectedContainerID string, request containerengine.TerminalExecRequest) (int, error) {
	active, ok := controller.activeContainer(serviceID)
	if !ok || active.container.ID != expectedContainerID {
		return -1, errors.New("service terminal target is no longer active")
	}
	return controller.engine.ExecTerminalContainer(ctx, expectedContainerID, request)
}

func (controller *Controller) ProbeTerminalShell(ctx context.Context, serviceID, expectedContainerID, shell string) bool {
	active, ok := controller.activeContainer(serviceID)
	if !ok || active.container.ID != expectedContainerID {
		return false
	}
	exitCode, err := controller.engine.ExecContainer(ctx, expectedContainerID, containerengine.ExecRequest{
		Command: []string{shell, "-c", "exit 0"},
	})
	return err == nil && exitCode == 0
}

func (controller *Controller) runDeployment(ctx context.Context, desired state.ServiceDesired, deploymentID, imageID string) error {
	candidate, placement, err := controller.createRuntimeContainer(ctx, desired, deploymentID, imageID)
	if err != nil {
		return controller.fail(deploymentID, "candidate_create_failed", err)
	}
	candidateActive := true
	defer func() {
		if candidateActive {
			_ = controller.engine.RemoveContainer(context.Background(), candidate.ID, true)
		}
	}()

	old, hasOld := controller.activeContainer(desired.ID)
	if hasOld && old.deploymentID != desired.ActiveDeploymentID {
		return controller.fail(deploymentID, "active_runtime_missing", errors.New("active deployment has no matching runtime container"))
	}
	if hasOld {
		if err := controller.publisher.Withdraw(desired); err != nil {
			controller.restoreOld(desired, old, true)
			return controller.fail(deploymentID, "publication_withdraw_failed", err)
		}
		if err := controller.engine.StopContainer(old.container.ID, stopTimeoutSeconds); err != nil {
			controller.restoreOld(desired, old, true)
			return controller.fail(deploymentID, "old_stop_failed", err)
		}
	}
	if err := controller.engine.StartContainer(ctx, candidate.ID); err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "candidate_start_failed", err)
	}
	if err := controller.recordVolumeInitializations(ctx, desired); err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "volume_initialization_commit_failed", err)
	}
	ready, err := controller.waitReady(ctx, desired, candidate.ID, placement.NetworkName)
	if err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "readiness_failed", err)
	}
	if err := controller.store.ActivateDeployment(ctx, desired.ID, deploymentID, desired.ActiveDeploymentID, controller.now().UnixMilli()); err != nil {
		controller.restoreOld(desired, old, hasOld)
		return controller.fail(deploymentID, "publication_commit_failed", err)
	}
	desired.ActiveDeploymentID = deploymentID
	controller.setActive(desired.ID, activeContainer{
		deploymentID: deploymentID, container: ready,
		networkName: placement.NetworkName,
	})
	candidateActive = false
	if err := controller.publisher.Publish(desired, ready); err != nil {
		return fmt.Errorf("publish ready service after deployment commit: %w", err)
	}
	if hasOld {
		_ = controller.engine.RemoveContainer(context.Background(), old.container.ID, true)
	}
	return nil
}

func (controller *Controller) createRuntimeContainer(ctx context.Context, desired state.ServiceDesired, deploymentID, imageID string) (containerengine.Container, Placement, error) {
	placement, err := controller.placement(desired)
	if err != nil {
		return containerengine.Container{}, Placement{}, fmt.Errorf("place service runtime: %w", err)
	}
	attemptID, err := controller.newID(controller.now())
	if err != nil {
		return containerengine.Container{}, Placement{}, fmt.Errorf("allocate runtime attempt ID: %w", err)
	}
	logPath := filepath.Join(controller.logRoot, "services", desired.ID, deploymentID, attemptID+".log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return containerengine.Container{}, Placement{}, fmt.Errorf("create service log directory: %w", err)
	}
	volumes := make([]containerengine.ManagedVolumeMount, 0, len(desired.Snapshot.VolumeMounts))
	for _, mount := range desired.Snapshot.VolumeMounts {
		volumePath := filepath.Join(controller.volumeRoot, desired.ProjectID, mount.VolumeID)
		initialized, err := controller.store.VolumeInitialized(ctx, desired.ProjectID, desired.ID, mount.VolumeID)
		if err != nil {
			return containerengine.Container{}, Placement{}, fmt.Errorf("inspect volume initialization: %w", err)
		}
		volumes = append(volumes, containerengine.ManagedVolumeMount{
			ID:          mount.VolumeID,
			Source:      volumePath,
			Destination: mount.ContainerPath,
			Initialized: initialized,
		})
	}
	environment := desired.Snapshot.Environment
	if controller.environment != nil {
		resolved, resolveErr := controller.environment.Resolve(ctx, desired, deploymentID)
		if resolveErr != nil {
			return containerengine.Container{}, Placement{}, fmt.Errorf("resolve service variables: %w", resolveErr)
		}
		environment = resolved
	}
	container, err := controller.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-service-" + deploymentID,
		Entrypoint: desired.Snapshot.Command, Command: desired.Snapshot.Args,
		Environment: environment,
		Labels: map[string]string{
			"io.platformd.owner": "service", "io.platformd.project-id": desired.ProjectID,
			"io.platformd.service-id": desired.ID, "io.platformd.deployment-id": deploymentID,
		},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()},
		DNSSearch: []string{placement.DNSSearch}, ManagedVolumes: volumes,
		LogPath: logPath, LogSizeBytes: controller.logSizeBytes, LogMaxFiles: controller.logMaxFiles,
		CgroupParent:  placement.CgroupParent,
		CPUMillicores: desired.Snapshot.CPUMillicores, MemoryMaxBytes: desired.Snapshot.MemoryMaxBytes,
	})
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	return container, placement, nil
}

func (controller *Controller) recordVolumeInitializations(ctx context.Context, desired state.ServiceDesired) error {
	for _, mount := range desired.Snapshot.VolumeMounts {
		if err := controller.store.RecordVolumeInitialization(
			ctx, desired.ProjectID, desired.ID, mount.VolumeID, controller.now().UnixMilli(),
		); err != nil {
			return fmt.Errorf("record volume %s initialization: %w", mount.VolumeID, err)
		}
	}
	return nil
}

func (controller *Controller) waitReady(ctx context.Context, desired state.ServiceDesired, containerID, networkName string) (containerengine.Container, error) {
	healthCheck := desired.Snapshot.HealthCheck
	timeout := time.Duration(serviceconfig.DefaultHealthTimeoutSeconds) * time.Second
	if healthCheck != nil {
		timeout = time.Duration(healthCheck.TimeoutSeconds) * time.Second
	}
	deadline := controller.now().Add(timeout)
	if healthCheck == nil {
		deadline = controller.now().Add(processStartupGrace)
	}
	ticker := time.NewTicker(probeInterval)
	defer ticker.Stop()
	for {
		container, err := controller.engine.InspectContainer(containerID)
		if err != nil {
			return containerengine.Container{}, err
		}
		if container.State != "running" {
			return containerengine.Container{}, fmt.Errorf("container state is %s", container.State)
		}
		if healthCheck == nil {
			if !controller.now().Before(deadline) {
				return container, nil
			}
		} else {
			addresses := container.IPs[networkName]
			if len(addresses) == 1 {
				ready, probeErr := controller.probeHTTP(ctx, addresses[0], healthCheck.Port, healthCheck.Path)
				if ready {
					return container, nil
				}
				if probeErr != nil && !controller.now().Before(deadline) {
					return containerengine.Container{}, probeErr
				}
			}
			if !controller.now().Before(deadline) {
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

func (controller *Controller) probeHTTP(ctx context.Context, address string, port int, healthPath string) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(address, fmt.Sprintf("%d", port))+healthPath, nil)
	if err != nil {
		return false, err
	}
	response, err := controller.httpClient.Do(request)
	if err != nil {
		return false, err
	}
	response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 400, nil
}

func (controller *Controller) fail(deploymentID, code string, cause error) error {
	message := cause.Error()
	if err := controller.store.FailDeployment(context.Background(), deploymentID, code, message, controller.now().UnixMilli()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (controller *Controller) stopDisabled(ctx context.Context, desired state.ServiceDesired) error {
	active, ok := controller.activeContainer(desired.ID)
	if !ok {
		return nil
	}
	if err := controller.publisher.Withdraw(desired); err != nil {
		return err
	}
	if err := controller.engine.StopContainer(active.container.ID, stopTimeoutSeconds); err != nil {
		return err
	}
	if err := controller.engine.RemoveContainer(ctx, active.container.ID, true); err != nil {
		return err
	}
	controller.clearActive(desired.ID)
	return nil
}

func (controller *Controller) restoreOld(desired state.ServiceDesired, old activeContainer, exists bool) {
	if !exists {
		return
	}
	if err := controller.engine.StartContainer(context.Background(), old.container.ID); err != nil {
		return
	}
	container, err := controller.engine.InspectContainer(old.container.ID)
	if err != nil {
		return
	}
	_ = controller.publisher.Publish(desired, container)
}

func (controller *Controller) serviceLock(serviceID string) *sync.Mutex {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	lock := controller.locks[serviceID]
	if lock == nil {
		lock = &sync.Mutex{}
		controller.locks[serviceID] = lock
	}
	return lock
}

func (controller *Controller) activeContainer(serviceID string) (activeContainer, bool) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	active, ok := controller.active[serviceID]
	return active, ok
}

func (controller *Controller) setActive(serviceID string, active activeContainer) {
	controller.mu.Lock()
	controller.active[serviceID] = active
	controller.mu.Unlock()
}

func (controller *Controller) clearActive(serviceID string) {
	controller.mu.Lock()
	delete(controller.active, serviceID)
	controller.mu.Unlock()
}

func safeRoot(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != string(filepath.Separator)
}
