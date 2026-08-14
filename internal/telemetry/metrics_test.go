package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
)

func TestMetricQueryDecodesCompactProtocol(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("scopeIds") != "service-a,service-b" {
			t.Errorf("scopeIds = %q", request.URL.Query().Get("scopeIds"))
		}
		_ = json.NewEncoder(writer).Encode([]map[string]any{{
			"scope_kind": "resource_service", "scope_id": "service-a", "time_unix_nano": uint64(42_000_000),
			"values":     map[string]string{"MemoryBytes": "i:42", "Running": "b:1"},
			"attributes": map[string]string{},
		}})
	}))
	defer server.Close()
	store := &MetricStore{queryEndpoint: server.URL, queryClient: server.Client()}
	samples, err := store.query(context.Background(), "resource_service", []string{"service-a", "service-b"}, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].ScopeID != "service-a" || samples[0].ObservedAt != 42 {
		t.Fatalf("samples = %#v", samples)
	}
	var values struct {
		MemoryBytes uint64
		Running     bool
	}
	if err := decodeMetricValue(samples[0], &values); err != nil {
		t.Fatal(err)
	}
	if values.MemoryBytes != 42 || !values.Running {
		t.Fatalf("values = %#v", values)
	}
}

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
		Values: make(map[string]string), Attributes: make(map[string]string),
	}
	for _, point := range points {
		attributes := make(map[string]string)
		for key, value := range point.dimensions {
			attributes["platformd.dimension."+key] = value
		}
		switch value := point.value.(type) {
		case bool:
			if value {
				sample.Values[point.field] = "b:1"
			} else {
				sample.Values[point.field] = "b:0"
			}
		case json.Number:
			if _, err := value.Int64(); err == nil {
				sample.Values[point.field] = "i:" + value.String()
			} else {
				sample.Values[point.field] = "f:" + value.String()
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
