package backup

import (
	"context"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/state"
)

type scheduleStoreStub struct {
	policies []state.BackupPolicy
	started  map[string]bool
}

func (store scheduleStoreStub) BackupPolicies(context.Context) ([]state.BackupPolicy, error) {
	return append([]state.BackupPolicy(nil), store.policies...), nil
}

func (store scheduleStoreStub) ScheduledBackupExists(_ context.Context, kind, id string, occurrence int64) (bool, error) {
	return store.started[kind+"/"+id+"/"+time.UnixMilli(occurrence).UTC().Format(time.RFC3339)], nil
}

func TestSelectDueCandidateChoosesGloballyOldestAndCollapsesOccurrences(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.July, 13, 10, 0, 30, 0, time.UTC)
	now := time.Date(2026, time.July, 13, 10, 9, 40, 0, time.UTC)
	store := scheduleStoreStub{policies: []state.BackupPolicy{
		{ResourceKind: "redis", ResourceID: "redis-b", Enabled: true, Cron: "*/2 * * * *", RetentionCount: 7},
		{ResourceKind: "postgres", ResourceID: "postgres-a", Enabled: true, Cron: "*/5 * * * *", RetentionCount: 3},
	}}
	candidate, exists, err := SelectDueCandidate(
		context.Background(), store, time.Date(2026, time.July, 13, 10, 6, 0, 0, time.UTC), true, startedAt, now,
	)
	if err != nil || !exists {
		t.Fatalf("candidate = %+v, %t, %v", candidate, exists, err)
	}
	// PostgreSQL's latest occurrence is 10:05; earlier 10:00 is not replayed.
	if candidate.ResourceKind != "postgres" || candidate.ResourceID != "postgres-a" ||
		candidate.ScheduledOccurrenceMillis == nil || *candidate.ScheduledOccurrenceMillis != time.Date(2026, time.July, 13, 10, 5, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("oldest due candidate = %+v", candidate)
	}
}

func TestSelectDueCandidateSkipsPreStartupAndAlreadyStartedOccurrence(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.July, 13, 10, 5, 30, 0, time.UTC)
	now := time.Date(2026, time.July, 13, 10, 6, 20, 0, time.UTC)
	occurrence := time.Date(2026, time.July, 13, 10, 6, 0, 0, time.UTC)
	store := scheduleStoreStub{
		policies: []state.BackupPolicy{
			{ResourceKind: "redis", ResourceID: "already", Enabled: true, Cron: "* * * * *", RetentionCount: 7},
			{ResourceKind: "postgres", ResourceID: "before-start", Enabled: true, Cron: "0 * * * *", RetentionCount: 7},
		},
		started: map[string]bool{"redis/already/" + occurrence.Format(time.RFC3339): true},
	}
	_, exists, err := SelectDueCandidate(context.Background(), store, time.Time{}, false, startedAt, now)
	if err != nil || exists {
		t.Fatalf("unexpected candidate exists=%t err=%v", exists, err)
	}
}

func TestSelectDueCandidateDeterministicallyBreaksTimestampTie(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, time.July, 13, 10, 0, 30, 0, time.UTC)
	now := time.Date(2026, time.July, 13, 10, 1, 1, 0, time.UTC)
	store := scheduleStoreStub{policies: []state.BackupPolicy{
		{ResourceKind: "redis", ResourceID: "z", Enabled: true, Cron: "* * * * *", RetentionCount: 7},
		{ResourceKind: "postgres", ResourceID: "a", Enabled: true, Cron: "* * * * *", RetentionCount: 7},
	}}
	candidate, exists, err := SelectDueCandidate(context.Background(), store, time.Time{}, false, startedAt, now)
	if err != nil || !exists || candidate.ResourceKind != "postgres" {
		t.Fatalf("tie candidate = %+v, %t, %v", candidate, exists, err)
	}
}
