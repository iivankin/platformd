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
