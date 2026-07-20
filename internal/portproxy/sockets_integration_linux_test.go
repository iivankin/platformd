//go:build linux && integration

package portproxy

import (
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestNamespaceSocketsStayInsideTargetNamespace(t *testing.T) {
	if os.Getenv("PLATFORMD_PORTPROXY_NAMESPACE_INTEGRATION") != "1" {
		t.Skip("set PLATFORMD_PORTPROXY_NAMESPACE_INTEGRATION=1 to run privileged namespace checks")
	}
	if os.Geteuid() != 0 {
		t.Fatal("namespace integration test requires root")
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Fatal(err)
	}

	namespace := exec.Command("unshare", "--net", "sh", "-ceu", "ip link set lo up; exec sleep 30")
	if err := namespace.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = namespace.Process.Kill()
		_ = namespace.Wait()
	})
	waitForDistinctNetworkNamespace(t, namespace.Process.Pid)

	listener, err := listenTCPInNamespace(namespace.Process.Pid, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			accepted <- acceptErr
			return
		}
		defer connection.Close()
		_, acceptErr = io.Copy(connection, connection)
		accepted <- acceptErr
	}()

	connection, err := dialTCPInNamespace(
		namespace.Process.Pid,
		net.Dialer{Timeout: 2 * time.Second},
		listener.Addr().String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write([]byte("mesh")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(connection, buffer); err != nil || string(buffer) != "mesh" {
		t.Fatalf("namespace echo = %q, %v", buffer, err)
	}
	_ = connection.Close()
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}

func waitForDistinctNetworkNamespace(t *testing.T, pid int) {
	t.Helper()
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	targetPath := "/proc/" + strconv.Itoa(pid) + "/ns/net"
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		target, readErr := os.Readlink(targetPath)
		if readErr == nil && target != current {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d did not enter a distinct network namespace", pid)
}
