package telemetry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

func TestWebhookDeliveryStopsWhenDispatcherIsCanceled(t *testing.T) {
	t.Parallel()
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-releaseRequest
	}))
	defer server.Close()
	defer close(releaseRequest)

	master := cryptobox.MasterKey{1, 2, 3, 4}
	box, err := cryptobox.NewBox(master, []byte("webhook"), webhookSecretDomain)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := box.Seal([]byte("secret"), []byte("service"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatcher := &WebhookDispatcher{
		master: master,
		client: server.Client(),
		ctx:    ctx,
		cancel: cancel,
	}
	deliveryDone := make(chan error, 1)
	go func() {
		deliveryDone <- dispatcher.deliver(state.ServiceTelemetryWebhook{
			ID: "webhook", URL: server.URL, SecretEncrypted: secret,
		}, serviceTelemetryNotification{
			ServiceID: "service", EventType: "event_received", Timestamp: "2026-08-12T00:00:00Z",
			Issue: serviceTelemetryIssue{ID: "issue"},
			Event: serviceTelemetryEvent{ID: "event", Timestamp: "2026-08-12T00:00:00Z"},
		})
	}()

	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("webhook request did not start")
	}
	cancel()
	select {
	case err := <-deliveryDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("delivery error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("webhook delivery did not stop after cancellation")
	}
}
