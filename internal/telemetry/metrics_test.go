package telemetry

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
)

func TestMetricFlatteningRoundTripsNestedDimensionsAndBooleans(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"running":    true,
		"queryCount": int64(7),
		"topQueries": []any{map[string]any{
			"queryId": "query-1", "query": "SELECT cart", "callCount": int64(3),
			"meanLatencyMillis": 1.5,
		}},
	}
	points, err := flattenMetricSample(input)
	if err != nil {
		t.Fatal(err)
	}
	sample := decodedMetricSample{
		Values: make(map[string]string), Types: make(map[string]string), Attributes: make(map[string]string),
	}
	for _, point := range points {
		attributes := []any{attribute("platformd.field", point.field)}
		for key, value := range point.dimensions {
			attributes = append(attributes, attribute("platformd.dimension."+key, value))
		}
		switch value := point.value.(type) {
		case bool:
			if value {
				sample.Values[point.field] = "1"
			} else {
				sample.Values[point.field] = "0"
			}
			sample.Types[point.field] = "int"
			attributes = append(attributes, attribute("platformd.value.type", "bool"))
		case json.Number:
			sample.Values[point.field] = value.String()
			if _, err := value.Int64(); err == nil {
				sample.Types[point.field] = "int"
			} else {
				sample.Types[point.field] = "double"
			}
		}
		encoded, err := json.Marshal(attributes)
		if err != nil {
			t.Fatal(err)
		}
		sample.Attributes[point.field] = string(encoded)
	}
	rebuilt, err := rebuildMetricValue(sample)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rebuilt["running"], true) || rebuilt["queryCount"] != int64(7) {
		t.Fatalf("rebuilt scalar metrics = %#v", rebuilt)
	}
	top, ok := rebuilt["topQueries"].([]any)
	if !ok || len(top) != 1 {
		t.Fatalf("rebuilt top queries = %#v", rebuilt["topQueries"])
	}
	query := top[0].(map[string]any)
	if query["queryId"] != "query-1" || query["query"] != "SELECT cart" || query["callCount"] != int64(3) || query["meanLatencyMillis"] != 1.5 {
		t.Fatalf("rebuilt top query = %#v", query)
	}
}

func TestMetricExportUsesDeltaAndCumulativeSums(t *testing.T) {
	t.Parallel()
	delta, err := otlpMetric(metricSample{namespace: "managed_postgres", scopeKind: "managed_postgres", scopeID: "db", observedAt: 1}, metricPoint{
		field: "queryCount", value: json.Number("3"), dimensions: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	cumulative, err := otlpMetric(metricSample{namespace: "resource", scopeKind: "resource_service", scopeID: "service", observedAt: 1}, metricPoint{
		field: "Proxy.HTTP.RequestsTotal", value: json.Number(strconv.Itoa(10)), dimensions: map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if delta["sum"].(map[string]any)["aggregationTemporality"] != 1 || cumulative["sum"].(map[string]any)["aggregationTemporality"] != 2 {
		t.Fatalf("sum temporalities = %#v / %#v", delta, cumulative)
	}
}
