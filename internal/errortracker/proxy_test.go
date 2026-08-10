package errortracker

import "testing"

func TestDataPlanePathAllowed(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"/public/api/v1/apps":                    true,
		"/public/mcp":                            true,
		"/api/project/envelope/":                 true,
		"/api/project/minidump/":                 true,
		"/api/0/organizations/org/chunk-upload/": true,
		"/api/v1/apps":                           false,
		"/health":                                false,
		"/public/mcp/tools":                      false,
	}
	for path, allowed := range tests {
		if actual := DataPlanePathAllowed(path); actual != allowed {
			t.Errorf("DataPlanePathAllowed(%q) = %t, want %t", path, actual, allowed)
		}
	}
}
