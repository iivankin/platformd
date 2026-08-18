package analytics

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type Evaluation struct {
	Key            string `json:"key"`
	Value          any    `json:"value"`
	Variant        string `json:"variant,omitempty"`
	Reason         string `json:"reason"`
	Metadata       any    `json:"metadata,omitempty"`
	TargetingMatch bool   `json:"-"`
}

type TargetingGroup struct {
	Properties        []TargetingProperty `json:"properties"`
	RolloutPercentage int                 `json:"rollout_percentage"`
	RolloutSteps      []RolloutStep       `json:"rollout_steps,omitempty"`
	Variant           *string             `json:"variant"`
}

type RolloutStep struct {
	Percentage int   `json:"percentage"`
	AtMillis   int64 `json:"at"`
}

type TargetingProperty struct {
	Key      string `json:"key"`
	Operator string `json:"operator"`
	Value    any    `json:"value"`
}

type Targeting struct {
	Groups []TargetingGroup `json:"groups"`
}

func EvaluateFlag(flag state.AnalyticsFlag, targetingKey string, context map[string]any) Evaluation {
	return EvaluateFlagAt(flag, targetingKey, context, time.Now())
}

func EvaluateFlagAt(flag state.AnalyticsFlag, targetingKey string, context map[string]any, now time.Time) Evaluation {
	defaults := Evaluation{
		Key: flag.Key, Value: defaultFlagValue(flag), Variant: defaultVariant(flag),
		Reason: "DEFAULT", Metadata: flagMetadata(flag),
	}
	if !flag.Enabled || targetingKey == "" {
		return defaults
	}
	var targeting Targeting
	if err := json.Unmarshal([]byte(flag.TargetingJSON), &targeting); err != nil {
		return defaults
	}
	if len(targeting.Groups) == 0 {
		targeting.Groups = []TargetingGroup{{RolloutPercentage: 100}}
	}
	nowMillis := now.UnixMilli()
	for _, group := range targeting.Groups {
		if !groupMatches(group, context) {
			continue
		}
		if int(RolloutBucket(flag.Key, targetingKey)) >= groupRollout(group, nowMillis) {
			return defaults
		}
		variant := pickVariant(flag, group.Variant, targetingKey)
		return Evaluation{
			Key: flag.Key, Value: variantValue(flag, variant), Variant: variant,
			Reason: "TARGETING_MATCH", Metadata: flagMetadata(flag), TargetingMatch: true,
		}
	}
	return defaults
}

func groupRollout(group TargetingGroup, nowMillis int64) int {
	percentage := group.RolloutPercentage
	found := false
	latest := int64(0)
	for _, step := range group.RolloutSteps {
		if step.AtMillis <= nowMillis && (!found || step.AtMillis >= latest) {
			found = true
			latest = step.AtMillis
			percentage = step.Percentage
		}
	}
	if percentage < 0 {
		return 0
	}
	if percentage > 100 {
		return 100
	}
	return percentage
}

func flagMetadata(flag state.AnalyticsFlag) any {
	raw := strings.TrimSpace(flag.PayloadJSON)
	if raw == "" || raw == "null" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return nil
	}
	if _, ok := value.(map[string]any); ok {
		return value
	}
	return map[string]any{"payload": value}
}

func ofrepBody(evaluation Evaluation) map[string]any {
	body := map[string]any{
		"key": evaluation.Key, "value": evaluation.Value, "reason": evaluation.Reason,
	}
	if evaluation.Variant != "" {
		body["variant"] = evaluation.Variant
	}
	if evaluation.Metadata != nil {
		body["metadata"] = evaluation.Metadata
	}
	return body
}

func RolloutBucket(flagKey, targetingKey string) uint64 {
	sum := sha256.Sum256(append(append([]byte(flagKey), 0), targetingKey...))
	return binary.BigEndian.Uint64(sum[:8]) % 100
}

func ConfigVersion(flags []state.AnalyticsFlag, targetingKey string, context map[string]any, now time.Time) string {
	sum := sha256.New()
	_, _ = sum.Write([]byte(targetingKey))
	encoded, _ := json.Marshal(context)
	_, _ = sum.Write(encoded)
	nowMillis := now.UnixMilli()
	for _, flag := range flags {
		_, _ = sum.Write([]byte(flag.Key))
		_, _ = sum.Write([]byte(strconv.FormatInt(flag.UpdatedAtMillis, 10)))
		_, _ = sum.Write([]byte(flag.TargetingJSON))
		_, _ = sum.Write([]byte(flag.PayloadJSON))
		encodedVariants, _ := json.Marshal(flag.Variants)
		_, _ = sum.Write(encodedVariants)
		var targeting Targeting
		_ = json.Unmarshal([]byte(flag.TargetingJSON), &targeting)
		for _, group := range targeting.Groups {
			_, _ = sum.Write([]byte(strconv.Itoa(groupRollout(group, nowMillis))))
		}
	}
	return hex16(sum.Sum(nil))
}

func hex16(sum []byte) string {
	const hexdigits = "0123456789abcdef"
	if len(sum) < 16 {
		padded := make([]byte, 16)
		copy(padded, sum)
		sum = padded
	}
	out := make([]byte, 32)
	for i, b := range sum[:16] {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}

func defaultFlagValue(flag state.AnalyticsFlag) any {
	if flag.Type == "boolean" {
		return false
	}
	if len(flag.Variants) == 0 {
		return nil
	}
	return flag.Variants[0].Key
}

func defaultVariant(flag state.AnalyticsFlag) string {
	if flag.Type == "boolean" {
		return "false"
	}
	if len(flag.Variants) == 0 {
		return ""
	}
	return flag.Variants[0].Key
}

func pickVariant(flag state.AnalyticsFlag, override *string, targetingKey string) string {
	if override != nil && *override != "" {
		return *override
	}
	// Boolean flags ignore the weight table: in-rollout is true unless a group
	// pins a variant override (used when shipping a false winner).
	if flag.Type == "boolean" {
		return "true"
	}
	total := 0.0
	for _, variant := range flag.Variants {
		total += variant.Percentage
	}
	if total <= 0 {
		return defaultVariant(flag)
	}
	bucket := float64(RolloutBucket(flag.Key+"|variant", targetingKey))
	cursor := 0.0
	for _, variant := range flag.Variants {
		cursor += variant.Percentage * 100 / total
		if bucket < cursor {
			return variant.Key
		}
	}
	return flag.Variants[len(flag.Variants)-1].Key
}

func variantValue(flag state.AnalyticsFlag, variant string) any {
	if flag.Type == "boolean" {
		return variant == "true"
	}
	return variant
}

func groupMatches(group TargetingGroup, context map[string]any) bool {
	for _, property := range group.Properties {
		if !propertyMatches(property, context) {
			return false
		}
	}
	return true
}

func propertyMatches(property TargetingProperty, context map[string]any) bool {
	value, exists := context[property.Key]
	text := stringify(value)
	want := stringify(property.Value)
	switch property.Operator {
	case "is_set":
		return exists && text != ""
	case "exact":
		return exists && text == want
	case "is_not":
		return !exists || text != want
	case "icontains":
		return exists && strings.Contains(strings.ToLower(text), strings.ToLower(want))
	case "gt":
		return exists && toFloat(value) > toFloat(property.Value)
	case "lt":
		return exists && toFloat(value) < toFloat(property.Value)
	default:
		return false
	}
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	default:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	}
}

func toFloat(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}
