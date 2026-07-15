package server_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/objectstore"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

func TestObjectStoreAdminWorkspaceCreateBrowseUploadPreviewDownloadAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "user",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	master := cryptobox.MasterKey{1, 2, 3}
	payloads, err := objectstore.NewPayloadStore(filepath.Join(t.TempDir(), "objects"), master, nil)
	if err != nil {
		t.Fatal(err)
	}
	application, err := objectstore.NewApplication(store, payloads, master, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := server.Handler(server.DefaultMeta("ready"), server.WithObjectStores(application))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)
	invalid := projectRequest(http.MethodPost, "/api/v1/projects/project/object-stores", `{
  "name":"assets","bucketName":"Not Valid","corsOrigins":["https://example.com/path"]
}`)
	invalid.Header.Set("Origin", "https://admin.example.com")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || !strings.Contains(invalidResponse.Body.String(), "invalid_object_store") {
		t.Fatalf("invalid create = %d/%s", invalidResponse.Code, invalidResponse.Body)
	}

	create := projectRequest(http.MethodPost, "/api/v1/projects/project/object-stores", `{
  "name":"assets","bucketName":"shop-assets","corsOrigins":[]
}`)
	create.Header.Set("Origin", "https://admin.example.com")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create = %d/%s", createResponse.Code, createResponse.Body)
	}
	var created map[string]any
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	storeID, _ := created["id"].(string)
	if storeID == "" || created["accessKey"] == "" || created["secret"] == "" || created["region"] != objectstore.Region {
		t.Fatalf("create response = %#v", created)
	}

	base := "/api/v1/projects/project/object-stores/" + storeID
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, projectRequest(http.MethodGet, base, ""))
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"accessKey":"`+created["accessKey"].(string)+`"`) || !strings.Contains(getResponse.Body.String(), `"secret":"`+created["secret"].(string)+`"`) {
		t.Fatalf("get = %d/%s", getResponse.Code, getResponse.Body)
	}

	payload := []byte("hello from object storage")
	upload := adminObjectRequest(http.MethodPut, base+"/objects?key=docs%2Fhello.txt", payload, "text/plain")
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, upload)
	if uploadResponse.Code != http.StatusCreated || !strings.Contains(uploadResponse.Body.String(), `"objectKey":"docs/hello.txt"`) || strings.Contains(uploadResponse.Body.String(), "ObjectKey") {
		t.Fatalf("upload = %d/%s", uploadResponse.Code, uploadResponse.Body)
	}

	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, projectRequest(http.MethodGet, base+"/objects?prefix=docs%2F", ""))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"contentType":"text/plain"`) {
		t.Fatalf("list = %d/%s", listResponse.Code, listResponse.Body)
	}
	previewResponse := httptest.NewRecorder()
	handler.ServeHTTP(previewResponse, projectRequest(http.MethodGet, base+"/objects/preview?key=docs%2Fhello.txt", ""))
	if previewResponse.Code != http.StatusOK || !strings.Contains(previewResponse.Body.String(), `"text":"hello from object storage"`) {
		t.Fatalf("preview = %d/%s", previewResponse.Code, previewResponse.Body)
	}
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, projectRequest(http.MethodGet, base+"/objects/download?key=docs%2Fhello.txt", ""))
	if downloadResponse.Code != http.StatusOK || !bytes.Equal(downloadResponse.Body.Bytes(), payload) || downloadResponse.Header().Get("Content-Disposition") != "attachment" {
		t.Fatalf("download = %d/%q headers=%v", downloadResponse.Code, downloadResponse.Body.Bytes(), downloadResponse.Header())
	}

	deleteRequest := adminObjectRequest(http.MethodDelete, base+"/objects?key=docs%2Fhello.txt", nil, "")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, projectRequest(http.MethodGet, base+"/objects/preview?key=docs%2Fhello.txt", ""))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted preview = %d/%s", missingResponse.Code, missingResponse.Body)
	}

	directResponse := httptest.NewRecorder()
	raw.ServeHTTP(directResponse, httptest.NewRequest(http.MethodGet, base+"/objects", nil))
	if directResponse.Code != http.StatusForbidden {
		t.Fatalf("workspace without Access = %d/%s", directResponse.Code, directResponse.Body)
	}
}

func adminObjectRequest(method, path string, body []byte, contentType string) *http.Request {
	request := httptest.NewRequest(method, "https://admin.example.com"+path, bytes.NewReader(body))
	request.Host = "admin.example.com"
	request.TLS = &tls.ConnectionState{ServerName: "admin.example.com"}
	request.RemoteAddr = "203.0.113.5:43210"
	request.Header.Set("Cf-Access-Jwt-Assertion", "token")
	request.Header.Set("Origin", "https://admin.example.com")
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request
}
