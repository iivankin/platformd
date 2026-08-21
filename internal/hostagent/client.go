package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/hostconn"
	"github.com/iivankin/platformd/internal/hosttunnel"
	"github.com/iivankin/platformd/internal/id"
	"github.com/iivankin/platformd/internal/state"
)

const rpcTimeout = 60 * time.Second

type Handlers struct {
	Certificates func([]hostconn.CertificatePEM) error
	Projects     func([]state.RuntimeProject) error
	Reconcile    func(string, bool) error
	Withdraw     func(string) error
	SyncPublic   func(string) error
}

type Conn struct {
	mu       sync.Mutex
	conn     *websocket.Conn
	pending  map[string]chan rpcReply
	handlers Handlers
	jobs     []func()
	kick     chan struct{}
	closed   chan struct{}
	stopped  bool
}

type rpcReply struct {
	payload json.RawMessage
	err     string
}

type DialOptions struct {
	ParentURL  string
	HostToken  string
	PublicIPv4 string
	Handlers   Handlers
	HTTPClient *http.Client
}

func Dial(ctx context.Context, parentURL, hostToken, publicIPv4 string, handlers Handlers) (*Conn, hostconn.Welcome, error) {
	return DialWithOptions(ctx, DialOptions{
		ParentURL: parentURL, HostToken: hostToken, PublicIPv4: publicIPv4, Handlers: handlers,
	})
}

func DialWithOptions(ctx context.Context, options DialOptions) (*Conn, hostconn.Welcome, error) {
	connectURL, err := parentWebSocketURL(options.ParentURL, hostconn.ConnectPath)
	if err != nil {
		return nil, hostconn.Welcome{}, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+options.HostToken)
	connection, _, err := websocket.Dial(ctx, connectURL, &websocket.DialOptions{
		HTTPHeader:   header,
		Subprotocols: []string{hostconn.WebSocketProtocol},
		HTTPClient:   options.HTTPClient,
	})
	if err != nil {
		return nil, hostconn.Welcome{}, fmt.Errorf("connect to parent: %w", err)
	}
	connection.SetReadLimit(4 << 20)
	client := &Conn{
		conn: connection, pending: map[string]chan rpcReply{}, handlers: options.Handlers,
		kick: make(chan struct{}, 1), closed: make(chan struct{}),
	}
	if err := client.Write(ctx, hostconn.KindHello, hostconn.Hello{PublicIPv4: options.PublicIPv4}); err != nil {
		_ = connection.Close(websocket.StatusGoingAway, "")
		return nil, hostconn.Welcome{}, err
	}
	var envelope hostconn.Envelope
	if err := wsjson.Read(ctx, connection, &envelope); err != nil {
		_ = connection.Close(websocket.StatusGoingAway, "")
		return nil, hostconn.Welcome{}, err
	}
	if envelope.Kind != hostconn.KindWelcome {
		_ = connection.Close(websocket.StatusGoingAway, "")
		return nil, hostconn.Welcome{}, fmt.Errorf("parent sent %s, want welcome", envelope.Kind)
	}
	var welcome hostconn.Welcome
	if err := json.Unmarshal(envelope.Payload, &welcome); err != nil {
		_ = connection.Close(websocket.StatusGoingAway, "")
		return nil, hostconn.Welcome{}, err
	}
	go client.runWork()
	return client, welcome, nil
}

func (client *Conn) Close() error {
	if client == nil {
		return nil
	}
	client.mu.Lock()
	if !client.stopped {
		client.stopped = true
		close(client.closed)
	}
	connection := client.conn
	client.mu.Unlock()
	if connection == nil {
		return nil
	}
	return connection.Close(websocket.StatusNormalClosure, "")
}

func (client *Conn) Write(ctx context.Context, kind string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return wsjson.Write(ctx, client.conn, hostconn.Envelope{Kind: kind, Payload: body})
}

func (client *Conn) Purge(ctx context.Context, hostnames []string) error {
	callID, err := id.New()
	if err != nil {
		return err
	}
	body, err := json.Marshal(hostconn.PurgeCache{Hostnames: hostnames})
	if err != nil {
		return err
	}
	reply := make(chan rpcReply, 1)
	client.mu.Lock()
	client.pending[callID] = reply
	writeErr := wsjson.Write(ctx, client.conn, hostconn.Envelope{ID: callID, Kind: hostconn.KindPurgeCache, Payload: body})
	client.mu.Unlock()
	if writeErr != nil {
		client.finish(callID)
		return writeErr
	}
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		client.finish(callID)
		return ctx.Err()
	case message := <-reply:
		if message.err != "" {
			return errors.New(message.err)
		}
		return nil
	}
}

func (client *Conn) Call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	callID, err := id.New()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(hostconn.RPCRequest{Method: method, Params: body})
	if err != nil {
		return err
	}
	reply := make(chan rpcReply, 1)
	client.mu.Lock()
	client.pending[callID] = reply
	writeErr := wsjson.Write(ctx, client.conn, hostconn.Envelope{ID: callID, Kind: hostconn.KindRPC, Payload: payload})
	client.mu.Unlock()
	if writeErr != nil {
		client.finish(callID)
		return writeErr
	}
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	select {
	case <-ctx.Done():
		client.finish(callID)
		return ctx.Err()
	case message := <-reply:
		if message.err != "" {
			return errors.New(message.err)
		}
		if result == nil || len(message.payload) == 0 {
			return nil
		}
		return json.Unmarshal(message.payload, result)
	}
}

func (client *Conn) Serve(ctx context.Context) error {
	for {
		var envelope hostconn.Envelope
		if err := wsjson.Read(ctx, client.conn, &envelope); err != nil {
			return err
		}
		if err := client.dispatch(ctx, envelope); err != nil {
			return err
		}
	}
}

func (client *Conn) dispatch(ctx context.Context, envelope hostconn.Envelope) error {
	switch envelope.Kind {
	case hostconn.KindRPCResult:
		client.complete(envelope)
		return nil
	case hostconn.KindPurgeResult:
		client.complete(envelope)
		return nil
	case hostconn.KindCertificates:
		var certificates []hostconn.CertificatePEM
		if err := json.Unmarshal(envelope.Payload, &certificates); err != nil {
			return err
		}
		client.enqueue("certificates", func() error {
			if client.handlers.Certificates == nil {
				return nil
			}
			return client.handlers.Certificates(certificates)
		})
	case hostconn.KindProjects:
		var projects []state.RuntimeProject
		if err := json.Unmarshal(envelope.Payload, &projects); err != nil {
			return err
		}
		client.enqueue("projects", func() error {
			if client.handlers.Projects == nil {
				return nil
			}
			return client.handlers.Projects(projects)
		})
	case hostconn.KindReconcile:
		var request hostconn.Reconcile
		if err := json.Unmarshal(envelope.Payload, &request); err != nil {
			return err
		}
		// Parent commands that create networks or issue RPCs must not run on this
		// read loop: RPC replies arrive here.
		client.enqueue("reconcile "+request.ServiceID, func() error {
			if client.handlers.Reconcile == nil {
				return nil
			}
			return client.handlers.Reconcile(request.ServiceID, request.Force)
		})
	case hostconn.KindWithdraw:
		var request hostconn.Withdraw
		if err := json.Unmarshal(envelope.Payload, &request); err != nil {
			return err
		}
		client.enqueue("withdraw "+request.ServiceID, func() error {
			if client.handlers.Withdraw == nil {
				return nil
			}
			return client.handlers.Withdraw(request.ServiceID)
		})
	case hostconn.KindSyncPublic:
		var request hostconn.SyncPublic
		if err := json.Unmarshal(envelope.Payload, &request); err != nil {
			return err
		}
		client.enqueue("sync-public "+request.ServiceID, func() error {
			if client.handlers.SyncPublic == nil {
				return nil
			}
			return client.handlers.SyncPublic(request.ServiceID)
		})
	}
	return nil
}

func (client *Conn) enqueue(name string, fn func() error) {
	client.mu.Lock()
	if client.stopped {
		client.mu.Unlock()
		return
	}
	client.jobs = append(client.jobs, func() {
		if err := fn(); err != nil {
			log.Printf("host %s: %v", name, err)
		}
	})
	client.mu.Unlock()
	select {
	case client.kick <- struct{}{}:
	default:
	}
}

func (client *Conn) runWork() {
	for {
		select {
		case <-client.closed:
			return
		case <-client.kick:
			for client.runOne() {
			}
		}
	}
}

func (client *Conn) runOne() bool {
	client.mu.Lock()
	if len(client.jobs) == 0 {
		client.mu.Unlock()
		return false
	}
	fn := client.jobs[0]
	client.jobs = client.jobs[1:]
	client.mu.Unlock()
	fn()
	return true
}

func (client *Conn) complete(envelope hostconn.Envelope) {
	reply, ok := client.take(envelope.ID)
	if !ok {
		return
	}
	reply <- rpcReply{payload: envelope.Payload, err: envelope.Error}
}

func (client *Conn) take(id string) (chan rpcReply, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	reply, ok := client.pending[id]
	if ok {
		delete(client.pending, id)
	}
	return reply, ok
}

func (client *Conn) finish(id string) {
	client.mu.Lock()
	delete(client.pending, id)
	client.mu.Unlock()
}

type RemoteStore struct {
	conn *Conn
}

func NewRemoteStore(conn *Conn) *RemoteStore {
	return &RemoteStore{conn: conn}
}

func (store *RemoteStore) DesiredService(ctx context.Context, serviceID string) (state.ServiceDesired, error) {
	var desired state.ServiceDesired
	err := store.conn.Call(ctx, hostconn.RPCDesiredService, hostconn.ServiceIDParams{ServiceID: serviceID}, &desired)
	return desired, err
}

func (store *RemoteStore) BeginDeployment(ctx context.Context, input state.BeginDeployment) error {
	return store.conn.Call(ctx, hostconn.RPCBeginDeployment, hostconn.BeginDeploymentParams{Input: input}, nil)
}

func (store *RemoteStore) DiscardDeployment(ctx context.Context, deploymentID string) error {
	return store.conn.Call(ctx, hostconn.RPCDiscardDeployment, hostconn.DiscardDeploymentParams{DeploymentID: deploymentID}, nil)
}

func (store *RemoteStore) UpdateDeploymentSource(ctx context.Context, deploymentID, imageDigest, imageReference, sourceRevision, commitMessage string) error {
	return store.conn.Call(ctx, hostconn.RPCUpdateDeploymentSource, hostconn.UpdateDeploymentSourceParams{
		DeploymentID: deploymentID, ImageDigest: imageDigest, ImageReference: imageReference,
		SourceRevision: sourceRevision, CommitMessage: commitMessage,
	}, nil)
}

func (store *RemoteStore) FinishDeployment(ctx context.Context, deploymentID, status, code, message string, finishedAt int64) error {
	return store.conn.Call(ctx, hostconn.RPCFinishDeployment, hostconn.FinishDeploymentParams{
		DeploymentID: deploymentID, Status: status, ErrorCode: code, ErrorMessage: message, FinishedAt: finishedAt,
	}, nil)
}

func (store *RemoteStore) ActivateDeployment(ctx context.Context, serviceID, deploymentID, expectedActive string, finishedAt int64) error {
	return store.conn.Call(ctx, hostconn.RPCActivateDeployment, hostconn.ActivateDeploymentParams{
		ServiceID: serviceID, DeploymentID: deploymentID, ExpectedActiveDeploymentID: expectedActive, ActivatedAt: finishedAt,
	}, nil)
}

func (store *RemoteStore) FailDeployment(ctx context.Context, deploymentID, code, message string, failedAt int64) error {
	return store.conn.Call(ctx, hostconn.RPCFailDeployment, hostconn.FailDeploymentParams{
		DeploymentID: deploymentID, ErrorCode: code, ErrorMessage: message, FailedAt: failedAt,
	}, nil)
}

func (store *RemoteStore) LatestFailedDeployment(ctx context.Context, serviceID, configHash, imageRef string) (bool, error) {
	var result struct {
		Failed bool `json:"failed"`
	}
	err := store.conn.Call(ctx, hostconn.RPCLatestFailedDeployment, hostconn.LatestFailedDeploymentParams{
		ServiceID: serviceID, ConfigHash: configHash, ImageRef: imageRef,
	}, &result)
	return result.Failed, err
}

func (store *RemoteStore) LatestUploadedDeployment(ctx context.Context, serviceID string) (state.DeploymentRecord, error) {
	var record state.DeploymentRecord
	err := store.conn.Call(ctx, hostconn.RPCLatestUploadedDeployment, hostconn.ServiceIDParams{ServiceID: serviceID}, &record)
	return record, err
}

func (store *RemoteStore) LatestReusableProductionRevision(ctx context.Context, serviceID string) (state.ImageRevision, error) {
	var revision state.ImageRevision
	err := store.conn.Call(ctx, hostconn.RPCLatestReusableProductionRevision, hostconn.ServiceIDParams{ServiceID: serviceID}, &revision)
	return revision, err
}

func (store *RemoteStore) Deployment(ctx context.Context, deploymentID string) (state.DeploymentRecord, error) {
	var record state.DeploymentRecord
	err := store.conn.Call(ctx, hostconn.RPCDeployment, hostconn.DiscardDeploymentParams{DeploymentID: deploymentID}, &record)
	return record, err
}

func (store *RemoteStore) VolumeInitialized(ctx context.Context, projectID, serviceID, volumeID string) (bool, error) {
	var result struct {
		Initialized bool `json:"initialized"`
	}
	err := store.conn.Call(ctx, hostconn.RPCVolumeInitialized, hostconn.VolumeInitializedParams{
		ProjectID: projectID, ServiceID: serviceID, VolumeID: volumeID,
	}, &result)
	return result.Initialized, err
}

func (store *RemoteStore) RecordVolumeInitialization(ctx context.Context, projectID, serviceID, volumeID string, at int64) error {
	return store.conn.Call(ctx, hostconn.RPCRecordVolumeInitialization, hostconn.RecordVolumeInitializationParams{
		ProjectID: projectID, ServiceID: serviceID, VolumeID: volumeID, At: at,
	}, nil)
}

type RemoteCredentials struct {
	conn *Conn
}

func NewRemoteCredentials(conn *Conn) *RemoteCredentials {
	return &RemoteCredentials{conn: conn}
}

func (resolver *RemoteCredentials) Resolve(ctx context.Context, service state.ServiceDesired) (deployment.ImageCredential, error) {
	var credential deployment.ImageCredential
	err := resolver.conn.Call(ctx, hostconn.RPCResolveImageCredential, hostconn.ServiceIDParams{ServiceID: service.ID}, &credential)
	return credential, err
}

type RemoteEnvironment struct {
	conn *Conn
}

func NewRemoteEnvironment(conn *Conn) *RemoteEnvironment {
	return &RemoteEnvironment{conn: conn}
}

func (resolver *RemoteEnvironment) Resolve(ctx context.Context, service state.ServiceDesired, environment deployment.EnvironmentContext) (map[string]string, error) {
	var values map[string]string
	err := resolver.conn.Call(ctx, hostconn.RPCResolveEnvironment, hostconn.ResolveEnvironmentParams{
		ServiceID: service.ID, Context: environment,
	}, &values)
	return values, err
}

func ServiceDomains(ctx context.Context, conn *Conn, projectID, serviceID string) ([]state.ServiceDomain, error) {
	var domains []state.ServiceDomain
	err := conn.Call(ctx, hostconn.RPCServiceDomains, hostconn.ServiceDomainsParams{ProjectID: projectID, ServiceID: serviceID}, &domains)
	return domains, err
}

func ServiceListeners(ctx context.Context, conn *Conn, projectID, serviceID string) ([]state.ServiceListener, error) {
	var listeners []state.ServiceListener
	err := conn.Call(ctx, hostconn.RPCServiceListeners, hostconn.ServiceDomainsParams{ProjectID: projectID, ServiceID: serviceID}, &listeners)
	return listeners, err
}

func LookupInternal(ctx context.Context, conn *Conn, hostname string) (state.InternalName, error) {
	var result state.InternalName
	err := conn.Call(ctx, hostconn.RPCLookupInternal, hostconn.LookupInternalParams{Hostname: hostname}, &result)
	return result, err
}

type TunnelOptions struct {
	ParentURL  string
	HostToken  string
	DialLocal  hosttunnel.DialLocal
	HTTPClient *http.Client
}

func DialTunnel(ctx context.Context, parentURL, hostToken string, dial hosttunnel.DialLocal) (*hosttunnel.Peer, error) {
	return DialTunnelWithOptions(ctx, TunnelOptions{
		ParentURL: parentURL, HostToken: hostToken, DialLocal: dial,
	})
}

func DialTunnelWithOptions(ctx context.Context, options TunnelOptions) (*hosttunnel.Peer, error) {
	tunnelURL, err := parentWebSocketURL(options.ParentURL, hostconn.TunnelPath)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+options.HostToken)
	connection, _, err := websocket.Dial(ctx, tunnelURL, &websocket.DialOptions{
		HTTPHeader:   header,
		Subprotocols: []string{hostconn.WebSocketTunnelProtocol},
		HTTPClient:   options.HTTPClient,
	})
	if err != nil {
		return nil, fmt.Errorf("connect parent tunnel: %w", err)
	}
	return hosttunnel.NewPeer(hosttunnel.WrapWebsocket(connection), options.DialLocal), nil
}
