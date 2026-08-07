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

func TestNormalizeApexRequiresRegistrableDomain(t *testing.T) {
	apex, err := NormalizeApex("Example.COM")
	if err != nil || apex != "example.com" {
		t.Fatalf("NormalizeApex = %q, %v", apex, err)
	}
	ok, err := IsApex("example.com")
	if err != nil || !ok {
		t.Fatalf("IsApex(example.com) = %v, %v", ok, err)
	}
	ok, err = IsApex("app.example.com")
	if err != nil || ok {
		t.Fatalf("IsApex(app.example.com) = %v, %v", ok, err)
	}
	for _, invalid := range []string{"", "app.example.com", "localhost"} {
		if _, err := NormalizeApex(invalid); err == nil {
			t.Fatalf("NormalizeApex(%q) accepted", invalid)
		}
	}
}
