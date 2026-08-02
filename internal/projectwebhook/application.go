package projectwebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const (
	EventDeploymentStarted     = "deployment.started"
	EventDeploymentSucceeded   = "deployment.succeeded"
	EventDeploymentFailed      = "deployment.failed"
	EventDeploymentInterrupted = "deployment.interrupted"
	EventDeploymentSkipped     = "deployment.skipped"
	EventWebhookTest           = "webhook.test"

	deliveryTimeout = 10 * time.Second
	maximumURLBytes = 2048
)

var EventTypes = []string{
	EventDeploymentStarted,
	EventDeploymentSucceeded,
	EventDeploymentFailed,
	EventDeploymentInterrupted,
	EventDeploymentSkipped,
}

var retryDelays = []time.Duration{time.Second, 3 * time.Second, 9 * time.Second}

var (
	ErrInvalidURL       = errors.New("webhook URL is invalid")
	ErrInvalidEventType = errors.New("webhook event type is invalid")
)

type Store interface {
	Project(context.Context, string) (state.ProjectSummary, error)
	ProjectWebhooks(context.Context, string) ([]state.ProjectWebhook, error)
	CreateProjectWebhook(context.Context, state.ProjectWebhookMutation) (state.ProjectWebhook, error)
	UpdateProjectWebhook(context.Context, state.ProjectWebhookMutation) (state.ProjectWebhook, error)
	DeleteProjectWebhook(context.Context, state.DeleteProjectWebhook) error
}

type EventProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type EventResource struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type EventDeployment struct {
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
	ID           string `json:"id"`
	Status       string `json:"status"`
}

type Event struct {
	Deployment *EventDeployment `json:"deployment,omitempty"`
	ID         string           `json:"id"`
	Project    EventProject     `json:"project"`
	Resource   *EventResource   `json:"resource,omitempty"`
	Timestamp  string           `json:"timestamp"`
	Type       string           `json:"type"`
}

type Mutation struct {
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	TimestampMillis      int64
}

type Config struct {
	Context context.Context
	Store   Store
	Client  *http.Client
	Now     func() time.Time
	NewID   func() (string, error)
	Sleep   func(context.Context, time.Duration) error
	OnError func(error)
}

type Application struct {
	ctx     context.Context
	store   Store
	client  *http.Client
	now     func() time.Time
	newID   func() (string, error)
	sleep   func(context.Context, time.Duration) error
	onError func(error)
}

func New(config Config) (*Application, error) {
	if config.Store == nil {
		return nil, errors.New("project webhook store is required")
	}
	applicationContext := config.Context
	if applicationContext == nil {
		applicationContext = context.Background()
	}
	client := config.Client
	if client == nil {
		client = &http.Client{
			Timeout: deliveryTimeout,
			Transport: &http.Transport{
				Proxy:                 nil,
				DialContext:           (&net.Dialer{Timeout: deliveryTimeout}).DialContext,
				ResponseHeaderTimeout: deliveryTimeout,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newID := config.NewID
	if newID == nil {
		newID = id.New
	}
	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	onError := config.OnError
	if onError == nil {
		onError = func(error) {}
	}
	return &Application{
		ctx: applicationContext, store: config.Store, client: client,
		now: now, newID: newID, sleep: sleep, onError: onError,
	}, nil
}

func (application *Application) List(ctx context.Context, projectID string) ([]state.ProjectWebhook, error) {
	return application.store.ProjectWebhooks(ctx, projectID)
}

func (application *Application) Create(ctx context.Context, projectID, webhookURL string, eventTypes []string, mutation Mutation) (state.ProjectWebhook, error) {
	normalizedURL, normalizedEvents, err := validateConfiguration(webhookURL, eventTypes)
	if err != nil {
		return state.ProjectWebhook{}, err
	}
	if err := validateMutation(mutation); err != nil {
		return state.ProjectWebhook{}, err
	}
	webhookID, err := application.newID()
	if err != nil {
		return state.ProjectWebhook{}, err
	}
	return application.store.CreateProjectWebhook(ctx, state.ProjectWebhookMutation{
		Webhook: state.ProjectWebhook{
			ID: webhookID, ProjectID: projectID, URL: normalizedURL, EventTypes: normalizedEvents,
			CreatedAtMillis: mutation.TimestampMillis, UpdatedAtMillis: mutation.TimestampMillis,
		},
		AuditEventID: mutation.AuditEventID, ActorID: mutation.ActorID, ActorEmail: mutation.ActorEmail,
		RequestCorrelationID: mutation.RequestCorrelationID,
	})
}

func (application *Application) Update(ctx context.Context, projectID, webhookID, webhookURL string, eventTypes []string, mutation Mutation) (state.ProjectWebhook, error) {
	normalizedURL, normalizedEvents, err := validateConfiguration(webhookURL, eventTypes)
	if err != nil {
		return state.ProjectWebhook{}, err
	}
	if err := validateMutation(mutation); err != nil {
		return state.ProjectWebhook{}, err
	}
	return application.store.UpdateProjectWebhook(ctx, state.ProjectWebhookMutation{
		Webhook: state.ProjectWebhook{
			ID: webhookID, ProjectID: projectID, URL: normalizedURL,
			EventTypes: normalizedEvents, UpdatedAtMillis: mutation.TimestampMillis,
		},
		AuditEventID: mutation.AuditEventID, ActorID: mutation.ActorID, ActorEmail: mutation.ActorEmail,
		RequestCorrelationID: mutation.RequestCorrelationID,
	})
}

func (application *Application) Delete(ctx context.Context, projectID, webhookID string, mutation Mutation) error {
	if err := validateMutation(mutation); err != nil {
		return err
	}
	return application.store.DeleteProjectWebhook(ctx, state.DeleteProjectWebhook{
		ID: webhookID, ProjectID: projectID, AuditEventID: mutation.AuditEventID,
		ActorID: mutation.ActorID, ActorEmail: mutation.ActorEmail,
		RequestCorrelationID: mutation.RequestCorrelationID, DeletedAtMillis: mutation.TimestampMillis,
	})
}

func (application *Application) Test(ctx context.Context, projectID, webhookURL string) error {
	normalizedURL, err := validateURL(webhookURL)
	if err != nil {
		return err
	}
	project, err := application.store.Project(ctx, projectID)
	if err != nil {
		return err
	}
	event, err := application.prepareEvent(Event{
		Project: EventProject{ID: project.ID, Name: project.Name},
		Type:    EventWebhookTest,
	})
	if err != nil {
		return err
	}
	return application.deliver(ctx, normalizedURL, event)
}

func (application *Application) Dispatch(event Event) {
	if !slices.Contains(EventTypes, event.Type) || event.Project.ID == "" {
		application.onError(fmt.Errorf("dispatch project webhook: %w %q", ErrInvalidEventType, event.Type))
		return
	}
	prepared, err := application.prepareEvent(event)
	if err != nil {
		application.onError(fmt.Errorf("prepare project webhook: %w", err))
		return
	}
	webhooks, err := application.store.ProjectWebhooks(application.ctx, event.Project.ID)
	if err != nil {
		application.onError(fmt.Errorf("load project webhooks for %s: %w", event.Project.ID, err))
		return
	}
	for _, webhook := range webhooks {
		if !slices.Contains(webhook.EventTypes, event.Type) {
			continue
		}
		go func() {
			if err := application.deliver(application.ctx, webhook.URL, prepared); err != nil && !errors.Is(err, context.Canceled) {
				application.onError(fmt.Errorf("deliver project webhook %s: %w", webhook.ID, err))
			}
		}()
	}
}

func (application *Application) prepareEvent(event Event) (Event, error) {
	if event.ID == "" {
		eventID, err := application.newID()
		if err != nil {
			return Event{}, err
		}
		event.ID = eventID
	}
	if event.Timestamp == "" {
		event.Timestamp = application.now().UTC().Format(time.RFC3339Nano)
	}
	return event, nil
}

func (application *Application) deliver(ctx context.Context, webhookURL string, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode webhook payload: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		retry, err := application.send(ctx, webhookURL, event, payload)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retry || attempt == len(retryDelays) {
			break
		}
		if err := application.sleep(ctx, retryDelays[attempt]); err != nil {
			return err
		}
	}
	return lastErr
}

func (application *Application) send(ctx context.Context, webhookURL string, event Event, payload []byte) (bool, error) {
	requestContext, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "platformd-webhooks/1")
	request.Header.Set("X-Platformd-Delivery", event.ID)
	request.Header.Set("X-Platformd-Event", event.Type)
	response, err := application.client.Do(request)
	if err != nil {
		return true, fmt.Errorf("send webhook request: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return false, closeErr
	}
	retry := response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError
	return retry, fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
}

func validateConfiguration(webhookURL string, eventTypes []string) (string, []string, error) {
	normalizedURL, err := validateURL(webhookURL)
	if err != nil {
		return "", nil, err
	}
	if len(eventTypes) == 0 {
		return "", nil, errors.New("select at least one webhook event")
	}
	selected := make(map[string]struct{}, len(eventTypes))
	for _, eventType := range eventTypes {
		if !slices.Contains(EventTypes, eventType) {
			return "", nil, fmt.Errorf("%w: %s", ErrInvalidEventType, eventType)
		}
		selected[eventType] = struct{}{}
	}
	normalizedEvents := make([]string, 0, len(selected))
	for _, eventType := range EventTypes {
		if _, ok := selected[eventType]; ok {
			normalizedEvents = append(normalizedEvents, eventType)
		}
	}
	return normalizedURL, normalizedEvents, nil
}

func validateURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maximumURLBytes {
		return "", ErrInvalidURL
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return "", ErrInvalidURL
	}
	return parsed.String(), nil
}

func validateMutation(mutation Mutation) error {
	if mutation.AuditEventID == "" || mutation.ActorID == "" || mutation.ActorEmail == "" || mutation.TimestampMillis <= 0 {
		return errors.New("project webhook mutation context is incomplete")
	}
	return nil
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
