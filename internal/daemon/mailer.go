package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iivankin/platformd/internal/mailer"
	"github.com/iivankin/platformd/internal/state"
)

type mailMetricQuery struct {
	repository *liveServiceTelemetryRepository
}

func (query mailMetricQuery) Query(
	ctx context.Context,
	scope state.MetricScope,
	sql string,
	fromMillis, toMillis, stepMillis int64,
) ([]mailer.MetricPoint, error) {
	encoded, err := json.Marshal(map[string]any{
		"from": fromMillis, "sql": sql, "step": stepMillis, "to": toMillis,
	})
	if err != nil {
		return nil, err
	}
	result, err := query.repository.QueryMetricScope(ctx, scope, "query", encoded)
	if err != nil {
		return nil, err
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		message := strings.TrimSpace(string(result.Body))
		if message == "" {
			message = http.StatusText(result.StatusCode)
		}
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("HTTP %d: %s", result.StatusCode, message)
	}
	var points []mailer.MetricPoint
	if err := json.Unmarshal(result.Body, &points); err != nil {
		return nil, fmt.Errorf("decode metric query: %w", err)
	}
	return points, nil
}
