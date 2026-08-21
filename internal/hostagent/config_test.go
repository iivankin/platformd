package hostagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerConfigPersistsPrivateParentURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := Save(path, Config{
		HostID: "host", HostToken: "token", ParentURL: "http://10.20.0.4/", Name: "edge",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"parentUrl": "http://10.20.0.4"`) || strings.Contains(string(raw), "parentHostname") {
		t.Fatalf("worker config = %s", raw)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ParentURL != "http://10.20.0.4" {
		t.Fatalf("parent URL = %q", loaded.ParentURL)
	}
}

func TestWorkerConfigRejectsPublicHTTPParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.json")
	if err := Save(path, Config{
		HostID: "host", HostToken: "token", ParentURL: "http://8.8.8.8", Name: "edge",
	}); err == nil {
		t.Fatal("public plaintext parent URL was accepted")
	}
}
