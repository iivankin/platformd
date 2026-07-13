package bucketname

import "testing"

func TestValidate(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"assets", "assets.prod", "a-123"} {
		if err := Validate(value); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"ab", "Assets", "192.0.2.1", "a..b", "-assets", "assets-"} {
		if err := Validate(value); err == nil {
			t.Fatalf("invalid bucket %q accepted", value)
		}
	}
}
