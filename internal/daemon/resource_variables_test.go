package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iivankin/platformd/internal/serviceconfig"
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

func TestResourceVariableResolverExpandsServiceVariablesAndDomainOutputs(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := store.CreateService(ctx, state.CreateService{
		ID: "api", ProjectID: project.ID, Name: "api", Enabled: true,
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PublicImageSource("alpine"),
			Environment: map[string]string{
				"API_PATH":        "/v1",
				"PUBLIC_ENDPOINT": "https://backend${{api.API_PATH}}",
			},
		},
		AuditEventID: "api-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AttachServiceDomain(ctx, state.AttachServiceDomainInput{
		ProjectID: project.ID, ServiceID: api.ID, Hostname: "api.example.com", TargetPort: 8080,
		AuditEventID: "domain-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := store.CreateService(ctx, state.CreateService{
		ID: "worker", ProjectID: project.ID, Name: "worker", Enabled: true,
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PublicImageSource("alpine"),
			Environment: map[string]string{
				"UPSTREAM":          "${{api.PUBLIC_ENDPOINT}}/ready",
				"UPSTREAM_PUBLIC":   "${{api.API_URL}}",
				"UPSTREAM_INTERNAL": "${{api.API_URL_INTERNAL}}/health",
			},
		},
		AuditEventID: "worker-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := (resourceVariableResolver{store: store}).Resolve(ctx, worker, "deployment")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"UPSTREAM":                 "https://backend/v1/ready",
		"UPSTREAM_PUBLIC":          "https://api.example.com",
		"UPSTREAM_INTERNAL":        "http://api.shop.internal:8080/health",
		"PLATFORMD_PROJECT_ID":     "project",
		"PLATFORMD_PROJECT_NAME":   "shop",
		"PLATFORMD_SERVICE_ID":     "worker",
		"PLATFORMD_SERVICE_NAME":   "worker",
		"PLATFORMD_DEPLOYMENT_ID":  "deployment",
		"PLATFORMD_PRIVATE_DOMAIN": "worker.shop.internal",
		"PLATFORMD_PUBLIC_URLS":    "",
	}
	for name, value := range want {
		if resolved[name] != value {
			t.Fatalf("resolved %s = %q, want %q", name, resolved[name], value)
		}
	}
}

func TestResourceVariableResolverExpandsImportedNetworkGatewayOutputs(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateNetworkGateway(ctx, state.CreateNetworkGateway{
		ID: "warehouse", ProjectID: project.ID,
		Configuration: state.NetworkGatewayConfiguration{
			Name: "warehouse", Mode: "import", Transport: "vpc", Protocol: "tcp",
			InterfaceName: "wg0", SourceAddress: "10.20.0.2", ListenPort: 5432,
			RemoteHost: "10.20.0.9", RemotePort: 5432,
		},
		AuditEventID: "gateway-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	worker, err := store.CreateService(ctx, state.CreateService{
		ID: "worker", ProjectID: project.ID, Name: "worker", Enabled: true,
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PublicImageSource("alpine"),
			Environment: map[string]string{
				"WAREHOUSE_HOST":    "${{warehouse.HOST}}",
				"WAREHOUSE_PORT":    "${{warehouse.PORT}}",
				"WAREHOUSE_ADDRESS": "postgres://${{warehouse.ADDRESS}}/inventory",
			},
		},
		AuditEventID: "worker-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := (resourceVariableResolver{store: store}).Resolve(ctx, worker, "deployment")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"WAREHOUSE_HOST":    "warehouse.shop.internal",
		"WAREHOUSE_PORT":    "5432",
		"WAREHOUSE_ADDRESS": "postgres://warehouse.shop.internal:5432/inventory",
	}
	for name, value := range want {
		if resolved[name] != value {
			t.Fatalf("resolved %s = %q, want %q", name, resolved[name], value)
		}
	}
}

func TestResourceVariableResolverRejectsCycles(t *testing.T) {
	ctx := context.Background()
	store, err := state.Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	project, err := store.CreateProject(ctx, state.CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateService(ctx, state.CreateService{
		ID: "cycle", ProjectID: project.ID, Name: "cycle", Enabled: true,
		Snapshot: serviceconfig.Snapshot{
			Source: serviceconfig.PublicImageSource("alpine"),
			Environment: map[string]string{
				"FIRST":  "${{cycle.SECOND}}",
				"SECOND": "${{cycle.FIRST}}",
			},
		},
		AuditEventID: "service-audit", ActorKind: "access", ActorID: "actor",
		ActorEmail: "admin@example.com", CreatedAtMillis: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (resourceVariableResolver{store: store}).Resolve(ctx, service, "deployment")
	if err == nil || !strings.Contains(err.Error(), "variable reference cycle") {
		t.Fatalf("cycle resolution error = %v", err)
	}
}
