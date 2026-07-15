package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
)

type TerminalAuditInput struct {
	ID               string
	ActorID          string
	ActorEmail       string
	Action           string
	TargetKind       string
	TargetID         string
	ProjectID        string
	ServiceID        string
	ContainerID      string
	Command          []string
	SourceIP         string
	Result           string
	StartedAtMillis  int64
	FinishedAtMillis int64
	DurationMillis   int64
	CloseReason      string
	ExitCode         *int
	ErrorClass       string
	CreatedAtMillis  int64
}

func (store *Store) AppendTerminalAudit(ctx context.Context, input TerminalAuditInput) error {
	if err := validateTerminalAudit(input); err != nil {
		return err
	}
	metadata := map[string]any{
		"actorEmail": input.ActorEmail,
		"sourceIp":   input.SourceIP,
	}
	if input.ProjectID != "" {
		metadata["projectId"] = input.ProjectID
	}
	if input.ServiceID != "" {
		metadata["serviceId"] = input.ServiceID
	}
	if input.ContainerID != "" {
		metadata["containerId"] = input.ContainerID
	}
	if len(input.Command) > 0 {
		metadata["command"] = input.Command
	}
	if input.StartedAtMillis > 0 {
		metadata["startedAt"] = input.StartedAtMillis
	}
	if input.FinishedAtMillis > 0 {
		metadata["finishedAt"] = input.FinishedAtMillis
		metadata["durationMillis"] = input.DurationMillis
		metadata["closeReason"] = input.CloseReason
	}
	if input.ExitCode != nil {
		metadata["exitCode"] = *input.ExitCode
	}
	if input.ErrorClass != "" {
		metadata["errorClass"] = input.ErrorClass
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode terminal audit metadata: %w", err)
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, ?, ?, ?, ?, ?)`,
			input.ID, input.ActorID, input.Action, input.TargetKind, input.TargetID,
			input.Result, string(encoded), input.CreatedAtMillis,
		)
		if err != nil {
			return fmt.Errorf("append terminal audit: %w", err)
		}
		return nil
	})
}

func validateTerminalAudit(input TerminalAuditInput) error {
	if input.ID == "" || input.ActorID == "" || input.ActorEmail == "" || input.TargetID == "" || input.CreatedAtMillis <= 0 {
		return errors.New("terminal audit input is incomplete")
	}
	allowedActions := map[string]bool{
		"container_terminal.start": true,
		"container_terminal.end":   true,
		"server_terminal.start":    true,
		"server_terminal.end":      true,
	}
	validTarget := input.TargetKind == "service" || input.TargetKind == "postgres" || input.TargetKind == "redis" || input.TargetKind == "installation"
	if !allowedActions[input.Action] || !validTarget {
		return errors.New("terminal audit action or target is invalid")
	}
	if input.Result != "succeeded" && input.Result != "failed" {
		return errors.New("terminal audit result is invalid")
	}
	if _, err := netip.ParseAddr(input.SourceIP); err != nil {
		return errors.New("terminal audit source IP is invalid")
	}
	if len(input.Command) > 64 || input.DurationMillis < 0 || input.FinishedAtMillis < 0 || input.StartedAtMillis < 0 {
		return errors.New("terminal audit bounds are invalid")
	}
	return nil
}
