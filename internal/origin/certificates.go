package origin

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	certificates []certificate
}

func Load(master cryptobox.MasterKey, values []state.OriginCertificate) (*Selector, error) {
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
	return &Selector{certificates: certificates}, nil
}

func (selector *Selector) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	hostname := strings.ToLower(strings.TrimSpace(hello.ServerName))
	if hostname == "" || strings.HasSuffix(hostname, ".") {
		return nil, errors.New("TLS SNI is required")
	}
	var wildcard *tls.Certificate
	for index := range selector.certificates {
		candidate := &selector.certificates[index]
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

func (selector *Selector) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: selector.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"h2", "http/1.1"},
	}
}
