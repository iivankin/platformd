package installationsettings

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/state"
)

type repositoryStub struct {
	installation state.Installation
	hostnames    []string
	adminSet     bool
	accessSet    bool
	replaced     bool
}

func (repository *repositoryStub) Installation(context.Context) (state.Installation, error) {
	return repository.installation, nil
}

func (repository *repositoryStub) PublicHostnames(context.Context) ([]string, error) {
	return append([]string(nil), repository.hostnames...), nil
}

func (repository *repositoryStub) SetAdminHostname(_ context.Context, input state.SetAdminHostnameInput) error {
	repository.installation.AdminHostname = input.Hostname
	repository.adminSet = true
	return nil
}

func (repository *repositoryStub) SetAccessConfiguration(_ context.Context, input state.SetAccessConfigurationInput) error {
	repository.installation.AccessTeamDomain = input.TeamDomain
	repository.installation.AccessAudience = input.Audience
	repository.accessSet = true
	return nil
}

func (repository *repositoryStub) AddOriginCertificate(_ context.Context, input state.PutOriginCertificateInput) error {
	repository.installation.OriginCertificates = append(repository.installation.OriginCertificates, input.Certificate)
	return nil
}

func (repository *repositoryStub) ReplaceOriginCertificate(_ context.Context, input state.PutOriginCertificateInput) error {
	for index := range repository.installation.OriginCertificates {
		if repository.installation.OriginCertificates[index].ID == input.Certificate.ID {
			repository.installation.OriginCertificates[index] = input.Certificate
			repository.replaced = true
			return nil
		}
	}
	return state.ErrOriginCertificateNotFound
}

func (repository *repositoryStub) DeleteOriginCertificate(_ context.Context, input state.DeleteOriginCertificateInput) error {
	for index, certificate := range repository.installation.OriginCertificates {
		if certificate.ID == input.CertificateID {
			repository.installation.OriginCertificates = append(
				repository.installation.OriginCertificates[:index],
				repository.installation.OriginCertificates[index+1:]...,
			)
			return nil
		}
	}
	return state.ErrOriginCertificateNotFound
}

func TestSetAdminHostnameCommitsCoveredHostnameAndReportsChange(t *testing.T) {
	master := testMasterKey(t)
	certificatePEM, privateKey := testCertificate(t, []string{"admin.example.com", "control.example.com"})
	certificate := encryptTestCertificate(t, master, "certificate-a", certificatePEM, privateKey)
	selector, err := origin.Load(master, []state.OriginCertificate{certificate})
	if err != nil {
		t.Fatal(err)
	}
	repository := &repositoryStub{installation: testInstallation(certificate), hostnames: []string{"admin.example.com"}}
	application, err := New(repository, master, selector, &sync.Mutex{})
	if err != nil {
		t.Fatal(err)
	}
	settings, changed, err := application.SetAdminHostname(context.Background(), "CONTROL.Example.com", testMutation())
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !repository.adminSet || settings.AdminHostname != "control.example.com" {
		t.Fatalf("admin hostname settings = %+v, changed=%t, committed=%t", settings, changed, repository.adminSet)
	}
	repository.adminSet = false
	_, changed, err = application.SetAdminHostname(context.Background(), "control.example.com", testMutation())
	if err != nil || changed || repository.adminSet {
		t.Fatalf("idempotent admin hostname update = changed=%t, committed=%t, err=%v", changed, repository.adminSet, err)
	}
}

func TestSetAccessConfigurationNormalizesValuesAndReportsChange(t *testing.T) {
	master := testMasterKey(t)
	certificatePEM, privateKey := testCertificate(t, []string{"admin.example.com"})
	certificate := encryptTestCertificate(t, master, "certificate-a", certificatePEM, privateKey)
	selector, err := origin.Load(master, []state.OriginCertificate{certificate})
	if err != nil {
		t.Fatal(err)
	}
	repository := &repositoryStub{installation: testInstallation(certificate)}
	application, err := New(repository, master, selector, &sync.Mutex{})
	if err != nil {
		t.Fatal(err)
	}
	settings, changed, err := application.SetAccessConfiguration(
		context.Background(), " NEW-TEAM.CloudflareAccess.com ", " new-audience ", testMutation(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || !repository.accessSet || settings.AccessTeamDomain != "new-team.cloudflareaccess.com" || settings.AccessAudience != "new-audience" {
		t.Fatalf("Access settings = %+v, changed=%t, committed=%t", settings, changed, repository.accessSet)
	}
	repository.accessSet = false
	_, changed, err = application.SetAccessConfiguration(
		context.Background(), "new-team.cloudflareaccess.com", "new-audience", testMutation(),
	)
	if err != nil || changed || repository.accessSet {
		t.Fatalf("idempotent Access update = changed=%t, committed=%t, err=%v", changed, repository.accessSet, err)
	}
	_, _, err = application.SetAccessConfiguration(
		context.Background(), "not-cloudflare.example.com", "audience", testMutation(),
	)
	if err == nil || repository.accessSet {
		t.Fatalf("invalid Access update = committed=%t, err=%v", repository.accessSet, err)
	}
}

func TestReplaceCertificateRejectsUncoveredHostWithoutPublishing(t *testing.T) {
	master := testMasterKey(t)
	oldPEM, oldKey := testCertificate(t, []string{"admin.example.com"})
	old := encryptTestCertificate(t, master, "certificate-a", oldPEM, oldKey)
	selector, err := origin.Load(master, []state.OriginCertificate{old})
	if err != nil {
		t.Fatal(err)
	}
	repository := &repositoryStub{installation: testInstallation(old), hostnames: []string{"admin.example.com"}}
	application, err := New(repository, master, selector, &sync.Mutex{})
	if err != nil {
		t.Fatal(err)
	}
	newPEM, newKey := testCertificate(t, []string{"other.example.com"})
	_, err = application.ReplaceCertificate(context.Background(), old.ID, CertificateMutation{
		Mutation: testMutation(), CertificatePEM: newPEM, PrivateKeyPEM: newKey,
	})
	var coverage *state.OriginCertificateCoverageError
	if !errors.As(err, &coverage) || len(coverage.Hostnames) != 1 || coverage.Hostnames[0] != "admin.example.com" {
		t.Fatalf("coverage error = %v", err)
	}
	if repository.replaced || !selector.Covers("admin.example.com") || selector.Covers("other.example.com") {
		t.Fatal("rejected replacement changed durable or live certificate state")
	}
}

func TestDeleteOnlyCertificateReportsDependentHostnames(t *testing.T) {
	master := testMasterKey(t)
	certificatePEM, privateKey := testCertificate(t, []string{"admin.example.com"})
	certificate := encryptTestCertificate(t, master, "certificate-a", certificatePEM, privateKey)
	selector, err := origin.Load(master, []state.OriginCertificate{certificate})
	if err != nil {
		t.Fatal(err)
	}
	repository := &repositoryStub{installation: testInstallation(certificate), hostnames: []string{"admin.example.com"}}
	application, err := New(repository, master, selector, &sync.Mutex{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.DeleteCertificate(context.Background(), certificate.ID, testMutation())
	var coverage *state.OriginCertificateCoverageError
	if !errors.As(err, &coverage) || len(coverage.Hostnames) != 1 || coverage.Hostnames[0] != "admin.example.com" {
		t.Fatalf("coverage error = %v", err)
	}
}

func testInstallation(certificate state.OriginCertificate) state.Installation {
	return state.Installation{
		ID: "installation-a", AdminHostname: "admin.example.com",
		AccessTeamDomain: "team.cloudflareaccess.com", AccessAudience: "audience",
		OriginCertificates: []state.OriginCertificate{certificate},
	}
}

func testMutation() Mutation {
	return Mutation{
		AuditEventID: "audit-a", CorrelationID: "request-a",
		Actor:     Actor{ID: "subject-a", Email: "admin@example.com"},
		Timestamp: time.Unix(1_700_000_000, 0),
	}
}

func testMasterKey(t *testing.T) cryptobox.MasterKey {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	master, err := cryptobox.ParseMasterKey(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	return master
}

func testCertificate(t *testing.T, names []string) (string, []byte) {
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
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
}

func encryptTestCertificate(
	t *testing.T,
	master cryptobox.MasterKey,
	id, certificatePEM string,
	privateKey []byte,
) state.OriginCertificate {
	t.Helper()
	certificate, _, err := origin.EncryptCertificate(master, id, certificatePEM, privateKey, rand.Reader, time.Now().UnixMilli())
	clear(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
