package cloudflaredns

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type dnsRepository struct {
	mu       sync.Mutex
	settings state.CloudflareDNSSettings
}

type resolverStub struct {
	addresses []string
	err       error
}

func (resolver *resolverStub) LookupHost(context.Context, string) ([]string, error) {
	return resolver.addresses, resolver.err
}

func (repository *dnsRepository) CloudflareDNSSettings(context.Context) (state.CloudflareDNSSettings, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.settings.APITokenEncrypted) == 0 {
		return state.CloudflareDNSSettings{}, state.ErrCloudflareDNSNotConfigured
	}
	return repository.settings, nil
}

func (repository *dnsRepository) PutCloudflareDNSSettings(_ context.Context, input state.PutCloudflareDNSSettingsInput) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.settings = input.Settings
	repository.settings.UpdatedAtMillis = input.UpdatedAtMillis
	return nil
}

func TestPreviewHostnameClonesCanonicalRecordIdempotentlyAndDeletesIt(t *testing.T) {
	const token = "cloudflare-test-token-with-enough-entropy"
	records := map[string][]dnsRecord{
		"app.example.com": {{
			ID: "canonical", Type: "CNAME", Name: "app.example.com",
			Content: "tunnel.example.net", Proxied: true, TTL: 1,
		}},
	}
	var mu sync.Mutex
	var purgedHostnames []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			http.Error(response, "missing bearer token", http.StatusUnauthorized)
			return
		}
		writeResult := func(status int, result any) {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(status)
			_ = json.NewEncoder(response).Encode(map[string]any{"success": true, "result": result})
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/user/tokens/verify":
			writeResult(http.StatusOK, map[string]string{"status": "active"})
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			writeResult(http.StatusOK, []zone{{ID: "zone", Name: "example.com"}})
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone/dns_records":
			mu.Lock()
			result := append([]dnsRecord(nil), records[request.URL.Query().Get("name")]...)
			mu.Unlock()
			writeResult(http.StatusOK, result)
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone/dns_records":
			var created dnsRecord
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				http.Error(response, "invalid record", http.StatusBadRequest)
				return
			}
			created.ID = "preview-record"
			mu.Lock()
			records[created.Name] = append(records[created.Name], created)
			mu.Unlock()
			writeResult(http.StatusOK, created)
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone/purge_cache":
			var body struct {
				Hosts []string `json:"hosts"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(response, "invalid purge", http.StatusBadRequest)
				return
			}
			purgedHostnames = body.Hosts
			writeResult(http.StatusOK, map[string]string{"id": "zone"})
		case request.Method == http.MethodDelete && strings.HasPrefix(request.URL.Path, "/zones/zone/dns_records/"):
			id := strings.TrimPrefix(request.URL.Path, "/zones/zone/dns_records/")
			mu.Lock()
			for hostname, items := range records {
				kept := items[:0]
				for _, item := range items {
					if item.ID != id {
						kept = append(kept, item)
					}
				}
				records[hostname] = kept
			}
			mu.Unlock()
			writeResult(http.StatusOK, map[string]string{"id": id})
		default:
			http.Error(response, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := &dnsRepository{}
	application, err := New(Config{
		Repository: repository, Master: master, InstallationID: "installation",
		HTTPClient: server.Client(), BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Configure(context.Background(), ConfigureInput{
		APIToken: []byte(token), AuditEventID: "audit", ActorID: "actor",
		ActorEmail: "actor@example.com", UpdatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		ids, err := application.EnsurePreviewHostname(context.Background(), "app.example.com", "preview-a.example.com", "preview-a")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != "preview-record" {
			t.Fatalf("record IDs = %#v", ids)
		}
	}
	mu.Lock()
	if len(records["preview-a.example.com"]) != 1 || records["preview-a.example.com"][0].Comment != managedRecordCommentPrefix+"preview-a" {
		t.Fatalf("managed preview records = %#v", records["preview-a.example.com"])
	}
	records["app.example.com"][0].Content = "new-tunnel.example.net"
	mu.Unlock()
	if _, err := application.EnsurePreviewHostname(context.Background(), "app.example.com", "preview-a.example.com", "preview-a"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(records["preview-a.example.com"]) != 1 || records["preview-a.example.com"][0].Content != "new-tunnel.example.net" {
		t.Fatalf("reconciled preview records = %#v", records["preview-a.example.com"])
	}
	mu.Unlock()
	if err := application.DeletePreviewHostname(context.Background(), "preview-a.example.com", []string{"preview-record"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	remaining := len(records["preview-a.example.com"])
	mu.Unlock()
	if remaining != 0 {
		t.Fatalf("preview records remaining = %d", remaining)
	}
	if err := application.PurgeHostnames(context.Background(), []string{"WWW.Example.com", "api.example.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(purgedHostnames, ",") != "api.example.com,www.example.com" {
		t.Fatalf("purged hostnames = %#v", purgedHostnames)
	}
}

func TestPreviewHostnameRejectsAnUnmanagedConflict(t *testing.T) {
	existing := []dnsRecord{{
		ID: "someone-else", Type: "CNAME", Name: "preview-a.example.com",
		Content: "other.example.net", Proxied: true,
	}}
	source := []dnsRecord{{Type: "CNAME", Content: "tunnel.example.net", Proxied: true}}
	if ids := matchingManagedRecordIDs(existing, source, managedRecordCommentPrefix+"preview-a"); ids != nil {
		t.Fatalf("unmanaged records matched: %#v", ids)
	}
}

func TestServiceHostnameCreatesUpdatesChecksAndDeletesManagedCNAME(t *testing.T) {
	const token = "cloudflare-test-token-with-enough-entropy"
	records := map[string][]dnsRecord{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		writeResult := func(status int, result any) {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(status)
			_ = json.NewEncoder(response).Encode(map[string]any{"success": true, "result": result})
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/user/tokens/verify":
			writeResult(http.StatusOK, map[string]string{"status": "active"})
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			writeResult(http.StatusOK, []zone{{ID: "zone", Name: "example.com"}})
		case request.Method == http.MethodGet && request.URL.Path == "/zones/zone/dns_records":
			writeResult(http.StatusOK, records[request.URL.Query().Get("name")])
		case request.Method == http.MethodPost && request.URL.Path == "/zones/zone/dns_records":
			var created dnsRecord
			if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
				http.Error(response, "invalid record", http.StatusBadRequest)
				return
			}
			created.ID = "service-record"
			records[created.Name] = []dnsRecord{created}
			writeResult(http.StatusOK, created)
		case request.Method == http.MethodPatch && request.URL.Path == "/zones/zone/dns_records/service-record":
			var updated dnsRecord
			if err := json.NewDecoder(request.Body).Decode(&updated); err != nil {
				http.Error(response, "invalid record", http.StatusBadRequest)
				return
			}
			updated.ID = "service-record"
			records[updated.Name] = []dnsRecord{updated}
			writeResult(http.StatusOK, updated)
		case request.Method == http.MethodDelete && request.URL.Path == "/zones/zone/dns_records/service-record":
			delete(records, "app.example.com")
			writeResult(http.StatusOK, map[string]string{"id": "service-record"})
		default:
			http.Error(response, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := &dnsRepository{}
	resolver := &resolverStub{}
	application, err := New(Config{
		Repository: repository, Master: master, InstallationID: "installation",
		HTTPClient: server.Client(), Resolver: resolver, BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := application.ServiceHostnameDNSStatus(context.Background(), "app.example.com")
	if err != nil || status != DNSStatusUnmanaged {
		t.Fatalf("unconfigured DNS status = %q, %v", status, err)
	}
	if _, err := application.Configure(context.Background(), ConfigureInput{
		APIToken: []byte(token), AuditEventID: "audit", ActorID: "actor",
		ActorEmail: "actor@example.com", UpdatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := application.EnsureServiceHostname(context.Background(), "APP.Example.com", "admin.example.com")
	if err != nil || !created {
		t.Fatalf("create service CNAME = created %t, %v", created, err)
	}
	record := records["app.example.com"][0]
	if record.Type != "CNAME" || record.Content != "admin.example.com" || !record.Proxied || record.Comment != managedServiceRecordComment {
		t.Fatalf("service CNAME = %+v", record)
	}
	if created, err := application.EnsureServiceHostname(context.Background(), "app.example.com", "admin.example.com"); err != nil || created {
		t.Fatalf("idempotent service CNAME = created %t, %v", created, err)
	}
	if _, err := application.EnsureServiceHostname(context.Background(), "app.example.com", "control.example.com"); err != nil {
		t.Fatal(err)
	}
	if records["app.example.com"][0].Content != "control.example.com" {
		t.Fatalf("updated service CNAME = %+v", records["app.example.com"][0])
	}
	if status, err := application.ServiceHostnameDNSStatus(context.Background(), "app.example.com"); err != nil || status != DNSStatusPending {
		t.Fatalf("pending DNS status = %q, %v", status, err)
	}
	resolver.addresses = []string{"203.0.113.10"}
	if status, err := application.ServiceHostnameDNSStatus(context.Background(), "app.example.com"); err != nil || status != DNSStatusReady {
		t.Fatalf("ready DNS status = %q, %v", status, err)
	}
	deleted, err := application.DeleteServiceHostname(context.Background(), "app.example.com")
	if err != nil || !deleted || len(records["app.example.com"]) != 0 {
		t.Fatalf("delete service CNAME = deleted %t, records %#v, %v", deleted, records["app.example.com"], err)
	}
	records["app.example.com"] = []dnsRecord{{
		ID: "unmanaged", Type: "CNAME", Name: "app.example.com",
		Content: "other.example.com", Proxied: true,
	}}
	if _, err := application.EnsureServiceHostname(context.Background(), "app.example.com", "admin.example.com"); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("unmanaged conflict error = %v", err)
	}
	if deleted, err := application.DeleteServiceHostname(context.Background(), "app.example.com"); err != nil || deleted || len(records["app.example.com"]) != 1 {
		t.Fatalf("unmanaged delete = deleted %t, records %#v, %v", deleted, records["app.example.com"], err)
	}
}
