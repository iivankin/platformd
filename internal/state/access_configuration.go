package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type SetAccessConfigurationInput struct {
	TeamDomain           string
	Audience             string
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

func (store *Store) SetAccessConfiguration(ctx context.Context, input SetAccessConfigurationInput) error {
	if input.TeamDomain == "" || input.Audience == "" || input.AuditEventID == "" || input.ActorID == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("set Access configuration input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var installationID, previousTeamDomain, previousAudience string
		if err := transaction.QueryRowContext(ctx, `
SELECT id, access_team_domain, access_audience FROM installation WHERE singleton = 1`).Scan(
			&installationID, &previousTeamDomain, &previousAudience,
		); errors.Is(err, sql.ErrNoRows) {
			return ErrNotInitialized
		} else if err != nil {
			return fmt.Errorf("read installation for Access configuration: %w", err)
		}
		if input.TeamDomain == previousTeamDomain && input.Audience == previousAudience {
			return nil
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE installation
SET access_team_domain = ?, access_audience = ?, updated_at = ?
WHERE singleton = 1`, input.TeamDomain, input.Audience, input.UpdatedAtMillis); err != nil {
			return fmt.Errorf("set Access configuration: %w", err)
		}
		metadata, err := json.Marshal(map[string]string{
			"actorEmail":         input.ActorEmail,
			"audience":           input.Audience,
			"previousAudience":   previousAudience,
			"previousTeamDomain": previousTeamDomain,
			"teamDomain":         input.TeamDomain,
		})
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, 'installation.access_configuration.set', 'installation', ?, ?, 'succeeded', ?, ?)`,
			input.AuditEventID, input.ActorID, installationID,
			nullableString(input.RequestCorrelationID), string(metadata), input.UpdatedAtMillis)
		return err
	})
}
