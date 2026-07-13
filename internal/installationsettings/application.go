package installationsettings

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/origin"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

type Repository interface {
	Installation(context.Context) (state.Installation, error)
	PublicHostnames(context.Context) ([]string, error)
	SetAutomationHostname(context.Context, state.SetAutomationHostnameInput) (*string, error)
	AddOriginCertificate(context.Context, state.PutOriginCertificateInput) error
	ReplaceOriginCertificate(context.Context, state.PutOriginCertificateInput) error
	DeleteOriginCertificate(context.Context, state.DeleteOriginCertificateInput) error
}

type AutomationRoute interface {
	Prepare(string) (func() error, error)
}

type Actor struct {
	ID    string
	Email string
}

type Certificate struct {
	ID              string
	DNSNames        []string
	CreatedAtMillis int64
}

type Settings struct {
	InstallationID     string
	AdminHostname      string
	AutomationHostname string
	AccessTeamDomain   string
	AccessAudience     string
	Certificates       []Certificate
}

type Mutation struct {
	ResourceID    string
	AuditEventID  string
	CorrelationID string
	Actor         Actor
	Timestamp     time.Time
}

type CertificateMutation struct {
	Mutation
	CertificatePEM string
	PrivateKeyPEM  []byte
}

type Application struct {
	repository   Repository
	master       cryptobox.MasterKey
	certificates *origin.Selector
	automation   AutomationRoute
	random       io.Reader
	publicMu     *sync.Mutex
}

func New(
	repository Repository,
	master cryptobox.MasterKey,
	certificates *origin.Selector,
	automation AutomationRoute,
	publicMu *sync.Mutex,
) (*Application, error) {
	if repository == nil || certificates == nil || automation == nil || publicMu == nil {
		return nil, errors.New("installation settings dependencies are incomplete")
	}
	return &Application{
		repository: repository, master: master, certificates: certificates,
		automation: automation, random: rand.Reader, publicMu: publicMu,
	}, nil
}

func (application *Application) Settings(ctx context.Context) (Settings, error) {
	installation, err := application.repository.Installation(ctx)
	if err != nil {
		return Settings{}, err
	}
	result := Settings{
		InstallationID: installation.ID, AdminHostname: installation.AdminHostname,
		AccessTeamDomain: installation.AccessTeamDomain, AccessAudience: installation.AccessAudience,
		Certificates: make([]Certificate, 0, len(installation.OriginCertificates)),
	}
	if installation.AutomationHostname != nil {
		result.AutomationHostname = *installation.AutomationHostname
	}
	for _, certificate := range installation.OriginCertificates {
		names, err := origin.DNSNames(certificate.CertificatePEM)
		if err != nil {
			return Settings{}, err
		}
		result.Certificates = append(result.Certificates, Certificate{
			ID: certificate.ID, DNSNames: names, CreatedAtMillis: certificate.CreatedAtMillis,
		})
	}
	return result, nil
}

func (application *Application) SetAutomationHostname(ctx context.Context, hostname string, mutation Mutation) (Settings, error) {
	application.publicMu.Lock()
	defer application.publicMu.Unlock()

	normalized := ""
	if hostname != "" {
		var err error
		normalized, err = publichostname.Normalize(hostname)
		if err != nil {
			return Settings{}, err
		}
		if !application.certificates.Covers(normalized) {
			return Settings{}, &state.OriginCertificateCoverageError{Hostnames: []string{normalized}}
		}
	}
	publish, err := application.automation.Prepare(normalized)
	if err != nil {
		return Settings{}, err
	}
	_, err = application.repository.SetAutomationHostname(ctx, state.SetAutomationHostnameInput{
		Hostname: normalized, AuditEventID: mutation.AuditEventID,
		ActorID: mutation.Actor.ID, ActorEmail: mutation.Actor.Email,
		RequestCorrelationID: mutation.CorrelationID, UpdatedAtMillis: mutation.Timestamp.UnixMilli(),
	})
	if err != nil {
		return Settings{}, err
	}
	if err := publish(); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

func (application *Application) AddCertificate(ctx context.Context, mutation CertificateMutation) (Settings, error) {
	application.publicMu.Lock()
	defer application.publicMu.Unlock()

	certificate, _, err := origin.EncryptCertificate(
		application.master, mutation.ResourceID, mutation.CertificatePEM,
		mutation.PrivateKeyPEM, application.random, mutation.Timestamp.UnixMilli(),
	)
	if err != nil {
		return Settings{}, err
	}
	installation, err := application.repository.Installation(ctx)
	if err != nil {
		return Settings{}, err
	}
	candidateValues := append(append([]state.OriginCertificate(nil), installation.OriginCertificates...), certificate)
	candidate, err := origin.Load(application.master, candidateValues)
	if err != nil {
		return Settings{}, err
	}
	if err := application.repository.AddOriginCertificate(ctx, putCertificateInput(certificate, mutation)); err != nil {
		return Settings{}, err
	}
	if err := application.certificates.Replace(candidate); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

func (application *Application) ReplaceCertificate(ctx context.Context, certificateID string, mutation CertificateMutation) (Settings, error) {
	mutation.ResourceID = certificateID
	certificate, _, err := origin.EncryptCertificate(
		application.master, certificateID, mutation.CertificatePEM,
		mutation.PrivateKeyPEM, application.random, mutation.Timestamp.UnixMilli(),
	)
	if err != nil {
		return Settings{}, err
	}
	return application.replaceCertificateSet(ctx, certificateID, &certificate, mutation.Mutation)
}

func (application *Application) DeleteCertificate(ctx context.Context, certificateID string, mutation Mutation) (Settings, error) {
	return application.replaceCertificateSet(ctx, certificateID, nil, mutation)
}

func (application *Application) replaceCertificateSet(
	ctx context.Context,
	certificateID string,
	replacement *state.OriginCertificate,
	mutation Mutation,
) (Settings, error) {
	application.publicMu.Lock()
	defer application.publicMu.Unlock()

	installation, err := application.repository.Installation(ctx)
	if err != nil {
		return Settings{}, err
	}
	candidateValues := make([]state.OriginCertificate, 0, len(installation.OriginCertificates))
	found := false
	for _, certificate := range installation.OriginCertificates {
		if certificate.ID != certificateID {
			candidateValues = append(candidateValues, certificate)
			continue
		}
		found = true
		if replacement != nil {
			candidateValues = append(candidateValues, *replacement)
		}
	}
	if !found {
		return Settings{}, state.ErrOriginCertificateNotFound
	}
	hostnames, err := application.repository.PublicHostnames(ctx)
	if err != nil {
		return Settings{}, err
	}
	if len(candidateValues) == 0 {
		return Settings{}, &state.OriginCertificateCoverageError{Hostnames: hostnames}
	}
	candidate, err := origin.Load(application.master, candidateValues)
	if err != nil {
		return Settings{}, err
	}
	if uncovered := uncoveredHostnames(candidate, hostnames); len(uncovered) != 0 {
		return Settings{}, &state.OriginCertificateCoverageError{Hostnames: uncovered}
	}
	if replacement != nil {
		err = application.repository.ReplaceOriginCertificate(ctx, putCertificateInput(*replacement, CertificateMutation{Mutation: mutation}))
	} else {
		err = application.repository.DeleteOriginCertificate(ctx, state.DeleteOriginCertificateInput{
			CertificateID: certificateID, AuditEventID: mutation.AuditEventID,
			ActorID: mutation.Actor.ID, ActorEmail: mutation.Actor.Email,
			RequestCorrelationID: mutation.CorrelationID, DeletedAtMillis: mutation.Timestamp.UnixMilli(),
		})
	}
	if err != nil {
		return Settings{}, err
	}
	if err := application.certificates.Replace(candidate); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

func putCertificateInput(certificate state.OriginCertificate, mutation CertificateMutation) state.PutOriginCertificateInput {
	return state.PutOriginCertificateInput{
		Certificate: certificate, AuditEventID: mutation.AuditEventID,
		ActorID: mutation.Actor.ID, ActorEmail: mutation.Actor.Email,
		RequestCorrelationID: mutation.CorrelationID, UpdatedAtMillis: mutation.Timestamp.UnixMilli(),
	}
}

func uncoveredHostnames(selector *origin.Selector, hostnames []string) []string {
	uncovered := make([]string, 0)
	for _, hostname := range hostnames {
		if !selector.Covers(hostname) {
			uncovered = append(uncovered, hostname)
		}
	}
	return uncovered
}
