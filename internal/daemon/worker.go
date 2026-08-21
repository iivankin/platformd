package daemon

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/admission"
	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/hostagent"
	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/layout"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/portproxy"
	"github.com/iivankin/platformd/internal/sdnotify"
	"github.com/iivankin/platformd/internal/servicerestart"
	"github.com/iivankin/platformd/internal/singletonlock"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/telemetry"
	"golang.org/x/net/netutil"
)

type permitAllGrowth struct{}

func (permitAllGrowth) PermitGrowth(context.Context) error { return nil }

type workerPurge struct {
	conn *hostagent.Conn
}

func (purge workerPurge) PurgeHostnames(ctx context.Context, hostnames []string) error {
	return purge.conn.Purge(ctx, hostnames)
}

type workerEnvironment struct {
	inner   *hostagent.RemoteEnvironment
	runtime *runtimeStack
}

func (resolver workerEnvironment) Resolve(ctx context.Context, service state.ServiceDesired, environment deployment.EnvironmentContext) (map[string]string, error) {
	values, err := resolver.inner.Resolve(ctx, service, environment)
	if err != nil {
		return nil, err
	}
	placement, err := resolver.runtime.servicePlacement(service)
	if err != nil {
		return values, nil
	}
	if values == nil {
		values = map[string]string{}
	}
	values["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://" + placement.Gateway.String() + ":4318"
	values["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/protobuf"
	return values, nil
}

const workerStatusInterval = 20 * time.Second

type workerControl struct {
	mu          sync.Mutex
	ops         sync.Mutex
	conn        *hostagent.Conn
	store       *hostagent.ArchiveStore
	runtime     *runtimeStack
	router      *ingress.Router
	selector    *origin.Selector
	assigned    map[string]state.ServiceDesired
	forwarder   *hostagent.Forwarder
	proxy       *portproxy.Manager
	listenerIDs map[string]struct{}
	statusMu    sync.Mutex
	lastStatus  []hostconn.ServiceRuntime
	reported    bool
}

func RunWorker(ctx context.Context) error {
	return runWorker(ctx, layout.Production())
}

func runWorker(ctx context.Context, paths layout.Paths) (returnErr error) {
	config, err := hostagent.Load(paths.WorkerConfig)
	if err != nil {
		return fmt.Errorf("load worker config: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lock, err := singletonlock.Acquire(paths.DaemonLock, 0)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lock.Close()) }()
	cgroups, err := cgrouptree.Setup()
	if err != nil {
		return fmt.Errorf("configure delegated cgroups: %w", err)
	}
	if err := prepareRuntimeHost(ctx, paths, cgroups.WorkloadRoot()); err != nil {
		return fmt.Errorf("clean runtime before worker startup: %w", err)
	}
	publicIPv4, err := hostagent.DetectPublicIPv4(ctx)
	if err != nil {
		return err
	}
	control := &workerControl{
		assigned: map[string]state.ServiceDesired{}, listenerIDs: map[string]struct{}{},
	}
	conn, welcome, err := hostagent.Dial(ctx, config.ParentURL, config.HostToken, publicIPv4, hostagent.Handlers{
		Certificates: func(certificates []hostconn.CertificatePEM) error {
			return control.replaceCertificates(certificates)
		},
		Projects: func(projects []state.RuntimeProject) error {
			return control.addProjects(projects)
		},
		Reconcile: func(serviceID string, force bool) error {
			return control.reconcile(ctx, serviceID, force)
		},
		Withdraw: func(serviceID string) error {
			return control.withdraw(ctx, serviceID)
		},
		SyncPublic: func(string) error {
			control.ops.Lock()
			defer control.ops.Unlock()
			if err := control.reloadPublic(ctx); err != nil {
				log.Printf("worker public listeners: %v", err)
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	control.conn = conn
	selector, err := origin.LoadMaterials(certificateMaterials(welcome.Certificates))
	if err != nil {
		return err
	}
	control.selector = selector
	projects := make([]state.RuntimeProject, 0, len(welcome.Projects))
	for _, project := range welcome.Projects {
		project.ObjectStoreEnabled = false
		projects = append(projects, project)
	}
	runtime, err := startRuntime(ctx, paths, cgroups.WorkloadRoot(), projects, permitAllGrowth{}, admission.New(), func() {})
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, runtime.Close()) }()
	control.runtime = runtime
	mesh := newInternalBridge(welcome.HostID, func(lookupCtx context.Context, hostname string) (state.InternalName, error) {
		return hostagent.LookupInternal(lookupCtx, conn, hostname)
	})
	runtime.AttachInternalMesh(mesh)
	if err := mesh.Listen(ctx); err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, mesh.Close()) }()
	tunnel, err := hostagent.DialTunnel(ctx, config.ParentURL, config.HostToken, mesh.DialLocal)
	if err != nil {
		return err
	}
	mesh.SetParent(tunnel)
	go func() {
		if err := tunnel.Serve(ctx); err != nil && ctx.Err() == nil {
			log.Printf("parent .internal tunnel lost: %v", err)
			cancel()
		}
	}()
	forwarder, err := hostagent.NewForwarder(config.ParentURL, config.HostToken)
	if err != nil {
		return err
	}
	otlpAddresses := []string{"127.0.0.1:4318"}
	for _, project := range runtime.firewallProjects {
		otlpAddresses = append(otlpAddresses, net.JoinHostPort(project.Gateway.String(), "4318"))
	}
	if err := forwarder.Listen(ctx, otlpAddresses...); err != nil {
		return fmt.Errorf("listen for OTLP: %w", err)
	}
	defer forwarder.Close()
	control.forwarder = forwarder
	containerLogs := telemetry.NewLogExporter(ctx, "http://127.0.0.1:4318")
	defer containerLogs.Close()
	remoteStore := hostagent.NewArchiveStore(
		hostagent.NewRemoteStore(conn), config.ParentURL, config.HostToken, paths.ImagesRoot,
	)
	control.store = remoteStore
	if err := configureWorkerDeployments(ctx, runtime, remoteStore, conn, containerLogs); err != nil {
		return err
	}
	publicPorts, err := portproxy.New(portproxy.Config{
		Backends: runtime,
		OnError: func(routeID string, proxyErr error) {
			log.Printf("worker port proxy %s: %v", routeID, proxyErr)
		},
	})
	if err != nil {
		return fmt.Errorf("configure public service listeners: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, publicPorts.Close()) }()
	control.proxy = publicPorts
	admin := http.NewServeMux()
	admin.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = response.Write([]byte("ok\n"))
	})
	router, err := ingress.New(ingress.Config{
		AdminHostname: welcome.ParentHostname, AdminHandler: admin, Backends: runtime,
	})
	if err != nil {
		return err
	}
	control.router = router
	go func() {
		if err := conn.Serve(ctx); err != nil && ctx.Err() == nil {
			log.Printf("parent connection lost: %v", err)
			cancel()
		}
	}()
	go control.reportLoop(ctx)
	for _, serviceID := range welcome.AssignedServiceIDs {
		if err := control.reconcile(ctx, serviceID, false); err != nil {
			log.Printf("worker reconcile %s: %v", serviceID, err)
		}
	}
	tlsConfig := selector.TLSConfig()
	httpServer := &http.Server{
		Addr: ":443", Handler: router, ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10, TLSConfig: tlsConfig,
	}
	rawListener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", httpServer.Addr, err)
	}
	listener := tls.NewListener(netutil.LimitListener(rawListener, maximumHTTPSConnections), tlsConfig)
	defer func() { _ = sdnotify.Stopping("platformd worker is stopping") }()
	return serveListener(ctx, httpServer, listener, func() error {
		return sdnotify.Ready("platformd worker is ready")
	})
}

func configureWorkerDeployments(
	ctx context.Context,
	runtime *runtimeStack,
	store deployment.Store,
	conn *hostagent.Conn,
	containerLogs deployment.ContainerLogSink,
) error {
	environment := workerEnvironment{inner: hostagent.NewRemoteEnvironment(conn), runtime: runtime}
	controller, err := deployment.New(deployment.Config{
		Store: store, Engine: runtime.engine, Publisher: runtime,
		Credentials: hostagent.NewRemoteCredentials(conn),
		Environment: environment,
		Growth:      permitAllGrowth{}, Admission: runtime.admission,
		BeforeDeploy: beforeDeployExecutor{
			engine: runtime.engine, environment: environment,
			placement: runtime.servicePlacement, cloudflare: workerPurge{conn: conn},
			logs: containerLogs,
		},
		Placement: runtime.servicePlacement, VolumeRoot: runtime.paths.VolumesRoot, ContainerLogs: containerLogs,
	})
	if err != nil {
		return err
	}
	restarts, err := servicerestart.New(servicerestart.Config{
		Context: ctx, Engine: runtime.engine, Controller: controller,
		OnResult: runtime.recordServiceResult,
	})
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		restarts.Close()
		return errors.New("container runtime is closed")
	}
	runtime.deployments = controller
	runtime.serviceRestarts = restarts
	return nil
}

func (control *workerControl) replaceCertificates(certificates []hostconn.CertificatePEM) error {
	if control.selector == nil {
		return nil
	}
	return control.selector.ReplaceMaterials(certificateMaterials(certificates))
}

func (control *workerControl) addProjects(projects []state.RuntimeProject) error {
	if control.runtime == nil {
		return nil
	}
	for _, project := range projects {
		project.ObjectStoreEnabled = false
		existed := control.runtime.projectNetworkExists(project.ID)
		if err := control.runtime.AddProject(project); err != nil {
			return err
		}
		if existed || control.forwarder == nil {
			continue
		}
		gateway, ok := control.runtime.projectGateway(project.ID)
		if !ok {
			continue
		}
		if err := control.forwarder.Add(net.JoinHostPort(gateway.String(), "4318")); err != nil {
			return fmt.Errorf("listen for OTLP on project %s: %w", project.ID, err)
		}
	}
	return nil
}

func (control *workerControl) reconcile(ctx context.Context, serviceID string, force bool) error {
	control.ops.Lock()
	defer control.ops.Unlock()
	control.mu.Lock()
	runtime := control.runtime
	store := control.store
	control.mu.Unlock()
	if runtime == nil || store == nil {
		return errors.New("worker runtime is not ready")
	}
	desired, err := store.DesiredService(ctx, serviceID)
	if err != nil {
		return err
	}
	desired.HostID = ""
	control.mu.Lock()
	control.assigned[serviceID] = desired
	control.mu.Unlock()
	if err := runtime.DeployService(ctx, serviceID, force); err != nil && !errors.Is(err, deployment.ErrBlockedPair) {
		runtime.recordServiceFailure(serviceID, err)
		reloadErr := control.reloadPublic(ctx)
		control.reportStatus(ctx)
		return errors.Join(err, reloadErr)
	}
	if err := control.reloadPublic(ctx); err != nil {
		control.reportStatus(ctx)
		return err
	}
	control.reportStatus(ctx)
	return nil
}

func (control *workerControl) withdraw(ctx context.Context, serviceID string) error {
	control.ops.Lock()
	defer control.ops.Unlock()
	control.mu.Lock()
	desired, cached := control.assigned[serviceID]
	delete(control.assigned, serviceID)
	runtime := control.runtime
	store := control.store
	control.mu.Unlock()
	if runtime == nil {
		return nil
	}
	if !cached {
		desired = state.ServiceDesired{ID: serviceID}
		if store != nil {
			if loaded, err := store.DesiredService(ctx, serviceID); err == nil {
				desired = loaded
			}
		}
	}
	desired.HostID = ""
	if err := runtime.DeleteService(ctx, desired); err != nil {
		control.reportStatus(ctx)
		return err
	}
	if err := control.reloadPublic(ctx); err != nil {
		control.reportStatus(ctx)
		return err
	}
	control.reportStatus(ctx)
	return nil
}

func (control *workerControl) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(workerStatusInterval)
	defer ticker.Stop()
	control.reportStatus(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !control.reportStatus(ctx) {
				control.reportHeartbeat(ctx)
			}
		}
	}
}

func (control *workerControl) reportStatus(ctx context.Context) bool {
	control.statusMu.Lock()
	defer control.statusMu.Unlock()
	control.mu.Lock()
	conn := control.conn
	runtime := control.runtime
	assigned := make([]state.ServiceDesired, 0, len(control.assigned))
	for _, desired := range control.assigned {
		assigned = append(assigned, desired)
	}
	control.mu.Unlock()
	if conn == nil || runtime == nil {
		return false
	}
	services := make([]hostconn.ServiceRuntime, 0, len(assigned))
	for _, desired := range assigned {
		status, message := runtime.ServiceStatus(desired.ID, desired.Enabled)
		services = append(services, hostconn.ServiceRuntime{
			ServiceID: desired.ID, Status: status, Message: message,
		})
	}
	// assigned is a map; keep snapshot order stable so it does not create
	// false status changes and unnecessary traffic.
	sort.Slice(services, func(left, right int) bool { return services[left].ServiceID < services[right].ServiceID })
	if control.reported && slices.Equal(control.lastStatus, services) {
		return false
	}
	if err := conn.Write(ctx, hostconn.KindStatus, hostconn.Status{Services: services}); err != nil {
		if ctx.Err() == nil {
			log.Printf("worker status: %v", err)
		}
		return true
	}
	control.lastStatus = slices.Clone(services)
	control.reported = true
	return true
}

func (control *workerControl) reportHeartbeat(ctx context.Context) {
	control.mu.Lock()
	conn := control.conn
	control.mu.Unlock()
	if conn == nil {
		return
	}
	if err := conn.Write(ctx, hostconn.KindHeartbeat, struct{}{}); err != nil && ctx.Err() == nil {
		log.Printf("worker heartbeat: %v", err)
	}
}

func (control *workerControl) reloadPublic(ctx context.Context) error {
	return errors.Join(control.reloadRoutes(ctx), control.reloadListeners(ctx))
}

func (control *workerControl) reloadRoutes(ctx context.Context) error {
	if control.router == nil || control.store == nil {
		return nil
	}
	control.mu.Lock()
	assigned := make([]string, 0, len(control.assigned))
	for serviceID := range control.assigned {
		assigned = append(assigned, serviceID)
	}
	control.mu.Unlock()
	routes := map[string]ingress.Route{}
	for _, serviceID := range assigned {
		desired, err := control.store.DesiredService(ctx, serviceID)
		if err != nil {
			continue
		}
		domains, err := hostagent.ServiceDomains(ctx, control.conn, desired.ProjectID, serviceID)
		if err != nil {
			return err
		}
		for _, domain := range domains {
			routes[domain.Hostname] = ingress.Route{ServiceID: domain.ServiceID, TargetPort: domain.TargetPort}
		}
	}
	control.router.Reload(routes)
	return nil
}

func (control *workerControl) reloadListeners(ctx context.Context) error {
	if control.proxy == nil || control.store == nil {
		return nil
	}
	control.mu.Lock()
	assigned := make([]state.ServiceDesired, 0, len(control.assigned))
	for _, desired := range control.assigned {
		assigned = append(assigned, desired)
	}
	previous := control.listenerIDs
	control.mu.Unlock()
	wanted := map[string]portproxy.Route{}
	for _, desired := range assigned {
		if desired.ProjectID == "" {
			continue
		}
		listeners, err := hostagent.ServiceListeners(ctx, control.conn, desired.ProjectID, desired.ID)
		if err != nil {
			return err
		}
		for _, listener := range listeners {
			route := listenerRoute(listener)
			wanted[route.ID] = route
		}
	}
	var failures []error
	for routeID := range previous {
		if _, keep := wanted[routeID]; keep {
			continue
		}
		failures = append(failures, control.proxy.Remove(routeID))
	}
	for _, route := range wanted {
		failures = append(failures, control.proxy.Add(route))
	}
	if err := errors.Join(failures...); err != nil {
		return err
	}
	ids := make(map[string]struct{}, len(wanted))
	for routeID := range wanted {
		ids[routeID] = struct{}{}
	}
	control.mu.Lock()
	control.listenerIDs = ids
	control.mu.Unlock()
	return nil
}

func certificateMaterials(certificates []hostconn.CertificatePEM) []origin.Material {
	materials := make([]origin.Material, 0, len(certificates))
	for _, certificate := range certificates {
		materials = append(materials, origin.Material{
			ID: certificate.ID, CertificatePEM: certificate.CertificatePEM,
			PrivateKeyPEM: []byte(certificate.PrivateKeyPEM),
		})
	}
	return materials
}
