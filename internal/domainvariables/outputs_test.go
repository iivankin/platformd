package domainvariables

import "testing"

func TestOutputNamesUseSubdomainOrRootDomain(t *testing.T) {
	t.Parallel()
	tests := map[string]Names{
		"api.example.com":       {Public: "API_URL", Internal: "API_URL_INTERNAL"},
		"example.com":           {Public: "EXAMPLE_COM_URL", Internal: "EXAMPLE_COM_URL_INTERNAL"},
		"jobs.eu.example.co.uk": {Public: "JOBS_EU_URL", Internal: "JOBS_EU_URL_INTERNAL"},
	}
	for hostname, expected := range tests {
		actual, err := OutputNames(hostname)
		if err != nil {
			t.Fatalf("OutputNames(%q): %v", hostname, err)
		}
		if actual != expected {
			t.Fatalf("OutputNames(%q) = %#v, want %#v", hostname, actual, expected)
		}
	}
}
