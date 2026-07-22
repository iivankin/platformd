package containerengine

import (
	"io"
	"time"
)

type PullRequest struct {
	Reference string
	Username  string
	Password  string
	Refresh   bool
}

type BuildRequest struct {
	ContextDirectory string
	Dockerfile       string
	Reference        string
	Network          string
	Timeout          time.Duration
	Log              io.Writer
}

type Image struct {
	ID           string
	Digest       string
	Names        []string
	User         string
	Architecture string
	OS           string
	Labels       map[string]string
	Entrypoint   []string
	Command      []string
	Size         int64
	Created      time.Time
}

type ImageGarbageCollectRequest struct {
	Before           time.Time
	ProtectedDigests map[string]struct{}
}

type ImageGarbageCollectResult struct {
	Removed      int
	RemovedBytes int64
	Skipped      int
}

type DerivedImageRequest struct {
	ContainerID string
	BaseImageID string
	Reference   string
	Labels      map[string]string
}

type NetworkSpec struct {
	Name       string
	Interface  string
	Subnet     string
	Gateway    string
	LeaseStart string
	LeaseEnd   string
	Labels     map[string]string
}

type Network struct {
	ID        string
	Name      string
	Interface string
	Subnet    string
	Gateway   string
}

type Mount struct {
	Source      string
	Destination string
	ReadOnly    bool
}

// ManagedVolumeMount presents platform-owned durable storage to libpod as a
// named volume. Libpod supplies Docker-compatible first-mount copy-up and
// ownership behavior for both service and managed-database volumes.
type ManagedVolumeMount struct {
	ID          string
	Source      string
	Destination string
	ReadOnly    bool
	Initialized bool
}

type ContainerSecurityProfile string

const (
	ContainerSecurityDefault        ContainerSecurityProfile = ""
	ContainerSecurityCloudflareMesh ContainerSecurityProfile = "cloudflare_mesh"
)

type ContainerSpec struct {
	ImageID         string
	Name            string
	Entrypoint      []string
	Command         []string
	Environment     map[string]string
	Labels          map[string]string
	Network         string
	DNSServers      []string
	DNSSearch       []string
	Mounts          []Mount
	ManagedVolumes  []ManagedVolumeMount
	LogPath         string
	LogSizeBytes    int64
	LogMaxFiles     uint
	CgroupParent    string
	CPUMillicores   int64
	MemoryMaxBytes  int64
	SecurityProfile ContainerSecurityProfile
}

type Container struct {
	ID        string
	Name      string
	State     string
	Pid       int
	ConmonPID int
	ExitCode  int32
	IPs       map[string][]string
}

type NetworkCounters struct {
	RXBytes uint64
	TXBytes uint64
}

type ListeningPort struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type ExecRequest struct {
	Command     []string
	Environment map[string]string
	User        string
	WorkDir     string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

type TerminalSize struct {
	Cols uint16
	Rows uint16
}

type TerminalExecRequest struct {
	Command     []string
	Environment map[string]string
	User        string
	WorkDir     string
	Stdin       io.Reader
	Output      io.Writer
	InitialSize TerminalSize
	Resizes     <-chan TerminalSize
}

type ContainerFileEntry struct {
	Path       string    `json:"path"`
	Directory  bool      `json:"directory"`
	SizeBytes  int64     `json:"sizeBytes"`
	Mode       uint32    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt"`
}
