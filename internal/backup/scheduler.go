package backup

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/iivankin/platformd/internal/backupcron"
	"github.com/iivankin/platformd/internal/state"
)

type ScheduleStore interface {
	BackupPolicies(context.Context) ([]state.BackupPolicy, error)
	ScheduledBackupExists(context.Context, string, string, int64) (bool, error)
}

type DueCandidate struct {
	ResourceKind              string
	ResourceID                string
	DueAt                     time.Time
	ScheduledOccurrenceMillis *int64
	RetentionCount            int
}

func SelectDueCandidate(
	ctx context.Context,
	store ScheduleStore,
	dirtySince time.Time,
	dirty bool,
	startedAt, now time.Time,
) (DueCandidate, bool, error) {
	if store == nil || startedAt.IsZero() || now.IsZero() || now.Before(startedAt) || (dirty && dirtySince.IsZero()) {
		return DueCandidate{}, false, errors.New("backup due-candidate input is invalid")
	}
	candidates := make([]DueCandidate, 0)
	if dirty {
		candidates = append(candidates, DueCandidate{ResourceKind: "control", DueAt: dirtySince})
	}
	policies, err := store.BackupPolicies(ctx)
	if err != nil {
		return DueCandidate{}, false, err
	}
	for _, policy := range policies {
		if !policy.Enabled {
			continue
		}
		schedule, err := backupcron.Parse(policy.Cron)
		if err != nil {
			return DueCandidate{}, false, err
		}
		// Previous is strict, so advancing one minute includes a schedule that
		// falls exactly on the current UTC minute.
		latest, err := schedule.Previous(now.UTC().Truncate(time.Minute).Add(time.Minute))
		if err != nil {
			return DueCandidate{}, false, err
		}
		if !latest.After(startedAt) {
			continue
		}
		exists, err := store.ScheduledBackupExists(ctx, policy.ResourceKind, policy.ResourceID, latest.UnixMilli())
		if err != nil {
			return DueCandidate{}, false, err
		}
		if exists {
			continue
		}
		occurrence := latest.UnixMilli()
		candidates = append(candidates, DueCandidate{
			ResourceKind: policy.ResourceKind, ResourceID: policy.ResourceID, DueAt: latest,
			ScheduledOccurrenceMillis: &occurrence, RetentionCount: policy.RetentionCount,
		})
	}
	if len(candidates) == 0 {
		return DueCandidate{}, false, nil
	}
	sort.Slice(candidates, func(left, right int) bool {
		if !candidates[left].DueAt.Equal(candidates[right].DueAt) {
			return candidates[left].DueAt.Before(candidates[right].DueAt)
		}
		if candidates[left].ResourceKind != candidates[right].ResourceKind {
			return candidates[left].ResourceKind < candidates[right].ResourceKind
		}
		return candidates[left].ResourceID < candidates[right].ResourceID
	})
	return candidates[0], true, nil
}
