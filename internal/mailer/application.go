package mailer

import (
	"context"
	"errors"
	"fmt"
	netmail "net/mail"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const (
	smtpPasswordDomain     = "platformd/smtp/v1"
	smtpPasswordAD         = "smtp-password"
	errorNotificationQueue = 256
	metricEvaluateInterval = time.Minute
	sendTimeout            = 20 * time.Second
)

var allowedErrorEvents = []string{"issue_created", "issue_regressed", "issue_resolved"}

type MetricPoint struct {
	TimeUnixNano string  `json:"timeUnixNano"`
	Value        float64 `json:"value"`
	Series       string  `json:"series,omitempty"`
}

type MetricQuerier interface {
	Query(ctx context.Context, scope state.MetricScope, sql string, fromMillis, toMillis, stepMillis int64) ([]MetricPoint, error)
}

type Sender func(context.Context, SMTPConfig, []byte, Message) error

type Config struct {
	Store          *state.Store
	Master         cryptobox.MasterKey
	InstallationID string
	Metrics        MetricQuerier
	Send           Sender
	Now            func() time.Time
	OnError        func(error)
	EvaluateEvery  time.Duration
}

type Application struct {
	store   *state.Store
	box     cryptobox.Box
	metrics MetricQuerier
	send    Sender
	now     func() time.Time
	onError func(error)
	every   time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	queue  chan serviceErrorNotification
	mu     sync.Mutex
	closed bool
	wait   sync.WaitGroup
}

type SMTP struct {
	Configured      bool   `json:"configured"`
	Host            string `json:"host,omitempty"`
	Port            int    `json:"port,omitempty"`
	Username        string `json:"username,omitempty"`
	PasswordSet     bool   `json:"passwordSet"`
	FromAddress     string `json:"fromAddress,omitempty"`
	FromName        string `json:"fromName,omitempty"`
	Encryption      string `json:"encryption,omitempty"`
	UpdatedAtMillis int64  `json:"updatedAt,omitempty"`
}

type Settings struct {
	SMTP         SMTP                     `json:"smtp"`
	ErrorAlerts  []state.MailErrorAlert   `json:"errorAlerts"`
	MetricAlerts []state.MailMetricAlert  `json:"metricAlerts"`
	Services     []state.MailAlertService `json:"services"`
}

type SMTPInput struct {
	Host        string
	Port        int
	Username    string
	Password    []byte
	FromAddress string
	FromName    string
	Encryption  string
}

type Mutation struct {
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func New(config Config) (*Application, error) {
	if config.Store == nil || config.InstallationID == "" || config.OnError == nil {
		return nil, errors.New("mailer dependencies are incomplete")
	}
	box, err := cryptobox.NewBox(config.Master, []byte(config.InstallationID), smtpPasswordDomain)
	if err != nil {
		return nil, err
	}
	send := config.Send
	if send == nil {
		send = sendSMTP
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	every := config.EvaluateEvery
	if every <= 0 {
		every = metricEvaluateInterval
	}
	ctx, cancel := context.WithCancel(context.Background())
	application := &Application{
		store: config.Store, box: box, metrics: config.Metrics, send: send, now: now,
		onError: config.OnError, every: every, ctx: ctx, cancel: cancel,
		queue: make(chan serviceErrorNotification, errorNotificationQueue),
	}
	for range 2 {
		application.wait.Add(1)
		go application.runErrorQueue()
	}
	return application, nil
}

func (application *Application) SetMetricQuerier(querier MetricQuerier) {
	application.metrics = querier
}

func (application *Application) Close() {
	application.mu.Lock()
	if !application.closed {
		application.closed = true
		application.cancel()
		close(application.queue)
	}
	application.mu.Unlock()
	application.wait.Wait()
}

func (application *Application) Settings(ctx context.Context) (Settings, error) {
	smtp, err := application.publicSMTP(ctx)
	if err != nil {
		return Settings{}, err
	}
	errorAlerts, err := application.store.MailErrorAlerts(ctx)
	if err != nil {
		return Settings{}, err
	}
	metricAlerts, err := application.store.MailMetricAlerts(ctx)
	if err != nil {
		return Settings{}, err
	}
	services, err := application.store.MailAlertServices(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{SMTP: smtp, ErrorAlerts: errorAlerts, MetricAlerts: metricAlerts, Services: services}, nil
}

func (application *Application) ConfigureSMTP(ctx context.Context, input SMTPInput, mutation Mutation) (SMTP, error) {
	host, err := normalizeSMTPHost(input.Host)
	if err != nil {
		return SMTP{}, err
	}
	encryption, err := normalizeEncryption(input.Encryption)
	if err != nil {
		return SMTP{}, err
	}
	from, err := normalizeAddress(input.FromAddress)
	if err != nil {
		return SMTP{}, fmt.Errorf("from address: %w", err)
	}
	fromName := strings.TrimSpace(input.FromName)
	if len(fromName) > 80 {
		return SMTP{}, errors.New("from name must be at most 80 characters")
	}
	username := strings.TrimSpace(input.Username)
	if len(username) > 320 {
		return SMTP{}, errors.New("SMTP username must be at most 320 characters")
	}
	if input.Port < 1 || input.Port > 65535 {
		return SMTP{}, errors.New("SMTP port must be between 1 and 65535")
	}
	if mutation.AuditEventID == "" || mutation.ActorID == "" || mutation.ActorEmail == "" || mutation.UpdatedAtMillis <= 0 {
		return SMTP{}, errors.New("SMTP configuration mutation is incomplete")
	}
	password := bytesTrimSpace(input.Password)
	defer clear(password)
	existing, err := application.store.SMTPSettings(ctx)
	if err != nil && !errors.Is(err, state.ErrSMTPNotConfigured) {
		return SMTP{}, err
	}
	if len(password) == 0 {
		if errors.Is(err, state.ErrSMTPNotConfigured) {
			return SMTP{}, errors.New("SMTP password is required")
		}
		password, err = application.box.Open(existing.PasswordEncrypted, []byte(smtpPasswordAD))
		if err != nil {
			return SMTP{}, err
		}
		defer clear(password)
	}
	sealed, err := application.box.Seal(password, []byte(smtpPasswordAD))
	if err != nil {
		return SMTP{}, err
	}
	if err := application.store.PutSMTPSettings(ctx, state.PutSMTPSettingsInput{
		Settings: state.SMTPSettings{
			Host: host, Port: input.Port, Username: username, PasswordEncrypted: sealed,
			FromAddress: from, FromName: fromName, Encryption: encryption,
		},
		AuditEventID: mutation.AuditEventID, ActorID: mutation.ActorID, ActorEmail: mutation.ActorEmail,
		RequestCorrelationID: mutation.CorrelationID, UpdatedAtMillis: mutation.UpdatedAtMillis,
	}); err != nil {
		return SMTP{}, err
	}
	return application.publicSMTP(ctx)
}

func (application *Application) TestSMTP(ctx context.Context, recipient string) error {
	to, err := normalizeAddress(recipient)
	if err != nil {
		return fmt.Errorf("test recipient: %w", err)
	}
	config, password, err := application.credentials(ctx)
	if err != nil {
		return err
	}
	defer clear(password)
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	return application.send(sendCtx, config, password, Message{
		To:      []string{to},
		Subject: "[platformd] Test email",
		Body:    "This is a test email from platformd mail sending.",
	})
}

func (application *Application) CreateErrorAlert(ctx context.Context, alert state.MailErrorAlert, mutation Mutation) (state.MailErrorAlert, error) {
	normalized, err := normalizeErrorAlert(alert, true, mutation.UpdatedAtMillis)
	if err != nil {
		return state.MailErrorAlert{}, err
	}
	idValue, err := allocateAlertID(alert.ID)
	if err != nil {
		return state.MailErrorAlert{}, err
	}
	normalized.ID = idValue
	if err := application.store.CreateMailErrorAlert(ctx, normalized, mailMutation(mutation)); err != nil {
		return state.MailErrorAlert{}, err
	}
	return normalized, nil
}

func (application *Application) UpdateErrorAlert(ctx context.Context, alert state.MailErrorAlert, mutation Mutation) (state.MailErrorAlert, error) {
	normalized, err := normalizeErrorAlert(alert, false, mutation.UpdatedAtMillis)
	if err != nil {
		return state.MailErrorAlert{}, err
	}
	if err := application.store.UpdateMailErrorAlert(ctx, normalized, mailMutation(mutation)); err != nil {
		return state.MailErrorAlert{}, err
	}
	return normalized, nil
}

func (application *Application) DeleteErrorAlert(ctx context.Context, alertID string, mutation Mutation) error {
	if alertID == "" {
		return errors.New("mail error alert ID is required")
	}
	return application.store.DeleteMailErrorAlert(ctx, alertID, mailMutation(mutation), mutation.UpdatedAtMillis)
}

func (application *Application) CreateMetricAlert(ctx context.Context, alert state.MailMetricAlert, mutation Mutation) (state.MailMetricAlert, error) {
	normalized, err := normalizeMetricAlert(alert, true, mutation.UpdatedAtMillis)
	if err != nil {
		return state.MailMetricAlert{}, err
	}
	if err := application.validateMetricSQL(ctx, normalized); err != nil {
		return state.MailMetricAlert{}, err
	}
	idValue, err := allocateAlertID(alert.ID)
	if err != nil {
		return state.MailMetricAlert{}, err
	}
	normalized.ID = idValue
	if err := application.store.CreateMailMetricAlert(ctx, normalized, mailMutation(mutation)); err != nil {
		return state.MailMetricAlert{}, err
	}
	return normalized, nil
}

func (application *Application) UpdateMetricAlert(ctx context.Context, alert state.MailMetricAlert, mutation Mutation) (state.MailMetricAlert, error) {
	normalized, err := normalizeMetricAlert(alert, false, mutation.UpdatedAtMillis)
	if err != nil {
		return state.MailMetricAlert{}, err
	}
	if err := application.validateMetricSQL(ctx, normalized); err != nil {
		return state.MailMetricAlert{}, err
	}
	if err := application.store.UpdateMailMetricAlert(ctx, normalized, mailMutation(mutation)); err != nil {
		return state.MailMetricAlert{}, err
	}
	return normalized, nil
}

func (application *Application) DeleteMetricAlert(ctx context.Context, alertID string, mutation Mutation) error {
	if alertID == "" {
		return errors.New("mail metric alert ID is required")
	}
	return application.store.DeleteMailMetricAlert(ctx, alertID, mailMutation(mutation), mutation.UpdatedAtMillis)
}

func (application *Application) publicSMTP(ctx context.Context) (SMTP, error) {
	stored, err := application.store.SMTPSettings(ctx)
	if errors.Is(err, state.ErrSMTPNotConfigured) {
		return SMTP{}, nil
	}
	if err != nil {
		return SMTP{}, err
	}
	return SMTP{
		Configured: true, Host: stored.Host, Port: stored.Port, Username: stored.Username,
		PasswordSet: true, FromAddress: stored.FromAddress, FromName: stored.FromName,
		Encryption: stored.Encryption, UpdatedAtMillis: stored.UpdatedAtMillis,
	}, nil
}

func (application *Application) credentials(ctx context.Context) (SMTPConfig, []byte, error) {
	stored, err := application.store.SMTPSettings(ctx)
	if err != nil {
		return SMTPConfig{}, nil, err
	}
	password, err := application.box.Open(stored.PasswordEncrypted, []byte(smtpPasswordAD))
	if err != nil {
		return SMTPConfig{}, nil, err
	}
	return SMTPConfig{
		Host: stored.Host, Port: stored.Port, Username: stored.Username,
		FromAddress: stored.FromAddress, FromName: stored.FromName, Encryption: stored.Encryption,
	}, password, nil
}

func (application *Application) validateMetricSQL(ctx context.Context, alert state.MailMetricAlert) error {
	if application.metrics == nil {
		return nil
	}
	to := application.now().UnixMilli()
	from := to - int64(alert.WindowSeconds)*1000
	step := metricStepMillis(alert.WindowSeconds)
	if _, err := application.metrics.Query(ctx, alert.Scope, alert.SQL, from, to, step); err != nil {
		return fmt.Errorf("metric alert SQL failed: %w", err)
	}
	return nil
}

func mailMutation(mutation Mutation) state.MailAlertMutation {
	return state.MailAlertMutation{
		AuditEventID: mutation.AuditEventID, ActorID: mutation.ActorID, ActorEmail: mutation.ActorEmail,
		RequestCorrelationID: mutation.CorrelationID,
	}
}

func allocateAlertID(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	return id.New()
}

func normalizeErrorAlert(alert state.MailErrorAlert, creating bool, timestamp int64) (state.MailErrorAlert, error) {
	name := strings.TrimSpace(alert.Name)
	if name == "" || len(name) > 80 {
		return state.MailErrorAlert{}, errors.New("alert name must be between 1 and 80 characters")
	}
	recipients, err := normalizeRecipients(alert.Recipients)
	if err != nil {
		return state.MailErrorAlert{}, err
	}
	events, err := normalizeErrorEvents(alert.EventTypes)
	if err != nil {
		return state.MailErrorAlert{}, err
	}
	serviceIDs, err := uniqueIDs(alert.ServiceIDs, 50)
	if err != nil {
		return state.MailErrorAlert{}, fmt.Errorf("alert services: %w", err)
	}
	alert.Name = name
	alert.Recipients = recipients
	alert.EventTypes = events
	alert.ServiceIDs = serviceIDs
	alert.UpdatedAtMillis = timestamp
	if creating {
		alert.CreatedAtMillis = timestamp
	}
	return alert, nil
}

func normalizeMetricAlert(alert state.MailMetricAlert, creating bool, timestamp int64) (state.MailMetricAlert, error) {
	name := strings.TrimSpace(alert.Name)
	if name == "" || len(name) > 80 {
		return state.MailMetricAlert{}, errors.New("alert name must be between 1 and 80 characters")
	}
	recipients, err := normalizeRecipients(alert.Recipients)
	if err != nil {
		return state.MailMetricAlert{}, err
	}
	sql := strings.TrimSpace(alert.SQL)
	if sql == "" || len(sql) > 16<<10 {
		return state.MailMetricAlert{}, errors.New("metric alert SQL must be between 1 and 16384 characters")
	}
	operator := strings.ToLower(strings.TrimSpace(alert.Operator))
	if operator != "gt" && operator != "gte" && operator != "lt" && operator != "lte" {
		return state.MailMetricAlert{}, errors.New("metric alert operator must be gt, gte, lt, or lte")
	}
	if alert.WindowSeconds < 60 || alert.WindowSeconds > 86400 {
		return state.MailMetricAlert{}, errors.New("metric alert window must be between 60 and 86400 seconds")
	}
	if err := validateMetricScope(alert.Scope); err != nil {
		return state.MailMetricAlert{}, err
	}
	alert.Name = name
	alert.Recipients = recipients
	alert.SQL = sql
	alert.Operator = operator
	alert.UpdatedAtMillis = timestamp
	alert.Firing = false
	alert.LastValue = nil
	alert.LastEvaluatedAt = 0
	if creating {
		alert.CreatedAtMillis = timestamp
	}
	return alert, nil
}

func validateMetricScope(scope state.MetricScope) error {
	switch scope.Kind {
	case state.MetricScopeInstallation:
		if scope.ProjectID == "" && scope.ServiceID == "" {
			return nil
		}
	case state.MetricScopeProject:
		if scope.ProjectID != "" && scope.ServiceID == "" {
			return nil
		}
	case state.MetricScopeService:
		if scope.ProjectID != "" && scope.ServiceID != "" {
			return nil
		}
	}
	return errors.New("metric alert scope is invalid")
}

func normalizeRecipients(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 20 {
		return nil, errors.New("alerts require between 1 and 20 recipients")
	}
	recipients := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		address, err := normalizeAddress(value)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(address)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		recipients = append(recipients, address)
	}
	if len(recipients) == 0 {
		return nil, errors.New("alerts require between 1 and 20 recipients")
	}
	return recipients, nil
}

func normalizeErrorEvents(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("error alerts require at least one event")
	}
	events := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		event := strings.TrimSpace(value)
		if !slices.Contains(allowedErrorEvents, event) {
			return nil, fmt.Errorf("unsupported error alert event %q", event)
		}
		if _, exists := seen[event]; exists {
			continue
		}
		seen[event] = struct{}{}
		events = append(events, event)
	}
	slices.Sort(events)
	return events, nil
}

func uniqueIDs(values []string, maximum int) ([]string, error) {
	if len(values) > maximum {
		return nil, fmt.Errorf("at most %d IDs are allowed", maximum)
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		idValue := strings.TrimSpace(value)
		if idValue == "" {
			return nil, errors.New("ID is empty")
		}
		if _, exists := seen[idValue]; exists {
			continue
		}
		seen[idValue] = struct{}{}
		ids = append(ids, idValue)
	}
	slices.Sort(ids)
	return ids, nil
}

func normalizeAddress(value string) (string, error) {
	parsed, err := netmail.ParseAddress(strings.TrimSpace(value))
	if err != nil || parsed.Address == "" || strings.ContainsAny(parsed.Address, " \t\r\n") {
		return "", errors.New("email address is invalid")
	}
	if len(parsed.Address) > 320 {
		return "", errors.New("email address is too long")
	}
	return parsed.Address, nil
}

func normalizeSMTPHost(value string) (string, error) {
	host := strings.TrimSpace(strings.ToLower(value))
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if host == "" || len(host) > 253 {
		return "", errors.New("SMTP host must be a hostname or IP without a port")
	}
	if parsed, err := netip.ParseAddr(host); err == nil {
		return parsed.String(), nil
	}
	if strings.ContainsAny(host, " /\\@:?#") {
		return "", errors.New("SMTP host must be a hostname or IP without a port")
	}
	for _, label := range strings.Split(host, ".") {
		if !validSMTPHostLabel(label) {
			return "", errors.New("SMTP host contains an invalid DNS label")
		}
	}
	return host, nil
}

func validSMTPHostLabel(label string) bool {
	if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		r := label[i]
		if r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			continue
		}
		return false
	}
	return true
}

func normalizeEncryption(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "none", "starttls", "tls":
		return strings.ToLower(strings.TrimSpace(value)), nil
	default:
		return "", errors.New("SMTP encryption must be none, starttls, or tls")
	}
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

func metricStepMillis(windowSeconds int) int64 {
	step := int64(windowSeconds) * 1000 / 60
	if step < 1000 {
		return 1000
	}
	return step
}
