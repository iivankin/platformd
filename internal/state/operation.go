package state

import (
	"context"
	"database/sql"
	"errors"
)

var (
	ErrOperationNotFound = errors.New("operation not found")
	ErrOperationFinished = errors.New("operation is already finished")
)

type Operation struct {
	ID               string
	Kind             string
	TargetID         string
	Status           string
	Progress         string
	ErrorCode        string
	ErrorMessage     string
	StartedAtMillis  int64
	FinishedAtMillis int64
}

type BeginOperation struct {
	ID              string
	Kind            string
	TargetID        string
	Progress        string
	StartedAtMillis int64
}

type FinishOperation struct {
	ID               string
	Status           string
	Progress         string
	ErrorCode        string
	ErrorMessage     string
	FinishedAtMillis int64
}

func (store *Store) BeginOperation(ctx context.Context, input BeginOperation) error {
	if input.ID == "" || input.Kind == "" || input.TargetID == "" || input.StartedAtMillis <= 0 ||
		len(input.Kind) > 128 || len(input.TargetID) > 256 || len(input.Progress) > 4096 {
		return errors.New("begin operation input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		_, err := transaction.ExecContext(ctx, `
INSERT INTO operations(id, kind, target_id, status, progress, started_at)
VALUES (?, ?, ?, 'running', ?, ?)`, input.ID, input.Kind, input.TargetID,
			nullableString(input.Progress), input.StartedAtMillis)
		return err
	})
}

func (store *Store) SetOperationProgress(ctx context.Context, operationID, progress string) error {
	if operationID == "" || len(progress) > 4096 {
		return errors.New("operation progress input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx,
			"UPDATE operations SET progress = ? WHERE id = ? AND status = 'running'",
			nullableString(progress), operationID,
		)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return operationTransitionError(ctx, transaction, operationID)
		}
		return nil
	})
}

func (store *Store) FinishOperation(ctx context.Context, input FinishOperation) error {
	if input.ID == "" || input.FinishedAtMillis <= 0 || len(input.Progress) > 4096 ||
		len(input.ErrorCode) > 128 || len(input.ErrorMessage) > 4096 ||
		(input.Status != "succeeded" && input.Status != "failed" && input.Status != "interrupted") ||
		(input.Status == "succeeded" && (input.ErrorCode != "" || input.ErrorMessage != "")) ||
		(input.Status == "failed" && (input.ErrorCode == "" || input.ErrorMessage == "")) {
		return errors.New("finish operation input is invalid")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE operations
SET status = ?, progress = ?, error_code = ?, error_message = ?, finished_at = ?
WHERE id = ? AND status = 'running' AND started_at <= ?`,
			input.Status, nullableString(input.Progress), nullableString(input.ErrorCode),
			nullableString(input.ErrorMessage), input.FinishedAtMillis, input.ID, input.FinishedAtMillis,
		)
		if err != nil {
			return err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updated != 1 {
			return operationTransitionError(ctx, transaction, input.ID)
		}
		return nil
	})
}

func operationTransitionError(ctx context.Context, transaction *sql.Tx, operationID string) error {
	var exists int
	if err := transaction.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM operations WHERE id = ?)", operationID,
	).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrOperationNotFound
	}
	return ErrOperationFinished
}

func (store *Store) Operation(ctx context.Context, operationID string) (Operation, error) {
	if operationID == "" {
		return Operation{}, ErrOperationNotFound
	}
	var result Operation
	var progress, errorCode, errorMessage sql.NullString
	var finishedAt sql.NullInt64
	err := store.database.QueryRowContext(ctx, `
SELECT id, kind, target_id, status, progress, error_code, error_message, started_at, finished_at
FROM operations WHERE id = ?`, operationID).Scan(
		&result.ID, &result.Kind, &result.TargetID, &result.Status, &progress,
		&errorCode, &errorMessage, &result.StartedAtMillis, &finishedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrOperationNotFound
	}
	if err != nil {
		return Operation{}, err
	}
	result.Progress = progress.String
	result.ErrorCode = errorCode.String
	result.ErrorMessage = errorMessage.String
	result.FinishedAtMillis = finishedAt.Int64
	return result, nil
}
