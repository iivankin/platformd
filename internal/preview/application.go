package preview

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/containerengine"
	"github.com/iivankin/platformd/internal/containerlogs"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/servicesource"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/systemevent"
)

const (
	Retention           = 14 * 24 * time.Hour
	cleanupInterval     = time.Hour
	processStartupGrace = 3 * time.Second
	probeInterval       = 250 * time.Millisecond
	probeTimeout        = 2 * time.Second
	stopTimeoutSeconds  = 10
	// HostnamePrefix is the stable DNS label prefix for every preview hostname
	// (preview-<hash>.<root>), so external systems can match the whole class.
	HostnamePrefix = "preview-"
)

type Store interface {
	DesiredService(context.Context, string) (state.ServiceDesired, error)
	ServiceDomains(context.Context, string, string) ([]state.ServiceDomain, error)
	BeginPreviewDeployment(context.Context, state.BeginPreviewDeployment) error
	SetPreviewDNSRecords(context.Context, string, []string) error
	ActivatePreviewDeployment(context.Context, string, string, []string, int64) error
	FinishPreviewDeployment(context.Context, string, string, string, string, int64) error
	StopPreviewDeployment(context.Context, string, int64) error
	ActivePreviewDeployment(context.Context, string, string) (state.PreviewDeployment, error)
	ActivePreviewDeployments(context.Context) ([]state.PreviewDeployment, error)
	ExpiredActivePreviewDeployments(context.Context, int64) ([]state.PreviewDeployment, error)
	FinishedPreviewDeploymentsWithDNS(context.Context) ([]state.PreviewDeployment, error)
	ClearPreviewDNSRecords(context.Context, string) error
	DeleteFinishedPreviewDeployments(context.Context, int64) ([]state.PreviewDeployment, error)
}

type Engine interface {
	Pull(context.Context, containerengine.PullRequest) (containerengine.Image, error)
	InspectImage(context.Context, string) (containerengine.Image, error)
	CreateContainer(context.Context, containerengine.ContainerSpec) (containerengine.Container, error)
	StartContainerAttached(context.Context, string, io.WriteCloser, io.WriteCloser) (<-chan error, error)
	StopContainer(string, uint) error
	RemoveContainer(context.Context, string, bool) error
	InspectContainer(string) (containerengine.Container, error)
}

type EnvironmentResolver interface {
	Resolve(context.Context, state.ServiceDesired, deployment.EnvironmentContext) (map[string]string, error)
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
	DNS               DNS
	Growth            deployment.GrowthGate
	Admission         *admission.Gate
	Placement         func(state.ServiceDesired) (Placement, error)
	RoutesChanged     func(context.Context) error
	CertificateCovers func(string) bool
	ContainerLogs     containerlogs.Sink
	Now               func() time.Time
}

type activeContainer struct {
	container   containerengine.Container
	networkName string
}

type Application struct {
	store             Store
	engine            Engine
	environment       EnvironmentResolver
	dns               DNS
	growth            deployment.GrowthGate
	admission         *admission.Gate
	placement         func(state.ServiceDesired) (Placement, error)
	routesChanged     func(context.Context) error
	certificateCovers func(string) bool
	containerLogs     containerlogs.Sink
	now               func() time.Time
	httpClient        *http.Client

	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	active map[string]activeContainer
}

func New(config Config) (*Application, error) {
	if config.Store == nil || config.Engine == nil || config.Environment == nil || config.DNS == nil ||
		config.Growth == nil || config.Admission == nil || config.Placement == nil || config.RoutesChanged == nil ||
		config.CertificateCovers == nil || config.ContainerLogs == nil {
		return nil, errors.New("image preview dependencies are incomplete")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Application{
		store: config.Store, engine: config.Engine, environment: config.Environment, dns: config.DNS,
		growth: config.Growth, admission: config.Admission, placement: config.Placement,
		routesChanged: config.RoutesChanged, certificateCovers: config.CertificateCovers,
		containerLogs: config.ContainerLogs, now: now,
		httpClient: &http.Client{
			Timeout:       probeTimeout,
			Transport:     &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: (&net.Dialer{Timeout: probeTimeout}).DialContext},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		locks: make(map[string]*sync.Mutex), active: make(map[string]activeContainer),
	}, nil
}

func (application *Application) DeployUploaded(
	ctx context.Context,
	serviceID, previewID, tag, revisionID, imageReference string,
	image containerengine.Image,
	identity state.ImageUploadIdentity,
) (string, error) {
	if serviceID == "" || previewID == "" || tag == "" || tag == "latest" || revisionID == "" ||
		imageReference == "" || image.ID == "" || image.Digest == "" {
		return "", errors.New("uploaded preview input is incomplete")
	}
	lease, err := application.admission.Begin("service_image_preview", serviceID)
	if err != nil {
		return "", err
	}
	defer lease.Release()
	lock := application.previewLock(serviceID, tag)
	lock.Lock()
	defer lock.Unlock()

	plan, err := application.desiredPreview(ctx, serviceID)
	if err != nil {
		return "", err
	}
	hostname := previewHostname(serviceID, tag, plan.root)
	if !application.certificateCovers(hostname) {
		return "", fmt.Errorf("origin certificate does not cover preview hostname %s", hostname)
	}
	if err := application.growth.PermitGrowth(ctx); err != nil {
		return "", err
	}
	normalized, snapshotJSON, configHash, err := serviceconfig.Canonical(plan.desired.Snapshot)
	if err != nil {
		return "", err
	}
	plan.desired.Snapshot = normalized
	current, currentErr := application.store.ActivePreviewDeployment(ctx, serviceID, tag)
	if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
		return "", currentErr
	}
	now := application.now()
	if err := application.store.BeginPreviewDeployment(ctx, state.BeginPreviewDeployment{
		ID: previewID, ServiceID: serviceID, Tag: tag, ImageRevisionID: revisionID,
		Hostname: hostname, TargetPort: plan.targetPort, ImageDigest: image.Digest,
		ImageReference: imageReference, ConfigHash: configHash, SnapshotJSON: snapshotJSON,
		CreatedAtMillis: now.UnixMilli(), ExpiresAtMillis: now.Add(Retention).UnixMilli(),
	}); err != nil {
		return "", err
	}
	environmentContext := deployment.EnvironmentContext{
		DeploymentID: previewID, Kind: deployment.EnvironmentPreview,
		PreviewURL: "https://" + hostname, SourceRevision: identity.SHA,
	}
	candidate, placement, err := application.createContainer(ctx, plan.desired, environmentContext, image.ID)
	if err != nil {
		return "", application.fail(ctx, previewID, "candidate_create_failed", err)
	}
	owned := true
	defer func() {
		if owned {
			_ = application.engine.RemoveContainer(context.Background(), candidate.ID, true)
		}
	}()
	if err := application.startContainer(ctx, plan.desired, previewID, candidate.ID); err != nil {
		return "", application.fail(ctx, previewID, "candidate_start_failed", err)
	}
	ready, err := application.waitReady(ctx, plan.desired, candidate.ID, placement.NetworkName)
	if err != nil {
		return "", application.fail(ctx, previewID, "readiness_failed", err)
	}
	recordIDs, err := application.dns.EnsurePreviewHostname(ctx, plan.canonicalHostname, hostname, previewID)
	if err != nil {
		return "", application.fail(ctx, previewID, "cloudflare_dns_failed", err)
	}
	if err := application.store.SetPreviewDNSRecords(ctx, previewID, recordIDs); err != nil {
		_ = application.dns.DeletePreviewHostname(context.Background(), hostname, recordIDs)
		return "", application.fail(ctx, previewID, "state_update_failed", err)
	}
	application.setActive(previewID, activeContainer{container: ready, networkName: placement.NetworkName})
	if err := application.store.ActivatePreviewDeployment(ctx, previewID, current.ID, recordIDs, application.now().UnixMilli()); err != nil {
		application.clearActive(previewID)
		_ = application.dns.DeletePreviewHostname(context.Background(), hostname, recordIDs)
		return "", application.fail(ctx, previewID, "publication_commit_failed", err)
	}
	if err := application.routesChanged(ctx); err != nil {
		_ = application.stop(ctx, state.PreviewDeployment{ID: previewID, ServiceID: serviceID, Tag: tag, Hostname: hostname, CloudflareRecordIDs: recordIDs})
		return "", err
	}
	owned = false
	if current.ID != "" {
		application.removeRuntime(current.ID)
		_ = application.deleteDNS(context.Background(), current)
	}
	return "https://" + hostname, nil
}

func (application *Application) StopService(ctx context.Context, serviceID, _ string) error {
	previews, err := application.store.ActivePreviewDeployments(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range previews {
		if item.ServiceID != serviceID {
			continue
		}
		lock := application.previewLock(item.ServiceID, item.Tag)
		lock.Lock()
		failures = append(failures, application.stop(ctx, item))
		lock.Unlock()
	}
	return errors.Join(failures...)
}

func (application *Application) StopAll(ctx context.Context) error {
	previews, err := application.store.ActivePreviewDeployments(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, item := range previews {
		lock := application.previewLock(item.ServiceID, item.Tag)
		lock.Lock()
		failures = append(failures, application.stop(ctx, item))
		lock.Unlock()
	}
	return errors.Join(failures...)
}

func (application *Application) Restore(ctx context.Context) error {
	items, err := application.store.ActivePreviewDeployments(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ExpiresAtMillis <= application.now().UnixMilli() {
			_ = application.stop(ctx, item)
			continue
		}
		desired, err := application.store.DesiredService(ctx, item.ServiceID)
		if err != nil {
			return err
		}
		desired.Snapshot = item.Snapshot
		image, err := application.engine.InspectImage(ctx, item.ImageDigest)
		if err != nil {
			image, err = application.engine.Pull(ctx, containerengine.PullRequest{Reference: item.ImageReference})
		}
		if err != nil || image.Digest != item.ImageDigest {
			return fmt.Errorf("restore preview %s image: %w", item.ID, err)
		}
		container, placement, err := application.createContainer(ctx, desired, deployment.EnvironmentContext{
			DeploymentID: item.ID, Kind: deployment.EnvironmentPreview, PreviewURL: "https://" + item.Hostname,
		}, image.ID)
		if err != nil {
			return err
		}
		if err := application.startContainer(ctx, desired, item.ID, container.ID); err != nil {
			_ = application.engine.RemoveContainer(context.Background(), container.ID, true)
			return err
		}
		ready, err := application.waitReady(ctx, desired, container.ID, placement.NetworkName)
		if err != nil {
			_ = application.engine.RemoveContainer(context.Background(), container.ID, true)
			return err
		}
		application.reconcileDNS(ctx, desired, item)
		application.setActive(item.ID, activeContainer{container: ready, networkName: placement.NetworkName})
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
	if expired, err := application.store.ExpiredActivePreviewDeployments(ctx, now.UnixMilli()); err == nil {
		for _, item := range expired {
			_ = application.stop(ctx, item)
		}
	}
	if items, err := application.store.FinishedPreviewDeploymentsWithDNS(ctx); err == nil {
		for _, item := range items {
			_ = application.deleteDNS(ctx, item)
		}
	}
	_, _ = application.store.DeleteFinishedPreviewDeployments(ctx, now.Add(-Retention).UnixMilli())
}

func (application *Application) desiredPreview(ctx context.Context, serviceID string) (previewPlan, error) {
	desired, err := application.store.DesiredService(ctx, serviceID)
	if err != nil {
		return previewPlan{}, err
	}
	if !desired.Enabled || !servicesource.ImageUploadPreviewsEnabled(desired.Snapshot.Source) {
		return previewPlan{}, errors.New("service is not configured for uploaded image previews")
	}
	root := servicesource.ImageUploadPreviewDomain(desired.Snapshot.Source)
	if root == "" {
		return previewPlan{}, state.ErrPreviewDomain
	}
	domains, err := application.store.ServiceDomains(ctx, desired.ProjectID, serviceID)
	if err != nil {
		return previewPlan{}, err
	}
	targetPort, err := previewTargetPort(desired, domains)
	if err != nil {
		return previewPlan{}, err
	}
	return previewPlan{
		desired:           desired,
		root:              root,
		targetPort:        targetPort,
		canonicalHostname: previewDNSCanonical(root, domains),
	}, nil
}

func (application *Application) createContainer(ctx context.Context, desired state.ServiceDesired, environmentContext deployment.EnvironmentContext, imageID string) (containerengine.Container, Placement, error) {
	placement, err := application.placement(desired)
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	environment, err := application.environment.Resolve(ctx, desired, environmentContext)
	if err != nil {
		return containerengine.Container{}, Placement{}, err
	}
	container, err := application.engine.CreateContainer(ctx, containerengine.ContainerSpec{
		ImageID: imageID, Name: "platformd-preview-" + environmentContext.DeploymentID,
		Entrypoint: desired.Snapshot.Command, Command: desired.Snapshot.Args, Environment: environment,
		Labels:  map[string]string{"io.platformd.owner": "preview", "io.platformd.project-id": desired.ProjectID, "io.platformd.service-id": desired.ID, "io.platformd.preview-id": environmentContext.DeploymentID},
		Network: placement.NetworkName, DNSServers: []string{placement.Gateway.String()}, DNSSearch: []string{placement.DNSSearch},
		LogDriver:    containerengine.ContainerLogNone,
		CgroupParent: placement.CgroupParent, CPUMillicores: desired.Snapshot.CPUMillicores, MemoryMaxBytes: desired.Snapshot.MemoryMaxBytes,
	})
	return container, placement, err
}

func (application *Application) startContainer(
	ctx context.Context,
	desired state.ServiceDesired,
	previewID string,
	containerID string,
) error {
	attached, err := containerlogs.StartAttached(ctx, application.engine, application.containerLogs, containerlogs.RuntimeMetadata{
		ResourceID: desired.ID, ResourceName: desired.Name,
		DeploymentID: previewID, ContainerID: containerID,
	})
	if err != nil {
		return err
	}
	go func() {
		if attachErr, ok := <-attached; ok && attachErr != nil {
			systemevent.Failure(
				"preview_log_attach_failed", attachErr,
				systemevent.String("service_id", desired.ID),
				systemevent.String("preview_id", previewID),
			)
		}
	}()
	return nil
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

func (application *Application) fail(ctx context.Context, previewID, code string, cause error) error {
	_ = application.store.FinishPreviewDeployment(ctx, previewID, "failed", code, cause.Error(), application.now().UnixMilli())
	return cause
}

func (application *Application) stop(ctx context.Context, item state.PreviewDeployment) error {
	stateErr := application.store.StopPreviewDeployment(ctx, item.ID, application.now().UnixMilli())
	routeErr := application.routesChanged(ctx)
	application.removeRuntime(item.ID)
	return errors.Join(stateErr, routeErr, application.deleteDNS(context.Background(), item))
}

func (application *Application) deleteDNS(ctx context.Context, item state.PreviewDeployment) error {
	if len(item.CloudflareRecordIDs) == 0 {
		return nil
	}
	if err := application.dns.DeletePreviewHostname(ctx, item.Hostname, item.CloudflareRecordIDs); err != nil {
		return err
	}
	return application.store.ClearPreviewDNSRecords(ctx, item.ID)
}

func (application *Application) reconcileDNS(ctx context.Context, desired state.ServiceDesired, item state.PreviewDeployment) {
	domains, err := application.store.ServiceDomains(ctx, desired.ProjectID, desired.ID)
	var records []string
	if err == nil {
		// Clone from a hostname in the preview's own zone, not the currently
		// configured root — active previews keep the hostname they were published with.
		canonical := previewDNSCanonicalForHostname(item.Hostname, domains)
		if canonical == "" {
			err = state.ErrPreviewDomain
		} else {
			records, err = application.dns.EnsurePreviewHostname(ctx, canonical, item.Hostname, item.ID)
		}
	}
	if err == nil {
		_ = application.store.SetPreviewDNSRecords(ctx, item.ID, records)
	}
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
		return deployment.Backend{}, true, fmt.Errorf("preview container has %d backend addresses, want one", len(addresses))
	}
	return deployment.Backend{DeploymentID: previewID, Address: addresses[0], Port: targetPort}, true, nil
}

func previewHostname(serviceID, tag, previewRoot string) string {
	digest := sha256.Sum256([]byte(serviceID + "\x00" + tag))
	return HostnamePrefix + hex.EncodeToString(digest[:])[:12] + "." + previewRoot
}

// previewDNSCanonical picks a hostname in the preview zone whose A/AAAA/CNAME
// records Cloudflare can clone onto the preview hostname.
func previewDNSCanonical(previewRoot string, domains []state.ServiceDomain) string {
	var fallback string
	for _, domain := range domains {
		if domain.Hostname == previewRoot {
			return previewRoot
		}
		if strings.HasSuffix(domain.Hostname, "."+previewRoot) && fallback == "" {
			fallback = domain.Hostname
		}
	}
	if fallback != "" {
		return fallback
	}
	return previewRoot
}

// previewDNSCanonicalForHostname picks a clone source in the same zone as an
// existing preview hostname (label.previewRoot).
func previewDNSCanonicalForHostname(previewHostname string, domains []state.ServiceDomain) string {
	_, root, ok := strings.Cut(previewHostname, ".")
	if !ok || root == "" {
		return ""
	}
	return previewDNSCanonical(root, domains)
}

func previewTargetPort(desired state.ServiceDesired, domains []state.ServiceDomain) (int, error) {
	if health := desired.Snapshot.HealthCheck; health != nil && health.Port >= 1 && health.Port <= 65535 {
		return health.Port, nil
	}
	if len(domains) > 0 {
		return domains[0].TargetPort, nil
	}
	return 0, state.ErrPreviewTargetPort
}

type previewPlan struct {
	desired           state.ServiceDesired
	root              string
	targetPort        int
	canonicalHostname string
}

func (application *Application) previewLock(serviceID, tag string) *sync.Mutex {
	application.mu.Lock()
	defer application.mu.Unlock()
	key := serviceID + ":" + tag
	lock := application.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		application.locks[key] = lock
	}
	return lock
}
func (application *Application) setActive(id string, active activeContainer) {
	application.mu.Lock()
	application.active[id] = active
	application.mu.Unlock()
}
func (application *Application) clearActive(id string) {
	application.mu.Lock()
	delete(application.active, id)
	application.mu.Unlock()
}
func (application *Application) removeRuntime(id string) {
	application.mu.Lock()
	active, ok := application.active[id]
	delete(application.active, id)
	application.mu.Unlock()
	if ok {
		_ = application.engine.StopContainer(active.container.ID, stopTimeoutSeconds)
		_ = application.engine.RemoveContainer(context.Background(), active.container.ID, true)
	}
}
