package rootexec

import (
	"context"
	"io"
	"time"
)

type Leaf interface {
	FD() uintptr
	Kill() error
	Close(context.Context) error
}

type Config struct {
	CreateLeaf      func(string) (Leaf, error)
	Random          io.Reader
	Now             func() time.Time
	CommandBytes    int
	OutputBytes     int
	DefaultTimeout  time.Duration
	MaximumTimeout  time.Duration
	MaximumParallel int
}

type Request struct {
	Command string
	Timeout time.Duration
}

type Result struct {
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exitCode"`
	TimedOut        bool   `json:"timedOut"`
	Cancelled       bool   `json:"cancelled"`
	StdoutTruncated bool   `json:"stdoutTruncated"`
	StderrTruncated bool   `json:"stderrTruncated"`
	DurationMillis  int64  `json:"durationMillis"`
	StartedAt       int64  `json:"startedAt"`
	FinishedAt      int64  `json:"finishedAt"`
}
