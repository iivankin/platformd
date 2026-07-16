package variableexpression

import "testing"

func TestExpandInterpolatesMultipleResourceValues(t *testing.T) {
	t.Parallel()
	resolved, err := Expand("http://${{api.HOST}}:${{api.PORT}}/v1", func(reference Reference) (string, error) {
		values := map[Reference]string{
			{Resource: "api", Output: "HOST"}: "api.shop.internal",
			{Resource: "api", Output: "PORT"}: "8080",
		}
		return values[reference], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "http://api.shop.internal:8080/v1" {
		t.Fatalf("resolved = %q", resolved)
	}
}

func TestExpandRejectsMalformedReference(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"${{api}}", "${{API.URL}}", "${{api.bad-name}}", "${{api.URL}"} {
		if _, err := Expand(value, func(Reference) (string, error) { return "", nil }); err == nil {
			t.Fatalf("Expand(%q) succeeded", value)
		}
	}
}
