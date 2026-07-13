package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
)

const maximumInputBytes = 1 << 20

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Input struct {
	AdminHostname        string `json:"adminHostname"`
	AutomationHostname   string `json:"automationHostname"`
	AccessTeamDomain     string `json:"accessTeamDomain"`
	AccessAudience       string `json:"accessAudience"`
	ConsolePassphrase    string `json:"consolePassphrase"`
	OriginCertificatePEM string `json:"originCertificatePem"`
	OriginPrivateKeyPEM  string `json:"originPrivateKeyPem"`
}

type ValidatedInput struct {
	AdminHostname        string
	AutomationHostname   *string
	AccessTeamDomain     string
	AccessAudience       string
	ConsolePassphrase    []byte
	OriginCertificatePEM string
	OriginPrivateKeyPEM  []byte
}

func ReadInput(reader io.Reader) (Input, error) {
	decoder := json.NewDecoder(io.LimitReader(reader, maximumInputBytes+1))
	decoder.DisallowUnknownFields()
	var input Input
	if err := decoder.Decode(&input); err != nil {
		return Input{}, fmt.Errorf("decode init input: %w", err)
	}
	if decoder.InputOffset() > maximumInputBytes {
		return Input{}, errors.New("init input exceeds 1 MiB")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Input{}, errors.New("init input contains multiple JSON values")
	}
	return input, nil
}

func ValidateInput(input Input) (ValidatedInput, error) {
	adminHostname, err := normalizeHostname(input.AdminHostname)
	if err != nil {
		return ValidatedInput{}, fmt.Errorf("admin hostname: %w", err)
	}
	teamDomain, audience, err := ValidateAccessConfiguration(input.AccessTeamDomain, input.AccessAudience)
	if err != nil {
		return ValidatedInput{}, err
	}
	passphrase := []byte(input.ConsolePassphrase)
	if len(passphrase) == 0 || len(passphrase) > 1024 {
		clear(passphrase)
		return ValidatedInput{}, errors.New("console passphrase must contain 1..1024 bytes")
	}

	var automationHostname *string
	if strings.TrimSpace(input.AutomationHostname) != "" {
		value, err := normalizeHostname(input.AutomationHostname)
		if err != nil {
			clear(passphrase)
			return ValidatedInput{}, fmt.Errorf("automation hostname: %w", err)
		}
		if value == adminHostname {
			clear(passphrase)
			return ValidatedInput{}, errors.New("admin and automation hostnames must differ")
		}
		automationHostname = &value
	}

	pair, err := tls.X509KeyPair([]byte(input.OriginCertificatePEM), []byte(input.OriginPrivateKeyPEM))
	if err != nil {
		clear(passphrase)
		return ValidatedInput{}, fmt.Errorf("parse Origin certificate/key pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		clear(passphrase)
		return ValidatedInput{}, errors.New("Origin certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		clear(passphrase)
		return ValidatedInput{}, fmt.Errorf("parse Origin leaf certificate: %w", err)
	}
	if err := leaf.VerifyHostname(adminHostname); err != nil {
		clear(passphrase)
		return ValidatedInput{}, fmt.Errorf("Origin certificate does not cover admin hostname: %w", err)
	}
	if automationHostname != nil {
		if err := leaf.VerifyHostname(*automationHostname); err != nil {
			clear(passphrase)
			return ValidatedInput{}, fmt.Errorf("Origin certificate does not cover automation hostname: %w", err)
		}
	}

	return ValidatedInput{
		AdminHostname:        adminHostname,
		AutomationHostname:   automationHostname,
		AccessTeamDomain:     teamDomain,
		AccessAudience:       audience,
		ConsolePassphrase:    passphrase,
		OriginCertificatePEM: input.OriginCertificatePEM,
		OriginPrivateKeyPEM:  []byte(input.OriginPrivateKeyPEM),
	}, nil
}

func ValidateAccessConfiguration(team, audience string) (string, string, error) {
	teamDomain, err := normalizeTeamDomain(team)
	if err != nil {
		return "", "", fmt.Errorf("Access team domain: %w", err)
	}
	audience = strings.TrimSpace(audience)
	if audience == "" || len(audience) > 512 {
		return "", "", errors.New("Access audience must contain 1..512 bytes")
	}
	return teamDomain, audience, nil
}

func normalizeTeamDomain(value string) (string, error) {
	hostname, err := normalizeHostname(value)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(hostname, ".cloudflareaccess.com") || hostname == "cloudflareaccess.com" {
		return "", errors.New("must match <team>.cloudflareaccess.com")
	}
	return hostname, nil
}

func normalizeHostname(value string) (string, error) {
	hostname := strings.ToLower(strings.TrimSpace(value))
	if hostname == "" || len(hostname) > 253 || strings.HasSuffix(hostname, ".") || net.ParseIP(hostname) != nil {
		return "", errors.New("must be a non-empty ASCII DNS hostname without trailing dot")
	}
	labels := strings.Split(hostname, ".")
	if len(labels) < 2 {
		return "", errors.New("must contain at least two DNS labels")
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return "", fmt.Errorf("invalid DNS label %q", label)
		}
	}
	return hostname, nil
}
