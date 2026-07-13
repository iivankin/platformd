package corsorigin

import "testing"

func TestNormalizeAllCanonicalizesAndDeduplicatesExactOrigins(t *testing.T) {
	result, err := NormalizeAll([]string{"HTTPS://App.Example.com/", "https://app.example.com", "http://localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || result[0] != "https://app.example.com" || result[1] != "http://localhost:3000" {
		t.Fatalf("origins = %#v", result)
	}
	for _, invalid := range []string{"*", "ftp://example.com", "https://example.com/path", "https://user@example.com"} {
		if _, err := NormalizeAll([]string{invalid}); err == nil {
			t.Fatalf("accepted invalid origin %q", invalid)
		}
	}
}
