package cloudflaredns

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

const managedServiceRecordComment = "Managed by platformd service domain"

type DNSStatus string

const (
	DNSStatusUnmanaged DNSStatus = "unmanaged"
	DNSStatusPending   DNSStatus = "pending"
	DNSStatusReady     DNSStatus = "ready"
)

type HostResolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

func (application *Application) EnsureServiceHostname(ctx context.Context, hostname, targetHostname string) (bool, error) {
	targetHostname, err := publichostname.Normalize(targetHostname)
	if err != nil {
		return false, err
	}
	return application.ensureManagedRecord(ctx, hostname, "CNAME", targetHostname)
}

func (application *Application) EnsureServiceHostnameAddress(ctx context.Context, hostname, ipv4 string) (bool, error) {
	if err := state.ValidatePublicIPv4(ipv4); err != nil {
		return false, err
	}
	return application.ensureManagedRecord(ctx, hostname, "A", ipv4)
}

func (application *Application) ensureManagedRecord(ctx context.Context, hostname, recordType, content string) (bool, error) {
	hostname, err := publichostname.Normalize(hostname)
	if err != nil {
		return false, err
	}
	token, err := application.token(ctx)
	if errors.Is(err, state.ErrCloudflareDNSNotConfigured) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer clear(token)
	zone, err := application.zoneForHostname(ctx, string(token), hostname)
	if err != nil {
		return false, err
	}
	records, err := application.records(ctx, string(token), zone.ID, hostname)
	if err != nil {
		return false, err
	}
	var managed *dnsRecord
	for index := range records {
		record := &records[index]
		if record.Comment != managedServiceRecordComment {
			return false, fmt.Errorf("Cloudflare DNS name %s is already in use", hostname)
		}
		if managed != nil {
			return false, fmt.Errorf("Cloudflare DNS name %s has multiple platformd records", hostname)
		}
		managed = record
	}
	body := map[string]any{
		"type": recordType, "name": hostname, "content": content,
		"proxied": true, "ttl": 1, "comment": managedServiceRecordComment,
	}
	if managed == nil {
		if err := application.request(ctx, http.MethodPost, "/zones/"+zone.ID+"/dns_records", string(token), body, nil); err != nil {
			return false, err
		}
		return true, nil
	}
	if managed.Type == recordType && managed.Content == content && managed.Proxied {
		return false, nil
	}
	path := "/zones/" + zone.ID + "/dns_records/" + url.PathEscape(managed.ID)
	if err := application.request(ctx, http.MethodPatch, path, string(token), body, nil); err != nil {
		return false, err
	}
	return false, nil
}

func (application *Application) DeleteServiceHostname(ctx context.Context, hostname string) (bool, error) {
	hostname, err := publichostname.Normalize(hostname)
	if err != nil {
		return false, err
	}
	token, err := application.token(ctx)
	if errors.Is(err, state.ErrCloudflareDNSNotConfigured) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer clear(token)
	zone, err := application.zoneForHostname(ctx, string(token), hostname)
	if err != nil {
		return false, err
	}
	records, err := application.records(ctx, string(token), zone.ID, hostname)
	if err != nil {
		return false, err
	}
	ids := make([]string, 0, 1)
	for _, record := range records {
		if record.Comment == managedServiceRecordComment {
			ids = append(ids, record.ID)
		}
	}
	if err := application.deleteRecords(ctx, string(token), zone.ID, ids); err != nil {
		return false, err
	}
	return len(ids) > 0, nil
}

func (application *Application) ServiceHostnameDNSStatus(ctx context.Context, hostname string) (DNSStatus, error) {
	hostname, err := publichostname.Normalize(hostname)
	if err != nil {
		return "", err
	}
	if _, err := application.repository.CloudflareDNSSettings(ctx); errors.Is(err, state.ErrCloudflareDNSNotConfigured) {
		return DNSStatusUnmanaged, nil
	} else if err != nil {
		return "", err
	}
	// Cloudflare flattens proxied CNAMEs into A/AAAA answers, so public
	// resolution is the observable propagation signal rather than LookupCNAME.
	addresses, err := application.resolver.LookupHost(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return DNSStatusPending, nil
	}
	return DNSStatusReady, nil
}
