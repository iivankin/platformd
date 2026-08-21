package hosthub

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/hosttunnel"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/resourcename"
	"github.com/iivankin/platformd/internal/state"
)

const (
	joinTokenTTL          = 24 * time.Hour
	otlpTimeout           = 10 * time.Second
	maximumHostFrameBytes = 4 << 20
	writeQueueDepth       = 64
	sessionIdleAfter      = 90 * time.Second

	childOfflineMessage        = "Child server is offline"
	childAwaitingStatusMessage = "Waiting for child status"
)

type serviceReport struct {
	status  string
	message string
}

type CachePurger interface {
	PurgeHostnames(context.Context, []string) error
}

type Config struct {
	Store         *state.Store
	Master        cryptobox.MasterKey
	AdminHostname string
	Random        io.Reader
	Now           func() time.Time
	Credentials   deployment.CredentialResolver
	Environment   deployment.EnvironmentResolver
	Cloudflare    CachePurger
	OTLPBaseURL   string
	OnAddress     func(hostID string)
	DialLocal     hosttunnel.DialLocal
}

type Hub struct {
	store         *state.Store
	master        cryptobox.MasterKey
	adminHostname string
	random        io.Reader
	now           func() time.Time
	credentials   deployment.CredentialResolver
	environment   deployment.EnvironmentResolver
	cloudflare    CachePurger
	otlpBaseURL   string
	otlpClient    *http.Client
	onAddress     func(hostID string)
	dialLocal     hosttunnel.DialLocal

	mu       sync.Mutex
	closed   bool
	sessions map[string]*session
}

type session struct {
	hostID string
	hub    *Hub
	cancel context.CancelFunc
	// OTLP is high-volume, so reuse the credential verified by the control
	// handshake instead of reading SQLite for every exported batch.
	tokenHMAC []byte
	writes    chan hostconn.Envelope
	tunnel    *hosttunnel.Peer
	services  map[string]serviceReport
}

func New(config Config) (*Hub, error) {
	if config.Store == nil || config.AdminHostname == "" {
		return nil, errors.New("host hub dependencies are incomplete")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.OTLPBaseURL == "" {
		config.OTLPBaseURL = "http://127.0.0.1:4318"
	}
	return &Hub{
		store: config.Store, master: config.Master, adminHostname: config.AdminHostname,
		random: config.Random, now: config.Now, credentials: config.Credentials,
		environment: config.Environment, cloudflare: config.Cloudflare,
		otlpBaseURL: strings.TrimRight(config.OTLPBaseURL, "/"),
		otlpClient:  &http.Client{Timeout: otlpTimeout},
		onAddress:   config.OnAddress,
		dialLocal:   config.DialLocal,
		sessions:    map[string]*session{},
	}, nil
}

func (hub *Hub) Close() {
	hub.mu.Lock()
	hub.closed = true
	sessions := make([]*session, 0, len(hub.sessions))
	for _, current := range hub.sessions {
		sessions = append(sessions, current)
	}
	hub.sessions = map[string]*session{}
	hub.mu.Unlock()
	for _, current := range sessions {
		current.cancel()
	}
}

func (hub *Hub) Connected(hostID string) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	_, ok := hub.sessions[hostID]
	return ok
}

func (hub *Hub) ServiceStatus(hostID, serviceID string, enabled bool) (string, string) {
	if !enabled {
		return "disabled", ""
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current := hub.sessions[hostID]
	if current == nil {
		return "pending", childOfflineMessage
	}
	report, ok := current.services[serviceID]
	if !ok {
		return "pending", childAwaitingStatusMessage
	}
	return report.status, report.message
}

func (hub *Hub) Reconcile(_ context.Context, hostID, serviceID string, force bool) error {
	return hub.send(hostID, hostconn.KindReconcile, hostconn.Reconcile{ServiceID: serviceID, Force: force})
}

func (hub *Hub) Withdraw(_ context.Context, hostID, serviceID string) error {
	return hub.send(hostID, hostconn.KindWithdraw, hostconn.Withdraw{ServiceID: serviceID})
}

func (hub *Hub) SyncPublic(hostID, serviceID string) error {
	return hub.send(hostID, hostconn.KindSyncPublic, hostconn.SyncPublic{ServiceID: serviceID})
}

func (hub *Hub) Hosts(ctx context.Context) ([]state.Host, error) {
	return hub.store.Hosts(ctx)
}

func (hub *Hub) ActiveHostJoinTokens(ctx context.Context) ([]state.HostJoinToken, error) {
	return hub.store.ActiveHostJoinTokens(ctx)
}

func (hub *Hub) DeleteHost(ctx context.Context, input state.DeleteHostInput) error {
	if err := hub.store.DeleteHost(ctx, input); err != nil {
		return err
	}
	hub.mu.Lock()
	current := hub.sessions[input.ID]
	hub.mu.Unlock()
	if current != nil {
		current.cancel()
	}
	return nil
}

func (hub *Hub) DeleteJoinToken(ctx context.Context, input state.DeleteHostJoinTokenInput) error {
	return hub.store.DeleteHostJoinToken(ctx, input)
}

func (hub *Hub) CreateJoinToken(ctx context.Context, name, actorID, actorEmail, correlationID string) (string, state.HostJoinToken, error) {
	if err := resourcename.Validate(name); err != nil {
		return "", state.HostJoinToken{}, err
	}
	tokenID, err := id.New()
	if err != nil {
		return "", state.HostJoinToken{}, err
	}
	auditID, err := id.New()
	if err != nil {
		return "", state.HostJoinToken{}, err
	}
	plaintext, secret, err := hosttoken.GenerateJoin(tokenID, hub.random)
	if err != nil {
		return "", state.HostJoinToken{}, err
	}
	createdAt := hub.now().UnixMilli()
	actorKind := "access"
	if actorEmail == "" {
		actorKind = "token"
	}
	token, err := hub.store.CreateHostJoinToken(ctx, state.CreateHostJoinTokenInput{
		ID: tokenID, Name: name, TokenHMAC: hosttoken.Digest("join", tokenID, secret),
		ExpiresAtMillis: createdAt + joinTokenTTL.Milliseconds(),
		AuditEventID:    auditID, ActorKind: actorKind, ActorID: actorID, ActorEmail: actorEmail,
		RequestCorrelationID: correlationID, CreatedAtMillis: createdAt,
	})
	if err != nil {
		return "", state.HostJoinToken{}, err
	}
	return plaintext, token, nil
}

const workerInstallCommand = "curl -fsSL https://raw.githubusercontent.com/iivankin/platformd/main/install.sh | sudo sh -s -- worker"

func (hub *Hub) JoinCommand(token string) string {
	return workerInstallCommand + "\nsudo platformd join --url https://" + hub.adminHostname + " --token " + token
}

func (hub *Hub) welcomePayload(ctx context.Context, hostID string) (hostconn.Welcome, error) {
	certificates, err := hub.exportedCertificates(ctx)
	if err != nil {
		return hostconn.Welcome{}, err
	}
	projects, err := hub.store.RuntimeProjects(ctx)
	if err != nil {
		return hostconn.Welcome{}, err
	}
	assigned, err := hub.store.EnabledServiceIDsOnHost(ctx, hostID)
	if err != nil {
		return hostconn.Welcome{}, err
	}
	return hostconn.Welcome{
		HostID: hostID, ParentHostname: hub.adminHostname,
		Certificates: certificates, Projects: projects, AssignedServiceIDs: assigned,
	}, nil
}

func (hub *Hub) PushProjects(projects []state.RuntimeProject) {
	if len(projects) == 0 {
		return
	}
	stripped := make([]state.RuntimeProject, len(projects))
	for index, project := range projects {
		project.ObjectStoreEnabled = false
		stripped[index] = project
	}
	hub.broadcast(hostconn.KindProjects, stripped)
}

func (hub *Hub) PushCertificates(ctx context.Context) error {
	certificates, err := hub.exportedCertificates(ctx)
	if err != nil {
		return err
	}
	hub.broadcast(hostconn.KindCertificates, certificates)
	return nil
}

func (hub *Hub) exportedCertificates(ctx context.Context) ([]hostconn.CertificatePEM, error) {
	installation, err := hub.store.Installation(ctx)
	if err != nil {
		return nil, err
	}
	materials, err := origin.Export(hub.master, installation.OriginCertificates)
	if err != nil {
		return nil, err
	}
	certificates := make([]hostconn.CertificatePEM, 0, len(materials))
	for _, material := range materials {
		certificates = append(certificates, hostconn.CertificatePEM{
			ID: material.ID, CertificatePEM: material.CertificatePEM, PrivateKeyPEM: string(material.PrivateKeyPEM),
		})
		clear(material.PrivateKeyPEM)
	}
	return certificates, nil
}

func (hub *Hub) broadcast(kind string, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("encode %s for child servers: %v", kind, err)
		return
	}
	envelope := hostconn.Envelope{Kind: kind, Payload: body}
	hub.mu.Lock()
	sessions := make([]*session, 0, len(hub.sessions))
	for _, current := range hub.sessions {
		sessions = append(sessions, current)
	}
	hub.mu.Unlock()
	for _, current := range sessions {
		select {
		case current.writes <- envelope:
		default:
			log.Printf("host %s: drop %s (write queue full)", current.hostID, kind)
		}
	}
}

func (hub *Hub) send(hostID, kind string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	hub.mu.Lock()
	current := hub.sessions[hostID]
	hub.mu.Unlock()
	if current == nil {
		return ErrHostOffline
	}
	envelope := hostconn.Envelope{Kind: kind, Payload: body}
	select {
	case current.writes <- envelope:
		return nil
	default:
		return ErrHostBusy
	}
}

func (hub *Hub) attach(hostID string, tokenHMAC []byte, cancel context.CancelFunc) *session {
	current := &session{
		hostID: hostID, hub: hub, cancel: cancel, tokenHMAC: append([]byte(nil), tokenHMAC...),
		writes:   make(chan hostconn.Envelope, writeQueueDepth),
		services: map[string]serviceReport{},
	}
	hub.mu.Lock()
	previous := hub.sessions[hostID]
	if !hub.closed {
		hub.sessions[hostID] = current
	}
	hub.mu.Unlock()
	if previous != nil {
		previous.cancel()
	}
	return current
}

func (hub *Hub) detach(current *session) {
	hub.mu.Lock()
	if hub.sessions[current.hostID] == current {
		delete(hub.sessions, current.hostID)
	}
	tunnel := current.tunnel
	current.tunnel = nil
	hub.mu.Unlock()
	if tunnel != nil {
		_ = tunnel.Close()
	}
}

var (
	ErrHostOffline = errors.New("child server is offline")
	ErrHostBusy    = errors.New("child server is not accepting commands")
)

func (hub *Hub) Tunnel(hostID string) *hosttunnel.Peer {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	current := hub.sessions[hostID]
	if current == nil {
		return nil
	}
	return current.tunnel
}

func (hub *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("POST "+hostconn.JoinPath, hub.JoinHandler())
	mux.Handle("GET "+hostconn.ConnectPath, hub.ConnectHandler())
	mux.Handle("GET "+hostconn.TunnelPath, hub.TunnelHandler())
	mux.Handle("POST "+hostconn.OTLPPathPrefix+"/v1/{signal}", hub.OTLPHandler())
	mux.Handle("GET "+hostconn.ImagePathPrefix+"{revisionID}", hub.ImageHandler())
	return mux
}
