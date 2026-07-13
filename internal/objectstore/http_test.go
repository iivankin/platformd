package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestS3HTTPPutRangeListHeadAndDelete(t *testing.T) {
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
	root := filepath.Join(t.TempDir(), "objects")
	master := cryptobox.MasterKey{1, 2, 3}
	payloads, err := NewPayloadStore(root, master, bytes.NewReader(bytes.Repeat([]byte{9}, 1024)))
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, master, bytes.NewReader(bytes.Repeat([]byte{4}, 1024)), func() time.Time {
		return time.UnixMilli(1_720_000_000_000)
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.Create(ctx, CreateInput{
		ProjectID: "project", Name: "assets", BucketName: "shop-assets",
		CORSOrigins: []string{"https://app.example.com"},
		Actor:       Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
	handler, err := NewHTTPHandler(HTTPConfig{
		Application: application, Now: func() time.Time { return timestamp },
		LookupHost: func(_ context.Context, hostname string) (state.ObjectStore, error) {
			if hostname != "assets.shop.internal" {
				return state.ObjectStore{}, state.ErrObjectStoreNotFound
			}
			return created.Store, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	preflight := httptest.NewRequest(http.MethodOptions, "http://assets.shop.internal:9000/shop-assets/folder/object.txt", nil)
	preflight.Header.Set("Origin", "https://app.example.com")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPut)
	preflight.Header.Set("Access-Control-Request-Headers", "content-type,x-amz-content-sha256,x-amz-date")
	preflightResponse := httptest.NewRecorder()
	handler.ServeHTTP(preflightResponse, preflight)
	if preflightResponse.Code != http.StatusNoContent || preflightResponse.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || preflightResponse.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD, PUT" {
		t.Fatalf("CORS preflight = %d headers=%v body=%s", preflightResponse.Code, preflightResponse.Header(), preflightResponse.Body)
	}
	deniedPreflight := preflight.Clone(ctx)
	deniedPreflight.Header.Set("Origin", "https://attacker.example.com")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, deniedPreflight)
	if deniedResponse.Code != http.StatusForbidden || deniedResponse.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("denied CORS preflight = %d headers=%v", deniedResponse.Code, deniedResponse.Header())
	}
	plaintext := []byte("encrypted object payload")
	put := signedS3Request(t, http.MethodPut, "http://assets.shop.internal:9000/shop-assets/folder/object.txt", plaintext, created.AccessKey, created.Secret, timestamp)
	put.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, put)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
		t.Fatalf("PUT = %d/%s headers=%v", response.Code, response.Body, response.Header())
	}
	encryptedFiles, err := filepath.Glob(filepath.Join(root, created.Store.ID, "payloads", "*", "*.chunk"))
	if err != nil || len(encryptedFiles) != 1 {
		t.Fatalf("encrypted payload files = %v, %v", encryptedFiles, err)
	}
	encoded, err := os.ReadFile(encryptedFiles[0])
	if err != nil || bytes.Contains(encoded, plaintext) {
		t.Fatalf("payload encryption = %v, contains plaintext=%v", err, bytes.Contains(encoded, plaintext))
	}

	get := signedS3Request(t, http.MethodGet, "http://assets.shop.internal:9000/shop-assets/folder/object.txt", nil, created.AccessKey, created.Secret, timestamp)
	get.Header.Set("Range", "bytes=10-15")
	get.Header.Set("Origin", "https://app.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, get)
	if response.Code != http.StatusPartialContent || response.Body.String() != string(plaintext[10:16]) || response.Header().Get("Content-Range") != fmt.Sprintf("bytes 10-15/%d", len(plaintext)) || response.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("Range GET = %d/%q headers=%v", response.Code, response.Body.String(), response.Header())
	}

	head := signedS3Request(t, http.MethodHead, "http://assets.shop.internal:9000/shop-assets/folder/object.txt", nil, created.AccessKey, created.Secret, timestamp)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, head)
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("Content-Length") != strconv.Itoa(len(plaintext)) {
		t.Fatalf("HEAD = %d/%s headers=%v", response.Code, response.Body, response.Header())
	}

	list := signedS3Request(t, http.MethodGet, "http://assets.shop.internal:9000/shop-assets?list-type=2&prefix=folder%2F", nil, created.AccessKey, created.Secret, timestamp)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, list)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<Key>folder/object.txt</Key>") || !strings.Contains(response.Body.String(), "<KeyCount>1</KeyCount>") {
		t.Fatalf("ListObjectsV2 = %d/%s", response.Code, response.Body)
	}

	deleteRequest := signedS3Request(t, http.MethodDelete, "http://assets.shop.internal:9000/shop-assets/folder/object.txt", nil, created.AccessKey, created.Secret, timestamp)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, deleteRequest)
	if response.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d/%s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, signedS3Request(t, http.MethodGet, "http://assets.shop.internal:9000/shop-assets/folder/object.txt", nil, created.AccessKey, created.Secret, timestamp))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "<Code>NoSuchKey</Code>") {
		t.Fatalf("deleted GET = %d/%s", response.Code, response.Body)
	}
}

func TestS3HTTPMultipartEncryptsPartsListsAndPublishesExactFinalETag(t *testing.T) {
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
	root := filepath.Join(t.TempDir(), "objects")
	master := cryptobox.MasterKey{1, 2, 3}
	payloads, err := NewPayloadStore(root, master, bytes.NewReader(bytes.Repeat([]byte{9}, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	application, err := NewApplication(store, payloads, master, bytes.NewReader(bytes.Repeat([]byte{4}, 4096)), func() time.Time {
		return time.UnixMilli(1_720_000_000_000)
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := application.Create(ctx, CreateInput{
		ProjectID: "project", Name: "assets", BucketName: "shop-assets",
		Actor: Actor{Kind: "access", ID: "user", Email: "admin@example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 7, 13, 10, 11, 12, 0, time.UTC)
	handler, err := NewHTTPHandler(HTTPConfig{
		Application: application, Now: func() time.Time { return timestamp },
		LookupHost: func(_ context.Context, _ string) (state.ObjectStore, error) {
			return created.Store, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	create := signedS3Request(t, http.MethodPost, "http://assets.shop.internal:9000/shop-assets/archive.bin?uploads", nil, created.AccessKey, created.Secret, timestamp)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create multipart = %d/%s", createResponse.Code, createResponse.Body)
	}
	var initiated createMultipartUploadResult
	if err := xml.Unmarshal(createResponse.Body.Bytes(), &initiated); err != nil || initiated.UploadID == "" {
		t.Fatalf("create multipart XML = %+v, %v", initiated, err)
	}

	first := bytes.Repeat([]byte("a"), int(MinimumMultipartPartSize))
	second := []byte("final-part")
	partETags := make([]string, 2)
	for index, part := range [][]byte{first, second} {
		partNumber := index + 1
		target := fmt.Sprintf("http://assets.shop.internal:9000/shop-assets/archive.bin?partNumber=%d&uploadId=%s", partNumber, initiated.UploadID)
		request := signedS3Request(t, http.MethodPut, target, part, created.AccessKey, created.Secret, timestamp)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
			t.Fatalf("upload part %d = %d/%s headers=%v", partNumber, response.Code, response.Body, response.Header())
		}
		partETags[index] = response.Header().Get("ETag")
	}
	encryptedParts, err := filepath.Glob(filepath.Join(root, created.Store.ID, "multipart", initiated.UploadID, "*", "*.chunk"))
	if err != nil || len(encryptedParts) != 3 {
		t.Fatalf("encrypted multipart chunks = %v, %v", encryptedParts, err)
	}
	for _, encryptedPart := range encryptedParts {
		encoded, err := os.ReadFile(encryptedPart)
		if err != nil || bytes.Contains(encoded, []byte("final-part")) || bytes.Contains(encoded, bytes.Repeat([]byte("a"), 64)) {
			t.Fatalf("multipart chunk %s contains plaintext or cannot be read: %v", encryptedPart, err)
		}
	}

	listTarget := "http://assets.shop.internal:9000/shop-assets/archive.bin?max-parts=1&uploadId=" + initiated.UploadID
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, signedS3Request(t, http.MethodGet, listTarget, nil, created.AccessKey, created.Secret, timestamp))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), "<IsTruncated>true</IsTruncated>") || !strings.Contains(listResponse.Body.String(), strings.Trim(partETags[0], `"`)) {
		t.Fatalf("list parts = %d/%s", listResponse.Code, listResponse.Body)
	}

	completionXML := []byte("<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>" + partETags[0] + "</ETag></Part><Part><PartNumber>2</PartNumber><ETag>" + partETags[1] + "</ETag></Part></CompleteMultipartUpload>")
	completeTarget := "http://assets.shop.internal:9000/shop-assets/archive.bin?uploadId=" + initiated.UploadID
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, signedS3Request(t, http.MethodPost, completeTarget, completionXML, created.AccessKey, created.Secret, timestamp))
	joined := append(append([]byte(nil), first...), second...)
	joinedHash := sha256.Sum256(joined)
	expectedETag := fmt.Sprintf(`"%x"`, joinedHash)
	var completed completeMultipartUploadResult
	if completeResponse.Code != http.StatusOK || xml.Unmarshal(completeResponse.Body.Bytes(), &completed) != nil || completed.ETag != expectedETag {
		t.Fatalf("complete multipart = %d/%s, expected ETag %s", completeResponse.Code, completeResponse.Body, expectedETag)
	}
	if _, err := os.Stat(filepath.Join(root, created.Store.ID, "multipart", initiated.UploadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed multipart directory remains: %v", err)
	}

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, signedS3Request(t, http.MethodGet, "http://assets.shop.internal:9000/shop-assets/archive.bin", nil, created.AccessKey, created.Secret, timestamp))
	if getResponse.Code != http.StatusOK || !bytes.Equal(getResponse.Body.Bytes(), joined) || getResponse.Header().Get("ETag") != expectedETag {
		t.Fatalf("multipart GET = %d size=%d ETag=%s", getResponse.Code, getResponse.Body.Len(), getResponse.Header().Get("ETag"))
	}
}

func signedS3Request(t *testing.T, method, target string, body []byte, accessKey, secret string, timestamp time.Time) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	payload := sha256.Sum256(body)
	payloadHash := fmt.Sprintf("%x", payload)
	request.Header.Set("X-Amz-Date", timestamp.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	signature, err := signatureForTest(request, secret, accessKey, Region, signedHeaders, payloadHash, timestamp, false)
	if err != nil {
		t.Fatal(err)
	}
	scope := timestamp.Format("20060102") + "/" + Region + "/s3/aws4_request"
	request.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+accessKey+"/"+scope+",SignedHeaders="+signedHeaders+",Signature="+signature)
	return request
}
