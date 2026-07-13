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

type Image struct {
	ID      string
	Digest  string
	Names   []string
	Size    int64
	Created time.Time
}

type NetworkSpec struct {
	Name      string
	Interface string
	Subnet    string
	Gateway   string
	Labels    map[string]string
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

type ContainerSpec struct {
	ImageID        string
	Name           string
	Entrypoint     []string
	Command        []string
	Environment    map[string]string
	Labels         map[string]string
	Network        string
	DNSServers     []string
	DNSSearch      []string
	Mounts         []Mount
	LogPath        string
	LogSizeBytes   int64
	LogMaxFiles    uint
	CgroupParent   string
	CPUMillicores  int64
	MemoryMaxBytes int64
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
