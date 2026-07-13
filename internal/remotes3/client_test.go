package remotes3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/objectstore"
)

const (
	remoteTestAccessKey = "remote-access"
	remoteTestSecret    = "remote-secret"
)

func TestCapabilityProbeUsesSignedPutHeadGetListAndDelete(t *testing.T) {
	t.Parallel()
	timestamp := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	verifier, err := objectstore.NewSigV4Verifier(objectstore.SigV4Config{
		Now: func() time.Time { return timestamp },
		Resolve: func(_ context.Context, accessKey string) (objectstore.Credential, error) {
			if accessKey != remoteTestAccessKey {
				return objectstore.Credential{}, objectstore.ErrInvalidSignature
			}
			return objectstore.Credential{Secret: remoteTestSecret, Permission: "read_write"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	objects := make(map[string][]byte)
	methods := make(map[string]int)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, err := verifier.Verify(request.Context(), request); err != nil {
			http.Error(response, err.Error(), http.StatusForbidden)
			return
		}
		mutex.Lock()
		methods[request.Method]++
		mutex.Unlock()
		if request.Method == http.MethodGet && request.URL.Query().Get("list-type") == "2" {
			prefix := request.URL.Query().Get("prefix")
			type content struct {
				Key  string
				Size int64
			}
			result := struct {
				XMLName     xml.Name
				IsTruncated bool
				Contents    []content
			}{XMLName: xml.Name{Local: "ListBucketResult"}}
			mutex.Lock()
			for key, payload := range objects {
				if strings.HasPrefix(key, prefix) {
					result.Contents = append(result.Contents, content{Key: key, Size: int64(len(payload))})
				}
			}
			mutex.Unlock()
			response.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(response).Encode(result)
			return
		}
		key := strings.TrimPrefix(request.URL.Path, "/test-bucket/")
		switch request.Method {
		case http.MethodPut:
			payload, err := io.ReadAll(request.Body)
			if err != nil {
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			checksum := sha256.Sum256(payload)
			if request.Header.Get("X-Amz-Content-Sha256") != hex.EncodeToString(checksum[:]) {
				http.Error(response, "payload checksum differs", http.StatusBadRequest)
				return
			}
			mutex.Lock()
			objects[key] = payload
			mutex.Unlock()
			response.WriteHeader(http.StatusOK)
		case http.MethodHead:
			mutex.Lock()
			payload, exists := objects[key]
			mutex.Unlock()
			if !exists {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			response.WriteHeader(http.StatusOK)
		case http.MethodGet:
			mutex.Lock()
			payload, exists := objects[key]
			mutex.Unlock()
			if !exists {
				response.WriteHeader(http.StatusNotFound)
				return
			}
			response.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = response.Write(payload)
		case http.MethodDelete:
			mutex.Lock()
			delete(objects, key)
			mutex.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client, err := New(Config{
		Endpoint: server.URL, Region: objectstore.Region, Bucket: "test-bucket", Prefix: "backups",
		AccessKeyID: remoteTestAccessKey, SecretAccessKey: remoteTestSecret,
		HTTPClient: server.Client(), Now: func() time.Time { return timestamp },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x42}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(objects) != 0 {
		t.Fatalf("probe left remote objects: %+v", objects)
	}
	for _, method := range []string{http.MethodPut, http.MethodHead, http.MethodGet, http.MethodDelete} {
		if methods[method] == 0 {
			t.Errorf("probe did not use %s", method)
		}
	}
	if methods[http.MethodGet] < 2 {
		t.Fatalf("probe GET count = %d, want object GET and LIST", methods[http.MethodGet])
	}
}

func TestConfigurationAndObjectInputsAreStrict(t *testing.T) {
	t.Parallel()
	valid := Config{
		Endpoint: "https://s3.example.com", Region: "eu-central-003", Bucket: "test-bucket",
		Prefix: "platformd/backups", AccessKeyID: "access", SecretAccessKey: "secret",
	}
	client, err := New(valid)
	if err != nil {
		t.Fatal(err)
	}
	if value := client.Key("registry/generation"); value != "platformd/backups/registry/generation" {
		t.Fatalf("joined key = %q", value)
	}
	invalid := []Config{
		withConfig(valid, func(config *Config) { config.Endpoint = "http://s3.example.com" }),
		withConfig(valid, func(config *Config) { config.Endpoint = "https://user@s3.example.com" }),
		withConfig(valid, func(config *Config) { config.Region = "EU Central" }),
		withConfig(valid, func(config *Config) { config.Bucket = "BadBucket" }),
		withConfig(valid, func(config *Config) { config.Prefix = "../escape" }),
		withConfig(valid, func(config *Config) { config.SecretAccessKey = "" }),
	}
	for index, config := range invalid {
		if _, err := New(config); err == nil {
			t.Errorf("accepted invalid remote config %d: %+v", index, config)
		}
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256(nil))
	for _, key := range []string{"", "/absolute", "a//b", "a/../b", "line\nbreak"} {
		if err := client.Put(context.Background(), key, bytes.NewReader(nil), 0, checksum); err == nil {
			t.Errorf("accepted invalid key %q", key)
		}
	}
}

func withConfig(value Config, mutate func(*Config)) Config {
	mutate(&value)
	return value
}
