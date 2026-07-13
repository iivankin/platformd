//go:build linux && amd64 && cgo && integration

package rootexec_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/cgrouptree"
	"github.com/iivankin/platformd/internal/rootexec"
)

func TestServerExecUsesDelegatedLeafAndKillsProcessTree(t *testing.T) {
	if os.Getenv("PLATFORMD_ROOTEXEC_INTEGRATION") != "1" {
		t.Skip("run inside a delegated systemd unit with PLATFORMD_ROOTEXEC_INTEGRATION=1")
	}
	tree, err := cgrouptree.Setup()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := rootexec.New(rootexec.Config{
		CreateLeaf:   func(identifier string) (rootexec.Leaf, error) { return tree.CreateOperationLeaf(identifier) },
		CommandBytes: 4096, OutputBytes: 8, DefaultTimeout: time.Second,
		MaximumTimeout: 2 * time.Second, MaximumParallel: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Execute(context.Background(), rootexec.Request{
		Command: "printf 123456789; printf error-message >&2; exit 7",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || result.Stdout != "12345678" || result.Stderr != "error-me" || !result.StdoutTruncated || !result.StderrTruncated || result.TimedOut {
		t.Fatalf("bounded result = %+v", result)
	}

	pidFile := "/tmp/platformd-rootexec-child.pid"
	_ = os.Remove(pidFile)
	result, err = runner.Execute(context.Background(), rootexec.Request{
		Command: "sleep 30 & echo $! > " + pidFile + "; wait", Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.ExitCode == 0 {
		t.Fatalf("timeout result = %+v", result)
	}
	value, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(pidFile)
	pid, err := strconv.Atoi(strings.TrimSpace(string(value)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background PID %d survived cgroup cancellation: %v", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
