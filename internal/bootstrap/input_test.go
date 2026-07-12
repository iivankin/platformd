package bootstrap_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/bootstrap"
)

func TestReadAndValidateInput(t *testing.T) {
	t.Parallel()

	certificate, privateKey := testCertificate(t, []string{"*.example.com"})
	encoded, err := json.Marshal(bootstrap.Input{
		AdminHostname:        "ADMIN.EXAMPLE.COM",
		AutomationHostname:   "api.example.com",
		AccessTeamDomain:     "team.cloudflareaccess.com",
		AccessAudience:       "audience",
		ConsolePassphrase:    "passphrase",
		OriginCertificatePEM: certificate,
		OriginPrivateKeyPEM:  privateKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := bootstrap.ReadInput(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	validated, err := bootstrap.ValidateInput(input)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(validated.ConsolePassphrase)
	defer clear(validated.OriginPrivateKeyPEM)
	if validated.AdminHostname != "admin.example.com" || validated.AutomationHostname == nil || *validated.AutomationHostname != "api.example.com" {
		t.Fatalf("validated hostnames = %s/%v", validated.AdminHostname, validated.AutomationHostname)
	}
}

func TestInputRejectsUnknownFieldsAndUncoveredHostname(t *testing.T) {
	t.Parallel()

	if _, err := bootstrap.ReadInput(strings.NewReader(`{"adminHostname":"admin.example.com","unknown":true}`)); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
	certificate, privateKey := testCertificate(t, []string{"other.example.com"})
	_, err := bootstrap.ValidateInput(bootstrap.Input{
		AdminHostname:        "admin.example.com",
		AccessTeamDomain:     "team.cloudflareaccess.com",
		AccessAudience:       "audience",
		ConsolePassphrase:    "passphrase",
		OriginCertificatePEM: certificate,
		OriginPrivateKeyPEM:  privateKey,
	})
	if err == nil || !strings.Contains(err.Error(), "does not cover admin hostname") {
		t.Fatalf("uncovered hostname error = %v", err)
	}
}

func testCertificate(t *testing.T, names []string) (string, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}))
}
