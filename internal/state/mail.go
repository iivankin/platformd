package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const maximumMailAlerts = 20

var (
	ErrSMTPNotConfigured       = errors.New("SMTP is not configured")
	ErrMailAlertNotFound       = errors.New("mail alert not found")
	ErrMailAlertLimit          = errors.New("mail alert limit reached")
	ErrMailAlertServiceMissing = errors.New("mail alert references a missing service")
)

type SMTPSettings struct {
	Host              string
	Port              int
	Username          string
	PasswordEncrypted []byte
	FromAddress       string
	FromName          string
	Encryption        string
	CreatedAtMillis   int64
	UpdatedAtMillis   int64
}

type PutSMTPSettingsInput struct {
	Settings             SMTPSettings
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
	UpdatedAtMillis      int64
}

type MailAlertService struct {
	ID          string
	Name        string
	ProjectID   string
	ProjectName string
}

type MailErrorAlert struct {
	ID              string
	Name            string
	Enabled         bool
	Recipients      []string
	EventTypes      []string
	ServiceIDs      []string
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type MailMetricAlert struct {
	ID              string
	Name            string
	Enabled         bool
	Recipients      []string
	Scope           MetricScope
	SQL             string
	Operator        string
	Threshold       float64
	WindowSeconds   int
	Firing          bool
	LastValue       *float64
	LastEvaluatedAt int64
	LastSentAt      int64
	CreatedAtMillis int64
	UpdatedAtMillis int64
}

type MailAlertMutation struct {
	AuditEventID         string
	ActorID              string
	ActorEmail           string
	RequestCorrelationID string
}

func (store *Store) SMTPSettings(ctx context.Context) (SMTPSettings, error) {
	var settings SMTPSettings
	err := store.database.QueryRowContext(ctx, `
SELECT host, port, username, password_encrypted, from_address, from_name, encryption, created_at, updated_at
FROM smtp_settings WHERE singleton = 1`).Scan(
		&settings.Host, &settings.Port, &settings.Username, &settings.PasswordEncrypted,
		&settings.FromAddress, &settings.FromName, &settings.Encryption,
		&settings.CreatedAtMillis, &settings.UpdatedAtMillis,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SMTPSettings{}, ErrSMTPNotConfigured
	}
	if err != nil {
		return SMTPSettings{}, fmt.Errorf("load SMTP settings: %w", err)
	}
	return settings, nil
}

func (store *Store) PutSMTPSettings(ctx context.Context, input PutSMTPSettingsInput) error {
	settings := input.Settings
	if settings.Host == "" || settings.Port < 1 || settings.Port > 65535 || len(settings.PasswordEncrypted) == 0 ||
		settings.FromAddress == "" || settings.Encryption == "" || input.AuditEventID == "" ||
		input.ActorID == "" || input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return errors.New("SMTP settings input is incomplete")
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		var createdAt int64
		err := transaction.QueryRowContext(ctx, "SELECT created_at FROM smtp_settings WHERE singleton = 1").Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			createdAt = input.UpdatedAtMillis
		} else if err != nil {
			return fmt.Errorf("load SMTP settings creation time: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO smtp_settings(
  singleton, host, port, username, password_encrypted, from_address, from_name, encryption, created_at, updated_at
) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(singleton) DO UPDATE SET
  host = excluded.host,
  port = excluded.port,
  username = excluded.username,
  password_encrypted = excluded.password_encrypted,
  from_address = excluded.from_address,
  from_name = excluded.from_name,
  encryption = excluded.encryption,
  updated_at = excluded.updated_at`,
			settings.Host, settings.Port, settings.Username, settings.PasswordEncrypted,
			settings.FromAddress, settings.FromName, settings.Encryption, createdAt, input.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("save SMTP settings: %w", err)
		}
		metadata, err := json.Marshal(map[string]any{
			"actorEmail":  input.ActorEmail,
			"encryption":  settings.Encryption,
			"fromAddress": settings.FromAddress,
			"host":        settings.Host,
			"port":        settings.Port,
		})
		if err != nil {
			return err
		}
		return insertMailAudit(ctx, transaction, input.AuditEventID, input.ActorID, "smtp.configure",
			"smtp", "singleton", input.RequestCorrelationID, metadata, input.UpdatedAtMillis)
	})
}

func (store *Store) MailAlertServices(ctx context.Context) ([]MailAlertService, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT services.id, services.name, projects.id, projects.name
FROM services JOIN projects ON projects.id = services.project_id
ORDER BY projects.name, services.name, services.id`)
	if err != nil {
		return nil, fmt.Errorf("list mail alert services: %w", err)
	}
	defer rows.Close()
	services := make([]MailAlertService, 0)
	for rows.Next() {
		var service MailAlertService
		if err := rows.Scan(&service.ID, &service.Name, &service.ProjectID, &service.ProjectName); err != nil {
			return nil, fmt.Errorf("scan mail alert service: %w", err)
		}
		services = append(services, service)
	}
	return services, rows.Err()
}

func (store *Store) MailErrorAlerts(ctx context.Context) ([]MailErrorAlert, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, name, enabled, recipients_json, event_types_json, service_ids_json, created_at, updated_at
FROM mail_error_alerts ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list mail error alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]MailErrorAlert, 0)
	for rows.Next() {
		alert, scanErr := scanMailErrorAlert(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (store *Store) CreateMailErrorAlert(ctx context.Context, alert MailErrorAlert, mutation MailAlertMutation) error {
	if err := validateMailErrorAlert(alert, true); err != nil {
		return err
	}
	if err := validateMailAlertMutation(mutation, alert.CreatedAtMillis); err != nil {
		return err
	}
	recipientsJSON, eventTypesJSON, serviceIDsJSON, metadata, err := mailErrorAlertJSON(alert, mutation.ActorEmail)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMailAlertServices(ctx, transaction, alert.ServiceIDs); err != nil {
			return err
		}
		var count int
		if err := transaction.QueryRowContext(ctx, "SELECT COUNT(*) FROM mail_error_alerts").Scan(&count); err != nil {
			return fmt.Errorf("count mail error alerts: %w", err)
		}
		if count >= maximumMailAlerts {
			return ErrMailAlertLimit
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO mail_error_alerts(
  id, name, enabled, recipients_json, event_types_json, service_ids_json, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			alert.ID, alert.Name, boolToInt(alert.Enabled), recipientsJSON, eventTypesJSON, serviceIDsJSON,
			alert.CreatedAtMillis, alert.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("create mail error alert: %w", err)
		}
		return insertMailAudit(ctx, transaction, mutation.AuditEventID, mutation.ActorID, "mail_error_alert.create",
			"mail_error_alert", alert.ID, mutation.RequestCorrelationID, metadata, alert.CreatedAtMillis)
	})
}

func (store *Store) UpdateMailErrorAlert(ctx context.Context, alert MailErrorAlert, mutation MailAlertMutation) error {
	if err := validateMailErrorAlert(alert, false); err != nil {
		return err
	}
	if err := validateMailAlertMutation(mutation, alert.UpdatedAtMillis); err != nil {
		return err
	}
	recipientsJSON, eventTypesJSON, serviceIDsJSON, metadata, err := mailErrorAlertJSON(alert, mutation.ActorEmail)
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMailAlertServices(ctx, transaction, alert.ServiceIDs); err != nil {
			return err
		}
		var createdAt int64
		if err := transaction.QueryRowContext(ctx, "SELECT created_at FROM mail_error_alerts WHERE id = ?", alert.ID).Scan(&createdAt); errors.Is(err, sql.ErrNoRows) {
			return ErrMailAlertNotFound
		} else if err != nil {
			return fmt.Errorf("load mail error alert: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE mail_error_alerts SET
  name = ?, enabled = ?, recipients_json = ?, event_types_json = ?, service_ids_json = ?, updated_at = ?
WHERE id = ?`,
			alert.Name, boolToInt(alert.Enabled), recipientsJSON, eventTypesJSON, serviceIDsJSON,
			alert.UpdatedAtMillis, alert.ID,
		); err != nil {
			return fmt.Errorf("update mail error alert: %w", err)
		}
		return insertMailAudit(ctx, transaction, mutation.AuditEventID, mutation.ActorID, "mail_error_alert.update",
			"mail_error_alert", alert.ID, mutation.RequestCorrelationID, metadata, alert.UpdatedAtMillis)
	})
}

func (store *Store) DeleteMailErrorAlert(ctx context.Context, alertID string, mutation MailAlertMutation, deletedAtMillis int64) error {
	if alertID == "" || validateMailAlertMutation(mutation, deletedAtMillis) != nil {
		return errors.New("delete mail error alert input is incomplete")
	}
	metadata, err := json.Marshal(map[string]string{"actorEmail": mutation.ActorEmail})
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, "DELETE FROM mail_error_alerts WHERE id = ?", alertID)
		if err != nil {
			return fmt.Errorf("delete mail error alert: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted mail error alert: %w", err)
		}
		if changed != 1 {
			return ErrMailAlertNotFound
		}
		return insertMailAudit(ctx, transaction, mutation.AuditEventID, mutation.ActorID, "mail_error_alert.delete",
			"mail_error_alert", alertID, mutation.RequestCorrelationID, metadata, deletedAtMillis)
	})
}

func (store *Store) MailMetricAlerts(ctx context.Context) ([]MailMetricAlert, error) {
	rows, err := store.database.QueryContext(ctx, `
SELECT id, name, enabled, recipients_json, scope_kind, IFNULL(project_id, ''), IFNULL(service_id, ''),
  sql, operator, threshold, window_seconds, firing, last_value, last_evaluated_at, last_sent_at, created_at, updated_at
FROM mail_metric_alerts ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list mail metric alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]MailMetricAlert, 0)
	for rows.Next() {
		alert, scanErr := scanMailMetricAlert(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		alerts = append(alerts, alert)
	}
	return alerts, rows.Err()
}

func (store *Store) CreateMailMetricAlert(ctx context.Context, alert MailMetricAlert, mutation MailAlertMutation) error {
	if err := validateMailMetricAlert(alert, true); err != nil {
		return err
	}
	if err := validateMailAlertMutation(mutation, alert.CreatedAtMillis); err != nil {
		return err
	}
	recipientsJSON, metadata, err := mailMetricAlertJSON(alert, mutation.ActorEmail)
	if err != nil {
		return err
	}
	projectID, serviceID := mailMetricScopeIDs(alert.Scope)
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMetricScope(ctx, transaction, alert.Scope); err != nil {
			return err
		}
		var count int
		if err := transaction.QueryRowContext(ctx, "SELECT COUNT(*) FROM mail_metric_alerts").Scan(&count); err != nil {
			return fmt.Errorf("count mail metric alerts: %w", err)
		}
		if count >= maximumMailAlerts {
			return ErrMailAlertLimit
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO mail_metric_alerts(
  id, name, enabled, recipients_json, scope_kind, project_id, service_id, sql, operator, threshold,
  window_seconds, firing, last_value, last_evaluated_at, last_sent_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NULL, NULL, NULL, ?, ?)`,
			alert.ID, alert.Name, boolToInt(alert.Enabled), recipientsJSON, alert.Scope.Kind, projectID, serviceID,
			alert.SQL, alert.Operator, alert.Threshold, alert.WindowSeconds, alert.CreatedAtMillis, alert.UpdatedAtMillis,
		); err != nil {
			return fmt.Errorf("create mail metric alert: %w", err)
		}
		return insertMailAudit(ctx, transaction, mutation.AuditEventID, mutation.ActorID, "mail_metric_alert.create",
			"mail_metric_alert", alert.ID, mutation.RequestCorrelationID, metadata, alert.CreatedAtMillis)
	})
}

func (store *Store) UpdateMailMetricAlert(ctx context.Context, alert MailMetricAlert, mutation MailAlertMutation) error {
	if err := validateMailMetricAlert(alert, false); err != nil {
		return err
	}
	if err := validateMailAlertMutation(mutation, alert.UpdatedAtMillis); err != nil {
		return err
	}
	recipientsJSON, metadata, err := mailMetricAlertJSON(alert, mutation.ActorEmail)
	if err != nil {
		return err
	}
	projectID, serviceID := mailMetricScopeIDs(alert.Scope)
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		if err := ensureMetricScope(ctx, transaction, alert.Scope); err != nil {
			return err
		}
		var createdAt int64
		if err := transaction.QueryRowContext(ctx, "SELECT created_at FROM mail_metric_alerts WHERE id = ?", alert.ID).Scan(&createdAt); errors.Is(err, sql.ErrNoRows) {
			return ErrMailAlertNotFound
		} else if err != nil {
			return fmt.Errorf("load mail metric alert: %w", err)
		}
		if _, err := transaction.ExecContext(ctx, `
UPDATE mail_metric_alerts SET
  name = ?, enabled = ?, recipients_json = ?, scope_kind = ?, project_id = ?, service_id = ?,
  sql = ?, operator = ?, threshold = ?, window_seconds = ?, firing = 0,
  last_value = NULL, last_evaluated_at = NULL, last_sent_at = NULL, updated_at = ?
WHERE id = ?`,
			alert.Name, boolToInt(alert.Enabled), recipientsJSON, alert.Scope.Kind, projectID, serviceID,
			alert.SQL, alert.Operator, alert.Threshold, alert.WindowSeconds, alert.UpdatedAtMillis, alert.ID,
		); err != nil {
			return fmt.Errorf("update mail metric alert: %w", err)
		}
		return insertMailAudit(ctx, transaction, mutation.AuditEventID, mutation.ActorID, "mail_metric_alert.update",
			"mail_metric_alert", alert.ID, mutation.RequestCorrelationID, metadata, alert.UpdatedAtMillis)
	})
}

func (store *Store) DeleteMailMetricAlert(ctx context.Context, alertID string, mutation MailAlertMutation, deletedAtMillis int64) error {
	if alertID == "" || validateMailAlertMutation(mutation, deletedAtMillis) != nil {
		return errors.New("delete mail metric alert input is incomplete")
	}
	metadata, err := json.Marshal(map[string]string{"actorEmail": mutation.ActorEmail})
	if err != nil {
		return err
	}
	return store.WriteControl(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, "DELETE FROM mail_metric_alerts WHERE id = ?", alertID)
		if err != nil {
			return fmt.Errorf("delete mail metric alert: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted mail metric alert: %w", err)
		}
		if changed != 1 {
			return ErrMailAlertNotFound
		}
		return insertMailAudit(ctx, transaction, mutation.AuditEventID, mutation.ActorID, "mail_metric_alert.delete",
			"mail_metric_alert", alertID, mutation.RequestCorrelationID, metadata, deletedAtMillis)
	})
}

func (store *Store) RecordMailMetricAlertEvaluation(
	ctx context.Context,
	alertID string,
	firing bool,
	value *float64,
	evaluatedAt, sentAt, expectedUpdatedAt int64,
) error {
	if alertID == "" || evaluatedAt <= 0 || expectedUpdatedAt <= 0 {
		return errors.New("mail metric alert evaluation is incomplete")
	}
	return store.Write(ctx, func(transaction *sql.Tx) error {
		result, err := transaction.ExecContext(ctx, `
UPDATE mail_metric_alerts SET firing = ?, last_value = ?, last_evaluated_at = ?, last_sent_at = ?
WHERE id = ? AND updated_at = ?`,
			boolToInt(firing), value, evaluatedAt, nullablePositive(sentAt), alertID, expectedUpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("record mail metric alert evaluation: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count mail metric alert evaluation: %w", err)
		}
		if changed != 1 {
			// Deleted or the alert definition changed while this evaluation was in flight.
			return nil
		}
		return nil
	})
}

func scanMailErrorAlert(scanner interface{ Scan(...any) error }) (MailErrorAlert, error) {
	var alert MailErrorAlert
	var enabled int
	var recipientsJSON, eventTypesJSON, serviceIDsJSON string
	if err := scanner.Scan(&alert.ID, &alert.Name, &enabled, &recipientsJSON, &eventTypesJSON, &serviceIDsJSON,
		&alert.CreatedAtMillis, &alert.UpdatedAtMillis); err != nil {
		return MailErrorAlert{}, fmt.Errorf("scan mail error alert: %w", err)
	}
	alert.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(recipientsJSON), &alert.Recipients); err != nil {
		return MailErrorAlert{}, fmt.Errorf("decode mail error alert recipients: %w", err)
	}
	if err := json.Unmarshal([]byte(eventTypesJSON), &alert.EventTypes); err != nil {
		return MailErrorAlert{}, fmt.Errorf("decode mail error alert events: %w", err)
	}
	if err := json.Unmarshal([]byte(serviceIDsJSON), &alert.ServiceIDs); err != nil {
		return MailErrorAlert{}, fmt.Errorf("decode mail error alert services: %w", err)
	}
	if alert.ServiceIDs == nil {
		alert.ServiceIDs = []string{}
	}
	return alert, nil
}

func scanMailMetricAlert(scanner interface{ Scan(...any) error }) (MailMetricAlert, error) {
	var alert MailMetricAlert
	var enabled, firing int
	var recipientsJSON, projectID, serviceID string
	var lastValue sql.NullFloat64
	var lastEvaluated, lastSent sql.NullInt64
	if err := scanner.Scan(
		&alert.ID, &alert.Name, &enabled, &recipientsJSON, &alert.Scope.Kind, &projectID, &serviceID,
		&alert.SQL, &alert.Operator, &alert.Threshold, &alert.WindowSeconds, &firing, &lastValue,
		&lastEvaluated, &lastSent, &alert.CreatedAtMillis, &alert.UpdatedAtMillis,
	); err != nil {
		return MailMetricAlert{}, fmt.Errorf("scan mail metric alert: %w", err)
	}
	alert.Enabled = enabled == 1
	alert.Firing = firing == 1
	alert.Scope.ProjectID = projectID
	alert.Scope.ServiceID = serviceID
	if lastValue.Valid {
		value := lastValue.Float64
		alert.LastValue = &value
	}
	if lastEvaluated.Valid {
		alert.LastEvaluatedAt = lastEvaluated.Int64
	}
	if lastSent.Valid {
		alert.LastSentAt = lastSent.Int64
	}
	if err := json.Unmarshal([]byte(recipientsJSON), &alert.Recipients); err != nil {
		return MailMetricAlert{}, fmt.Errorf("decode mail metric alert recipients: %w", err)
	}
	return alert, nil
}

func mailErrorAlertJSON(alert MailErrorAlert, actorEmail string) (string, string, string, []byte, error) {
	recipientsJSON, err := json.Marshal(alert.Recipients)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("encode mail error alert recipients: %w", err)
	}
	eventTypesJSON, err := json.Marshal(alert.EventTypes)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("encode mail error alert events: %w", err)
	}
	serviceIDsJSON, err := json.Marshal(alert.ServiceIDs)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("encode mail error alert services: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail": actorEmail,
		"enabled":    alert.Enabled,
		"eventTypes": alert.EventTypes,
		"name":       alert.Name,
		"services":   alert.ServiceIDs,
	})
	if err != nil {
		return "", "", "", nil, err
	}
	return string(recipientsJSON), string(eventTypesJSON), string(serviceIDsJSON), metadata, nil
}

func mailMetricAlertJSON(alert MailMetricAlert, actorEmail string) (string, []byte, error) {
	recipientsJSON, err := json.Marshal(alert.Recipients)
	if err != nil {
		return "", nil, fmt.Errorf("encode mail metric alert recipients: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"actorEmail":    actorEmail,
		"enabled":       alert.Enabled,
		"name":          alert.Name,
		"operator":      alert.Operator,
		"projectId":     alert.Scope.ProjectID,
		"serviceId":     alert.Scope.ServiceID,
		"threshold":     alert.Threshold,
		"windowSeconds": alert.WindowSeconds,
	})
	if err != nil {
		return "", nil, err
	}
	return string(recipientsJSON), metadata, nil
}

func validateMailErrorAlert(alert MailErrorAlert, creating bool) error {
	if alert.ID == "" || alert.Name == "" || len(alert.Name) > 80 || len(alert.Recipients) == 0 || len(alert.Recipients) > 20 ||
		len(alert.EventTypes) == 0 || len(alert.EventTypes) > 3 || len(alert.ServiceIDs) > 50 ||
		alert.UpdatedAtMillis <= 0 || (creating && (alert.CreatedAtMillis <= 0 || alert.CreatedAtMillis != alert.UpdatedAtMillis)) {
		return errors.New("mail error alert input is incomplete")
	}
	return nil
}

func validateMailMetricAlert(alert MailMetricAlert, creating bool) error {
	if err := validateMetricScope(alert.Scope); err != nil {
		return err
	}
	if alert.ID == "" || alert.Name == "" || len(alert.Name) > 80 || len(alert.Recipients) == 0 || len(alert.Recipients) > 20 ||
		alert.SQL == "" || len(alert.SQL) > 16<<10 || !validMailMetricOperator(alert.Operator) ||
		alert.WindowSeconds < 60 || alert.WindowSeconds > 86400 || alert.UpdatedAtMillis <= 0 ||
		(creating && (alert.CreatedAtMillis <= 0 || alert.CreatedAtMillis != alert.UpdatedAtMillis)) {
		return errors.New("mail metric alert input is incomplete")
	}
	return nil
}

func validateMailAlertMutation(mutation MailAlertMutation, timestamp int64) error {
	if mutation.AuditEventID == "" || mutation.ActorID == "" || mutation.ActorEmail == "" || timestamp <= 0 {
		return errors.New("mail alert mutation is incomplete")
	}
	return nil
}

func validMailMetricOperator(value string) bool {
	return value == "gt" || value == "gte" || value == "lt" || value == "lte"
}

func mailMetricScopeIDs(scope MetricScope) (any, any) {
	return nullableString(scope.ProjectID), nullableString(scope.ServiceID)
}

func ensureMailAlertServices(ctx context.Context, transaction *sql.Tx, serviceIDs []string) error {
	for _, serviceID := range serviceIDs {
		if serviceID == "" {
			return errors.New("mail alert service ID is empty")
		}
		var exists int
		if err := transaction.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM services WHERE id = ?)", serviceID).Scan(&exists); err != nil {
			return fmt.Errorf("check mail alert service: %w", err)
		}
		if exists != 1 {
			return fmt.Errorf("%w: %s", ErrMailAlertServiceMissing, serviceID)
		}
	}
	return nil
}

func insertMailAudit(
	ctx context.Context,
	transaction *sql.Tx,
	id, actorID, action, targetKind, targetID, correlationID string,
	metadata []byte,
	timestamp int64,
) error {
	_, err := transaction.ExecContext(ctx, `
INSERT INTO audit_events(
  id, actor_kind, actor_id, action, target_kind, target_id,
  request_correlation_id, result, metadata_json, created_at
) VALUES (?, 'access', ?, ?, ?, ?, ?, 'succeeded', ?, ?)`,
		id, actorID, action, targetKind, targetID, nullableString(correlationID), string(metadata), timestamp,
	)
	if err != nil {
		return fmt.Errorf("audit %s: %w", action, err)
	}
	return nil
}
