package analytics

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

const maximumEventBytes = 64 << 10

type Event struct {
	EventID        string   `json:"event_id"`
	TrackerID      string   `json:"tracker_id"`
	ServiceID      string   `json:"service_id"`
	DistinctID     string   `json:"distinct_id"`
	SessionID      string   `json:"session_id"`
	EventName      string   `json:"event_name"`
	Timestamp      string   `json:"timestamp"`
	Hostname       string   `json:"hostname"`
	Pathname       string   `json:"pathname"`
	PageTitle      string   `json:"page_title"`
	Referrer       string   `json:"referrer"`
	ReferrerDomain string   `json:"referrer_domain"`
	ReferrerSource string   `json:"referrer_source"`
	Channel        string   `json:"channel"`
	UTMSource      string   `json:"utm_source"`
	UTMMedium      string   `json:"utm_medium"`
	UTMCampaign    string   `json:"utm_campaign"`
	UTMContent     string   `json:"utm_content"`
	UTMTerm        string   `json:"utm_term"`
	Gclid          string   `json:"gclid"`
	Fbclid         string   `json:"fbclid"`
	Msclkid        string   `json:"msclkid"`
	Ttclid         string   `json:"ttclid"`
	LiFatID        string   `json:"li_fat_id"`
	Twclid         string   `json:"twclid"`
	Browser        string   `json:"browser"`
	BrowserVersion string   `json:"browser_version"`
	OS             string   `json:"os"`
	OSVersion      string   `json:"os_version"`
	Device         string   `json:"device"`
	Screen         string   `json:"screen"`
	Language       string   `json:"language"`
	Country        string   `json:"country"`
	Region         string   `json:"region"`
	City           string   `json:"city"`
	Interactive    uint8    `json:"interactive"`
	PropsKeys      []string `json:"props_keys"`
	PropsValues    []string `json:"props_values"`
	Revenue        *float64 `json:"revenue"`
	Currency       string   `json:"currency"`
	BotKind        string   `json:"bot_kind"`
	BotName        string   `json:"bot_name"`
	LCP            *float64 `json:"lcp"`
	INP            *float64 `json:"inp"`
	CLS            *float64 `json:"cls"`
	FCP            *float64 `json:"fcp"`
	TTFB           *float64 `json:"ttfb"`
}

type Catalog interface {
	AllAnalyticsTrackers(context.Context) ([]state.AnalyticsTracker, error)
	AnalyticsTrackerByID(context.Context, string) (state.AnalyticsTracker, error)
	AnalyticsFlags(context.Context, string) ([]state.AnalyticsFlag, error)
	AnalyticsFlagByKey(context.Context, string, string) (state.AnalyticsFlag, error)
	RunningAnalyticsExperiment(context.Context, string, int64) (state.AnalyticsExperiment, error)
	Project(context.Context, string) (state.ProjectSummary, error)
	ServiceIDByHostname(context.Context, string) (string, error)
	Installation(context.Context) (state.Installation, error)
}

type Transport interface {
	Do(*http.Request) (*http.Response, error)
}

type Handler struct {
	Catalog  Catalog
	Client   Transport
	Target   *url.URL
	Now      func() time.Time
	internal bool
}

func NewHandler(catalog Catalog, client Transport, target *url.URL) *Handler {
	return &Handler{Catalog: catalog, Client: client, Target: target, Now: time.Now}
}

func InternalHandler(catalog Catalog, client Transport, target *url.URL) *Handler {
	handler := NewHandler(catalog, client, target)
	handler.internal = true
	return handler
}

func Reserved(method, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if method == http.MethodGet && path == "/analytics.js" {
		return true
	}
	if method == http.MethodPost && path == "/analytics/e" {
		return true
	}
	if method == http.MethodPost && (path == "/ofrep/v1/evaluate/flags" || strings.HasPrefix(path, "/ofrep/v1/evaluate/flags/")) {
		return true
	}
	return false
}

func InternalAnalyticsHostname(projectName, slug string) string {
	return "analytics-" + slug + "." + projectName + ".internal"
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !handler.internal {
		request.Header.Del(TrackerHeader)
	}
	if !Reserved(request.Method, request.URL.Path) {
		http.NotFound(response, request)
		return
	}
	tracker, ok := handler.lookupTracker(request)
	if !ok {
		http.NotFound(response, request)
		return
	}
	request.Header.Set(TrackerHeader, tracker.ID)
	switch {
	case request.Method == http.MethodGet && strings.TrimSuffix(request.URL.Path, "/") == "/analytics.js":
		handler.serveScript(response, tracker)
	case request.Method == http.MethodPost && strings.TrimSuffix(request.URL.Path, "/") == "/analytics/e":
		handler.serveEvent(response, request, tracker)
	default:
		handler.serveOFREP(response, request, tracker)
	}
}

func (handler *Handler) ObserveDocument(request *http.Request, serviceID string) {
	if handler == nil || !IsDocumentRequest(request) || ClassifyBot(request.UserAgent()).Kind == "" {
		return
	}
	tracker, ok := handler.lookupTracker(request)
	if !ok {
		return
	}
	handler.RecordBotHit(request, tracker, serviceID)
}

func (handler *Handler) RecordBotHit(request *http.Request, tracker state.AnalyticsTracker, serviceID string) {
	if handler == nil || handler.Catalog == nil {
		return
	}
	bot := ClassifyBot(request.UserAgent())
	if bot.Kind == "" {
		return
	}
	installation, _ := handler.Catalog.Installation(request.Context())
	identity := ResolveIdentity(tracker, request, ClientIP(request), request.UserAgent(), installation.ID, handler.now())
	event := handler.baseEvent(tracker, request, identity, serviceID)
	event.EventName = "$bot_hit"
	event.BotKind = bot.Kind
	event.BotName = bot.Name
	event.DistinctID = "bot:" + bot.Name
	_ = handler.ingest(request.Context(), event)
}

func (handler *Handler) lookupTracker(request *http.Request) (state.AnalyticsTracker, bool) {
	host := request.Host
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if handler.internal {
		if trackerID := strings.TrimSpace(request.Header.Get(TrackerHeader)); trackerID != "" {
			tracker, err := handler.Catalog.AnalyticsTrackerByID(request.Context(), trackerID)
			return tracker, err == nil
		}
		trackers, err := handler.Catalog.AllAnalyticsTrackers(request.Context())
		if err != nil {
			return state.AnalyticsTracker{}, false
		}
		for _, tracker := range trackers {
			project, err := handler.Catalog.Project(request.Context(), tracker.ProjectID)
			if err != nil {
				continue
			}
			if host == InternalAnalyticsHostname(project.Name, state.TrackerSlug(tracker.RootDomain)) {
				return tracker, true
			}
		}
		return state.AnalyticsTracker{}, false
	}
	trackers, err := handler.Catalog.AllAnalyticsTrackers(request.Context())
	if err != nil {
		return state.AnalyticsTracker{}, false
	}
	return MatchTracker(trackers, host)
}

func (handler *Handler) serveScript(response http.ResponseWriter, tracker state.AnalyticsTracker) {
	response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	response.Header().Set("Cache-Control", "public, max-age=300")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, Script(tracker))
}

type browserEvent struct {
	Name        string         `json:"n"`
	URL         string         `json:"u"`
	Title       string         `json:"t"`
	Referrer    string         `json:"r"`
	Screen      string         `json:"w"`
	Language    string         `json:"l"`
	Props       map[string]any `json:"p"`
	Interactive bool           `json:"i"`
	SessionID   string         `json:"s"`
	DistinctID  string         `json:"d"`
}

func (handler *Handler) serveEvent(response http.ResponseWriter, request *http.Request, tracker state.AnalyticsTracker) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumEventBytes)
	var body browserEvent
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	installation, _ := handler.Catalog.Installation(request.Context())
	identity := ResolveIdentity(tracker, request, ClientIP(request), request.UserAgent(), installation.ID, handler.now())
	if handler.internal {
		if strings.TrimSpace(body.DistinctID) == "" {
			http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		identity = Identity{DistinctID: body.DistinctID, SessionID: body.SessionID}
	}
	if identity.Drop {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if body.Name == "$flag_called" && !handler.acceptFlagCalled(request.Context(), tracker, body, identity) {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	serviceID, _ := handler.Catalog.ServiceIDByHostname(request.Context(), hostnameOf(request))
	event := handler.baseEvent(tracker, request, identity, serviceID)
	if body.SessionID != "" {
		event.SessionID = body.SessionID
	}
	event.EventName = strings.TrimSpace(body.Name)
	if event.EventName == "" {
		event.EventName = "$pageview"
	}
	pathname, pageURL := StripQuery(body.URL)
	if pathname != "" {
		event.Pathname = pathname
	}
	event.PageTitle = body.Title
	acquisition := ParseAcquisition(pageURL, body.Referrer)
	applyAcquisition(&event, acquisition)
	event.Screen = body.Screen
	event.Language = body.Language
	if body.Interactive {
		event.Interactive = 1
	}
	applyProps(&event, body.Props)
	if event.EventName == "$flag_called" {
		handler.attachExperimentID(request.Context(), tracker, &event)
	}
	if err := handler.ingest(request.Context(), event); err != nil {
		http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (handler *Handler) acceptFlagCalled(ctx context.Context, tracker state.AnalyticsTracker, body browserEvent, identity Identity) bool {
	flagKey, _ := body.Props["flag"].(string)
	variant, _ := body.Props["variant"].(string)
	if flagKey == "" || variant == "" || identity.DistinctID == "" || tracker.Mode == state.AnalyticsModeCookieless {
		return false
	}
	flag, err := handler.Catalog.AnalyticsFlagByKey(ctx, tracker.ID, flagKey)
	if err != nil {
		return false
	}
	known := false
	for _, candidate := range flag.Variants {
		if candidate.Key == variant {
			known = true
			break
		}
	}
	if !known {
		return false
	}
	if _, err := handler.Catalog.RunningAnalyticsExperiment(ctx, flag.ID, handler.now().UnixMilli()); err != nil {
		return false
	}
	return true
}

func (handler *Handler) serveOFREP(response http.ResponseWriter, request *http.Request, tracker state.AnalyticsTracker) {
	request.Body = http.MaxBytesReader(response, request.Body, maximumEventBytes)
	var payload struct {
		Context map[string]any `json:"context"`
	}
	_ = json.NewDecoder(request.Body).Decode(&payload)
	if payload.Context == nil {
		payload.Context = map[string]any{}
	}
	targetingKey := ""
	if handler.internal {
		targetingKey, _ = payload.Context["targetingKey"].(string)
		if targetingKey == "" {
			http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
	} else if !RequestDenied(request) {
		installation, _ := handler.Catalog.Installation(request.Context())
		identity := ResolveIdentity(tracker, request, ClientIP(request), request.UserAgent(), installation.ID, handler.now())
		if !identity.Drop {
			targetingKey = identity.DistinctID
		}
	}
	flags, err := handler.Catalog.AnalyticsFlags(request.Context(), tracker.ID)
	if err != nil {
		http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	path := strings.TrimSuffix(request.URL.Path, "/")
	key := strings.TrimPrefix(path, "/ofrep/v1/evaluate/flags/")
	single := strings.HasPrefix(path, "/ofrep/v1/evaluate/flags/") && key != "" && key != "flags"
	now := handler.now()
	type evaluatedFlag struct {
		flag       state.AnalyticsFlag
		evaluation Evaluation
	}
	evaluated := make([]evaluatedFlag, 0, len(flags))
	for _, flag := range flags {
		if single && flag.Key != key {
			continue
		}
		evaluated = append(evaluated, evaluatedFlag{
			flag:       flag,
			evaluation: EvaluateFlagAt(flag, targetingKey, payload.Context, now),
		})
	}
	if single && len(evaluated) == 0 {
		http.NotFound(response, request)
		return
	}
	etag := `"` + ConfigVersion(flags, targetingKey, payload.Context, now) + `"`
	if strings.Trim(request.Header.Get("If-None-Match"), " ") == etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	// Public OFREP is paired with the browser OpenFeature hook, which
	// records $flag_called. Server-side exposure is for in-cluster SDKs.
	// Record only on 200: OFREP clients poll with If-None-Match.
	if handler.internal && targetingKey != "" {
		for _, item := range evaluated {
			if item.evaluation.TargetingMatch {
				handler.recordExposure(request.Context(), tracker, item.flag, targetingKey, item.evaluation)
			}
		}
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("ETag", etag)
	if single {
		_ = json.NewEncoder(response).Encode(ofrepBody(evaluated[0].evaluation))
		return
	}
	items := make([]map[string]any, 0, len(evaluated))
	for _, item := range evaluated {
		items = append(items, ofrepBody(item.evaluation))
	}
	_ = json.NewEncoder(response).Encode(map[string]any{"flags": items})
}

func (handler *Handler) attachExperimentID(ctx context.Context, tracker state.AnalyticsTracker, event *Event) {
	flagKey := ""
	for index, key := range event.PropsKeys {
		if key == "flag" && index < len(event.PropsValues) {
			flagKey = event.PropsValues[index]
		}
	}
	experimentID := ""
	if flagKey != "" {
		if flag, err := handler.Catalog.AnalyticsFlagByKey(ctx, tracker.ID, flagKey); err == nil {
			if experiment, err := handler.Catalog.RunningAnalyticsExperiment(ctx, flag.ID, handler.now().UnixMilli()); err == nil {
				experimentID = experiment.ID
			}
		}
	}
	setEventProp(event, "experiment_id", experimentID)
}

func setEventProp(event *Event, key, value string) {
	found := false
	for index, name := range event.PropsKeys {
		if name == key && index < len(event.PropsValues) {
			event.PropsValues[index] = value
			found = true
		}
	}
	if found || value == "" {
		return
	}
	event.PropsKeys = append(event.PropsKeys, key)
	event.PropsValues = append(event.PropsValues, value)
}

func (handler *Handler) recordExposure(ctx context.Context, tracker state.AnalyticsTracker, flag state.AnalyticsFlag, targetingKey string, evaluation Evaluation) {
	experiment, err := handler.Catalog.RunningAnalyticsExperiment(ctx, flag.ID, handler.now().UnixMilli())
	if err != nil {
		return
	}
	event := Event{
		TrackerID: tracker.ID, DistinctID: targetingKey, EventName: "$flag_called",
		Timestamp:   handler.now().UTC().Format("2006-01-02T15:04:05.000Z"),
		PropsKeys:   []string{"flag", "variant", "experiment_id"},
		PropsValues: []string{flag.Key, evaluation.Variant, experiment.ID},
	}
	_ = handler.ingest(ctx, event)
}

func (handler *Handler) baseEvent(tracker state.AnalyticsTracker, request *http.Request, identity Identity, serviceID string) Event {
	device := ParseUserAgent(request.UserAgent())
	pathname, _ := StripQuery(request.URL.String())
	host := hostnameOf(request)
	event := Event{
		TrackerID: tracker.ID, ServiceID: serviceID, DistinctID: identity.DistinctID, SessionID: identity.SessionID,
		Timestamp: handler.now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Hostname:  host, Pathname: pathname,
		Browser: device.Browser, BrowserVersion: device.BrowserVersion, OS: device.OS, OSVersion: device.OSVersion, Device: device.Device,
		Language: request.Header.Get("Accept-Language"),
		Country:  request.Header.Get("CF-IPCountry"), Region: request.Header.Get("CF-Region"), City: request.Header.Get("CF-IPCity"),
		PropsKeys: []string{}, PropsValues: []string{},
	}
	if comma := strings.IndexByte(event.Language, ','); comma >= 0 {
		event.Language = event.Language[:comma]
	}
	if dash := strings.IndexByte(event.Language, ';'); dash >= 0 {
		event.Language = event.Language[:dash]
	}
	return event
}

func applyAcquisition(event *Event, acquisition Acquisition) {
	event.Referrer = acquisition.Referrer
	event.ReferrerDomain = acquisition.ReferrerDomain
	event.ReferrerSource = acquisition.ReferrerSource
	event.Channel = acquisition.Channel
	event.UTMSource = acquisition.UTMSource
	event.UTMMedium = acquisition.UTMMedium
	event.UTMCampaign = acquisition.UTMCampaign
	event.UTMContent = acquisition.UTMContent
	event.UTMTerm = acquisition.UTMTerm
	event.Gclid = acquisition.Gclid
	event.Fbclid = acquisition.Fbclid
	event.Msclkid = acquisition.Msclkid
	event.Ttclid = acquisition.Ttclid
	event.LiFatID = acquisition.LiFatID
	event.Twclid = acquisition.Twclid
}

func applyProps(event *Event, props map[string]any) {
	for key, value := range props {
		switch key {
		case "interactive":
			if value == true {
				event.Interactive = 1
			}
		case "revenue":
			n := toFloat(value)
			event.Revenue = &n
		case "currency":
			event.Currency = stringify(value)
		case "lcp", "inp", "cls", "fcp", "ttfb":
			n := toFloat(value)
			switch key {
			case "lcp":
				event.LCP = &n
			case "inp":
				event.INP = &n
			case "cls":
				event.CLS = &n
			case "fcp":
				event.FCP = &n
			case "ttfb":
				event.TTFB = &n
			}
		default:
			event.PropsKeys = append(event.PropsKeys, key)
			event.PropsValues = append(event.PropsValues, stringify(value))
		}
	}
}

func (handler *Handler) ingest(ctx context.Context, event Event) error {
	if handler.Target == nil || handler.Client == nil {
		return nil
	}
	if event.EventID == "" {
		sum := sha256.Sum256([]byte(strings.Join([]string{
			event.TrackerID, event.Timestamp, event.EventName, event.DistinctID, event.Pathname, strings.Join(event.PropsValues, "\x00"),
		}, "\x1e")))
		event.EventID = hex16(sum[:])
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, handler.Target.String()+"/internal/analytics/ingest", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := handler.Client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 300 {
		return io.EOF
	}
	return nil
}

func (handler *Handler) Query(ctx context.Context, trackerID string, body json.RawMessage) (int, string, []byte, error) {
	if handler.Target == nil || handler.Client == nil {
		return http.StatusBadGateway, "", nil, io.EOF
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		handler.Target.String()+"/internal/analytics/"+url.PathEscape(trackerID)+"/query", bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := handler.Client.Do(request)
	if err != nil {
		return 0, "", nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return 0, "", nil, err
	}
	return response.StatusCode, response.Header.Get("Content-Type"), payload, nil
}

func (handler *Handler) now() time.Time {
	if handler.Now != nil {
		return handler.Now()
	}
	return time.Now()
}

func hostnameOf(request *http.Request) string {
	host := request.Host
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}
