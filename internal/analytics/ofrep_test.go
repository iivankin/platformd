package analytics

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

func TestRolloutBucketIsStableAndBounded(t *testing.T) {
	t.Parallel()
	first := RolloutBucket("pricing-v2", "aid-user")
	second := RolloutBucket("pricing-v2", "aid-user")
	if first != second {
		t.Fatalf("bucket moved from %d to %d", first, second)
	}
	if first > 99 {
		t.Fatalf("bucket %d is out of 0-99", first)
	}
	other := RolloutBucket("pricing-v2", "other-user")
	if first == other {
		t.Fatal("different targeting keys should usually land in different buckets")
	}
}

func TestEvaluateFlagDefaultsWithoutTargetingKey(t *testing.T) {
	t.Parallel()
	flag := state.AnalyticsFlag{
		Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:      []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
		TargetingJSON: `{"groups":[{"properties":[],"rollout_percentage":100,"variant":null}]}`,
	}
	evaluation := EvaluateFlag(flag, "", nil)
	if evaluation.TargetingMatch || evaluation.Value != false {
		t.Fatalf("empty targeting key should return default: %+v", evaluation)
	}
	matched := EvaluateFlag(flag, "aid-user", map[string]any{})
	if !matched.TargetingMatch || matched.Value != true || matched.Variant != "true" {
		t.Fatalf("full rollout should match: %+v", matched)
	}
}

func TestEvaluateFlagIgnoresBooleanVariantWeights(t *testing.T) {
	t.Parallel()
	flag := state.AnalyticsFlag{
		Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:      []state.AnalyticsFlagVariant{{Key: "false", Percentage: 99}, {Key: "true", Percentage: 1}},
		TargetingJSON: `{"groups":[{"properties":[],"rollout_percentage":100}]}`,
	}
	evaluation := EvaluateFlag(flag, "aid-user", map[string]any{})
	if evaluation.Value != true || evaluation.Variant != "true" {
		t.Fatalf("boolean match should serve true, not the weight table: %+v", evaluation)
	}
}

func TestEvaluateFlagHonorsGroupVariantOverride(t *testing.T) {
	t.Parallel()
	falseVariant := "false"
	flag := state.AnalyticsFlag{
		Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants: []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
		TargetingJSON: mustJSON(t, Targeting{Groups: []TargetingGroup{{
			RolloutPercentage: 100, Variant: &falseVariant,
		}}}),
	}
	evaluation := EvaluateFlag(flag, "aid-user", map[string]any{})
	if !evaluation.TargetingMatch || evaluation.Value != false || evaluation.Variant != "false" {
		t.Fatalf("override should force false: %+v", evaluation)
	}
}

func TestEvaluateFlagZeroRolloutIsOff(t *testing.T) {
	t.Parallel()
	flag := state.AnalyticsFlag{
		Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants: []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
		TargetingJSON: mustJSON(t, Targeting{Groups: []TargetingGroup{{
			RolloutPercentage: 0,
		}}}),
	}
	evaluation := EvaluateFlag(flag, "aid-user", map[string]any{})
	if evaluation.TargetingMatch || evaluation.Value != false {
		t.Fatalf("0%% rollout should stay default: %+v", evaluation)
	}
}

func TestEvaluateFlagGradualRollout(t *testing.T) {
	t.Parallel()
	const visitor = "aid-user"
	bucket := int(RolloutBucket("pricing-v2", visitor))
	flag := state.AnalyticsFlag{
		Key: "pricing-v2", Type: "boolean", Enabled: true,
		PayloadJSON: `{"plan":"pro"}`,
		Variants:    []state.AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
		TargetingJSON: mustJSON(t, Targeting{Groups: []TargetingGroup{{
			RolloutPercentage: 0,
			RolloutSteps: []RolloutStep{
				{Percentage: 0, AtMillis: 1_000},
				{Percentage: bucket, AtMillis: 2_000},
				{Percentage: bucket + 1, AtMillis: 3_000},
				{Percentage: 100, AtMillis: 4_000},
			},
		}}}),
	}
	before := EvaluateFlagAt(flag, visitor, map[string]any{}, time.UnixMilli(1_500))
	if before.TargetingMatch || before.Value != false {
		t.Fatalf("0%% step should stay default: %+v", before)
	}
	edge := EvaluateFlagAt(flag, visitor, map[string]any{}, time.UnixMilli(2_500))
	if edge.TargetingMatch {
		t.Fatalf("bucket %d should be excluded at %d%%: %+v", bucket, bucket, edge)
	}
	included := EvaluateFlagAt(flag, visitor, map[string]any{}, time.UnixMilli(3_500))
	if !included.TargetingMatch || included.Value != true {
		t.Fatalf("bucket %d should match at %d%%: %+v", bucket, bucket+1, included)
	}
	metadata, _ := included.Metadata.(map[string]any)
	if metadata["plan"] != "pro" {
		t.Fatalf("payload should be OFREP metadata: %+v", included.Metadata)
	}
}

func TestConfigVersionMovesWhenRolloutStepActivates(t *testing.T) {
	t.Parallel()
	flag := state.AnalyticsFlag{
		Key: "pricing-v2", Type: "boolean", Enabled: true, UpdatedAtMillis: 1,
		TargetingJSON: mustJSON(t, Targeting{Groups: []TargetingGroup{{
			RolloutPercentage: 1,
			RolloutSteps: []RolloutStep{
				{Percentage: 1, AtMillis: 1_000},
				{Percentage: 50, AtMillis: 2_000},
			},
		}}}),
	}
	flags := []state.AnalyticsFlag{flag}
	before := ConfigVersion(flags, "aid-user", map[string]any{}, time.UnixMilli(1_500))
	after := ConfigVersion(flags, "aid-user", map[string]any{}, time.UnixMilli(2_500))
	if before == after {
		t.Fatal("etag should change when a scheduled rollout step becomes active")
	}
}

func TestFlagMetadataWrapsNonObjectPayload(t *testing.T) {
	t.Parallel()
	wrapped := flagMetadata(state.AnalyticsFlag{PayloadJSON: `"ok"`})
	object, _ := wrapped.(map[string]any)
	if object["payload"] != "ok" {
		t.Fatalf("non-object payload should wrap: %+v", wrapped)
	}
	if flagMetadata(state.AnalyticsFlag{PayloadJSON: "null"}) != nil {
		t.Fatal("null payload should be omitted")
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
