package cloudflaremesh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

type meshRepository struct {
	mu       sync.Mutex
	settings state.CloudflareMeshSettings
}

func (repository *meshRepository) CloudflareMeshSettings(context.Context) (state.CloudflareMeshSettings, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.settings.APITokenEncrypted) == 0 {
		return state.CloudflareMeshSettings{}, state.ErrCloudflareMeshNotConfigured
	}
	return repository.settings, nil
}

func (repository *meshRepository) PutCloudflareMeshSettings(_ context.Context, input state.PutCloudflareMeshSettingsInput) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.settings = input.Settings
	repository.settings.UpdatedAtMillis = input.UpdatedAtMillis
	return nil
}

type meshRuntime struct {
	mu       sync.Mutex
	address  NetworkAddress
	token    string
	reenroll []bool
}

func (runtime *meshRuntime) Ensure(_ context.Context, token string, reenroll bool) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.token = token
	runtime.reenroll = append(runtime.reenroll, reenroll)
	runtime.address = NetworkAddress{InterfaceName: "CloudflareWARP", Address: "100.96.0.21"}
	return nil
}

func (runtime *meshRuntime) Address() (NetworkAddress, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.address.Address == "" {
		return NetworkAddress{}, errors.New("not connected")
	}
	return runtime.address, nil
}

func (*meshRuntime) Close() error { return nil }

func TestConfigureCreatesManagedHANodeAndRestoresItsClient(t *testing.T) {
	const (
		accountID = "0123456789abcdef0123456789abcdef"
		apiToken  = "cloudflare-api-token-for-testing-only"
		nodeToken = "cloudflare-mesh-node-token-for-testing-only"
	)
	createdHA := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+apiToken {
			http.Error(response, "missing bearer token", http.StatusUnauthorized)
			return
		}
		writeResult := func(result any) {
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(map[string]any{"success": true, "result": result})
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/accounts/"+accountID+"/warp_connector":
			writeResult([]meshNode{})
		case request.Method == http.MethodPost && request.URL.Path == "/accounts/"+accountID+"/warp_connector":
			var input struct {
				Name string `json:"name"`
				HA   bool   `json:"ha"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				http.Error(response, "bad JSON", http.StatusBadRequest)
				return
			}
			createdHA = input.HA
			writeResult(meshNode{ID: "node", Name: input.Name})
		case request.Method == http.MethodGet && request.URL.Path == "/accounts/"+accountID+"/warp_connector/node/token":
			writeResult(nodeToken)
		default:
			http.Error(response, "unexpected request", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	repository := &meshRepository{}
	runtime := &meshRuntime{}
	application, err := New(Config{
		Repository: repository, Master: master, InstallationID: "installation-a",
		Runtime: runtime, HTTPClient: server.Client(), BaseURL: server.URL,
		Random: bytes.NewReader(bytes.Repeat([]byte{0x24}, 128)),
	})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := application.Configure(context.Background(), ConfigureInput{
		AccountID: accountID, APIToken: []byte(apiToken), AuditEventID: "audit",
		ActorID: "actor", ActorEmail: "actor@example.com", UpdatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !createdHA || settings.Status != "connected" || settings.MeshIP != "100.96.0.21" {
		t.Fatalf("managed Mesh settings = %+v, HA = %t", settings, createdHA)
	}
	if bytes.Contains(repository.settings.APITokenEncrypted, []byte(apiToken)) {
		t.Fatal("stored Cloudflare API token is plaintext")
	}
	credential, err := application.Credential(context.Background())
	if err != nil || credential.AccountID != accountID || credential.APIToken != apiToken {
		t.Fatalf("credential = %+v, %v", credential, err)
	}
	if err := application.EnsureConfigured(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.token != nodeToken || len(runtime.reenroll) != 2 || !runtime.reenroll[0] || runtime.reenroll[1] {
		t.Fatalf("runtime enrollment token = %q, reenroll = %#v", runtime.token, runtime.reenroll)
	}
}

func TestRepairConnectionRecreatesConfiguredRuntimeOnlyAfterDisconnect(t *testing.T) {
	const (
		accountID = "0123456789abcdef0123456789abcdef"
		apiToken  = "cloudflare-api-token-for-testing-only"
		nodeToken = "cloudflare-mesh-node-token-for-testing-only"
	)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/accounts/"+accountID+"/warp_connector/node/token" {
			http.Error(response, "unexpected request", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]any{"success": true, "result": nodeToken})
	}))
	t.Cleanup(server.Close)

	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	box, err := cryptobox.NewBox(master, []byte("installation-a"), "platformd/cloudflare-mesh/v1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.SealWith(bytes.NewReader(bytes.Repeat([]byte{0x24}, 128)), []byte(apiToken), []byte("api-token"))
	if err != nil {
		t.Fatal(err)
	}
	repository := &meshRepository{settings: state.CloudflareMeshSettings{
		AccountID: accountID, APITokenEncrypted: sealed, NodeID: "node", NodeName: "platformd-installation-a",
	}}
	runtime := &meshRuntime{address: NetworkAddress{InterfaceName: "CloudflareWARP", Address: "100.96.0.21"}}
	application, err := New(Config{
		Repository: repository, Master: master, InstallationID: "installation-a",
		Runtime: runtime, HTTPClient: server.Client(), BaseURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := application.RepairConnection(context.Background())
	if err != nil || repaired {
		t.Fatalf("healthy connection repair = %t, %v", repaired, err)
	}
	runtime.mu.Lock()
	runtime.address = NetworkAddress{}
	runtime.mu.Unlock()
	repaired, err = application.RepairConnection(context.Background())
	if err != nil || !repaired {
		t.Fatalf("disconnected connection repair = %t, %v", repaired, err)
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.token != nodeToken || len(runtime.reenroll) != 1 || runtime.reenroll[0] {
		t.Fatalf("runtime enrollment token = %q, reenroll = %#v", runtime.token, runtime.reenroll)
	}
}
