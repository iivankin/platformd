package telemetry

import (
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
