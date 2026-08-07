package preview

import (
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
	"github.com/iivankin/platformd/internal/state"
)

func TestPreviewHostnameUsesConfiguredRoot(t *testing.T) {
	t.Parallel()
	hostname := previewHostname("service-id", "pr-12", "example.com")
	if !strings.HasPrefix(hostname, HostnamePrefix) {
		t.Fatalf("hostname = %q, want %s prefix", hostname, HostnamePrefix)
	}
	if !strings.HasSuffix(hostname, ".example.com") {
		t.Fatalf("hostname = %q", hostname)
	}
	label, _, ok := strings.Cut(hostname, ".")
	if !ok || !strings.HasPrefix(label, HostnamePrefix) || len(label) != len(HostnamePrefix)+12 {
		t.Fatalf("expected %s + 12-char hash label, got %q", HostnamePrefix, hostname)
	}
}

func TestPreviewDNSCanonicalPrefersApexThenChild(t *testing.T) {
	t.Parallel()
	domains := []state.ServiceDomain{
		{Hostname: "app.example.com", TargetPort: 8080},
		{Hostname: "example.com", TargetPort: 80},
	}
	if got := previewDNSCanonical("example.com", domains); got != "example.com" {
		t.Fatalf("canonical = %q, want apex", got)
	}
	if got := previewDNSCanonical("example.com", domains[:1]); got != "app.example.com" {
		t.Fatalf("canonical = %q, want child", got)
	}
	if got := previewDNSCanonical("example.com", nil); got != "example.com" {
		t.Fatalf("canonical = %q, want root fallback", got)
	}
	if got := previewDNSCanonicalForHostname("preview-aaaaaaaaaaaa.example.com", domains[:1]); got != "app.example.com" {
		t.Fatalf("canonical from hostname = %q", got)
	}
	if got := previewDNSCanonicalForHostname("preview-aaaaaaaaaaaa.example.com", nil); got != "example.com" {
		t.Fatalf("canonical from hostname fallback = %q", got)
	}
}

func TestPreviewTargetPortPrefersHealthCheck(t *testing.T) {
	t.Parallel()
	desired := state.ServiceDesired{Snapshot: serviceconfig.Snapshot{
		HealthCheck: &serviceconfig.HealthCheck{Port: 9090, Path: "/", TimeoutSeconds: 30},
	}}
	port, err := previewTargetPort(desired, []state.ServiceDomain{{Hostname: "app.example.com", TargetPort: 8080}})
	if err != nil || port != 9090 {
		t.Fatalf("port = %d, %v", port, err)
	}
	desired.Snapshot.HealthCheck = nil
	port, err = previewTargetPort(desired, []state.ServiceDomain{{Hostname: "app.example.com", TargetPort: 8080}})
	if err != nil || port != 8080 {
		t.Fatalf("port = %d, %v", port, err)
	}
	if _, err := previewTargetPort(desired, nil); err != state.ErrPreviewTargetPort {
		t.Fatalf("err = %v", err)
	}
}
