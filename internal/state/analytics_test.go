package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyticsTrackerRootsMustNotOverlap(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "shop", ProjectID: "project", Name: "Shop", RootDomain: "shop.com",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 2, UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "nested", ProjectID: "project", Name: "App", RootDomain: "app.shop.com",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != ErrAnalyticsTrackerConflict {
		t.Fatalf("nested root = %v", err)
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "admin", ProjectID: "project", Name: "Admin", RootDomain: "admin.other.io",
		Mode: AnalyticsModeCookieless, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	trackers, err := store.AnalyticsTrackers(ctx, "project")
	if err != nil || len(trackers) != 2 {
		t.Fatalf("trackers = %d, %v", len(trackers), err)
	}
	if !HostMatchesTracker("www.shop.com", "shop.com") || HostMatchesTracker("shop.io", "shop.com") {
		t.Fatal("hostname membership")
	}
	if NormalizeTrackerRoot("Example.com.:443") != "example.com" || NormalizeTrackerRoot("[::1]:443") != "::1" {
		t.Fatalf("normalize root = %q %q", NormalizeTrackerRoot("Example.com.:443"), NormalizeTrackerRoot("[::1]:443"))
	}
	if !RootsOverlap("shop.com:443", "www.shop.com") {
		t.Fatal("port must not bypass root overlap")
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "ported", ProjectID: "project", Name: "Ported", RootDomain: "shop.com:443",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 5, UpdatedAtMillis: 5,
	}); err != ErrAnalyticsTrackerConflict {
		t.Fatalf("ported overlapping root = %v", err)
	}
}

func TestAnalyticsTrackerRootsMustNotOverlapAcrossProjects(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, project := range []struct{ id, name, audit string }{
		{"project", "shop", "project-audit"},
		{"other", "other", "other-audit"},
	} {
		if _, err := store.CreateProject(ctx, CreateProject{
			ID: project.id, Name: project.name, AuditEventID: project.audit,
			ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "shop", ProjectID: "project", Name: "Shop", RootDomain: "shop.com",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 2, UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "stolen", ProjectID: "other", Name: "Stolen", RootDomain: "shop.com",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != ErrAnalyticsTrackerConflict {
		t.Fatalf("cross-project root = %v", err)
	}
}

func TestTrackerSlugKeepsHyphensDistinctFromDots(t *testing.T) {
	if TrackerSlug("my.site.com") == TrackerSlug("my-site.com") {
		t.Fatal("dot and hyphen roots must not share a slug")
	}
	if TrackerSlug("shop.example.com") != "shop--example--com" {
		t.Fatalf("slug = %q", TrackerSlug("shop.example.com"))
	}
}

func TestAnalyticsGoalOCCReturnsChanged(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsGoal(ctx, AnalyticsGoal{
		ID: "goal", TrackerID: "shop", Name: "Signup", ActionType: "event",
		ActionValue: "signup_completed", CreatedAtMillis: 2, UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAnalyticsGoal(ctx, AnalyticsGoal{
		ID: "goal", TrackerID: "shop", Name: "Signup", ActionType: "event",
		ActionValue: "signup_completed", UpdatedAtMillis: 4,
	}, 1); err != ErrAnalyticsChanged {
		t.Fatalf("stale goal update = %v", err)
	}
	if err := store.UpdateAnalyticsGoal(ctx, AnalyticsGoal{
		ID: "missing", TrackerID: "shop", Name: "Signup", ActionType: "event",
		ActionValue: "signup_completed", UpdatedAtMillis: 4,
	}, 2); err != ErrAnalyticsGoalNotFound {
		t.Fatalf("missing goal update = %v", err)
	}
}

func TestCreateAnalyticsExperimentRequiresFlagOnTracker(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "other", ProjectID: "project", Name: "Other", RootDomain: "other.com",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "experiment", FlagID: "flag", TrackerID: "other", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{EventName: "signup_completed"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 5, CreatedAtMillis: 5, UpdatedAtMillis: 5,
	}); err != ErrAnalyticsFlagNotFound {
		t.Fatalf("cross-tracker experiment = %v", err)
	}
}

func TestCreateAnalyticsExperimentRequiresGoalAndControlVariant(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "missing-goal", FlagID: "flag", TrackerID: "shop", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{GoalID: "gone"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 5, CreatedAtMillis: 5, UpdatedAtMillis: 5,
	}); err != ErrAnalyticsGoalNotFound {
		t.Fatalf("missing goal experiment = %v", err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "bad-control", FlagID: "flag", TrackerID: "shop", ControlVariant: "maybe",
		Metric:      AnalyticsExperimentMetric{EventName: "signup_completed"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 5, CreatedAtMillis: 5, UpdatedAtMillis: 5,
	}); err != ErrAnalyticsInvalid {
		t.Fatalf("unknown control variant = %v", err)
	}
}

func TestShipAnalyticsExperimentEndsTestAndPinsVariant(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		TargetingJSON:   `{"groups":[{"properties":[],"rollout_percentage":10}]}`,
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "experiment", FlagID: "flag", TrackerID: "shop", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{EventName: "signup_completed"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 4, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ShipAnalyticsExperiment(ctx, "shop", "experiment", "true", 4, 10); err != nil {
		t.Fatal(err)
	}
	flag, err := store.AnalyticsFlag(ctx, "shop", "flag")
	if err != nil {
		t.Fatal(err)
	}
	if len(flag.Variants) != 2 || flag.Variants[0].Percentage != 0 || flag.Variants[1].Percentage != 100 {
		t.Fatalf("shipped variants = %+v", flag.Variants)
	}
	var targeting struct {
		Groups []struct {
			RolloutPercentage int     `json:"rollout_percentage"`
			Variant           *string `json:"variant"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(flag.TargetingJSON), &targeting); err != nil {
		t.Fatal(err)
	}
	if len(targeting.Groups) != 1 || targeting.Groups[0].RolloutPercentage != 100 || targeting.Groups[0].Variant == nil || *targeting.Groups[0].Variant != "true" {
		t.Fatalf("shipped targeting = %s", flag.TargetingJSON)
	}
	experiments, err := store.AnalyticsExperiments(ctx, "shop")
	if err != nil || len(experiments) != 1 || experiments[0].EndedAtMillis != 10 {
		t.Fatalf("shipped experiment = %+v, %v", experiments, err)
	}
	if err := store.ShipAnalyticsExperiment(ctx, "shop", "experiment", "true", 4, 11); err != ErrAnalyticsChanged {
		t.Fatalf("second ship stale = %v", err)
	}
	if err := store.ShipAnalyticsExperiment(ctx, "shop", "experiment", "true", 10, 11); err != ErrAnalyticsExperimentConflict {
		t.Fatalf("second ship ended = %v", err)
	}
}

func TestShipAnalyticsExperimentReplacesFilteredTargeting(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		TargetingJSON:   `{"groups":[{"properties":[{"key":"email","operator":"icontains","value":"@acme.com"}],"rollout_percentage":50}]}`,
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "experiment", FlagID: "flag", TrackerID: "shop", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{EventName: "signup_completed"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 4, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ShipAnalyticsExperiment(ctx, "shop", "experiment", "true", 4, 10); err != nil {
		t.Fatal(err)
	}
	flag, err := store.AnalyticsFlag(ctx, "shop", "flag")
	if err != nil {
		t.Fatal(err)
	}
	var targeting struct {
		Groups []struct {
			Properties        []json.RawMessage `json:"properties"`
			RolloutPercentage int               `json:"rollout_percentage"`
			Variant           string            `json:"variant"`
		} `json:"groups"`
	}
	if err := json.Unmarshal([]byte(flag.TargetingJSON), &targeting); err != nil {
		t.Fatal(err)
	}
	if len(targeting.Groups) != 1 || len(targeting.Groups[0].Properties) != 0 || targeting.Groups[0].RolloutPercentage != 100 || targeting.Groups[0].Variant != "true" {
		t.Fatalf("shipped targeting = %s", flag.TargetingJSON)
	}
}

func TestUpdateAnalyticsFlagRejectsKeyChangeWhileExperimentRuns(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	flag := AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}
	if err := store.CreateAnalyticsFlag(ctx, flag); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "experiment", FlagID: "flag", TrackerID: "shop", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{EventName: "signup_completed"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 4, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	flag.Variants = []AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}}
	flag.UpdatedAtMillis = 5
	if err := store.UpdateAnalyticsFlag(ctx, flag, 3); err != ErrAnalyticsInvalid {
		t.Fatalf("weight change during experiment = %v", err)
	}
	flag.Variants = []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}}
	flag.Key = "pricing-v3"
	if err := store.UpdateAnalyticsFlag(ctx, flag, 3); err != ErrAnalyticsInvalid {
		t.Fatalf("rename during experiment = %v", err)
	}
	if err := store.StopAnalyticsExperiment(ctx, "shop", "experiment", 6, 4); err != nil {
		t.Fatal(err)
	}
	flag.UpdatedAtMillis = 7
	if err := store.UpdateAnalyticsFlag(ctx, flag, 3); err != nil {
		t.Fatalf("rename after stop = %v", err)
	}
}

func TestDeleteAnalyticsGoalRejectsExperimentMetric(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsGoal(ctx, AnalyticsGoal{
		ID: "goal", TrackerID: "shop", Name: "Signup", ActionType: "event",
		ActionValue: "signup_completed", CreatedAtMillis: 2, UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "experiment", FlagID: "flag", TrackerID: "shop", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{GoalID: "goal"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 4, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAnalyticsGoal(ctx, "shop", "goal"); err != ErrAnalyticsExperimentConflict {
		t.Fatalf("delete goal in use = %v", err)
	}
}

func TestCreateAnalyticsFlagRequiresBooleanKeysAndKnownServeVariant(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "bad-keys", TrackerID: "shop", Key: "broken", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "on", Percentage: 100}},
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != ErrAnalyticsInvalid {
		t.Fatalf("boolean without true/false = %v", err)
	}
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "bad-pin", TrackerID: "shop", Key: "pinned", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 0}, {Key: "true", Percentage: 100}},
		TargetingJSON:   `{"groups":[{"properties":[],"rollout_percentage":100,"variant":"treatment"}]}`,
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != ErrAnalyticsInvalid {
		t.Fatalf("unknown serve variant = %v", err)
	}
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "dup-keys", TrackerID: "shop", Key: "dup", Type: "multivariate", Enabled: false,
		Variants: []AnalyticsFlagVariant{
			{Key: "a", Percentage: 50},
			{Key: "a", Percentage: 50},
		},
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != ErrAnalyticsInvalid {
		t.Fatalf("duplicate variant keys = %v", err)
	}
}

func TestCreateAnalyticsFlagRejectsDuplicateKey(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	flag := AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: false,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}
	if err := store.CreateAnalyticsFlag(ctx, flag); err != nil {
		t.Fatal(err)
	}
	flag.ID = "other"
	if err := store.CreateAnalyticsFlag(ctx, flag); err != ErrAnalyticsFlagConflict {
		t.Fatalf("duplicate key = %v", err)
	}
}

func TestDeleteAnalyticsFlagRejectsRunningExperiment(t *testing.T) {
	ctx := context.Background()
	store := openAnalyticsStore(t)
	seedShopTracker(t, store)
	if err := store.CreateAnalyticsFlag(ctx, AnalyticsFlag{
		ID: "flag", TrackerID: "shop", Key: "pricing-v2", Type: "boolean", Enabled: true,
		Variants:        []AnalyticsFlagVariant{{Key: "false", Percentage: 50}, {Key: "true", Percentage: 50}},
		CreatedAtMillis: 3, UpdatedAtMillis: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsExperiment(ctx, AnalyticsExperiment{
		ID: "experiment", FlagID: "flag", TrackerID: "shop", ControlVariant: "false",
		Metric:      AnalyticsExperimentMetric{EventName: "signup_completed"},
		WindowValue: 14, WindowUnit: "day", StartedAtMillis: 4, CreatedAtMillis: 4, UpdatedAtMillis: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAnalyticsFlag(ctx, "shop", "flag"); err != ErrAnalyticsExperimentConflict {
		t.Fatalf("delete flag in experiment = %v", err)
	}
	if err := store.StopAnalyticsExperiment(ctx, "shop", "experiment", 6, 4); err != nil {
		t.Fatal(err)
	}
	if err := store.StopAnalyticsExperiment(ctx, "shop", "experiment", 7, 6); err != ErrAnalyticsExperimentConflict {
		t.Fatalf("stop already ended = %v", err)
	}
	if err := store.DeleteAnalyticsFlag(ctx, "shop", "flag"); err != nil {
		t.Fatalf("delete after stop = %v", err)
	}
}

func openAnalyticsStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "platformd.db"), os.Geteuid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func seedShopTracker(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateProject(ctx, CreateProject{
		ID: "project", Name: "shop", AuditEventID: "project-audit",
		ActorID: "actor", ActorEmail: "admin@example.com", CreatedAtMillis: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAnalyticsTracker(ctx, AnalyticsTracker{
		ID: "shop", ProjectID: "project", Name: "Shop", RootDomain: "shop.com",
		Mode: AnalyticsModeOptOut, CreatedAtMillis: 2, UpdatedAtMillis: 2,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateSchemaVersionSixteenAddsAnalyticsCatalog(t *testing.T) {
	database, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;
PRAGMA user_version = 16;`); err != nil {
		t.Fatal(err)
	}
	if err := migrateSchemaVersionSixteen(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	var version, tables int
	if err := database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`
SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name IN (
  'analytics_trackers', 'analytics_goals', 'analytics_funnels',
  'analytics_charts', 'analytics_flags', 'analytics_experiments'
)`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 17 || tables != 6 {
		t.Fatalf("schema version/tables = %d/%d", version, tables)
	}
}
