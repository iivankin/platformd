package mailer

import "testing"

func TestLatestSeriesValuesUsesNumericTimeOrder(t *testing.T) {
	got := latestSeriesValues([]MetricPoint{
		{TimeUnixNano: "10", Value: 1, Series: "a"},
		{TimeUnixNano: "9", Value: 2, Series: "a"},
	})
	if got["a"] != 1 {
		t.Fatalf("latest = %v, want the value from time 10", got)
	}
}
