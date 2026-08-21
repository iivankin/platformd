package analytics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBotRangeVerifierUsesOfficialSourceForClaimedAgent(t *testing.T) {
	t.Parallel()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(response, "unavailable", http.StatusServiceUnavailable)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "prefixes": [
    {"ipv4Prefix": "203.0.113.0/28"},
    {"ipv6Prefix": "2001:db8:42::/48"}
  ]
}`))
	}))
	t.Cleanup(server.Close)

	verifier := newBotRangeVerifier(server.Client(), []botRangeSource{{
		url: server.URL, botNames: []string{"OAI-SearchBot"},
	}})
	if err := verifier.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !verifier.Verify("OAI-SearchBot", "203.0.113.7") || !verifier.Verify("OAI-SearchBot", "2001:db8:42::1") {
		t.Fatal("official IPv4 and IPv6 ranges should verify")
	}
	if verifier.Verify("OAI-SearchBot", "198.51.100.7") || verifier.Verify("GPTBot", "203.0.113.7") {
		t.Fatal("wrong address or agent must not verify")
	}

	fail = true
	if err := verifier.refresh(context.Background()); err == nil {
		t.Fatal("failed refresh should be reported")
	}
	if !verifier.Verify("OAI-SearchBot", "203.0.113.7") {
		t.Fatal("failed refresh must retain the last successful snapshot")
	}
}
