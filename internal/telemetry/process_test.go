package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProcessEnvironmentUsesFixedPrivateListeners(t *testing.T) {
	environment := processEnvironment([]string{
		"KEEP=value",
		"PLATFORMD_TELEMETRY_SENTRY_LISTEN=0.0.0.0:8080",
		"PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN=0.0.0.0:9999",
	}, ProcessConfig{Volume: "/var/lib/platformd/telemetry"})
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("invalid environment entry %q", entry)
		}
		values[name] = value
	}
	if values["KEEP"] != "value" ||
		values["PLATFORMD_TELEMETRY_SENTRY_LISTEN"] != SentryHTTPAddress ||
		values["PLATFORMD_TELEMETRY_OTLP_GRPC_LISTEN"] != OTLPGRPCAddress ||
		values["PLATFORMD_TELEMETRY_OTLP_HTTP_LISTEN"] != OTLPHTTPAddress ||
		values["PLATFORMD_TELEMETRY_VOLUME"] != "/var/lib/platformd/telemetry" {
		t.Fatalf("telemetry environment = %#v", values)
	}
}

func TestRecordingBytesReadsInternalDiskUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/internal/disk-usage" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"recordingBytes":4096}`))
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := (&Process{client: server.Client(), target: target}).RecordingBytes(context.Background())
	if err != nil || bytes != 4096 {
		t.Fatalf("recording bytes = %d/%v", bytes, err)
	}
}
