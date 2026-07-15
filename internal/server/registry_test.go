package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/registry"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

type registrySettingsStub struct {
	hostname string
}

func (settings *registrySettingsStub) RegistryHostname(context.Context) (string, error) {
	return settings.hostname, nil
}

func (settings *registrySettingsStub) SetRegistryHostname(_ context.Context, input state.SetRegistryHostnameInput) (*string, error) {
	settings.hostname = strings.ToLower(input.Hostname)
	if settings.hostname == "" {
		return nil, nil
	}
	return &settings.hostname, nil
}

func TestRegistryAdminCreateListConfigureAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	payloads, err := registry.NewPayloadStore(filepath.Join(t.TempDir(), "registry"))
	if err != nil {
		t.Fatal(err)
	}
	application, err := registry.NewApplication(store, payloads, cryptobox.MasterKey{1, 2, 3}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := &registrySettingsStub{}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithRegistry(application, settings))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	setHostname := projectRequest(http.MethodPut, "/api/v1/registry/hostname", `{"hostname":"registry.example.com"}`)
	setHostname.Header.Set("Origin", "https://admin.example.com")
	setResponse := httptest.NewRecorder()
	handler.ServeHTTP(setResponse, setHostname)
	if setResponse.Code != http.StatusOK || !strings.Contains(setResponse.Body.String(), `"hostname":"registry.example.com"`) {
		t.Fatalf("set registry hostname = %d/%s", setResponse.Code, setResponse.Body)
	}

	create := projectRequest(http.MethodPost, "/api/v1/registry/repositories", `{
  "name":"team/api","publicPull":true,"credentialName":"deployer","credentialPermission":"pull_push"
}`)
	create.Header.Set("Origin", "https://admin.example.com")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create repository = %d/%s", createResponse.Code, createResponse.Body)
	}
	var created map[string]any
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	repositoryID, _ := created["id"].(string)
	if repositoryID == "" || created["username"] == "" || created["secret"] == "" {
		t.Fatalf("repository credential = %#v", created)
	}
	publicPullRequest := projectRequest(http.MethodPut, "/api/v1/registry/repositories/"+repositoryID+"/public-pull", `{"publicPull":false}`)
	publicPullRequest.Header.Set("Origin", "https://admin.example.com")
	publicPullResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicPullResponse, publicPullRequest)
	if publicPullResponse.Code != http.StatusOK || !strings.Contains(publicPullResponse.Body.String(), `"publicPull":false`) {
		t.Fatalf("set repository public pull = %d/%s", publicPullResponse.Code, publicPullResponse.Body)
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, projectRequest(http.MethodGet, "/api/v1/registry/repositories", ""))
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), `"secret"`) || !strings.Contains(listResponse.Body.String(), `"manifestCount":0`) {
		t.Fatalf("list repositories = %d/%s", listResponse.Code, listResponse.Body)
	}

	credentialPath := "/api/v1/registry/repositories/" + repositoryID + "/credentials"
	createCredential := projectRequest(http.MethodPost, credentialPath, `{"name":"reader","permission":"pull"}`)
	createCredential.Header.Set("Origin", "https://admin.example.com")
	credentialResponse := httptest.NewRecorder()
	handler.ServeHTTP(credentialResponse, createCredential)
	if credentialResponse.Code != http.StatusCreated {
		t.Fatalf("create credential = %d/%s", credentialResponse.Code, credentialResponse.Body)
	}
	var credential map[string]any
	if err := json.NewDecoder(credentialResponse.Body).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	credentialID, _ := credential["id"].(string)
	if credentialID == "" || credential["username"] == "" || credential["secret"] == "" {
		t.Fatalf("credential response = %#v", credential)
	}
	if err := store.Write(ctx, func(transaction *sql.Tx) error {
		_, insertErr := transaction.ExecContext(ctx, `
INSERT INTO registry_credentials(id, repository_id, name, permission, secret_hmac, created_at)
VALUES (?, ?, 'legacy', 'pull', ?, ?)`, "018bcfe5-687b-7fff-bfff-ffffffffffff", repositoryID, make([]byte, 32), 1)
		return insertErr
	}); err != nil {
		t.Fatal(err)
	}
	credentialsResponse := httptest.NewRecorder()
	handler.ServeHTTP(credentialsResponse, projectRequest(http.MethodGet, credentialPath, ""))
	if credentialsResponse.Code != http.StatusOK ||
		!strings.Contains(credentialsResponse.Body.String(), `"name":"reader"`) ||
		!strings.Contains(credentialsResponse.Body.String(), `"secret":"`) ||
		!strings.Contains(credentialsResponse.Body.String(), `"secretAvailable":true`) ||
		!strings.Contains(credentialsResponse.Body.String(), `"name":"legacy"`) ||
		!strings.Contains(credentialsResponse.Body.String(), `"secretAvailable":false`) {
		t.Fatalf("list credentials = %d/%s", credentialsResponse.Code, credentialsResponse.Body)
	}
	revokeRequest := projectRequest(http.MethodDelete, credentialPath+"/"+credentialID, "")
	revokeRequest.Header.Set("Origin", "https://admin.example.com")
	revokeResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke credential = %d/%s", revokeResponse.Code, revokeResponse.Body)
	}

	deleteRequest := projectRequest(http.MethodDelete, "/api/v1/registry/repositories/"+repositoryID, `{"expectedName":"team/api"}`)
	deleteRequest.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete repository = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
}
