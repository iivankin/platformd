package mailer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type serviceErrorNotification struct {
	ServiceID string            `json:"-"`
	EventType string            `json:"eventType"`
	Timestamp string            `json:"timestamp"`
	Issue     serviceErrorIssue `json:"issue"`
	Event     serviceErrorEvent `json:"event"`
}

type serviceErrorIssue struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Level    string `json:"level"`
	Platform string `json:"platform"`
}

type serviceErrorEvent struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

func (application *Application) EnqueueServiceError(serviceID string, payload []byte) {
	var notifications []serviceErrorNotification
	if serviceID == "" || len(payload) == 0 || len(payload) > 48<<10 ||
		json.Unmarshal(payload, &notifications) != nil || len(notifications) > 32 {
		application.onError(errors.New("invalid service error mail notification"))
		return
	}
	for _, item := range notifications {
		if !slices.Contains(allowedErrorEvents, item.EventType) {
			continue
		}
		item.ServiceID = serviceID
		if !validServiceErrorNotification(item) {
			application.onError(errors.New("invalid service error mail notification fields"))
			continue
		}
		application.mu.Lock()
		if application.closed {
			application.mu.Unlock()
			return
		}
		select {
		case application.queue <- item:
		default:
			application.onError(errors.New("service error mail queue is full; delivery dropped"))
		}
		application.mu.Unlock()
	}
}

func (application *Application) runErrorQueue() {
	defer application.wait.Done()
	for notification := range application.queue {
		if err := application.dispatchError(notification); err != nil && application.ctx.Err() == nil {
			application.onError(err)
		}
	}
}

func (application *Application) dispatchError(notification serviceErrorNotification) error {
	ctx, cancel := context.WithTimeout(application.ctx, 5*time.Second)
	alerts, err := application.store.MailErrorAlerts(ctx)
	cancel()
	if err != nil {
		return fmt.Errorf("load mail error alerts: %w", err)
	}
	ctx, cancel = context.WithTimeout(application.ctx, 5*time.Second)
	service, serviceErr := application.store.DesiredService(ctx, notification.ServiceID)
	cancel()
	if serviceErr != nil {
		return fmt.Errorf("load service for error mail: %w", serviceErr)
	}
	var result error
	for _, alert := range alerts {
		if !alert.Enabled || !slices.Contains(alert.EventTypes, notification.EventType) {
			continue
		}
		if len(alert.ServiceIDs) > 0 && !slices.Contains(alert.ServiceIDs, notification.ServiceID) {
			continue
		}
		if err := application.sendErrorMail(alert, service, notification); err != nil {
			result = errors.Join(result, fmt.Errorf("send error alert %s: %w", alert.ID, err))
		}
	}
	return result
}

func (application *Application) sendErrorMail(
	alert state.MailErrorAlert,
	service state.ServiceDesired,
	notification serviceErrorNotification,
) error {
	config, password, err := application.credentials(application.ctx)
	if errors.Is(err, state.ErrSMTPNotConfigured) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(password)
	subject := fmt.Sprintf("[platformd] %s in %s/%s: %s",
		errorEventLabel(notification.EventType), service.ProjectName, service.Name, firstLine(notification.Issue.Title))
	body := strings.Join([]string{
		"Alert: " + alert.Name,
		"Event: " + errorEventLabel(notification.EventType),
		"Project: " + service.ProjectName,
		"Service: " + service.Name,
		"Issue: " + notification.Issue.Title,
		"Level: " + notification.Issue.Level,
		"Issue ID: " + notification.Issue.ID,
		"Event ID: " + notification.Event.ID,
		"Time: " + notification.Timestamp,
	}, "\n")
	sendCtx, cancel := context.WithTimeout(application.ctx, sendTimeout)
	defer cancel()
	return application.send(sendCtx, config, password, Message{
		To: alert.Recipients, Subject: subject, Body: body,
	})
}

func validServiceErrorNotification(notification serviceErrorNotification) bool {
	return notification.ServiceID != "" && slices.Contains(allowedErrorEvents, notification.EventType) &&
		notification.Timestamp != "" && notification.Issue.ID != "" && notification.Event.ID != ""
}

func errorEventLabel(eventType string) string {
	switch eventType {
	case "issue_created":
		return "New error"
	case "issue_regressed":
		return "Regressed error"
	case "issue_resolved":
		return "Resolved error"
	default:
		return eventType
	}
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "untitled issue"
	}
	if index := strings.IndexAny(value, "\r\n"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	runes := []rune(value)
	if len(runes) > 120 {
		return string(runes[:117]) + "..."
	}
	return value
}
