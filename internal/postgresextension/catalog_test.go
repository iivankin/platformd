package postgresextension

import (
	"errors"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestCacheKeyPinsBaseArchitectureAndRecipe(t *testing.T) {
	recipe := VectorRecipe()
	desired := []state.ManagedPostgresExtension{{
		PostgresID: "postgres", Name: recipe.Name, Version: recipe.Version, RecipeDigest: recipe.Digest,
	}}
	first, err := CacheKey("sha256:base", "amd64", desired)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CacheKey("sha256:other", "amd64", desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || first == second {
		t.Fatalf("cache keys = %q/%q", first, second)
	}
	desired[0].RecipeDigest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := CacheKey("sha256:base", "amd64", desired); !errors.Is(err, ErrRecipeMismatch) {
		t.Fatalf("recipe mismatch error = %v", err)
	}
}
