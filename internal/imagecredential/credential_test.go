package imagecredential_test

import (
	"bytes"
	"testing"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/imagecredential"
)

func TestHostNormalizationMatchesOCIReferenceDomain(t *testing.T) {
	for input, expected := range map[string]string{
		"registry.example.com":      "registry.example.com",
		"REGISTRY.EXAMPLE.COM:5443": "registry.example.com:5443",
		"docker.io":                 "docker.io",
	} {
		host, err := imagecredential.NormalizeHost(input)
		if err != nil || host != expected {
			t.Fatalf("NormalizeHost(%q) = %q, %v", input, host, err)
		}
	}
	if host, err := imagecredential.HostForReference("alpine:3.22"); err != nil || host != "docker.io" {
		t.Fatalf("image host = %q, %v", host, err)
	}
	for _, invalid := range []string{"https://registry.example.com", "registry.example.com/team", "registry example.com"} {
		if _, err := imagecredential.NormalizeHost(invalid); err == nil {
			t.Fatalf("accepted invalid registry host %q", invalid)
		}
	}
}

func TestPasswordEncryptionIsBoundToCredentialID(t *testing.T) {
	master, err := cryptobox.ParseMasterKey(bytes.Repeat([]byte{0x25}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := imagecredential.SealPassword(master, "credential-1", "very-secret")
	if err != nil {
		t.Fatal(err)
	}
	opened, err := imagecredential.OpenPassword(master, "credential-1", sealed)
	if err != nil || opened != "very-secret" {
		t.Fatalf("opened = %q, %v", opened, err)
	}
	if _, err := imagecredential.OpenPassword(master, "credential-2", sealed); err == nil {
		t.Fatal("password opened under another credential ID")
	}
}
