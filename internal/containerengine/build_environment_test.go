package containerengine

import (
	"slices"
	"testing"
)

func TestBuildEnvironmentOptionsAreStable(t *testing.T) {
	values := buildEnvironmentOptions(map[string]string{
		"TOKEN":   "secret",
		"API_URL": "https://api.example.com",
	})
	if !slices.Equal(values, []string{"API_URL=https://api.example.com", "TOKEN=secret"}) {
		t.Fatalf("build environment = %v", values)
	}
}
