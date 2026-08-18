package mailer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func (application *Application) Run(ctx context.Context) {
	application.wait.Add(1)
	defer application.wait.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-application.ctx.Done():
			return
		case <-timer.C:
			if err := application.EvaluateMetrics(ctx); err != nil && ctx.Err() == nil && application.ctx.Err() == nil {
				application.onError(err)
			}
			timer.Reset(application.every)
		}
	}
}

func (application *Application) EvaluateMetrics(ctx context.Context) error {
	if application.metrics == nil {
		return nil
	}
	alerts, err := application.store.MailMetricAlerts(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, alert := range alerts {
		if !alert.Enabled {
			continue
		}
		if err := application.evaluateMetricAlert(ctx, alert); err != nil {
			result = errors.Join(result, fmt.Errorf("evaluate metric alert %s: %w", alert.ID, err))
		}
	}
	return result
}

func (application *Application) evaluateMetricAlert(ctx context.Context, alert state.MailMetricAlert) error {
	now := application.now()
	to := now.UnixMilli()
	from := to - int64(alert.WindowSeconds)*1000
	points, err := application.metrics.Query(ctx, alert.Scope, alert.SQL, from, to, metricStepMillis(alert.WindowSeconds))
	if err != nil {
		return err
	}
	latest := latestSeriesValues(points)
	firingSeries := make([]string, 0)
	var observed *float64
	for series, value := range latest {
		copied := value
		observed = pickObserved(alert.Operator, observed, copied)
		if metricMatches(alert.Operator, copied, alert.Threshold) {
			label := series
			if label == "" {
				label = "value"
			}
			firingSeries = append(firingSeries, fmt.Sprintf("%s=%g", label, copied))
		}
	}
	firing := len(firingSeries) > 0
	sentAt := alert.LastSentAt
	if firing != alert.Firing {
		if err := application.sendMetricMail(ctx, alert, firing, firingSeries, observed); err != nil {
			if !errors.Is(err, state.ErrSMTPNotConfigured) {
				return err
			}
			return application.store.RecordMailMetricAlertEvaluation(
				ctx, alert.ID, alert.Firing, observed, now.UnixMilli(), alert.LastSentAt, alert.UpdatedAtMillis,
			)
		}
		sentAt = now.UnixMilli()
	}
	return application.store.RecordMailMetricAlertEvaluation(
		ctx, alert.ID, firing, observed, now.UnixMilli(), sentAt, alert.UpdatedAtMillis,
	)
}

func (application *Application) sendMetricMail(
	ctx context.Context,
	alert state.MailMetricAlert,
	firing bool,
	series []string,
	value *float64,
) error {
	config, password, err := application.credentials(ctx)
	if err != nil {
		return err
	}
	defer clear(password)
	stateLabel := "RESOLVED"
	if firing {
		stateLabel = "FIRING"
	}
	valueText := "n/a"
	if value != nil {
		valueText = fmt.Sprintf("%g", *value)
	}
	body := strings.Join([]string{
		"Alert: " + alert.Name,
		"State: " + stateLabel,
		"Scope: " + metricScopeLabel(alert.Scope),
		fmt.Sprintf("Condition: value %s %g over %s", alert.Operator, alert.Threshold, formatWindow(alert.WindowSeconds)),
		"Observed: " + valueText,
		"Series: " + metricSeriesLine(series),
	}, "\n")
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return application.send(sendCtx, config, password, Message{
		To:      alert.Recipients,
		Subject: fmt.Sprintf("[platformd] %s: %s", stateLabel, alert.Name),
		Body:    body,
	})
}

func latestSeriesValues(points []MetricPoint) map[string]float64 {
	type sample struct {
		nano  uint64
		value float64
	}
	latest := make(map[string]sample)
	for _, point := range points {
		if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
			continue
		}
		nano, err := strconv.ParseUint(point.TimeUnixNano, 10, 64)
		if err != nil {
			continue
		}
		current, exists := latest[point.Series]
		if !exists || nano >= current.nano {
			latest[point.Series] = sample{nano: nano, value: point.Value}
		}
	}
	values := make(map[string]float64, len(latest))
	for series, sample := range latest {
		values[series] = sample.value
	}
	return values
}

func pickObserved(operator string, current *float64, next float64) *float64 {
	if current == nil {
		value := next
		return &value
	}
	if (operator == "lt" || operator == "lte") && next < *current {
		value := next
		return &value
	}
	if (operator == "gt" || operator == "gte") && next > *current {
		value := next
		return &value
	}
	return current
}

func metricMatches(operator string, value, threshold float64) bool {
	switch operator {
	case "gt":
		return value > threshold
	case "gte":
		return value >= threshold
	case "lt":
		return value < threshold
	case "lte":
		return value <= threshold
	default:
		return false
	}
}

func metricScopeLabel(scope state.MetricScope) string {
	switch scope.Kind {
	case state.MetricScopeProject:
		return "project " + scope.ProjectID
	case state.MetricScopeService:
		return "service " + scope.ServiceID
	default:
		return "installation"
	}
}

func metricSeriesLine(series []string) string {
	if len(series) == 0 {
		return "none"
	}
	return strings.Join(series, ", ")
}

func formatWindow(seconds int) string {
	if seconds%3600 == 0 && seconds >= 3600 {
		hours := seconds / 3600
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	if seconds%60 == 0 {
		minutes := seconds / 60
		if minutes == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", minutes)
	}
	return fmt.Sprintf("%d seconds", seconds)
}
