package portforward

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/automation"
	"github.com/iivankin/platformd/internal/id"
)

const (
	DefaultLifetime      = time.Hour
	MaximumLifetime      = 8 * time.Hour
	MaximumTickets       = 1024
	MaximumConnections   = 16
	ticketRandomByteSize = 32
)

var (
	ErrInvalidInput      = errors.New("invalid port forward input")
	ErrInvalidTicket     = errors.New("invalid or expired port forward ticket")
	ErrTicketCapacity    = errors.New("port forward ticket capacity reached")
	ErrConnectionLimit   = errors.New("port forward connection limit reached")
	ErrTargetUnavailable = errors.New("port forward target is unavailable")
)

type ResourceRepository interface {
	Resource(context.Context, string, string, string) error
}

type TargetResolver interface {
	ResolveResourceAddress(string, string, string, int) (string, error)
}

type AuditRecorder interface {
	RecordPortForwardTicket(context.Context, AuditRecord) error
}

type AuditRecord struct {
	ID           string
	ActorTokenID string
	TicketID     string
	ProjectID    string
	ResourceKind string
	ResourceID   string
	Port         int
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type Config struct {
	Repository ResourceRepository
	Resolver   TargetResolver
	Audit      AuditRecorder
	Random     io.Reader
	Now        func() time.Time
	NewID      func() (string, error)
}

type Application struct {
	repository ResourceRepository
	resolver   TargetResolver
	audit      AuditRecorder
	random     io.Reader
	now        func() time.Time
	newID      func() (string, error)

	mu      sync.Mutex
	tickets map[[sha256.Size]byte]*ticketState
}

type CreateInput struct {
	ProjectID       string
	ResourceKind    string
	ResourceID      string
	Port            int
	LifetimeSeconds int
}

type Grant struct {
	ID           string    `json:"id"`
	Ticket       string    `json:"ticket"`
	ProjectID    string    `json:"projectId"`
	ResourceKind string    `json:"resourceKind"`
	ResourceID   string    `json:"resourceId"`
	Port         int       `json:"port"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type Session struct {
	TicketID string
	Target   string
	release  func()
	once     sync.Once
}

type ticketState struct {
	id           string
	projectID    string
	resourceKind string
	resourceID   string
	port         int
	expiresAt    time.Time
	connections  int
}

func New(config Config) (*Application, error) {
	if config.Repository == nil || config.Resolver == nil || config.Audit == nil {
		return nil, errors.New("port forward dependencies are incomplete")
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.NewID == nil {
		config.NewID = id.New
	}
	return &Application{
		repository: config.Repository,
		resolver:   config.Resolver,
		audit:      config.Audit,
		random:     config.Random,
		now:        config.Now,
		newID:      config.NewID,
		tickets:    make(map[[sha256.Size]byte]*ticketState),
	}, nil
}

func (application *Application) Create(ctx context.Context, identity automation.Identity, input CreateInput) (Grant, error) {
	if !identity.IsAdmin() {
		return Grant{}, automation.ErrAdminRequired
	}
	if !identity.AllowsProject(input.ProjectID) {
		return Grant{}, automation.ErrProjectBoundary
	}
	lifetime, err := validateCreateInput(input)
	if err != nil {
		return Grant{}, err
	}
	if err := application.repository.Resource(ctx, input.ProjectID, input.ResourceKind, input.ResourceID); err != nil {
		return Grant{}, err
	}
	if _, err := application.resolver.ResolveResourceAddress(input.ProjectID, input.ResourceKind, input.ResourceID, input.Port); err != nil {
		return Grant{}, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	ticketID, err := application.newID()
	if err != nil {
		return Grant{}, fmt.Errorf("create port forward ticket ID: %w", err)
	}
	ticket, err := newTicket(application.random)
	if err != nil {
		return Grant{}, err
	}
	createdAt := application.now().UTC()
	expiresAt := createdAt.Add(lifetime)
	state := &ticketState{
		id: ticketID, projectID: input.ProjectID, resourceKind: input.ResourceKind,
		resourceID: input.ResourceID, port: input.Port, expiresAt: expiresAt,
	}
	hash := sha256.Sum256([]byte(ticket))
	application.mu.Lock()
	application.removeExpiredLocked(createdAt)
	if len(application.tickets) >= MaximumTickets {
		application.mu.Unlock()
		return Grant{}, ErrTicketCapacity
	}
	application.tickets[hash] = state
	application.mu.Unlock()

	audit := AuditRecord{
		ID: ticketID, ActorTokenID: identity.TokenID, TicketID: ticketID,
		ProjectID: input.ProjectID, ResourceKind: input.ResourceKind,
		ResourceID: input.ResourceID, Port: input.Port, CreatedAt: createdAt, ExpiresAt: expiresAt,
	}
	if err := application.audit.RecordPortForwardTicket(ctx, audit); err != nil {
		application.mu.Lock()
		if application.tickets[hash] == state {
			delete(application.tickets, hash)
		}
		application.mu.Unlock()
		return Grant{}, fmt.Errorf("audit port forward ticket: %w", err)
	}
	return Grant{
		ID: ticketID, Ticket: ticket, ProjectID: input.ProjectID, ResourceKind: input.ResourceKind,
		ResourceID: input.ResourceID, Port: input.Port, ExpiresAt: expiresAt,
	}, nil
}

func (application *Application) Acquire(ticket string) (*Session, error) {
	if len(ticket) < len("pft_")+1 {
		return nil, ErrInvalidTicket
	}
	hash := sha256.Sum256([]byte(ticket))
	now := application.now().UTC()
	application.mu.Lock()
	application.removeExpiredLocked(now)
	state, exists := application.tickets[hash]
	if !exists {
		application.mu.Unlock()
		return nil, ErrInvalidTicket
	}
	if state.connections >= MaximumConnections {
		application.mu.Unlock()
		return nil, ErrConnectionLimit
	}
	state.connections++
	application.mu.Unlock()

	target, err := application.resolver.ResolveResourceAddress(
		state.projectID, state.resourceKind, state.resourceID, state.port,
	)
	if err != nil {
		application.release(state)
		return nil, fmt.Errorf("%w: %v", ErrTargetUnavailable, err)
	}
	return &Session{
		TicketID: state.id, Target: target,
		release: func() { application.release(state) },
	}, nil
}

func (session *Session) Release() {
	if session != nil && session.release != nil {
		session.once.Do(session.release)
	}
}

func (application *Application) release(state *ticketState) {
	application.mu.Lock()
	if state.connections > 0 {
		state.connections--
	}
	application.mu.Unlock()
}

func (application *Application) removeExpiredLocked(now time.Time) {
	for hash, state := range application.tickets {
		if !now.Before(state.expiresAt) {
			delete(application.tickets, hash)
		}
	}
}

func validateCreateInput(input CreateInput) (time.Duration, error) {
	if input.ProjectID == "" || input.ResourceID == "" || input.Port < 1 || input.Port > 65535 {
		return 0, ErrInvalidInput
	}
	switch input.ResourceKind {
	case "service", "postgres", "redis":
	default:
		return 0, ErrInvalidInput
	}
	if input.LifetimeSeconds == 0 {
		return DefaultLifetime, nil
	}
	if input.LifetimeSeconds < int(time.Minute/time.Second) || input.LifetimeSeconds > int(MaximumLifetime/time.Second) {
		return 0, ErrInvalidInput
	}
	lifetime := time.Duration(input.LifetimeSeconds) * time.Second
	return lifetime, nil
}

func newTicket(random io.Reader) (string, error) {
	value := make([]byte, ticketRandomByteSize)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate port forward ticket: %w", err)
	}
	return "pft_" + base64.RawURLEncoding.EncodeToString(value), nil
}
