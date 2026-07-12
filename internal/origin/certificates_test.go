package origin_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/state"
)

func TestSelectorPrefersExactSANThenStableID(t *testing.T) {
	t.Parallel()

	rawMaster := make([]byte, 32)
	if _, err := rand.Read(rawMaster); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(rawMaster)
	clear(rawMaster)
	if err != nil {
		t.Fatal(err)
	}
	wildcard := encryptedCertificate(t, master, "certificate-a", []string{"*.example.com"})
	exact := encryptedCertificate(t, master, "certificate-z", []string{"admin.example.com"})
	selector, err := origin.Load(master, []state.OriginCertificate{exact, wildcard})
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selector.GetCertificate(&tls.ClientHelloInfo{ServerName: "admin.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Leaf == nil || len(selected.Leaf.DNSNames) != 1 || selected.Leaf.DNSNames[0] != "admin.example.com" {
		t.Fatalf("selected leaf = %+v", selected.Leaf)
	}
	if _, err := selector.GetCertificate(&tls.ClientHelloInfo{ServerName: "deep.admin.example.com"}); err == nil {
		t.Fatal("multi-label wildcard match was accepted")
	}
}

func encryptedCertificate(t *testing.T, master cryptobox.MasterKey, id string, names []string) state.OriginCertificate {
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
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	box, err := cryptobox.NewBox(master, []byte(id), "platformd/sqlite/origin-certificate/v1")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := box.Seal(privatePEM, []byte(id+":private-key"))
	clear(privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	return state.OriginCertificate{ID: id, CertificatePEM: string(certificatePEM), PrivateKeyEncrypted: encrypted}
}
