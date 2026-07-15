package daemon

import (
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/state"
)

func TestDatabaseResourceURLsEscapeCredentialValues(t *testing.T) {
	postgres := state.ManagedPostgres{
		Name: "db", ProjectName: "shop", DatabaseName: "app data", OwnerUsername: "app@owner",
	}
	postgresURL, err := postgresOutput(postgres, "p@ss/word", "DATABASE_URL")
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{"app%40owner", "p%40ss%2Fword", "app%20data"} {
		if !strings.Contains(postgresURL, encoded) {
			t.Fatalf("PostgreSQL URL %q does not contain %q", postgresURL, encoded)
		}
	}

	redisURL, err := redisOutput(
		state.ManagedRedis{Name: "cache", ProjectName: "shop"}, "p@ss/word", "REDIS_URL",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(redisURL, ":p%40ss%2Fword@cache.shop.internal:6379/0") {
		t.Fatalf("Redis URL is not safely encoded: %q", redisURL)
	}
}
