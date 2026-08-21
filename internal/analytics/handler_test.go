package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestReservedPaths(t *testing.T) {
	t.Parallel()
	if !Reserved(http.MethodPost, "/analytics/e") {
		t.Fatal("event path should be reserved")
	}
	if !Reserved(http.MethodPost, "/ofrep/v1/evaluate/flags") || !Reserved(http.MethodPost, "/ofrep/v1/evaluate/flags/pricing-v2") {
		t.Fatal("OFREP paths should be reserved")
	}
	if Reserved(http.MethodGet, "/analytics.js") || Reserved(http.MethodGet, "/") || Reserved(http.MethodPost, "/api/1/envelope/") {
		t.Fatal("application paths must not be reserved")
	}
}

func TestPublicHandlerStripsSpoofedTrackerHeader(t *testing.T) {
	t.Parallel()
	catalog := &catalogStub{trackers: []state.AnalyticsTracker{{
		ID: "tracker-shop", ProjectID: "project", RootDomain: "shop.example",
	}}}
	target, captured, client := ingestCapture(t)
	handler := NewHandler(catalog, client, target, nil)
	request := httptest.NewRequest(http.MethodPost, "https://shop.example/analytics/e", strings.NewReader(`{"n":"custom","u":"https://shop.example/"}`))
	request.Host = "shop.example"
	request.Header.Set(TrackerHeader, "spoofed")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: AidCookie, Value: "aid-1"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	events := captured.snapshot()
	if len(events) != 1 || events[0].TrackerID != "tracker-shop" {
		t.Fatalf("events = %+v", events)
	}
}

func TestPublicHandler404WithoutTracker(t *testing.T) {
	t.Parallel()
	handler := NewHandler(&catalogStub{}, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "https://unknown.example/analytics/e", strings.NewReader(`{"n":"custom"}`))
	request.Host = "unknown.example"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestPublicEventWithoutAIDUsesCookielessIdentity(t *testing.T) {
	t.Parallel()
	target, captured, client := ingestCapture(t)
	handler := NewHandler(shopCatalog(), client, target, nil)
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	handler.Now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodPost, "https://shop.example/analytics/e", strings.NewReader(`{"n":"$pageview","u":"https://shop.example/","s":"spoofed"}`))
	request.Host = "shop.example"
	request.RemoteAddr = "10.0.0.1:443"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	events := captured.snapshot()
	wantID := DailyHash("tracker-shop", "install", "10.0.0.1", "Mozilla/5.0", now)
	if len(events) != 1 || events[0].DistinctID != wantID || events[0].SessionID != "" {
		t.Fatalf("cookieless event = %+v", events)
	}
}

func TestPublicOFREPRequiresAIDForTargeting(t *testing.T) {
	t.Parallel()
	catalog := &catalogStub{
		trackers: []state.AnalyticsTracker{{
			ID: "tracker-shop", ProjectID: "project", RootDomain: "shop.example",
		}},
		flags: []state.AnalyticsFlag{{
			ID: "flag-1", TrackerID: "tracker-shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
			Variants:      []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
			TargetingJSON: `{"groups":[{"properties":[],"rollout_percentage":100}]}`,
		}},
	}
	handler := NewHandler(catalog, nil, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "https://shop.example/ofrep/v1/evaluate/flags/pricing-v2", strings.NewReader(`{"context":{}}`))
	request.Host = "shop.example"
	request.RemoteAddr = "10.0.0.1:443"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	var body struct {
		Reason  string `json:"reason"`
		Variant string `json:"variant"`
		Value   bool   `json:"value"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Reason != "DEFAULT" || body.Value || body.Variant != "false" {
		t.Fatalf("OFREP without AID = %+v", body)
	}

	identified := httptest.NewRequest(http.MethodPost, "https://shop.example/ofrep/v1/evaluate/flags/pricing-v2", strings.NewReader(`{"context":{}}`))
	identified.Host = "shop.example"
	identified.Header.Set("Content-Type", "application/json")
	identified.AddCookie(&http.Cookie{Name: AidCookie, Value: "aid-1"})
	identifiedResponse := httptest.NewRecorder()
	handler.ServeHTTP(identifiedResponse, identified)
	if identifiedResponse.Code != http.StatusOK {
		t.Fatalf("identified status = %d body=%s", identifiedResponse.Code, identifiedResponse.Body)
	}
	if err := json.NewDecoder(identifiedResponse.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Reason != "TARGETING_MATCH" || !body.Value || body.Variant != "true" {
		t.Fatalf("OFREP with AID = %+v", body)
	}
}

func TestObserveDocumentOnlyRecordsVerifiedBotIP(t *testing.T) {
	t.Parallel()
	target, captured, client := ingestCapture(t)
	handler := NewHandler(shopCatalog(), client, target, botIPVerifierStub{
		botName: "OAI-SearchBot", address: "203.0.113.7",
	})

	spoofed := httptest.NewRequest(http.MethodGet, "https://shop.example/.git/config", nil)
	spoofed.Host = "shop.example"
	spoofed.Header.Set("User-Agent", "OAI-SearchBot")
	spoofed.Header.Set("CF-Connecting-IP", "198.51.100.9")
	spoofed.Header.Set("X-Forwarded-For", "203.0.113.7")
	handler.ObserveDocument(spoofed, "service-shop")
	if captured.len() != 0 {
		t.Fatalf("spoofed bot events = %+v", captured.snapshot())
	}

	verified := httptest.NewRequest(http.MethodGet, "https://shop.example/products", nil)
	verified.Host = "shop.example"
	verified.Header.Set("User-Agent", "OAI-SearchBot")
	verified.Header.Set("CF-Connecting-IP", "203.0.113.7")
	handler.ObserveDocument(verified, "service-shop")
	events := captured.snapshot()
	if len(events) != 1 || events[0].BotName != "OAI-SearchBot" || events[0].Pathname != "/products" {
		t.Fatalf("verified bot events = %+v", events)
	}
}

func TestInternalOFREP304DoesNotRecordExposure(t *testing.T) {
	t.Parallel()
	target, captured, client := ingestCapture(t)
	catalog := internalOFREPCatalog()
	handler := InternalHandler(catalog, client, target)
	host := InternalAnalyticsHostname("shop", state.TrackerSlug("shop.example"))
	body := `{"context":{"targetingKey":"aid-user"}}`

	first := httptest.NewRequest(http.MethodPost, "http://"+host+"/ofrep/v1/evaluate/flags/pricing-v2", strings.NewReader(body))
	first.Host = host
	first.Header.Set("Content-Type", "application/json")
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("first status = %d body=%s", firstResponse.Code, firstResponse.Body)
	}
	etag := firstResponse.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	if captured.len() != 1 {
		t.Fatalf("first exposure count = %d", captured.len())
	}

	second := httptest.NewRequest(http.MethodPost, "http://"+host+"/ofrep/v1/evaluate/flags/pricing-v2", strings.NewReader(body))
	second.Host = host
	second.Header.Set("Content-Type", "application/json")
	second.Header.Set("If-None-Match", etag)
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusNotModified {
		t.Fatalf("second status = %d body=%s", secondResponse.Code, secondResponse.Body)
	}
	if captured.len() != 1 {
		t.Fatalf("304 must not ingest another $flag_called: %d", captured.len())
	}
}

func TestInternalOFREPUnknownFlagIs404WithETag(t *testing.T) {
	t.Parallel()
	catalog := internalOFREPCatalog()
	handler := InternalHandler(catalog, nil, nil)
	host := InternalAnalyticsHostname("shop", state.TrackerSlug("shop.example"))
	known := httptest.NewRequest(http.MethodPost, "http://"+host+"/ofrep/v1/evaluate/flags/pricing-v2", strings.NewReader(`{"context":{"targetingKey":"aid-user"}}`))
	known.Host = host
	known.Header.Set("Content-Type", "application/json")
	knownResponse := httptest.NewRecorder()
	handler.ServeHTTP(knownResponse, known)
	etag := knownResponse.Header().Get("ETag")
	if knownResponse.Code != http.StatusOK || etag == "" {
		t.Fatalf("known status = %d etag=%q", knownResponse.Code, etag)
	}

	missing := httptest.NewRequest(http.MethodPost, "http://"+host+"/ofrep/v1/evaluate/flags/missing", strings.NewReader(`{"context":{"targetingKey":"aid-user"}}`))
	missing.Host = host
	missing.Header.Set("Content-Type", "application/json")
	missing.Header.Set("If-None-Match", etag)
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing flag status = %d", missingResponse.Code)
	}
}

func internalOFREPCatalog() *catalogStub {
	return &catalogStub{
		trackers: []state.AnalyticsTracker{{
			ID: "tracker-shop", ProjectID: "project", RootDomain: "shop.example",
		}},
		flags: []state.AnalyticsFlag{{
			ID: "flag-1", TrackerID: "tracker-shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
			Variants:      []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
			TargetingJSON: `{"groups":[{"properties":[],"rollout_percentage":100}]}`,
		}},
		experiments: []state.AnalyticsExperiment{{
			ID: "exp-1", FlagID: "flag-1", TrackerID: "tracker-shop", ControlVariant: "false",
			StartedAtMillis: 1,
		}},
	}
}

type catalogStub struct {
	trackers    []state.AnalyticsTracker
	flags       []state.AnalyticsFlag
	experiments []state.AnalyticsExperiment
}

type botIPVerifierStub struct {
	botName string
	address string
}

func (verifier botIPVerifierStub) Verify(botName, address string) bool {
	return botName == verifier.botName && address == verifier.address
}

func (catalog *catalogStub) AllAnalyticsTrackers(context.Context) ([]state.AnalyticsTracker, error) {
	return catalog.trackers, nil
}

func (catalog *catalogStub) AnalyticsTrackerByID(_ context.Context, id string) (state.AnalyticsTracker, error) {
	for _, tracker := range catalog.trackers {
		if tracker.ID == id {
			return tracker, nil
		}
	}
	return state.AnalyticsTracker{}, io.EOF
}

func (catalog *catalogStub) AnalyticsFlags(_ context.Context, trackerID string) ([]state.AnalyticsFlag, error) {
	flags := make([]state.AnalyticsFlag, 0)
	for _, flag := range catalog.flags {
		if flag.TrackerID == trackerID {
			flags = append(flags, flag)
		}
	}
	return flags, nil
}

func (catalog *catalogStub) AnalyticsFlagByKey(_ context.Context, trackerID, key string) (state.AnalyticsFlag, error) {
	for _, flag := range catalog.flags {
		if flag.TrackerID == trackerID && flag.Key == key {
			return flag, nil
		}
	}
	return state.AnalyticsFlag{}, io.EOF
}

func (catalog *catalogStub) RunningAnalyticsExperiment(_ context.Context, flagID string, atMillis int64) (state.AnalyticsExperiment, error) {
	for _, experiment := range catalog.experiments {
		if experiment.FlagID != flagID {
			continue
		}
		if experiment.StartedAtMillis > atMillis {
			continue
		}
		if experiment.EndedAtMillis != 0 && experiment.EndedAtMillis < atMillis {
			continue
		}
		return experiment, nil
	}
	return state.AnalyticsExperiment{}, io.EOF
}

func (*catalogStub) Project(context.Context, string) (state.ProjectSummary, error) {
	return state.ProjectSummary{Name: "shop"}, nil
}

func (*catalogStub) ServiceIDByHostname(context.Context, string) (string, error) {
	return "", nil
}

func (*catalogStub) Installation(context.Context) (state.Installation, error) {
	return state.Installation{ID: "install"}, nil
}
