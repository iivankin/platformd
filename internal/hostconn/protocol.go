package hostconn

import (
	"encoding/json"

	"github.com/iivankin/platformd/internal/deployment"
	"github.com/iivankin/platformd/internal/state"
)

const WebSocketProtocol = "platformd-host-v1"

const (
	KindHello        = "hello"
	KindWelcome      = "welcome"
	KindReconcile    = "reconcile"
	KindWithdraw     = "withdraw"
	KindSyncPublic   = "sync-public"
	KindRPC          = "rpc"
	KindRPCResult    = "rpc.result"
	KindOTLP         = "otlp"
	KindStatus       = "status"
	KindCertificates = "certificates"
	KindProjects     = "projects"
	KindPurgeCache   = "purge-cache"
	KindPurgeResult  = "purge-cache.result"
)

type Envelope struct {
	ID      string          `json:"id,omitempty"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type Hello struct {
	PublicIPv4 string `json:"publicIpv4"`
}

type CertificatePEM struct {
	ID             string `json:"id"`
	CertificatePEM string `json:"certificatePem"`
	PrivateKeyPEM  string `json:"privateKeyPem"`
}

const (
	JoinPath        = "/public/api/v1/hosts/join"
	ConnectPath     = "/public/api/v1/hosts/connect"
	TunnelPath      = "/public/api/v1/hosts/tunnel"
	ImagePathPrefix = "/public/api/v1/hosts/images/"
)

const WebSocketTunnelProtocol = "platformd-host-tunnel-v1"

type Welcome struct {
	HostID             string                 `json:"hostId"`
	ParentHostname     string                 `json:"parentHostname"`
	Certificates       []CertificatePEM       `json:"certificates"`
	Projects           []state.RuntimeProject `json:"projects"`
	AssignedServiceIDs []string               `json:"assignedServiceIds"`
}

type Reconcile struct {
	ServiceID string `json:"serviceId"`
	Force     bool   `json:"force,omitempty"`
}

type Withdraw struct {
	ServiceID string `json:"serviceId"`
}

type SyncPublic struct {
	ServiceID string `json:"serviceId"`
}

type RPCRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type OTLPBatch struct {
	Path        string `json:"path"`
	ContentType string `json:"contentType"`
	Body        []byte `json:"body"`
}

type Status struct {
	PublicIPv4    string           `json:"publicIpv4"`
	CPUMillicores int64            `json:"cpuMillicores,omitempty"`
	MemoryBytes   int64            `json:"memoryBytes,omitempty"`
	DiskFreeBytes int64            `json:"diskFreeBytes,omitempty"`
	Services      []ServiceRuntime `json:"services"`
}

type ServiceRuntime struct {
	ServiceID string `json:"serviceId"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

type PurgeCache struct {
	Hostnames []string `json:"hostnames"`
}

const (
	RPCDesiredService                   = "DesiredService"
	RPCBeginDeployment                  = "BeginDeployment"
	RPCDiscardDeployment                = "DiscardDeployment"
	RPCUpdateDeploymentSource           = "UpdateDeploymentSource"
	RPCFinishDeployment                 = "FinishDeployment"
	RPCActivateDeployment               = "ActivateDeployment"
	RPCFailDeployment                   = "FailDeployment"
	RPCLatestFailedDeployment           = "LatestFailedDeployment"
	RPCLatestUploadedDeployment         = "LatestUploadedDeployment"
	RPCLatestReusableProductionRevision = "LatestReusableProductionRevision"
	RPCDeployment                       = "Deployment"
	RPCVolumeInitialized                = "VolumeInitialized"
	RPCRecordVolumeInitialization       = "RecordVolumeInitialization"
	RPCResolveEnvironment               = "ResolveEnvironment"
	RPCResolveImageCredential           = "ResolveImageCredential"
	RPCServiceDomains                   = "ServiceDomains"
	RPCServiceListeners                 = "ServiceListeners"
	RPCLookupInternal                   = "LookupInternal"
)

type ServiceIDParams struct {
	ServiceID string `json:"serviceId"`
}

type BeginDeploymentParams struct {
	Input state.BeginDeployment `json:"input"`
}

type DiscardDeploymentParams struct {
	DeploymentID string `json:"deploymentId"`
}

type UpdateDeploymentSourceParams struct {
	DeploymentID   string `json:"deploymentId"`
	ImageDigest    string `json:"imageDigest"`
	ImageReference string `json:"imageReference"`
	SourceRevision string `json:"sourceRevision"`
	CommitMessage  string `json:"commitMessage"`
}

type FinishDeploymentParams struct {
	DeploymentID string `json:"deploymentId"`
	Status       string `json:"status"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	FinishedAt   int64  `json:"finishedAt"`
}

type ActivateDeploymentParams struct {
	ServiceID                  string `json:"serviceId"`
	DeploymentID               string `json:"deploymentId"`
	ExpectedActiveDeploymentID string `json:"expectedActiveDeploymentId"`
	ActivatedAt                int64  `json:"activatedAt"`
}

type FailDeploymentParams struct {
	DeploymentID string `json:"deploymentId"`
	ErrorCode    string `json:"errorCode"`
	ErrorMessage string `json:"errorMessage"`
	FailedAt     int64  `json:"failedAt"`
}

type LatestFailedDeploymentParams struct {
	ServiceID  string `json:"serviceId"`
	ConfigHash string `json:"configHash"`
	ImageRef   string `json:"imageRef"`
}

type VolumeInitializedParams struct {
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
	VolumeID  string `json:"volumeId"`
}

type RecordVolumeInitializationParams struct {
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
	VolumeID  string `json:"volumeId"`
	At        int64  `json:"at"`
}

type ResolveEnvironmentParams struct {
	ServiceID string                        `json:"serviceId"`
	Context   deployment.EnvironmentContext `json:"context"`
}

type ServiceDomainsParams struct {
	ProjectID string `json:"projectId"`
	ServiceID string `json:"serviceId"`
}

type LookupInternalParams struct {
	Hostname string `json:"hostname"`
}
