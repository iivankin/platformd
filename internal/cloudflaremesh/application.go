package cloudflaremesh

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/cryptobox"
	"github.com/iivankin/platformd/internal/state"
)

var accountIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Repository interface {
	CloudflareMeshSettings(context.Context) (state.CloudflareMeshSettings, error)
	PutCloudflareMeshSettings(context.Context, state.PutCloudflareMeshSettingsInput) error
}

type Runtime interface {
	Ensure(context.Context, string, bool) error
	Address() (NetworkAddress, error)
	Close() error
}

type Config struct {
	Repository     Repository
	Master         cryptobox.MasterKey
	InstallationID string
	Runtime        Runtime
	HTTPClient     *http.Client
	BaseURL        string
	Random         io.Reader
}

type Application struct {
	repository     Repository
	installationID string
	box            cryptobox.Box
	runtime        Runtime
	client         *apiClient
	random         io.Reader
	reconcileMu    sync.Mutex
}

type Settings struct {
	Configured      bool
	AccountID       string
	NodeID          string
	NodeName        string
	Status          string
	InterfaceName   string
	MeshIP          string
	UpdatedAtMillis int64
}

type Credential struct {
	AccountID string
	APIToken  string
}

type ConfigureInput struct {
	AccountID       string
	APIToken        []byte
	AuditEventID    string
	ActorID         string
	ActorEmail      string
	CorrelationID   string
	UpdatedAtMillis int64
}

func New(config Config) (*Application, error) {
	if config.Repository == nil || config.InstallationID == "" || config.Runtime == nil {
		return nil, errors.New("Cloudflare Mesh dependencies are incomplete")
	}
	box, err := cryptobox.NewBox(config.Master, []byte(config.InstallationID), "platformd/cloudflare-mesh/v1")
	if err != nil {
		return nil, err
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	return &Application{
		repository: config.Repository, installationID: config.InstallationID,
		box: box, runtime: config.Runtime, client: newAPIClient(config.HTTPClient, config.BaseURL),
		random: config.Random,
	}, nil
}

func (application *Application) Settings(ctx context.Context) (Settings, error) {
	stored, err := application.repository.CloudflareMeshSettings(ctx)
	if errors.Is(err, state.ErrCloudflareMeshNotConfigured) {
		return Settings{Status: "not_configured"}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	result := Settings{
		Configured: true, AccountID: stored.AccountID, NodeID: stored.NodeID,
		NodeName: stored.NodeName, Status: "disconnected", UpdatedAtMillis: stored.UpdatedAtMillis,
	}
	if address, addressErr := application.runtime.Address(); addressErr == nil {
		result.Status = "connected"
		result.InterfaceName = address.InterfaceName
		result.MeshIP = address.Address
	}
	return result, nil
}

func (application *Application) Credential(ctx context.Context) (Credential, error) {
	stored, err := application.repository.CloudflareMeshSettings(ctx)
	if err != nil {
		return Credential{}, err
	}
	token, err := application.box.Open(stored.APITokenEncrypted, []byte("api-token"))
	if err != nil {
		return Credential{}, err
	}
	defer clear(token)
	return Credential{AccountID: stored.AccountID, APIToken: string(token)}, nil
}

func (application *Application) Configure(ctx context.Context, input ConfigureInput) (Settings, error) {
	application.reconcileMu.Lock()
	defer application.reconcileMu.Unlock()

	input.AccountID = strings.ToLower(strings.TrimSpace(input.AccountID))
	token := bytes.TrimSpace(input.APIToken)
	if !accountIDPattern.MatchString(input.AccountID) || len(token) < 20 || input.AuditEventID == "" ||
		input.ActorID == "" || input.ActorEmail == "" || input.UpdatedAtMillis <= 0 {
		return Settings{}, errors.New("Cloudflare Mesh credentials are incomplete")
	}
	current, currentErr := application.repository.CloudflareMeshSettings(ctx)
	if currentErr != nil && !errors.Is(currentErr, state.ErrCloudflareMeshNotConfigured) {
		return Settings{}, currentErr
	}
	nodeName := managedNodeName(application.installationID)
	nodeID := ""
	if currentErr == nil && current.AccountID == input.AccountID {
		if _, err := application.client.node(ctx, input.AccountID, string(token), current.NodeID); err == nil {
			nodeID = current.NodeID
			nodeName = current.NodeName
		}
	}
	if nodeID == "" {
		node, err := application.client.findOrCreateNode(ctx, input.AccountID, string(token), nodeName)
		if err != nil {
			return Settings{}, err
		}
		nodeID = node.ID
	}
	nodeToken, err := application.client.nodeToken(ctx, input.AccountID, string(token), nodeID)
	if err != nil {
		return Settings{}, err
	}
	defer clear(nodeToken)
	reenroll := currentErr != nil || current.AccountID != input.AccountID || current.NodeID != nodeID
	if err := application.runtime.Ensure(ctx, string(nodeToken), reenroll); err != nil {
		return Settings{}, fmt.Errorf("connect managed Cloudflare Mesh node: %w", err)
	}
	sealed, err := application.box.SealWith(application.random, token, []byte("api-token"))
	if err != nil {
		return Settings{}, err
	}
	if err := application.repository.PutCloudflareMeshSettings(ctx, state.PutCloudflareMeshSettingsInput{
		Settings: state.CloudflareMeshSettings{
			AccountID: input.AccountID, APITokenEncrypted: sealed,
			NodeID: nodeID, NodeName: nodeName,
		},
		AuditEventID: input.AuditEventID, ActorID: input.ActorID, ActorEmail: input.ActorEmail,
		CorrelationID: input.CorrelationID, UpdatedAtMillis: input.UpdatedAtMillis,
	}); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

// EnsureConfigured reconstructs the machine-local Cloudflare One Client state
// from authoritative SQLite credentials after a restart or disaster restore.
func (application *Application) EnsureConfigured(ctx context.Context) error {
	application.reconcileMu.Lock()
	defer application.reconcileMu.Unlock()
	_, err := application.ensureConfiguredLocked(ctx)
	return err
}

func (application *Application) ensureConfiguredLocked(ctx context.Context) (bool, error) {
	stored, err := application.repository.CloudflareMeshSettings(ctx)
	if errors.Is(err, state.ErrCloudflareMeshNotConfigured) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	token, err := application.box.Open(stored.APITokenEncrypted, []byte("api-token"))
	if err != nil {
		return false, err
	}
	defer clear(token)
	nodeToken, err := application.client.nodeToken(ctx, stored.AccountID, string(token), stored.NodeID)
	if err != nil {
		return false, err
	}
	defer clear(nodeToken)
	if err := application.runtime.Ensure(ctx, string(nodeToken), false); err != nil {
		return false, err
	}
	return true, nil
}

// RepairConnection reconstructs the managed client only when it is no longer
// reachable. The returned flag tells the daemon to rebind routes because a
// recreated sidecar has a different network namespace PID.
func (application *Application) RepairConnection(ctx context.Context) (bool, error) {
	if _, err := application.runtime.Address(); err == nil {
		return false, nil
	}
	application.reconcileMu.Lock()
	defer application.reconcileMu.Unlock()
	return application.ensureConfiguredLocked(ctx)
}

func (application *Application) Reconnect(ctx context.Context) (Settings, error) {
	if err := application.EnsureConfigured(ctx); err != nil {
		return Settings{}, err
	}
	return application.Settings(ctx)
}

func (application *Application) Address() (NetworkAddress, error) {
	return application.runtime.Address()
}

func managedNodeName(installationID string) string {
	const maximumSuffix = 20
	suffix := strings.ToLower(strings.TrimSpace(installationID))
	if len(suffix) > maximumSuffix {
		suffix = suffix[:maximumSuffix]
	}
	return "platformd-" + suffix
}

func newAPIClient(client *http.Client, baseURL string) *apiClient {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.cloudflare.com/client/v4"
	}
	return &apiClient{client: client, baseURL: baseURL}
}
