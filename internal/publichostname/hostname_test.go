package publichostname

import "testing"

func TestNormalizeCanonicalizesIDNAAndRejectsAmbiguousHosts(t *testing.T) {
	hostname, err := Normalize("BÜCHER.Example")
	if err != nil || hostname != "xn--bcher-kva.example" {
		t.Fatalf("normalized hostname = %q, %v", hostname, err)
	}
	for _, invalid := range []string{
		"example.com.", "https://example.com", "example.com:8443", "localhost",
		"127.0.0.1", "-bad.example", "bad..example",
	} {
		if _, err := Normalize(invalid); err == nil {
			t.Fatalf("invalid hostname %q was accepted", invalid)
		}
	}
	if hostname, err := NormalizeHostHeader("Example.com:443"); err != nil || hostname != "example.com" {
		t.Fatalf("Host header normalization = %q, %v", hostname, err)
	}
}
