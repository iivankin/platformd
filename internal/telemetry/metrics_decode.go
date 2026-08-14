package telemetry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/iivankin/platformd/internal/state"
)

func decodeMetricValue(sample decodedMetricSample, destination any) error {
	value, err := rebuildMetricValue(sample)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode rebuilt telemetry metric: %w", err)
	}
	if err := json.Unmarshal(encoded, destination); err != nil {
		return fmt.Errorf("decode rebuilt telemetry metric: %w", err)
	}
	return nil
}

func decodeAggregateSamples(samples []decodedMetricSample, kind string) ([]state.AggregateMetricSample, error) {
	result := make([]state.AggregateMetricSample, 0, len(samples))
	for _, sample := range samples {
		var value struct {
			state.MetricValues
			RunningResources int
			TotalResources   int
		}
		if err := decodeMetricValue(sample, &value); err != nil {
			return nil, err
		}
		result = append(result, state.AggregateMetricSample{
			ScopeKind: kind, ScopeID: sample.ScopeID, ObservedAt: sample.ObservedAt,
			RunningResources: value.RunningResources, TotalResources: value.TotalResources,
			MetricValues: value.MetricValues,
		})
	}
	return result, nil
}

func rebuildMetricValue(sample decodedMetricSample) (map[string]any, error) {
	root := make(map[string]any)
	for field, encoded := range sample.Values {
		value, err := parseMetricNumber(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode telemetry metric field %s: %w", field, err)
		}
		attributes, err := decodePointAttributes(sample.Attributes[field])
		if err != nil {
			return nil, fmt.Errorf("decode telemetry metric attributes for %s: %w", field, err)
		}
		segments := strings.Split(field, ".")
		setMetricPath(root, segments, value)
		itemPath := metricItemPath(segments)
		for key, dimension := range attributes {
			const prefix = "platformd.dimension."
			if strings.HasPrefix(key, prefix) && len(itemPath) > 0 {
				setMetricPath(root, appendPath(itemPath, strings.TrimPrefix(key, prefix)), dimension)
			}
		}
	}
	return normalizeMetricContainers(root).(map[string]any), nil
}

func parseMetricNumber(value string) (any, error) {
	if len(value) < 3 || value[1] != ':' {
		return nil, fmt.Errorf("invalid tagged metric value")
	}
	scalar := value[2:]
	switch value[0] {
	case 'b':
		integer, err := strconv.ParseInt(scalar, 10, 64)
		return integer != 0, err
	case 'i':
		return strconv.ParseInt(scalar, 10, 64)
	case 'f':
		return strconv.ParseFloat(scalar, 64)
	default:
		return nil, fmt.Errorf("unknown metric value type %q", value[:1])
	}
}

func decodePointAttributes(encoded string) (map[string]string, error) {
	values := make(map[string]string)
	if encoded == "" {
		return values, nil
	}
	if err := json.Unmarshal([]byte(encoded), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func metricItemPath(segments []string) []string {
	for index, segment := range segments {
		if _, err := strconv.Atoi(segment); err == nil {
			result := make([]string, index+1)
			copy(result, segments[:index+1])
			return result
		}
	}
	return nil
}

func setMetricPath(root map[string]any, segments []string, value any) {
	current := root
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = make(map[string]any)
			current[segment] = next
		}
		current = next
	}
	current[segments[len(segments)-1]] = value
}

func normalizeMetricContainers(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for key, child := range object {
		object[key] = normalizeMetricContainers(child)
	}
	indices := make([]int, 0, len(object))
	for key := range object {
		index, err := strconv.Atoi(key)
		if err != nil || index < 0 {
			return object
		}
		indices = append(indices, index)
	}
	if len(indices) == 0 {
		return object
	}
	sort.Ints(indices)
	if indices[len(indices)-1] != len(indices)-1 {
		return object
	}
	result := make([]any, len(indices))
	for index := range result {
		result[index] = object[strconv.Itoa(index)]
	}
	return result
}
