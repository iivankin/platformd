//go:build linux

package portproxy

import (
	"errors"
	"fmt"
	"net"
	"runtime"

	"github.com/vishvananda/netns"
)

const maximumNamespaceSocketWorkers = 32

var namespaceSocketWorkers = make(chan struct{}, maximumNamespaceSocketWorkers)

type namespaceResult[T any] struct {
	value T
	err   error
}

func inNetworkNamespace[T any](pid int, operation func() (T, error)) (T, error) {
	if pid == 0 {
		return operation()
	}
	var zero T
	if pid < 0 {
		return zero, fmt.Errorf("invalid network namespace PID %d", pid)
	}
	namespaceSocketWorkers <- struct{}{}
	results := make(chan namespaceResult[T], 1)
	go func() {
		defer func() { <-namespaceSocketWorkers }()
		runtime.LockOSThread()
		current, err := netns.Get()
		if err != nil {
			runtime.UnlockOSThread()
			results <- namespaceResult[T]{err: fmt.Errorf("open platformd network namespace: %w", err)}
			return
		}
		target, err := netns.GetFromPid(pid)
		if err != nil {
			_ = current.Close()
			runtime.UnlockOSThread()
			results <- namespaceResult[T]{err: fmt.Errorf("open process %d network namespace: %w", pid, err)}
			return
		}
		if err := netns.Set(target); err != nil {
			_ = target.Close()
			_ = current.Close()
			runtime.UnlockOSThread()
			results <- namespaceResult[T]{err: fmt.Errorf("enter process %d network namespace: %w", pid, err)}
			return
		}
		value, operationErr := operation()
		restoreErr := netns.Set(current)
		_ = target.Close()
		_ = current.Close()
		if restoreErr == nil {
			runtime.UnlockOSThread()
		} // A thread that cannot be restored must die with this goroutine.
		if err := errors.Join(operationErr, restoreErr); err != nil {
			if restoreErr != nil {
				err = fmt.Errorf("network namespace operation failed; restore platformd namespace: %w", err)
			}
			results <- namespaceResult[T]{err: err}
			return
		}
		results <- namespaceResult[T]{value: value}
	}()
	result := <-results
	return result.value, result.err
}

func listenTCPInNamespace(pid int, address string) (net.Listener, error) {
	return inNetworkNamespace(pid, func() (net.Listener, error) {
		return net.Listen("tcp4", address)
	})
}

func listenUDPInNamespace(pid int, address *net.UDPAddr) (*net.UDPConn, error) {
	return inNetworkNamespace(pid, func() (*net.UDPConn, error) {
		return net.ListenUDP("udp4", address)
	})
}

func dialTCPInNamespace(pid int, dialer net.Dialer, address string) (net.Conn, error) {
	return inNetworkNamespace(pid, func() (net.Conn, error) {
		return dialer.Dial("tcp4", address)
	})
}

func dialUDPInNamespace(pid int, source, target *net.UDPAddr) (*net.UDPConn, error) {
	return inNetworkNamespace(pid, func() (*net.UDPConn, error) {
		return net.DialUDP("udp4", source, target)
	})
}
