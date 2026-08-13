package telemetry

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const webhookSecretDomain = "platformd/sqlite/service-telemetry-webhook/v1"

var serviceTelemetryWebhookEvents = []string{
	"event_received", "issue_created", "issue_regressed", "issue_resolved",
}

type WebhookDispatcher struct {
	store   *state.Store
	master  cryptobox.MasterKey
	client  *http.Client
	onError func(error)
	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan serviceTelemetryNotification
	mu      sync.Mutex
	closed  bool
	wait    sync.WaitGroup
}

type serviceTelemetryNotification struct {
	ServiceID string                `json:"-"`
	EventType string                `json:"eventType"`
	Timestamp string                `json:"timestamp"`
	Issue     serviceTelemetryIssue `json:"issue"`
	Event     serviceTelemetryEvent `json:"event"`
}

type serviceTelemetryIssue struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Level    string `json:"level"`
	Platform string `json:"platform"`
}

type serviceTelemetryEvent struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

type serviceTelemetryWebhookPayload struct {
	ID        string                         `json:"id"`
	EventType string                         `json:"eventType"`
	Timestamp string                         `json:"timestamp"`
	Service   serviceTelemetryWebhookService `json:"service"`
	Issue     serviceTelemetryIssue          `json:"issue"`
	Event     serviceTelemetryEvent          `json:"event"`
}

type serviceTelemetryWebhookService struct {
	ID string `json:"id"`
}

func NewWebhookDispatcher(store *state.Store, master cryptobox.MasterKey, onError func(error)) (*WebhookDispatcher, error) {
	if store == nil || onError == nil {
		return nil, errors.New("service telemetry webhook dependencies are incomplete")
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := &WebhookDispatcher{
		store:  store,
		master: master,
		ctx:    ctx,
		cancel: cancel,
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 4,
			},
		},
		onError: onError,
		queue:   make(chan serviceTelemetryNotification, 256),
	}
	for range 4 {
		dispatcher.wait.Add(1)
		go dispatcher.run()
	}
	return dispatcher, nil
}

// Enqueue receives a trusted, bounded payload produced only by the embedded
// telemetry process. Public requests cannot set this value because the proxy
// removes the internal response header before returning it.
func (dispatcher *WebhookDispatcher) Enqueue(serviceID string, payload []byte) {
	var notifications []serviceTelemetryNotification
	if serviceID == "" || len(payload) == 0 || len(payload) > 48<<10 || json.Unmarshal(payload, &notifications) != nil || len(notifications) > 32 {
		dispatcher.onError(errors.New("invalid service telemetry webhook notification"))
		return
	}
	for index := range notifications {
		notification := notifications[index]
		notification.ServiceID = serviceID
		if !validServiceTelemetryNotification(notification) {
			dispatcher.onError(errors.New("invalid service telemetry webhook notification fields"))
			continue
		}
		dispatcher.mu.Lock()
		if dispatcher.closed {
			dispatcher.mu.Unlock()
			return
		}
		select {
		case dispatcher.queue <- notification:
		default:
			dispatcher.onError(errors.New("service telemetry webhook queue is full; delivery dropped"))
		}
		dispatcher.mu.Unlock()
	}
}

func (dispatcher *WebhookDispatcher) Close() {
	dispatcher.mu.Lock()
	if !dispatcher.closed {
		dispatcher.closed = true
		dispatcher.cancel()
		close(dispatcher.queue)
	}
	dispatcher.mu.Unlock()
	dispatcher.wait.Wait()
	dispatcher.client.CloseIdleConnections()
}

func (dispatcher *WebhookDispatcher) run() {
	defer dispatcher.wait.Done()
	for notification := range dispatcher.queue {
		if err := dispatcher.dispatch(notification); err != nil {
			if dispatcher.ctx.Err() == nil {
				dispatcher.onError(err)
			}
		}
	}
}

func (dispatcher *WebhookDispatcher) dispatch(notification serviceTelemetryNotification) error {
	ctx, cancel := context.WithTimeout(dispatcher.ctx, 5*time.Second)
	webhooks, err := dispatcher.store.ServiceTelemetryWebhooks(ctx, notification.ServiceID)
	cancel()
	if err != nil {
		return fmt.Errorf("load service telemetry webhooks: %w", err)
	}
	var result error
	for _, webhook := range webhooks {
		if !webhook.Enabled || !slices.Contains(webhook.EventTypes, notification.EventType) {
			continue
		}
		if err := dispatcher.deliver(webhook, notification); err != nil {
			result = errors.Join(result, fmt.Errorf("deliver service telemetry webhook %s: %w", webhook.ID, err))
		}
	}
	return result
}

func (dispatcher *WebhookDispatcher) deliver(webhook state.ServiceTelemetryWebhook, notification serviceTelemetryNotification) error {
	box, err := cryptobox.NewBox(dispatcher.master, []byte(webhook.ID), webhookSecretDomain)
	if err != nil {
		return err
	}
	secret, err := box.Open(webhook.SecretEncrypted, []byte(notification.ServiceID))
	if err != nil {
		return err
	}
	deliveryID, err := id.New()
	if err != nil {
		return err
	}
	body, err := json.Marshal(serviceTelemetryWebhookPayload{
		ID: deliveryID, EventType: notification.EventType, Timestamp: notification.Timestamp,
		Service: serviceTelemetryWebhookService{ID: notification.ServiceID},
		Issue:   notification.Issue, Event: notification.Event,
	})
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	clear(secret)

	delays := []time.Duration{0, time.Second, 3 * time.Second, 9 * time.Second}
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-dispatcher.ctx.Done():
				timer.Stop()
				return dispatcher.ctx.Err()
			case <-timer.C:
			}
		}
		request, err := http.NewRequestWithContext(dispatcher.ctx, http.MethodPost, webhook.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "platformd-webhooks/1")
		request.Header.Set("X-Platformd-Delivery", deliveryID)
		request.Header.Set("X-Platformd-Event", notification.EventType)
		request.Header.Set("X-Platformd-Signature", signature)
		response, err := dispatcher.client.Do(request)
		if err != nil {
			if dispatcher.ctx.Err() != nil {
				return dispatcher.ctx.Err()
			}
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}
		if response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			continue
		}
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return lastErr
}

func validServiceTelemetryNotification(notification serviceTelemetryNotification) bool {
	return notification.ServiceID != "" && slices.Contains(serviceTelemetryWebhookEvents, notification.EventType) &&
		notification.Timestamp != "" && notification.Issue.ID != "" && notification.Event.ID != "" && notification.Event.Timestamp != ""
}
