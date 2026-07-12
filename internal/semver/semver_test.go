package semver_test

import (
	"testing"

	"github.com/iivankin/platformd/internal/semver"
)

func TestStrictSemVerValidationAndPrecedence(t *testing.T) {
	t.Parallel()

	invalid := []string{"", "v1.2.3", "1.2", "01.2.3", "1.2.3+build", "1.2.3-01", "1.2.3-"}
	for _, value := range invalid {
		if _, err := semver.Parse(value); err == nil {
			t.Errorf("Parse(%q) succeeded", value)
		}
	}
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"2.0.0",
		"10.0.0",
	}
	for index := 1; index < len(ordered); index++ {
		previous, err := semver.Parse(ordered[index-1])
		if err != nil {
			t.Fatal(err)
		}
		current, err := semver.Parse(ordered[index])
		if err != nil {
			t.Fatal(err)
		}
		if semver.Compare(previous, current) >= 0 {
			t.Fatalf("%s is not lower than %s", ordered[index-1], ordered[index])
		}
	}
}
