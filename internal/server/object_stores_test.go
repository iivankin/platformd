package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
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
	application, err := objectstore.NewApplication(store, newServerObjectStorage(), master, nil, nil)
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
  "name":"assets","bucketName":"shop-assets","corsOrigins":[],
  "credentials":{"accessKey":"ps3_abcdefghijklmnopqrstuvwx","secret":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
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
	updatedAt, _ := created["updatedAt"].(float64)
	publicAccess := projectRequest(http.MethodPut, base+"/public-access", fmt.Sprintf(`{
  "expectedUpdatedAt": %d,
  "publicHostname": "objects.example.com",
  "corsOrigins": ["https://app.example.com"]
}`, int64(updatedAt)))
	publicAccess.Header.Set("Origin", "https://admin.example.com")
	publicAccessResponse := httptest.NewRecorder()
	handler.ServeHTTP(publicAccessResponse, publicAccess)
	if publicAccessResponse.Code != http.StatusOK || !strings.Contains(publicAccessResponse.Body.String(), `"publicHostname":"objects.example.com"`) || !strings.Contains(publicAccessResponse.Body.String(), `"https://app.example.com"`) {
		t.Fatalf("public access = %d/%s", publicAccessResponse.Code, publicAccessResponse.Body)
	}
	var afterAccess map[string]any
	if err := json.NewDecoder(publicAccessResponse.Body).Decode(&afterAccess); err != nil {
		t.Fatal(err)
	}
	stale := projectRequest(http.MethodPut, base+"/public-access", fmt.Sprintf(`{
  "expectedUpdatedAt": %d,
  "publicHostname": "",
  "corsOrigins": []
}`, int64(updatedAt)))
	stale.Header.Set("Origin", "https://admin.example.com")
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale public access = %d/%s", staleResponse.Code, staleResponse.Body)
	}
	clearAccess := projectRequest(http.MethodPut, base+"/public-access", fmt.Sprintf(`{
  "expectedUpdatedAt": %d,
  "publicHostname": "",
  "corsOrigins": []
}`, int64(afterAccess["updatedAt"].(float64))))
	clearAccess.Header.Set("Origin", "https://admin.example.com")
	clearResponse := httptest.NewRecorder()
	handler.ServeHTTP(clearResponse, clearAccess)
	if clearResponse.Code != http.StatusOK || strings.Contains(clearResponse.Body.String(), `"publicHostname"`) {
		t.Fatalf("clear public access = %d/%s", clearResponse.Code, clearResponse.Body)
	}

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
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"contentType":"text/plain"`) || !strings.Contains(listResponse.Body.String(), `"prefixes":[]`) {
		t.Fatalf("list = %d/%s", listResponse.Code, listResponse.Body)
	}
	folderResponse := httptest.NewRecorder()
	handler.ServeHTTP(folderResponse, projectRequest(http.MethodGet, base+"/objects?delimiter=%2F", ""))
	if folderResponse.Code != http.StatusOK || !strings.Contains(folderResponse.Body.String(), `"objects":[]`) || !strings.Contains(folderResponse.Body.String(), `"prefixes":["docs/"]`) {
		t.Fatalf("folder list = %d/%s", folderResponse.Code, folderResponse.Body)
	}
	statsResponse := httptest.NewRecorder()
	handler.ServeHTTP(statsResponse, projectRequest(http.MethodGet, base+"/stats", ""))
	if statsResponse.Code != http.StatusOK || !strings.Contains(statsResponse.Body.String(), `"objectCount":1`) || !strings.Contains(statsResponse.Body.String(), `"totalBytes":25`) {
		t.Fatalf("stats = %d/%s", statsResponse.Code, statsResponse.Body)
	}
	largestStatusResponse := httptest.NewRecorder()
	handler.ServeHTTP(largestStatusResponse, projectRequest(http.MethodGet, base+"/largest-objects", ""))
	if largestStatusResponse.Code != http.StatusOK || !strings.Contains(largestStatusResponse.Body.String(), `"status":"idle"`) {
		t.Fatalf("largest status = %d/%s", largestStatusResponse.Code, largestStatusResponse.Body)
	}
	largestStartResponse := httptest.NewRecorder()
	handler.ServeHTTP(largestStartResponse, adminObjectRequest(http.MethodPost, base+"/largest-objects", nil, ""))
	if largestStartResponse.Code != http.StatusAccepted ||
		!strings.Contains(largestStartResponse.Body.String(), `"status":"complete"`) ||
		!strings.Contains(largestStartResponse.Body.String(), `"key":"docs/hello.txt"`) ||
		!strings.Contains(largestStartResponse.Body.String(), `"size":25`) {
		t.Fatalf("largest start = %d/%s", largestStartResponse.Code, largestStartResponse.Body)
	}
	largestCancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(largestCancelResponse, adminObjectRequest(http.MethodDelete, base+"/largest-objects", nil, ""))
	if largestCancelResponse.Code != http.StatusOK || !strings.Contains(largestCancelResponse.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("largest cancel = %d/%s", largestCancelResponse.Code, largestCancelResponse.Body)
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

type serverObjectStorage struct {
	objects  map[string]map[string]serverStoredObject
	searches map[string]objectstore.LargestObjectsSearch
}

type serverStoredObject struct {
	metadata objectstore.ObjectMetadata
	body     []byte
}

func newServerObjectStorage() *serverObjectStorage {
	return &serverObjectStorage{
		objects: make(map[string]map[string]serverStoredObject), searches: make(map[string]objectstore.LargestObjectsSearch),
	}
}

func (storage *serverObjectStorage) ReconcileBuckets(_ context.Context, storeIDs []string) error {
	desired := make(map[string]struct{}, len(storeIDs))
	for _, storeID := range storeIDs {
		desired[storeID] = struct{}{}
		if storage.objects[storeID] == nil {
			storage.objects[storeID] = make(map[string]serverStoredObject)
		}
	}
	for storeID := range storage.objects {
		if _, exists := desired[storeID]; !exists {
			delete(storage.objects, storeID)
		}
	}
	return nil
}

func (storage *serverObjectStorage) EnsureBucket(_ context.Context, storeID string) error {
	storage.objects[storeID] = make(map[string]serverStoredObject)
	return nil
}

func (storage *serverObjectStorage) DeleteBucket(_ context.Context, storeID string) error {
	delete(storage.objects, storeID)
	return nil
}

func (storage *serverObjectStorage) ClearBucket(_ context.Context, storeID string) error {
	storage.objects[storeID] = make(map[string]serverStoredObject)
	return nil
}

func (storage *serverObjectStorage) Put(_ context.Context, input objectstore.PutInput) (objectstore.ObjectMetadata, error) {
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return objectstore.ObjectMetadata{}, err
	}
	digest := sha256.Sum256(body)
	metadata := objectstore.ObjectMetadata{
		ObjectStoreID: input.StoreID, ObjectKey: input.ObjectKey, ContentType: input.ContentType,
		ETag: `"` + hex.EncodeToString(digest[:]) + `"`, Size: int64(len(body)),
		CreatedAtMillis: 1, UpdatedAtMillis: 1,
	}
	storage.objects[input.StoreID][input.ObjectKey] = serverStoredObject{metadata, body}
	return metadata, nil
}

func (storage *serverObjectStorage) Object(_ context.Context, storeID, key string) (objectstore.ObjectMetadata, error) {
	object, ok := storage.objects[storeID][key]
	if !ok {
		return objectstore.ObjectMetadata{}, objectstore.ErrObjectNotFound
	}
	return object.metadata, nil
}

func (storage *serverObjectStorage) ReadRange(_ context.Context, storeID, key string, offset, length int64, output io.Writer) error {
	object, ok := storage.objects[storeID][key]
	if !ok {
		return objectstore.ErrObjectNotFound
	}
	_, err := output.Write(object.body[offset : offset+length])
	return err
}

func (storage *serverObjectStorage) ListEntries(_ context.Context, storeID, prefix, delimiter, after string, limit int) ([]objectstore.ObjectListEntry, bool, error) {
	entries := make([]objectstore.ObjectListEntry, 0)
	seen := make(map[string]struct{})
	for key, object := range storage.objects[storeID] {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if delimiter != "" {
			remainder := strings.TrimPrefix(key, prefix)
			if index := strings.Index(remainder, delimiter); index >= 0 {
				commonPrefix := prefix + remainder[:index+len(delimiter)]
				if commonPrefix > after {
					if _, exists := seen[commonPrefix]; !exists {
						entries = append(entries, objectstore.ObjectListEntry{CommonPrefix: commonPrefix})
						seen[commonPrefix] = struct{}{}
					}
				}
				continue
			}
		}
		if key > after {
			metadata := object.metadata
			entries = append(entries, objectstore.ObjectListEntry{Object: &metadata})
		}
	}
	sort.Slice(entries, func(left, right int) bool {
		leftKey := entries[left].CommonPrefix
		if entries[left].Object != nil {
			leftKey = entries[left].Object.ObjectKey
		}
		rightKey := entries[right].CommonPrefix
		if entries[right].Object != nil {
			rightKey = entries[right].Object.ObjectKey
		}
		return leftKey < rightKey
	})
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

func (storage *serverObjectStorage) Stats(_ context.Context, storeID string) (objectstore.ObjectStoreStats, error) {
	result := objectstore.ObjectStoreStats{
		Ready: true, ObservedAtMillis: 1,
		ObjectSizeHistogram: []objectstore.ObjectSizeHistogramBucket{},
	}
	for _, object := range storage.objects[storeID] {
		result.ObjectCount++
		result.TotalBytes += uint64(object.metadata.Size)
	}
	return result, nil
}

func (storage *serverObjectStorage) LargestObjects(_ context.Context, storeID string) (objectstore.LargestObjectsSearch, error) {
	if search, exists := storage.searches[storeID]; exists {
		return search, nil
	}
	return objectstore.LargestObjectsSearch{
		Status: objectstore.LargestObjectsIdle, Objects: []objectstore.LargestObject{},
	}, nil
}

func (storage *serverObjectStorage) StartLargestObjects(_ context.Context, storeID string) (objectstore.LargestObjectsSearch, error) {
	objects := make([]objectstore.LargestObject, 0, len(storage.objects[storeID]))
	for key, object := range storage.objects[storeID] {
		objects = append(objects, objectstore.LargestObject{Key: key, Size: uint64(object.metadata.Size)})
	}
	sort.Slice(objects, func(left, right int) bool {
		return objects[left].Size > objects[right].Size ||
			(objects[left].Size == objects[right].Size && objects[left].Key < objects[right].Key)
	})
	if len(objects) > 10 {
		objects = objects[:10]
	}
	search := objectstore.LargestObjectsSearch{
		Status: objectstore.LargestObjectsComplete, ScannedObjects: uint64(len(storage.objects[storeID])), Objects: objects,
	}
	storage.searches[storeID] = search
	return search, nil
}

func (storage *serverObjectStorage) CancelLargestObjects(_ context.Context, storeID string) (objectstore.LargestObjectsSearch, error) {
	search := objectstore.LargestObjectsSearch{
		Status: objectstore.LargestObjectsCancelled, Objects: []objectstore.LargestObject{},
	}
	storage.searches[storeID] = search
	return search, nil
}

func (storage *serverObjectStorage) Delete(_ context.Context, storeID, key string) error {
	delete(storage.objects[storeID], key)
	return nil
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
