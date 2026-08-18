package analytics

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestBrowserAnalyticsProtocol(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	catalog := shopCatalog()
	target, captured, client := ingestCapture(t)
	handler := NewHandler(catalog, client, target)
	handler.Now = func() time.Time { return now }

	script := httptest.NewRequest(http.MethodGet, "https://shop.example/analytics.js", nil)
	script.Host = "shop.example"
	scriptResponse := httptest.NewRecorder()
	handler.ServeHTTP(scriptResponse, script)
	if scriptResponse.Code != http.StatusOK {
		t.Fatalf("script status = %d", scriptResponse.Code)
	}
	source := scriptResponse.Body.String()
	for _, fragment := range []string{
		"window.platformd",
		"heatmapClick",
		"$heatmap",
		"platformd:consent",
		"navigator.sendBeacon('/analytics/e'",
		"wrapHistory('replaceState')",
		"location.protocol==='https:'",
		"TARGETING_MATCH",
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("script missing %q", fragment)
		}
	}
	if strings.Contains(source, "function identify") || strings.Contains(source, "identify:identify") {
		t.Fatal("script must not expose identify")
	}

	pageview := postBrowser(handler, `{
		"n":"$pageview","u":"https://shop.example/pricing?utm_source=google","t":"Pricing",
		"r":"https://www.google.com/","w":"1440x900","l":"en-US",
		"p":{"interactive":true},"i":true,"s":"sid-1"
	}`, nil)
	if pageview.Code != http.StatusNoContent {
		t.Fatalf("pageview status = %d", pageview.Code)
	}
	if captured.len() != 1 {
		t.Fatalf("pageview ingest count = %d", captured.len())
	}
	first := captured.snapshot()[0]
	if first.EventName != "$pageview" || first.DistinctID != "aid-1" || first.SessionID != "sid-1" {
		t.Fatalf("pageview identity = %+v", first)
	}
	if first.Pathname != "/pricing" || first.UTMSource != "google" || first.Interactive != 1 {
		t.Fatalf("pageview fields = %+v", first)
	}
	if first.Timestamp != "2026-08-18T12:00:00.000Z" {
		t.Fatalf("rfc3339 timestamp = %q", first.Timestamp)
	}

	heatmap := postBrowser(handler, `{
		"n":"$heatmap","u":"https://shop.example/","t":"Home","r":"","w":"1440x900","l":"en-US",
		"p":{"x":48,"y":32,"viewport_w":1440,"viewport_h":900,"page_h":2400,"event_type":"click"},
		"i":false,"s":"sid-1"
	}`, nil)
	if heatmap.Code != http.StatusNoContent {
		t.Fatalf("heatmap status = %d", heatmap.Code)
	}
	second := captured.snapshot()[1]
	if second.EventName != "$heatmap" || propValue(second, "event_type") != "click" || propValue(second, "x") != "48" {
		t.Fatalf("heatmap event = %+v keys=%v values=%v", second, second.PropsKeys, second.PropsValues)
	}
	if first.EventID == "" || first.EventID == second.EventID {
		t.Fatalf("event ids must be hashed and distinct: %q %q", first.EventID, second.EventID)
	}

	flag := postBrowser(handler, `{
		"n":"$flag_called","u":"https://shop.example/","t":"Home","r":"","w":"1440x900","l":"en-US",
		"p":{"flag":"pricing-v2","variant":"true"},"i":false,"s":"sid-1"
	}`, nil)
	if flag.Code != http.StatusNoContent {
		t.Fatalf("flag status = %d", flag.Code)
	}
	if captured.snapshot()[2].EventName != "$flag_called" {
		t.Fatalf("flag event = %+v", captured.snapshot()[2])
	}
	if propValue(captured.snapshot()[2], "experiment_id") != "exp-1" {
		t.Fatalf("flag experiment_id = %q", propValue(captured.snapshot()[2], "experiment_id"))
	}

	spoof := postBrowser(handler, `{
		"n":"$flag_called","u":"https://shop.example/","t":"Home","r":"","w":"1440x900","l":"en-US",
		"p":{"flag":"pricing-v2","variant":"true","experiment_id":"forged"},"i":false,"s":"sid-1"
	}`, nil)
	if spoof.Code != http.StatusNoContent {
		t.Fatalf("spoofed flag status = %d", spoof.Code)
	}
	if propValue(captured.snapshot()[3], "experiment_id") != "exp-1" {
		t.Fatalf("client experiment_id must be replaced: %q", propValue(captured.snapshot()[3], "experiment_id"))
	}

	beforeGPC := captured.len()
	gpc := postBrowser(handler, `{"n":"$pageview","u":"https://shop.example/","s":"sid-1"}`, map[string]string{"Sec-GPC": "1"})
	if gpc.Code != http.StatusNoContent {
		t.Fatalf("gpc status = %d", gpc.Code)
	}
	if captured.len() != beforeGPC {
		t.Fatal("GPC must drop the browser event before ingest")
	}

	unknown := postBrowser(handler, `{
		"n":"$flag_called","u":"https://shop.example/","p":{"flag":"pricing-v2","variant":"nope"},"s":"sid-1"
	}`, nil)
	if unknown.Code != http.StatusNoContent || captured.len() != beforeGPC {
		t.Fatalf("unknown variant ingested: status=%d count=%d", unknown.Code, captured.len())
	}

	ofrep := httptest.NewRequest(http.MethodPost, "https://shop.example/ofrep/v1/evaluate/flags", strings.NewReader(`{"context":{}}`))
	ofrep.Host = "shop.example"
	ofrep.Header.Set("Content-Type", "application/json")
	ofrep.AddCookie(&http.Cookie{Name: AidCookie, Value: "aid-1"})
	ofrepResponse := httptest.NewRecorder()
	handler.ServeHTTP(ofrepResponse, ofrep)
	if ofrepResponse.Code != http.StatusOK {
		t.Fatalf("ofrep status = %d body=%s", ofrepResponse.Code, ofrepResponse.Body)
	}
	if captured.len() != beforeGPC {
		t.Fatalf("public OFREP must not ingest $flag_called: %+v", captured.snapshot())
	}
}

func shopCatalog() *catalogStub {
	return &catalogStub{
		trackers: []state.AnalyticsTracker{{
			ID: "tracker-shop", ProjectID: "project", RootDomain: "shop.example", Mode: state.AnalyticsModeOptOut,
		}},
		flags: []state.AnalyticsFlag{{
			ID: "flag-1", TrackerID: "tracker-shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
			Variants:      []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
			TargetingJSON: `{"groups":[{"properties":[],"rollout_percentage":100}]}`,
		}},
		experiments: []state.AnalyticsExperiment{{
			ID: "exp-1", FlagID: "flag-1", TrackerID: "tracker-shop", StartedAtMillis: 1,
		}},
	}
}

func ingestCapture(t *testing.T) (*url.URL, *ingestLog, *http.Client) {
	t.Helper()
	log := &ingestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/internal/analytics/ingest" {
			http.NotFound(response, request)
			return
		}
		var event Event
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			http.Error(response, err.Error(), http.StatusBadRequest)
			return
		}
		log.add(event)
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed, log, server.Client()
}

type ingestLog struct {
	mu     sync.Mutex
	events []Event
}

func (log *ingestLog) add(event Event) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *ingestLog) len() int {
	log.mu.Lock()
	defer log.mu.Unlock()
	return len(log.events)
}

func (log *ingestLog) snapshot() []Event {
	log.mu.Lock()
	defer log.mu.Unlock()
	copied := make([]Event, len(log.events))
	copy(copied, log.events)
	return copied
}

func (log *ingestLog) waitNamed(t *testing.T, name string, n int) []Event {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		events := namedEvents(log.snapshot(), name)
		if len(events) >= n {
			return log.snapshot()
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s events: %+v", n, name, log.snapshot())
	return nil
}

func namedEvents(events []Event, name string) []Event {
	found := make([]Event, 0)
	for _, event := range events {
		if event.EventName == name {
			found = append(found, event)
		}
	}
	return found
}

func postBrowser(handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "https://shop.example/analytics/e", strings.NewReader(body))
	request.Host = "shop.example"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120.0.0.0")
	request.AddCookie(&http.Cookie{Name: AidCookie, Value: "aid-1"})
	request.AddCookie(&http.Cookie{Name: SidCookie, Value: "sid-1"})
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func propValue(event Event, key string) string {
	for index, name := range event.PropsKeys {
		if name == key && index < len(event.PropsValues) {
			return event.PropsValues[index]
		}
	}
	return ""
}
