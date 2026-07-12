package sdnotify_test

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/iivankin/platformd/internal/sdnotify"
)

func TestReadySendsDatagramAndSanitizesStatus(t *testing.T) {
	temporary, err := os.CreateTemp("/tmp", "pd-notify-")
	if err != nil {
		t.Fatal(err)
	}
	path := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path) })
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("NOTIFY_SOCKET", path)
	if err := sdnotify.Ready("admin\nready"); err != nil {
		t.Fatal(err)
	}
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	count, _, err := listener.ReadFromUnix(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buffer[:count]); got != "READY=1\nSTATUS=admin ready" {
		t.Fatalf("notification = %q", got)
	}
}
