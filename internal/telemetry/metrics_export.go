package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

type metricSample struct {
	namespace  string
	scopeKind  string
	scopeID    string
	observedAt int64
	value      any
}

type metricPoint struct {
	field      string
	value      any
	dimensions map[string]string
}

func (store *MetricStore) export(ctx context.Context, samples []metricSample) error {
	if len(samples) == 0 {
		return nil
	}
	resources := make([]any, 0, len(samples))
	for _, sample := range samples {
		points, err := flattenMetricSample(sample.value)
		if err != nil {
			return err
		}
		metrics := make([]any, 0, len(points))
		for _, point := range points {
			metric, err := otlpMetric(sample, point)
			if err != nil {
				return err
			}
			metrics = append(metrics, metric)
		}
		resources = append(resources, map[string]any{
			"resource": map[string]any{"attributes": []any{
				attribute("service.id", sample.scopeID),
				attribute("service.name", "platformd"),
			}},
			"scopeMetrics": []any{map[string]any{
				"scope":   map[string]any{"name": "platformd.metrics", "version": "1"},
				"metrics": metrics,
			}},
		})
	}
	body, err := json.Marshal(map[string]any{"resourceMetrics": resources})
	if err != nil {
		return fmt.Errorf("encode OTLP metrics: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, store.otlpEndpoint+"/v1/metrics", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := store.writeClient.Do(request)
	if err != nil {
		return fmt.Errorf("export OTLP metrics: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("export OTLP metrics returned HTTP %d", response.StatusCode)
	}
	return nil
}

func flattenMetricSample(value any) ([]metricPoint, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode metric sample: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, fmt.Errorf("normalize metric sample: %w", err)
	}
	points := make([]metricPoint, 0)
	flattenMetricValue(normalized, nil, nil, &points)
	return points, nil
}

func flattenMetricValue(value any, path []string, dimensions map[string]string, points *[]metricPoint) {
	switch value := value.(type) {
	case map[string]any:
		nextDimensions := cloneDimensions(dimensions)
		for key, child := range value {
			if text, ok := child.(string); ok {
				nextDimensions[key] = text
			}
		}
		for key, child := range value {
			if _, stringValue := child.(string); stringValue {
				continue
			}
			flattenMetricValue(child, appendPath(path, key), nextDimensions, points)
		}
	case []any:
		for index, child := range value {
			flattenMetricValue(child, appendPath(path, strconv.Itoa(index)), dimensions, points)
		}
	case json.Number, bool:
		if len(path) > 0 {
			*points = append(*points, metricPoint{
				field: strings.Join(path, "."), value: value, dimensions: cloneDimensions(dimensions),
			})
		}
	}
}

func appendPath(path []string, value string) []string {
	result := make([]string, len(path)+1)
	copy(result, path)
	result[len(path)] = value
	return result
}

func cloneDimensions(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func otlpMetric(sample metricSample, point metricPoint) (map[string]any, error) {
	attributes := []any{
		attribute("platformd.scope.kind", sample.scopeKind),
		attribute("platformd.scope.id", sample.scopeID),
		attribute("platformd.field", point.field),
	}
	for key, value := range point.dimensions {
		attributes = append(attributes, attribute("platformd.dimension."+key, value))
	}
	dataPoint := map[string]any{
		"timeUnixNano": strconv.FormatInt(sample.observedAt*1_000_000, 10),
		"attributes":   attributes,
	}
	switch value := point.value.(type) {
	case bool:
		if value {
			dataPoint["asInt"] = "1"
		} else {
			dataPoint["asInt"] = "0"
		}
		dataPoint["attributes"] = append(attributes, attribute("platformd.value.type", "bool"))
	case json.Number:
		if strings.ContainsAny(value.String(), ".eE") {
			parsed, err := value.Float64()
			if err != nil {
				return nil, fmt.Errorf("decode floating metric %s: %w", point.field, err)
			}
			dataPoint["asDouble"] = parsed
		} else {
			dataPoint["asInt"] = value.String()
		}
	default:
		return nil, fmt.Errorf("unsupported metric value for %s", point.field)
	}
	metric := map[string]any{
		"name":        metricName(sample.namespace, point.field),
		"description": "platformd collected metric",
		"unit":        "1",
	}
	kind, temporality := metricKind(sample.namespace, point.field)
	if kind == "gauge" {
		metric["gauge"] = map[string]any{"dataPoints": []any{dataPoint}}
	} else {
		metric["sum"] = map[string]any{
			"aggregationTemporality": temporality,
			"isMonotonic":            true,
			"dataPoints":             []any{dataPoint},
		}
	}
	return metric, nil
}

func metricName(namespace, field string) string {
	segments := strings.Split(field, ".")
	leaf := "value"
	for index := len(segments) - 1; index >= 0; index-- {
		if _, err := strconv.Atoi(segments[index]); err != nil {
			leaf = segments[index]
			break
		}
	}
	return "platformd." + strings.ReplaceAll(namespace, "_", ".") + "." + snakeMetricName(leaf)
}

func snakeMetricName(value string) string {
	var result strings.Builder
	for index, character := range value {
		if unicode.IsUpper(character) {
			if index > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(character))
		} else if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result.WriteRune(unicode.ToLower(character))
		} else if result.Len() > 0 && !strings.HasSuffix(result.String(), "_") {
			result.WriteByte('_')
		}
	}
	return strings.Trim(result.String(), "_")
}

func metricKind(namespace, field string) (string, int) {
	leaf := strings.ToLower(field[strings.LastIndex(field, ".")+1:])
	if strings.HasPrefix(namespace, "managed_") && managedDeltaField(leaf) {
		return "sum", 1
	}
	if namespace == "resource" && (strings.HasSuffix(leaf, "total") || strings.Contains(field, "DurationBuckets.")) {
		if strings.Contains(field, "DurationBuckets.") {
			return "sum", 1
		}
		return "sum", 2
	}
	return "gauge", 0
}

func managedDeltaField(field string) bool {
	switch field {
	case "querycount", "rowsread", "rowswritten", "bytesread", "byteshit",
		"commandcount", "netinputbytes", "netoutputbytes", "operationcount",
		"bytesin", "bytesout", "errorcount", "otherquerycount", "othercommandcount",
		"otheroperationcount", "callcount":
		return true
	default:
		return false
	}
}
