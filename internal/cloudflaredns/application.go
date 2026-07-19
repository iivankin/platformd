package cloudflaredns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/publichostname"
	"github.com/iivankin/platformd/internal/state"
)

const managedRecordCommentPrefix = "Managed by platformd PR preview "

type Repository interface {
	CloudflareDNSSettings(context.Context) (state.CloudflareDNSSettings, error)
	PutCloudflareDNSSettings(context.Context, state.PutCloudflareDNSSettingsInput) error
}

type Config struct {
	Repository     Repository
	Master         cryptobox.MasterKey
	InstallationID string
	HTTPClient     *http.Client
	BaseURL        string
}

type Application struct {
	repository Repository
	box        cryptobox.Box
	client     *http.Client
	baseURL    string
}

type Settings struct {
	Configured      bool
	UpdatedAtMillis int64
}

type ConfigureInput struct {
	APIToken        []byte
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func New(config Config) (*Application, error) {
	if config.Repository == nil || config.InstallationID == "" {
		return nil, errors.New("Cloudflare DNS dependencies are incomplete")
	}
	box, err := cryptobox.NewBox(config.Master, []byte(config.InstallationID), "platformd/cloudflare-dns/v1")
	if err != nil {
		return nil, err
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.cloudflare.com/client/v4"
	}
	return &Application{repository: config.Repository, box: box, client: client, baseURL: baseURL}, nil
}

func (application *Application) Settings(ctx context.Context) (Settings, error) {
	stored, err := application.repository.CloudflareDNSSettings(ctx)
	if errors.Is(err, state.ErrCloudflareDNSNotConfigured) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{Configured: true, UpdatedAtMillis: stored.UpdatedAtMillis}, nil
}

func (application *Application) Configure(ctx context.Context, input ConfigureInput) (Settings, error) {
	token := bytes.TrimSpace(input.APIToken)
	if len(token) < 20 || input.AuditEventID == "" || input.ActorID == "" || input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return Settings{}, errors.New("Cloudflare API token configuration is incomplete")
	}
	var verification struct {
		Status string `json:"status"`
	}
	if err := application.request(ctx, http.MethodGet, "/user/tokens/verify", string(token), nil, &verification); err != nil {
		return Settings{}, fmt.Errorf("verify Cloudflare API token: %w", err)
	}
	if verification.Status != "active" {
		return Settings{}, errors.New("Cloudflare API token is not active")
	}
	sealed, err := application.box.Seal(token, []byte("api-token"))
	if err != nil {
		return Settings{}, err
	}
	if err := application.repository.PutCloudflareDNSSettings(ctx, state.PutCloudflareDNSSettingsInput{
		Settings:     state.CloudflareDNSSettings{APITokenEncrypted: sealed},
		AuditEventID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
		CorrelationID: input.CorrelationID, UpdatedAtMillis: input.UpdatedAtMillis,
	}); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

func (application *Application) token(ctx context.Context) ([]byte, error) {
	stored, err := application.repository.CloudflareDNSSettings(ctx)
	if err != nil {
		return nil, err
	}
	return application.box.Open(stored.APITokenEncrypted, []byte("api-token"))
}

type zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dnsRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment"`
}

// EnsurePreviewHostname clones the public routing records of the service's
// only HTTP domain. This works for both tunnel CNAMEs and direct A/AAAA origins
// without storing a second, potentially stale ingress target in platformd.
func (application *Application) EnsurePreviewHostname(ctx context.Context, canonicalHostname, previewHostname, previewID string) ([]string, error) {
	if strings.TrimSpace(previewID) == "" {
		return nil, errors.New("PR preview ID is required")
	}
	canonical, err := publichostname.Normalize(canonicalHostname)
	if err != nil {
		return nil, err
	}
	preview, err := publichostname.Normalize(previewHostname)
	if err != nil {
		return nil, err
	}
	token, err := application.token(ctx)
	if err != nil {
		return nil, err
	}
	defer clear(token)
	zone, err := application.zoneForHostname(ctx, string(token), preview)
	if err != nil {
		return nil, err
	}
	if canonical != zone.Name && !strings.HasSuffix(canonical, "."+zone.Name) {
		return nil, errors.New("service and preview hostnames must belong to the same Cloudflare zone")
	}
	source, err := application.records(ctx, string(token), zone.ID, canonical)
	if err != nil {
		return nil, err
	}
	supported := make([]dnsRecord, 0, len(source))
	for _, record := range source {
		if record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME" {
			supported = append(supported, record)
		}
	}
	if len(supported) == 0 {
		return nil, fmt.Errorf("Cloudflare has no A, AAAA, or CNAME record for %s", canonical)
	}
	existing, err := application.records(ctx, string(token), zone.ID, preview)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		expectedComment := managedRecordCommentPrefix + previewID
		ids := matchingManagedRecordIDs(existing, supported, expectedComment)
		if len(existing) == len(supported) && len(ids) == len(supported) {
			return ids, nil
		}
		staleIDs := make([]string, 0, len(existing))
		for _, record := range existing {
			if record.Comment != expectedComment {
				return nil, fmt.Errorf("Cloudflare DNS name %s is already in use", preview)
			}
			staleIDs = append(staleIDs, record.ID)
		}
		if err := application.deleteRecords(ctx, string(token), zone.ID, staleIDs); err != nil {
			return nil, fmt.Errorf("replace stale PR preview DNS records: %w", err)
		}
	}
	created := make([]string, 0, len(supported))
	for _, record := range supported {
		var result dnsRecord
		err := application.request(ctx, http.MethodPost, "/zones/"+zone.ID+"/dns_records", string(token), map[string]any{
			"type": record.Type, "name": preview, "content": record.Content,
			"proxied": record.Proxied, "ttl": 1, "comment": managedRecordCommentPrefix + previewID,
		}, &result)
		if err != nil {
			_ = application.deleteRecords(ctx, string(token), zone.ID, created)
			return nil, err
		}
		created = append(created, result.ID)
	}
	return created, nil
}

func matchingManagedRecordIDs(existing, source []dnsRecord, expectedComment string) []string {
	ids := make([]string, 0, len(source))
	for _, expected := range source {
		matched := ""
		for _, candidate := range existing {
			if candidate.Comment == expectedComment && candidate.Type == expected.Type &&
				candidate.Content == expected.Content && candidate.Proxied == expected.Proxied {
				matched = candidate.ID
				break
			}
		}
		if matched == "" {
			return nil
		}
		ids = append(ids, matched)
	}
	sort.Strings(ids)
	return ids
}

func (application *Application) DeletePreviewHostname(ctx context.Context, hostname string, recordIDs []string) error {
	if len(recordIDs) == 0 {
		return nil
	}
	normalized, err := publichostname.Normalize(hostname)
	if err != nil {
		return err
	}
	token, err := application.token(ctx)
	if err != nil {
		return err
	}
	defer clear(token)
	zone, err := application.zoneForHostname(ctx, string(token), normalized)
	if err != nil {
		return err
	}
	return application.deleteRecords(ctx, string(token), zone.ID, recordIDs)
}

func (application *Application) deleteRecords(ctx context.Context, token, zoneID string, recordIDs []string) error {
	var result error
	for _, recordID := range recordIDs {
		if recordID == "" {
			continue
		}
		if err := application.request(ctx, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+url.PathEscape(recordID), token, nil, nil); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (application *Application) zoneForHostname(ctx context.Context, token, hostname string) (zone, error) {
	var zones []zone
	for page := 1; page <= 5; page++ {
		var response []zone
		path := fmt.Sprintf("/zones?status=active&per_page=50&page=%d", page)
		if err := application.request(ctx, http.MethodGet, path, token, nil, &response); err != nil {
			return zone{}, err
		}
		zones = append(zones, response...)
		if len(response) < 50 {
			break
		}
	}
	var selected zone
	for _, candidate := range zones {
		name, err := publichostname.Normalize(candidate.Name)
		if err != nil || (hostname != name && !strings.HasSuffix(hostname, "."+name)) {
			continue
		}
		if len(name) > len(selected.Name) {
			selected = candidate
			selected.Name = name
		}
	}
	if selected.ID == "" {
		return zone{}, fmt.Errorf("no accessible Cloudflare zone covers %s", hostname)
	}
	return selected, nil
}

func (application *Application) records(ctx context.Context, token, zoneID, hostname string) ([]dnsRecord, error) {
	var records []dnsRecord
	path := "/zones/" + zoneID + "/dns_records?name=" + url.QueryEscape(hostname) + "&per_page=100"
	if err := application.request(ctx, http.MethodGet, path, token, nil, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (application *Application) request(ctx context.Context, method, path, token string, body, destination any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, application.baseURL+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := application.client.Do(request)
	if err != nil {
		return fmt.Errorf("Cloudflare API request: %w", err)
	}
	defer response.Body.Close()
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("decode Cloudflare API response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		message := http.StatusText(response.StatusCode)
		if len(envelope.Errors) > 0 {
			message = envelope.Errors[0].Message
		}
		return fmt.Errorf("Cloudflare API %s %s returned %d: %s", method, path, response.StatusCode, message)
	}
	if destination != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, destination); err != nil {
			return fmt.Errorf("decode Cloudflare API result: %w", err)
		}
	}
	return nil
}
