package id_test

import (
	"testing"

	"github.com/iivankin/platformd/internal/id"
)

func TestCUID2Generation(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 1_000)
	for range 1_000 {
		value, err := id.New()
		if err != nil {
			t.Fatal(err)
		}
		if !id.Valid(value) {
			t.Fatalf("generated invalid CUID2 %q", value)
		}
		if _, exists := seen[value]; exists {
			t.Fatalf("generated duplicate CUID2 %q", value)
		}
		seen[value] = struct{}{}
	}
}

func TestCUID2ValidationRejectsLegacyAndMalformedIDs(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"018bcfe5-687b-7fff-bfff-ffffffffffff",
		"1bcdefghijklmnopqrstuvwx",
		"abcdefghijklmnopqrstuvwX",
		"abcdefghijklmnopqrstuvw",
	} {
		if id.Valid(value) {
			t.Fatalf("accepted invalid CUID2 %q", value)
		}
	}
}
