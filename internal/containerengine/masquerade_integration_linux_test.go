//go:build linux && amd64 && cgo && integration

package containerengine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

const (
	masqueradeHostAddress = "198.18.0.1"
	masqueradePeerAddress = "198.18.0.2"
	masqueradePeerPort    = "18443"
)

type masqueradeProbe struct {
	address  string
	port     string
	listener *net.TCPListener
	result   <-chan error
	done     <-chan struct{}
}

// startMasqueradeProbe creates a deterministic external network namespace.
// It proves forwarding and source NAT without depending on Internet egress
// policy of the CI runner.
func startMasqueradeProbe(t *testing.T) masqueradeProbe {
	t.Helper()
	hostNamespace, peerNamespace := createProbeNamespaces(t)

	hostLink := &netlink.Veth{
		LinkAttrs:     netlink.LinkAttrs{Name: "pdit-ext0"},
		PeerName:      "pdit-ext1",
		PeerNamespace: netlink.NsFd(peerNamespace),
	}
	if err := netlink.LinkAdd(hostLink); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if link, err := netlink.LinkByName(hostLink.Name); err == nil {
			_ = netlink.LinkDel(link)
		}
	})
	configuredHostLink, err := netlink.LinkByName(hostLink.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := addProbeAddress(nil, configuredHostLink, masqueradeHostAddress); err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(configuredHostLink); err != nil {
		t.Fatal(err)
	}

	peerHandle, err := netlink.NewHandleAt(peerNamespace)
	if err != nil {
		t.Fatal(err)
	}
	defer peerHandle.Close()
	peerLink, err := peerHandle.LinkByName(hostLink.PeerName)
	if err != nil {
		t.Fatal(err)
	}
	if err := addProbeAddress(peerHandle, peerLink, masqueradePeerAddress); err != nil {
		t.Fatal(err)
	}
	if err := peerHandle.LinkSetUp(peerLink); err != nil {
		t.Fatal(err)
	}

	type readyResult struct {
		listener *net.TCPListener
		err      error
	}
	ready := make(chan readyResult, 1)
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		if err := netns.Set(peerNamespace); err != nil {
			runtime.UnlockOSThread()
			ready <- readyResult{err: err}
			close(done)
			return
		}
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP(masqueradePeerAddress), Port: 18443})
		ready <- readyResult{listener: listener, err: err}
		if err == nil {
			connection, acceptErr := listener.AcceptTCP()
			if acceptErr == nil {
				remote, ok := connection.RemoteAddr().(*net.TCPAddr)
				if !ok || !remote.IP.Equal(net.ParseIP(masqueradeHostAddress)) {
					acceptErr = fmt.Errorf("external peer observed source %s, want %s", connection.RemoteAddr(), masqueradeHostAddress)
				}
				acceptErr = errors.Join(acceptErr, connection.Close())
			}
			err = acceptErr
		}
		restoreErr := netns.Set(hostNamespace)
		runtime.UnlockOSThread()
		result <- errors.Join(err, restoreErr)
		close(done)
	}()
	started := <-ready
	if started.err != nil {
		t.Fatalf("listen in external namespace: %v", started.err)
	}
	probe := masqueradeProbe{
		address: masqueradePeerAddress, port: masqueradePeerPort,
		listener: started.listener, result: result, done: done,
	}
	t.Cleanup(func() {
		_ = probe.listener.Close()
		select {
		case <-probe.done:
		case <-time.After(5 * time.Second):
			t.Error("external namespace listener did not stop")
		}
	})
	return probe
}

func createProbeNamespaces(t *testing.T) (netns.NsHandle, netns.NsHandle) {
	t.Helper()
	runtime.LockOSThread()
	hostNamespace, err := netns.Get()
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	peerNamespace, err := netns.New()
	if err != nil {
		_ = hostNamespace.Close()
		runtime.UnlockOSThread()
		t.Fatal(err)
	}
	if err := netns.Set(hostNamespace); err != nil {
		_ = peerNamespace.Close()
		_ = hostNamespace.Close()
		// The goroutine exits while still locked, so the runtime discards the
		// thread instead of reusing it in the wrong network namespace.
		t.Fatalf("restore host network namespace: %v", err)
	}
	runtime.UnlockOSThread()
	t.Cleanup(func() {
		_ = peerNamespace.Close()
		_ = hostNamespace.Close()
	})
	return hostNamespace, peerNamespace
}

func addProbeAddress(handle *netlink.Handle, link netlink.Link, address string) error {
	parsed, err := netlink.ParseAddr(address + "/24")
	if err != nil {
		return err
	}
	if handle == nil {
		return netlink.AddrAdd(link, parsed)
	}
	return handle.AddrAdd(link, parsed)
}

func (probe masqueradeProbe) verify(ctx context.Context) error {
	select {
	case err := <-probe.result:
		return err
	case <-ctx.Done():
		_ = probe.listener.Close()
		return ctx.Err()
	}
}
