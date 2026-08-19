package daemon

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cloudflaredns"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/hosttoken"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/ingress"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
	"github.com/iivankin/platformd/internal/trafficmetrics"
)

func TestDomainEnsureDNSUsesChildAddressAndSentryStaysOnAdmin(t *testing.T) {
	t.Parallel()

	env := startDomainFixture(t)
	if _, err := env.domains.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "web", Hostname: "web.example.com", TargetPort: 8080,
		AuditEventID: "audit-web-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-web-domain", CreatedAtMillis: 40,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.domains.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "api", Hostname: "api.example.com", TargetPort: 8080,
		AuditEventID: "audit-api-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-api-domain", CreatedAtMillis: 41,
	}); err != nil {
		t.Fatal(err)
	}
	telemetry := &liveServiceTelemetryRepository{
		cloudflare: env.cloudflare, adminHostname: "admin.example.com",
	}
	if _, err := telemetry.ensureDNS(context.Background(), "sentry.example.com"); err != nil {
		t.Fatal(err)
	}

	env.mu.Lock()
	defer env.mu.Unlock()
	web := env.records["web.example.com"]
	if len(web) != 1 || web[0].Type != "CNAME" || web[0].Content != "admin.example.com" || !web[0].Proxied {
		t.Fatalf("primary app DNS = %+v", web)
	}
	api := env.records["api.example.com"]
	if len(api) != 1 || api[0].Type != "A" || api[0].Content != "203.0.113.40" || !api[0].Proxied {
		t.Fatalf("child app DNS = %+v", api)
	}
	sentry := env.records["sentry.example.com"]
	if len(sentry) != 1 || sentry[0].Type != "CNAME" || sentry[0].Content != "admin.example.com" || !sentry[0].Proxied {
		t.Fatalf("Sentry DNS = %+v", sentry)
	}
}

func TestAttachChildDomainNotifiesWorker(t *testing.T) {
	t.Parallel()

	env := startDomainFixture(t)
	remote := &hostPublicSyncStub{}
	env.domains.SetRemote(remote)
	if _, err := env.domains.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "api", Hostname: "api.example.com", TargetPort: 8080,
		AuditEventID: "audit-api-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-api-domain", CreatedAtMillis: 40,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.domains.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "web", Hostname: "web.example.com", TargetPort: 8080,
		AuditEventID: "audit-web-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-web-domain", CreatedAtMillis: 41,
	}); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if len(remote.calls) != 1 || remote.calls[0] != "api" {
		t.Fatalf("sync-public = %v", remote.calls)
	}
}

type hostPublicSyncStub struct {
	mu    sync.Mutex
	calls []string
}

func (stub *hostPublicSyncStub) SyncPublic(_, serviceID string) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls = append(stub.calls, serviceID)
	return nil
}

func TestReconcileDNSRewritesRecordAfterServiceHostChange(t *testing.T) {
	t.Parallel()

	env := startDomainFixture(t)
	if _, err := env.domains.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "web", Hostname: "web.example.com", TargetPort: 8080,
		AuditEventID: "audit-web-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-web-move", CreatedAtMillis: 50,
	}); err != nil {
		t.Fatal(err)
	}
	env.mu.Lock()
	webDNS := env.records["web.example.com"]
	env.mu.Unlock()
	if len(webDNS) != 1 || webDNS[0].Type != "CNAME" {
		t.Fatalf("initial primary DNS = %+v", webDNS)
	}
	web, err := env.store.Service(context.Background(), "shop", "web")
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := env.store.Hosts(context.Background())
	if err != nil || len(hosts) != 1 {
		t.Fatalf("hosts = %d, %v", len(hosts), err)
	}
	if _, err := env.store.UpdateService(context.Background(), state.UpdateServiceInput{
		ID: "web", ProjectID: "shop", Enabled: web.Enabled, Snapshot: web.Snapshot,
		HostID: hosts[0].ID, ExpectedUpdatedMillis: web.UpdatedAtMillis,
		AuditEventID: "audit-web-move", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-web-move-store",
		UpdatedAtMillis: 60,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.domains.reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := env.domains.reconcileDNS(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.mu.Lock()
	moved := env.records["web.example.com"]
	env.mu.Unlock()
	if len(moved) != 1 || moved[0].Type != "A" || moved[0].Content != "203.0.113.40" {
		t.Fatalf("moved service DNS = %+v", moved)
	}
	unrouted := httptest.NewRecorder()
	env.router.ServeHTTP(unrouted, domainTLSRequest("web.example.com"))
	if unrouted.Code != http.StatusMisdirectedRequest {
		t.Fatalf("moved hostname status = %d, want 421", unrouted.Code)
	}
}

func TestReloadSkipsChildPlacedApplicationHostnames(t *testing.T) {
	t.Parallel()

	env := startDomainFixture(t)
	if _, err := env.store.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "web", Hostname: "web.example.com", TargetPort: 8080,
		AuditEventID: "audit-web-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-web-domain", CreatedAtMillis: 40,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.AttachServiceDomain(context.Background(), state.AttachServiceDomainInput{
		ProjectID: "shop", ServiceID: "api", Hostname: "api.example.com", TargetPort: 8080,
		AuditEventID: "audit-api-domain", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", RequestCorrelationID: "req-api-domain", CreatedAtMillis: 41,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.domains.reload(context.Background()); err != nil {
		t.Fatal(err)
	}

	primary := httptest.NewRecorder()
	env.router.ServeHTTP(primary, domainTLSRequest("web.example.com"))
	if primary.Code != http.StatusServiceUnavailable {
		t.Fatalf("primary hostname status = %d, want 503 from the local ingress route", primary.Code)
	}
	child := httptest.NewRecorder()
	env.router.ServeHTTP(child, domainTLSRequest("api.example.com"))
	if child.Code != http.StatusMisdirectedRequest {
		t.Fatalf("child hostname status = %d, want 421 because ingress must not listen on it", child.Code)
	}
}

type domainFixture struct {
	store      *state.Store
	domains    liveDomainRepository
	router     *ingress.Router
	cloudflare *cloudflaredns.Application
	records    map[string][]domainDNSRecord
	mu         sync.Mutex
}

type domainDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment"`
}

type domainBackendStub struct{}

func (domainBackendStub) ServiceBackend(string, int) (deployment.Backend, bool, error) {
	return deployment.Backend{}, false, nil
}

func startDomainFixture(t *testing.T) *domainFixture {
	t.Helper()
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	rawMaster := make([]byte, 32)
	if _, err := rand.Read(rawMaster); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(rawMaster)
	clear(rawMaster)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"admin.example.com", "web.example.com", "api.example.com", "sentry.example.com"}
	certificatePEM, encryptedKey := domainOriginCertificate(t, master, "origin-1", names)
	if err := store.CreateInstallation(context.Background(), state.InitialInstallation{
		ID: "installation", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "$argon2id$verifier", OriginCertificateID: "origin-1",
		OriginCertificatePEM: certificatePEM, OriginPrivateKey: encryptedKey,
		InitialAuditEventID: "audit-init", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(context.Background(), state.CreateProject{
		ID: "shop", Name: "shop", AuditEventID: "audit-project",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	hostID := joinDomainHost(t, store)
	if _, err := store.CreateService(context.Background(), state.CreateService{
		ID: "web", ProjectID: "shop", Name: "web", Enabled: true,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-web", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-web", CreatedAtMillis: 30,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateService(context.Background(), state.CreateService{
		ID: "api", ProjectID: "shop", Name: "api", Enabled: true, HostID: hostID,
		Snapshot:     serviceconfig.Snapshot{Source: serviceconfig.PublicImageSource("alpine:3.22")},
		AuditEventID: "audit-api", ActorKind: "access", ActorID: "actor", ActorEmail: "admin@example.com",
		RequestCorrelationID: "req-api", CreatedAtMillis: 31,
	}); err != nil {
		t.Fatal(err)
	}

	installation, err := store.Installation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	certificates, err := origin.Load(master, installation.OriginCertificates)
	if err != nil {
		t.Fatal(err)
	}
	router, err := ingress.New(ingress.Config{
		AdminHostname: "admin.example.com",
		AdminHandler:  http.NotFoundHandler(),
		Backends:      domainBackendStub{},
		Traffic:       trafficmetrics.NewRegistry(),
	})
	if err != nil {
		t.Fatal(err)
	}

	env := &domainFixture{store: store, router: router, records: map[string][]domainDNSRecord{}}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeResult := func(result any) {
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(map[string]any{"success": true, "result": result})
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/user/tokens/verify":
			writeResult(map[string]string{"status": "active"})
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			writeResult([]map[string]string{{"id": "zone", "name": "example.com"}})
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone/dns_records":
			env.mu.Lock()
			result := append([]domainDNSRecord(nil), env.records[request.URL.Query().Get("name")]...)
			env.mu.Unlock()
			writeResult(result)
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone/dns_records":
			var created domainDNSRecord
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				http.Error(response, "invalid record", http.StatusBadRequest)
				return
			}
			created.ID = "record-" + created.Name
			env.mu.Lock()
			env.records[created.Name] = []domainDNSRecord{created}
			env.mu.Unlock()
			writeResult(created)
		case request.Method == http.MethodPatch && strings.HasPrefix(request.URL.Path, "/zones/zone/dns_records/"):
			var updated domainDNSRecord
			if err := json.NewDecoder(request.Body).Decode(&updated); err != nil {
				http.Error(response, "invalid record", http.StatusBadRequest)
				return
			}
			updated.ID = strings.TrimPrefix(request.URL.Path, "/zones/zone/dns_records/")
			env.mu.Lock()
			env.records[updated.Name] = []domainDNSRecord{updated}
			env.mu.Unlock()
			writeResult(updated)
		default:
			http.Error(response, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	application, err := cloudflaredns.New(cloudflaredns.Config{
		Repository: store, Master: master, InstallationID: "installation",
		HTTPClient: server.Client(), BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Configure(context.Background(), cloudflaredns.ConfigureInput{
		APIToken:     []byte("cloudflare-test-token-with-enough-entropy"),
		AuditEventID: "audit-cf", ActorID: "actor", ActorEmail: "admin@example.com",
		UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	env.cloudflare = application
	env.domains = liveDomainRepository{
		store: store, certificates: certificates, router: router,
		publicMu: &sync.Mutex{}, cloudflare: application, adminHostname: "admin.example.com",
	}
	return env
}

func joinDomainHost(t *testing.T, store *state.Store) string {
	t.Helper()
	tokenID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	plaintext, secret, err := hosttoken.GenerateJoin(tokenID, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateHostJoinToken(context.Background(), state.CreateHostJoinTokenInput{
		ID: tokenID, Name: "edge-1", TokenHMAC: hosttoken.Digest("join", tokenID, secret),
		ExpiresAtMillis: 100_000, AuditEventID: "audit-token", ActorKind: "access",
		ActorID: "actor", ActorEmail: "admin@example.com", RequestCorrelationID: "req-token", CreatedAtMillis: 10,
	}); err != nil {
		t.Fatal(err)
	}
	hostID, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	parsedID, parsedSecret, err := hosttoken.ParseJoin(plaintext)
	if err != nil || parsedID != tokenID || parsedSecret != secret {
		t.Fatalf("parse join token = %q %v", parsedID, err)
	}
	if _, err := store.ConsumeHostJoinToken(context.Background(), state.ConsumeHostJoinTokenInput{
		TokenID: tokenID, HostID: hostID, AuditEventID: "audit-join", Name: "edge-1",
		PublicIPv4: "203.0.113.40", TokenHMAC: hosttoken.Digest("host", hostID, "unused"),
		ConsumedAtMillis: 20,
	}); err != nil {
		t.Fatal(err)
	}
	return hostID
}

func domainOriginCertificate(t *testing.T, master cryptobox.MasterKey, id string, names []string) (string, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	encrypted, _, err := origin.EncryptCertificate(master, id, string(certificatePEM), privatePEM, rand.Reader, 1)
	clear(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	return encrypted.CertificatePEM, encrypted.PrivateKeyEncrypted
}

func domainTLSRequest(host string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "https://"+host+"/path", nil)
	request.Host = host
	request.TLS = &tls.ConnectionState{ServerName: host}
	return request
}
