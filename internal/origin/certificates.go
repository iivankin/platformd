package origin

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

const privateKeyDomain = "platformd/sqlite/origin-certificate/v1"

type certificate struct {
	id    string
	exact map[string]struct{}
	value tls.Certificate
}

type Selector struct {
	current atomic.Pointer[certificateSnapshot]
}

type certificateSnapshot struct {
	certificates []certificate
}

func Load(master cryptobox.MasterKey, values []state.OriginCertificate) (*Selector, error) {
	snapshot, err := loadSnapshot(master, values)
	if err != nil {
		return nil, err
	}
	selector := &Selector{}
	selector.current.Store(snapshot)
	return selector, nil
}

func loadSnapshot(master cryptobox.MasterKey, values []state.OriginCertificate) (*certificateSnapshot, error) {
	if len(values) == 0 {
		return nil, errors.New("installation has no Origin certificates")
	}
	certificates := make([]certificate, 0, len(values))
	for _, value := range values {
		box, err := cryptobox.NewBox(master, []byte(value.ID), privateKeyDomain)
		if err != nil {
			return nil, err
		}
		privateKey, err := box.Open(value.PrivateKeyEncrypted, []byte(value.ID+":private-key"))
		if err != nil {
			return nil, fmt.Errorf("decrypt Origin certificate %s: %w", value.ID, err)
		}
		pair, err := tls.X509KeyPair([]byte(value.CertificatePEM), privateKey)
		clear(privateKey)
		if err != nil {
			return nil, fmt.Errorf("load Origin certificate %s: %w", value.ID, err)
		}
		if len(pair.Certificate) == 0 {
			return nil, fmt.Errorf("Origin certificate %s has no leaf", value.ID)
		}
		pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parse Origin certificate %s leaf: %w", value.ID, err)
		}
		exact := make(map[string]struct{}, len(pair.Leaf.DNSNames))
		for _, name := range pair.Leaf.DNSNames {
			if !strings.Contains(name, "*") {
				exact[strings.ToLower(name)] = struct{}{}
			}
		}
		certificates = append(certificates, certificate{id: value.ID, exact: exact, value: pair})
	}
	sort.Slice(certificates, func(left, right int) bool { return certificates[left].id < certificates[right].id })
	return &certificateSnapshot{certificates: certificates}, nil
}

// Replace atomically publishes an already validated certificate snapshot.
// Existing TLS handshakes keep their selected certificate; new handshakes see
// either the complete old or complete new set.
func (selector *Selector) Replace(replacement *Selector) error {
	if selector == nil || replacement == nil || replacement.current.Load() == nil {
		return errors.New("replacement Origin certificate selector is unavailable")
	}
	selector.current.Store(replacement.current.Load())
	return nil
}

func (selector *Selector) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	hostname := strings.ToLower(strings.TrimSpace(hello.ServerName))
	if hostname == "" || strings.HasSuffix(hostname, ".") {
		return nil, errors.New("TLS SNI is required")
	}
	snapshot := selector.current.Load()
	if snapshot == nil {
		return nil, errors.New("Origin certificate selector is unavailable")
	}
	var wildcard *tls.Certificate
	for index := range snapshot.certificates {
		candidate := &snapshot.certificates[index]
		if err := candidate.value.Leaf.VerifyHostname(hostname); err != nil {
			continue
		}
		if _, ok := candidate.exact[hostname]; ok {
			return &candidate.value, nil
		}
		if wildcard == nil {
			wildcard = &candidate.value
		}
	}
	if wildcard != nil {
		return wildcard, nil
	}
	return nil, fmt.Errorf("no Origin certificate covers SNI %q", hostname)
}

func EncryptCertificate(
	master cryptobox.MasterKey,
	id string,
	certificatePEM string,
	privateKeyPEM []byte,
	random io.Reader,
	createdAtMillis int64,
) (state.OriginCertificate, []string, error) {
	if id == "" || certificatePEM == "" || len(privateKeyPEM) == 0 || random == nil || createdAtMillis <= 0 {
		return state.OriginCertificate{}, nil, errors.New("Origin certificate input is incomplete")
	}
	pair, err := tls.X509KeyPair([]byte(certificatePEM), privateKeyPEM)
	if err != nil {
		return state.OriginCertificate{}, nil, fmt.Errorf("parse Origin certificate/key pair: %w", err)
	}
	if len(pair.Certificate) == 0 {
		return state.OriginCertificate{}, nil, errors.New("Origin certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return state.OriginCertificate{}, nil, fmt.Errorf("parse Origin certificate leaf: %w", err)
	}
	names := append([]string(nil), leaf.DNSNames...)
	for index := range names {
		names[index] = strings.ToLower(names[index])
	}
	sort.Strings(names)
	box, err := cryptobox.NewBox(master, []byte(id), privateKeyDomain)
	if err != nil {
		return state.OriginCertificate{}, nil, err
	}
	encrypted, err := box.SealWith(random, privateKeyPEM, []byte(id+":private-key"))
	if err != nil {
		return state.OriginCertificate{}, nil, err
	}
	return state.OriginCertificate{
		ID: id, CertificatePEM: certificatePEM, PrivateKeyEncrypted: encrypted,
		CreatedAtMillis: createdAtMillis,
	}, names, nil
}

func DNSNames(certificatePEM string) ([]string, error) {
	block, _ := pem.Decode([]byte(certificatePEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("Origin certificate PEM has no leaf certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	names := append([]string(nil), leaf.DNSNames...)
	for index := range names {
		names[index] = strings.ToLower(names[index])
	}
	sort.Strings(names)
	return names, nil
}

func (selector *Selector) Covers(hostname string) bool {
	_, err := selector.GetCertificate(&tls.ClientHelloInfo{ServerName: hostname})
	return err == nil
}

func (selector *Selector) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: selector.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
	}
}
