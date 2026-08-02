package server_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/access"
	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/installationsettings"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/server"
	"github.com/iivankin/platformd/internal/state"
)

func TestInstallationSettingsAPIKeepsKeysWriteOnlyAndChangesRestartedSettings(t *testing.T) {
	store, err := state.Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	master := cryptobox.MasterKey{1, 2, 3}
	certificatePEM, privateKey := serverSettingsCertificate(t, []string{"admin.example.com", "api.example.com", "control.example.com"})
	certificate, _, err := origin.EncryptCertificate(master, "certificate-a", certificatePEM, privateKey, rand.Reader, 1)
	clear(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstallation(context.Background(), state.InitialInstallation{
		ID: "installation-a", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		ConsolePassphrasePHC: "verifier", OriginCertificateID: certificate.ID,
		OriginCertificatePEM: certificate.CertificatePEM, OriginPrivateKey: certificate.PrivateKeyEncrypted,
		InitialAuditEventID: "audit-init", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	selector, err := origin.Load(master, []state.OriginCertificate{certificate})
	if err != nil {
		t.Fatal(err)
	}
	application, err := installationsettings.New(store, master, selector, &sync.Mutex{})
	if err != nil {
		t.Fatal(err)
	}
	restarts := 0
	raw := server.Handler(server.DefaultMeta("ready"), server.WithInstallationSettings(application, func() { restarts++ }))
	handler := access.ProtectAdmin("admin.example.com", projectVerifier{}, raw)

	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, projectRequest(http.MethodGet, "/api/v1/settings", ""))
	if getResponse.Code != http.StatusOK || strings.Contains(getResponse.Body.String(), "certificatePem") || strings.Contains(getResponse.Body.String(), "private") {
		t.Fatalf("settings response = %d/%s", getResponse.Code, getResponse.Body)
	}

	adminRequest := projectRequest(http.MethodPut, "/api/v1/settings/admin-hostname", `{"hostname":"CONTROL.Example.com"}`)
	adminRequest.Header.Set("Origin", "https://admin.example.com")
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || restarts != 1 || adminResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("admin settings response = %d/%s, restarts=%d", adminResponse.Code, adminResponse.Body, restarts)
	}
	if !strings.Contains(adminResponse.Body.String(), `"adminHostname":"control.example.com"`) {
		t.Fatalf("admin hostname response = %s", adminResponse.Body)
	}
	idempotentRequest := projectRequest(http.MethodPut, "/api/v1/settings/admin-hostname", `{"hostname":"control.example.com"}`)
	idempotentRequest.Header.Set("Origin", "https://admin.example.com")
	idempotentResponse := httptest.NewRecorder()
	handler.ServeHTTP(idempotentResponse, idempotentRequest)
	if idempotentResponse.Code != http.StatusOK || restarts != 1 {
		t.Fatalf("idempotent admin settings response = %d/%s, restarts=%d", idempotentResponse.Code, idempotentResponse.Body, restarts)
	}

	accessRequest := projectRequest(http.MethodPut, "/api/v1/settings/cloudflare-access", `{
  "teamDomain":"NEW-TEAM.CloudflareAccess.com",
  "audience":"new-audience"
}`)
	accessRequest.Header.Set("Origin", "https://admin.example.com")
	accessResponse := httptest.NewRecorder()
	handler.ServeHTTP(accessResponse, accessRequest)
	if accessResponse.Code != http.StatusOK || restarts != 2 || accessResponse.Header().Get("X-Request-ID") == "" {
		t.Fatalf("Access settings response = %d/%s, restarts=%d", accessResponse.Code, accessResponse.Body, restarts)
	}
	if !strings.Contains(accessResponse.Body.String(), `"accessTeamDomain":"new-team.cloudflareaccess.com"`) ||
		!strings.Contains(accessResponse.Body.String(), `"accessAudience":"new-audience"`) {
		t.Fatalf("Access settings response = %s", accessResponse.Body)
	}
	installation, err := store.Installation(context.Background())
	if err != nil || installation.AccessTeamDomain != "new-team.cloudflareaccess.com" || installation.AccessAudience != "new-audience" {
		t.Fatalf("stored Access settings = %+v, %v", installation, err)
	}

	secondCertificate, secondKey := serverSettingsCertificate(t, []string{"admin.example.com", "api.example.com"})
	body, err := json.Marshal(map[string]string{
		"certificatePem": secondCertificate, "privateKeyPem": string(secondKey),
	})
	clear(secondKey)
	if err != nil {
		t.Fatal(err)
	}
	addRequest := projectRequest(http.MethodPost, "/api/v1/settings/origin-certificates", string(body))
	addRequest.Header.Set("Origin", "https://admin.example.com")
	addResponse := httptest.NewRecorder()
	handler.ServeHTTP(addResponse, addRequest)
	if addResponse.Code != http.StatusCreated || addResponse.Header().Get("Location") == "" ||
		strings.Contains(addResponse.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(addResponse.Body.String(), "private") {
		t.Fatalf("add certificate response = %d/%s", addResponse.Code, addResponse.Body)
	}
	deleteRequest := projectRequest(http.MethodDelete, addResponse.Header().Get("Location"), "")
	deleteRequest.Header.Set("Origin", "https://admin.example.com")
	deleteResponse := httptest.NewRecorder()
	handler.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete certificate response = %d/%s", deleteResponse.Code, deleteResponse.Body)
	}
}

func serverSettingsCertificate(t *testing.T, names []string) (string, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
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
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	return string(certificatePEM), privateKeyPEM
}
