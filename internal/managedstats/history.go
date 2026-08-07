package managedstats

import (
	"context"
	"encoding/json"
	"time"
)

func (application *Application) History(ctx context.Context, kind, resourceID, rangeName string) (History, error) {
	if !validKind(kind) {
		return History{}, ErrInvalidKind
	}
	if resourceID == "" {
		return History{}, ErrInvalidTarget
	}
	window, err := windowForRange(rangeName)
	if err != nil {
		return History{}, err
	}
	step, err := stepForWindow(window)
	if err != nil {
		return History{}, err
	}
	to := application.now().UnixMilli()
	from := to - window.Milliseconds()
	samples, err := application.store.ManagedStatSamples(ctx, kind, resourceID, from, to)
	if err != nil {
		return History{}, err
	}
	history := History{
		From: from, To: to, StepMillis: step.Milliseconds(),
		Points: make([]Point, 0),
	}
	buckets := make(map[int64]*historyBucket)
	order := make([]int64, 0)
	for _, sample := range samples {
		metrics := map[string]any{}
		if err := json.Unmarshal([]byte(sample.MetricsJSON), &metrics); err != nil {
			return History{}, err
		}
		key := (sample.ObservedAt - from) / step.Milliseconds()
		bucket := buckets[key]
		if bucket == nil {
			bucket = &historyBucket{values: make(map[string]*bucketValue)}
			buckets[key] = bucket
			order = append(order, key)
		}
		bucket.observedAt = sample.ObservedAt
		bucket.add(metrics)
		history.Totals.add(metrics)
	}
	for _, key := range order {
		bucket := buckets[key]
		history.Points = append(history.Points, Point{
			ObservedAt: bucket.observedAt, Metrics: bucket.finish(),
		})
	}
	return history, nil
}

func windowForRange(rangeName string) (time.Duration, error) {
	switch rangeName {
	case "1h":
		return time.Hour, nil
	case "6h":
		return 6 * time.Hour, nil
	case "1d":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	default:
		return 0, ErrInvalidRange
	}
}

func stepForWindow(window time.Duration) (time.Duration, error) {
	switch window {
	case time.Hour:
		return time.Minute, nil
	case 6 * time.Hour:
		return 5 * time.Minute, nil
	case 24 * time.Hour:
		return 15 * time.Minute, nil
	case 7 * 24 * time.Hour:
		return time.Hour, nil
	case 30 * 24 * time.Hour:
		return 6 * time.Hour, nil
	default:
		return 0, ErrInvalidRange
	}
}

func validKind(kind string) bool {
	return kind == "postgres" || kind == "redis" || kind == "object_store"
}

type historyBucket struct {
	observedAt int64
	samples    int
	values     map[string]*bucketValue
}

type bucketValue struct {
	sum     float64
	count   int
	isCount bool
}

func (bucket *historyBucket) add(metrics map[string]any) {
	bucket.samples++
	for key, raw := range metrics {
		number, ok := asFloat(raw)
		if !ok {
			continue
		}
		value := bucket.values[key]
		if value == nil {
			value = &bucketValue{isCount: isCountField(key)}
			bucket.values[key] = value
		}
		value.sum += number
		value.count++
	}
}

func (bucket *historyBucket) finish() map[string]any {
	metrics := make(map[string]any, len(bucket.values))
	for key, value := range bucket.values {
		if value.count == 0 {
			continue
		}
		if value.isCount {
			metrics[key] = value.sum
			continue
		}
		// Missing samples count as zero so sparse cmd.* rates aren't inflated.
		denom := bucket.samples
		if denom <= 0 {
			denom = value.count
		}
		metrics[key] = value.sum / float64(denom)
	}
	return metrics
}

func (totals *Totals) add(metrics map[string]any) {
	totals.QueryCount += floatField(metrics, "queryCount")
	totals.RowsRead += floatField(metrics, "rowsRead")
	totals.RowsWritten += floatField(metrics, "rowsWritten")
	totals.BytesRead += floatField(metrics, "bytesRead")
	totals.BytesHit += floatField(metrics, "bytesHit")
	totals.CommandCount += floatField(metrics, "commandCount")
	totals.NetInputBytes += floatField(metrics, "netInputBytes")
	totals.NetOutputBytes += floatField(metrics, "netOutputBytes")
	totals.OperationCount += floatField(metrics, "operationCount")
	totals.BytesIn += floatField(metrics, "bytesIn")
	totals.BytesOut += floatField(metrics, "bytesOut")
	totals.ErrorCount += floatField(metrics, "errorCount")
	totals.OtherQueryCount += floatField(metrics, "otherQueryCount")
}

func isCountField(key string) bool {
	switch key {
	case "queryCount", "rowsRead", "rowsWritten", "bytesRead", "bytesHit",
		"commandCount", "netInputBytes", "netOutputBytes", "operationCount",
		"bytesIn", "bytesOut", "errorCount", "otherQueryCount", "otherCommandCount",
		"otherOperationCount", "callCount":
		return true
	default:
		return false
	}
}

func asFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func floatField(metrics map[string]any, key string) float64 {
	value, ok := asFloat(metrics[key])
	if !ok {
		return 0
	}
	return value
}
