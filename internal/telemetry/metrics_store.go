package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type MetricStore struct {
	state         *state.Store
	otlpEndpoint  string
	queryEndpoint string
	writeClient   *http.Client
	queryClient   *http.Client
}

func NewMetricStore(store *state.Store, otlpEndpoint, queryEndpoint string) (*MetricStore, error) {
	if store == nil || !validHTTPEndpoint(otlpEndpoint) || !validHTTPEndpoint(queryEndpoint) {
		return nil, errors.New("telemetry metric store configuration is incomplete")
	}
	return &MetricStore{
		state: store, otlpEndpoint: otlpEndpoint, queryEndpoint: queryEndpoint,
		writeClient: &http.Client{Timeout: 15 * time.Second},
		queryClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func validHTTPEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "http" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func (store *MetricStore) ResourceMetricTargets(ctx context.Context) ([]state.ResourceMetricTarget, error) {
	return store.state.ResourceMetricTargets(ctx)
}

func (store *MetricStore) ResourceMetricProjectIDs(ctx context.Context) ([]string, error) {
	return store.state.ResourceMetricProjectIDs(ctx)
}

func (store *MetricStore) ManagedPostgresResources(ctx context.Context) ([]state.ManagedPostgres, error) {
	return store.state.ManagedPostgresResources(ctx)
}

func (store *MetricStore) ManagedRedisResources(ctx context.Context) ([]state.ManagedRedis, error) {
	return store.state.ManagedRedisResources(ctx)
}

func (store *MetricStore) ObjectStores(ctx context.Context) ([]state.ObjectStore, error) {
	return store.state.ObjectStores(ctx)
}

func (store *MetricStore) RecordMetricBatch(ctx context.Context, batch state.MetricBatch) error {
	samples := make([]metricSample, 0, len(batch.Resources)+len(batch.Aggregates))
	for _, sample := range batch.Resources {
		samples = append(samples, metricSample{
			namespace: "resource", scopeKind: "resource_" + sample.Kind,
			scopeID:    sample.ResourceID,
			observedAt: sample.ObservedAt, value: sample.MetricValues,
		})
	}
	for _, sample := range batch.Aggregates {
		value := struct {
			state.MetricValues
			RunningResources int
			TotalResources   int
		}{sample.MetricValues, sample.RunningResources, sample.TotalResources}
		samples = append(samples, metricSample{
			namespace: "resource", scopeKind: sample.ScopeKind, scopeID: sample.ScopeID,
			observedAt: sample.ObservedAt, value: value,
		})
	}
	return store.export(ctx, samples)
}

func (store *MetricStore) RecordManagedStatBatch(ctx context.Context, samples []state.ManagedStatSample, _ int64) error {
	batch := make([]metricSample, 0, len(samples))
	for _, sample := range samples {
		decoder := json.NewDecoder(strings.NewReader(sample.MetricsJSON))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode managed metric sample: %w", err)
		}
		batch = append(batch, metricSample{
			namespace: "managed_" + sample.Kind, scopeKind: "managed_" + sample.Kind,
			scopeID: sample.ResourceID, observedAt: sample.ObservedAt, value: value,
		})
	}
	return store.export(ctx, batch)
}

func (store *MetricStore) ResourceMetricSamples(ctx context.Context, kind, resourceID string, from, to int64) ([]state.ResourceMetricSample, error) {
	samples, err := store.query(ctx, "resource_"+kind, resourceID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]state.ResourceMetricSample, 0, len(samples))
	for _, sample := range samples {
		var values state.MetricValues
		if err := decodeMetricValue(sample, &values); err != nil {
			return nil, err
		}
		result = append(result, state.ResourceMetricSample{
			Kind: kind, ResourceID: resourceID, ObservedAt: sample.ObservedAt,
			MetricValues: values,
		})
	}
	return result, nil
}

func (store *MetricStore) AggregateMetricSamples(ctx context.Context, kind, id string, from, to int64) ([]state.AggregateMetricSample, error) {
	samples, err := store.query(ctx, kind, id, from, to)
	if err != nil {
		return nil, err
	}
	return decodeAggregateSamples(samples, kind, id)
}

func (store *MetricStore) ResourceMetricSeriesByProject(ctx context.Context, projectID string, from, to int64) ([]state.ResourceMetricSeries, error) {
	catalog, err := store.state.ResourceMetricCatalog(ctx, projectID)
	if err != nil {
		return nil, err
	}
	series := make([]state.ResourceMetricSeries, 0, len(catalog))
	for _, resource := range catalog {
		samples, err := store.ResourceMetricSamples(ctx, resource.Kind, resource.ID, from, to)
		if err != nil {
			return nil, err
		}
		if len(samples) > 0 {
			series = append(series, state.ResourceMetricSeries{
				Kind: resource.Kind, ResourceID: resource.ID, Name: resource.Name, Samples: samples,
			})
		}
	}
	return series, nil
}

func (store *MetricStore) ProjectAggregateMetricSeries(ctx context.Context, from, to int64) ([]state.AggregateMetricSeries, error) {
	catalog, err := store.state.ProjectMetricCatalog(ctx)
	if err != nil {
		return nil, err
	}
	series := make([]state.AggregateMetricSeries, 0, len(catalog))
	for _, project := range catalog {
		samples, err := store.AggregateMetricSamples(ctx, "project", project.ID, from, to)
		if err != nil {
			return nil, err
		}
		if len(samples) > 0 {
			series = append(series, state.AggregateMetricSeries{ScopeID: project.ID, Name: project.Name, Samples: samples})
		}
	}
	return series, nil
}

func (store *MetricStore) ManagedStatSamples(ctx context.Context, kind, resourceID string, from, to int64) ([]state.ManagedStatSample, error) {
	samples, err := store.query(ctx, "managed_"+kind, resourceID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]state.ManagedStatSample, 0, len(samples))
	for _, sample := range samples {
		value, err := rebuildMetricValue(sample)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode managed metric history: %w", err)
		}
		result = append(result, state.ManagedStatSample{
			Kind: kind, ResourceID: resourceID, ObservedAt: sample.ObservedAt, MetricsJSON: string(encoded),
		})
	}
	return result, nil
}

type metricQueryRow struct {
	ScopeKind    string `json:"scope_kind"`
	ScopeID      string `json:"scope_id"`
	TimeUnixNano uint64 `json:"time_unix_nano"`
	Values       string `json:"values"`
	ValueTypes   string `json:"value_types"`
	Attributes   string `json:"attributes"`
}

type decodedMetricSample struct {
	ScopeKind  string
	ScopeID    string
	ObservedAt int64
	Values     map[string]string
	Types      map[string]string
	Attributes map[string]string
}

func (store *MetricStore) query(ctx context.Context, scopeKind, scopeID string, from, to int64) ([]decodedMetricSample, error) {
	endpoint, err := url.Parse(store.queryEndpoint + "/internal/metrics")
	if err != nil {
		return nil, err
	}
	query := endpoint.Query()
	query.Set("scopeKind", scopeKind)
	if scopeID != "" {
		query.Set("scopeId", scopeID)
	}
	query.Set("from", strconv.FormatInt(from, 10))
	query.Set("to", strconv.FormatInt(to, 10))
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := store.queryClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query telemetry metrics: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("query telemetry metrics returned HTTP %d", response.StatusCode)
	}
	var rows []metricQueryRow
	if err := json.NewDecoder(response.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode telemetry metric query: %w", err)
	}
	result := make([]decodedMetricSample, 0, len(rows))
	for _, row := range rows {
		sample := decodedMetricSample{
			ScopeKind: row.ScopeKind, ScopeID: row.ScopeID,
			ObservedAt: int64(row.TimeUnixNano / 1_000_000),
		}
		if err := json.Unmarshal([]byte(row.Values), &sample.Values); err != nil {
			return nil, fmt.Errorf("decode telemetry metric values: %w", err)
		}
		if err := json.Unmarshal([]byte(row.ValueTypes), &sample.Types); err != nil {
			return nil, fmt.Errorf("decode telemetry metric types: %w", err)
		}
		if err := json.Unmarshal([]byte(row.Attributes), &sample.Attributes); err != nil {
			return nil, fmt.Errorf("decode telemetry metric attributes: %w", err)
		}
		result = append(result, sample)
	}
	return result, nil
}
