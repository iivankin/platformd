package resourcename

import "testing"

func TestValidateDNSLabel(t *testing.T) {
	for _, valid := range []string{"a", "api", "shop-2026", "a1"} {
		if err := Validate(valid); err != nil {
			t.Errorf("valid name %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "Upper", "with space", "-leading", "trailing-", "two.labels", string(make([]byte, 64))} {
		if err := Validate(invalid); err == nil {
			t.Errorf("invalid name %q was accepted", invalid)
		}
	}
}
