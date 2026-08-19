package hosttunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

type pipeConn struct {
	incoming <-chan []byte
	outgoing chan<- []byte
	done     <-chan struct{}
}

func (conn pipeConn) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-conn.done:
		return nil, errPeerClosed
	case payload := <-conn.incoming:
		return payload, nil
	}
}

func (conn pipeConn) Write(_ context.Context, payload []byte) error {
	select {
	case <-conn.done:
		return errPeerClosed
	case conn.outgoing <- append([]byte(nil), payload...):
		return nil
	}
}

func (conn pipeConn) Close() error { return nil }

func TestPeerPipesBytesThroughLocalDial(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()

	leftFrames := make(chan []byte, 8)
	rightFrames := make(chan []byte, 8)
	done := make(chan struct{})
	defer close(done)
	client := NewPeer(pipeConn{incoming: rightFrames, outgoing: leftFrames, done: done}, nil)
	server := NewServerPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "db.shop.internal" || port != 5432 {
			t.Fatalf("dialed %s:%d", hostname, port)
		}
		return net.Dial("tcp", listener.Addr().String())
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	go func() { _ = client.Serve(ctx) }()

	dialCtx, dialCancel := context.WithTimeout(ctx, time.Second)
	defer dialCancel()
	conn, err := client.Dial(dialCtx, "db.shop.internal", 5432)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	backend := <-accepted
	defer backend.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := backend.Read(buffer); err != nil || string(buffer) != "ping" {
		t.Fatalf("backend read = %q %v", buffer, err)
	}
	if _, err := backend.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(buffer); err != nil || string(buffer) != "pong" {
		t.Fatalf("client read = %q %v", buffer, err)
	}
}

func TestPeerDialKeepsStreamAfterContextCancel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()

	leftFrames := make(chan []byte, 8)
	rightFrames := make(chan []byte, 8)
	done := make(chan struct{})
	defer close(done)
	client := NewPeer(pipeConn{incoming: rightFrames, outgoing: leftFrames, done: done}, nil)
	server := NewServerPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "web.shop.internal" || port != 8080 {
			t.Fatalf("dialed %s:%d", hostname, port)
		}
		return net.Dial("tcp", listener.Addr().String())
	})
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	go func() { _ = server.Serve(serveCtx) }()
	go func() { _ = client.Serve(serveCtx) }()

	dialCtx, cancelDial := context.WithCancel(context.Background())
	conn, err := client.Dial(dialCtx, "web.shop.internal", 8080)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cancelDial()
	backend := <-accepted
	defer backend.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := backend.Read(buffer); err != nil || string(buffer) != "ping" {
		t.Fatalf("backend read = %q %v", buffer, err)
	}
	if _, err := backend.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(buffer); err != nil || string(buffer) != "pong" {
		t.Fatalf("client read = %q %v", buffer, err)
	}
}

func TestKeepaliveFrameIsIgnored(t *testing.T) {
	t.Parallel()
	peer := NewPeer(pipeConn{}, nil)
	frame := make([]byte, headerSize)
	frame[0] = frameKeepalive
	if err := peer.handle(context.Background(), frame); err != nil {
		t.Fatal(err)
	}
}

func TestPeerKeepaliveDoesNotBreakDial(t *testing.T) {
	previous := keepaliveInterval
	keepaliveInterval = 15 * time.Millisecond
	t.Cleanup(func() { keepaliveInterval = previous })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()

	leftFrames := make(chan []byte, 64)
	rightFrames := make(chan []byte, 64)
	done := make(chan struct{})
	defer close(done)
	client := NewPeer(pipeConn{incoming: rightFrames, outgoing: leftFrames, done: done}, nil)
	server := NewServerPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "db.shop.internal" || port != 5432 {
			t.Fatalf("dialed %s:%d", hostname, port)
		}
		return net.Dial("tcp", listener.Addr().String())
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	go func() { _ = client.Serve(ctx) }()
	time.Sleep(50 * time.Millisecond)

	dialCtx, dialCancel := context.WithTimeout(ctx, time.Second)
	defer dialCancel()
	conn, err := client.Dial(dialCtx, "db.shop.internal", 5432)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	backend := <-accepted
	defer backend.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 4)
	if _, err := backend.Read(buffer); err != nil || string(buffer) != "ping" {
		t.Fatalf("backend read = %q %v", buffer, err)
	}
	if _, err := backend.Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(buffer); err != nil || string(buffer) != "pong" {
		t.Fatalf("client read = %q %v", buffer, err)
	}
}

func TestPeerBidirectionalDialUsesSeparateIDs(t *testing.T) {
	leftListener, leftAccepted := startPeerListener(t)
	rightListener, rightAccepted := startPeerListener(t)
	leftPort := uint16(leftListener.Addr().(*net.TCPAddr).Port)
	rightPort := uint16(rightListener.Addr().(*net.TCPAddr).Port)

	leftFrames := make(chan []byte, 16)
	rightFrames := make(chan []byte, 16)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	client := NewPeer(pipeConn{incoming: rightFrames, outgoing: leftFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "server.shop.internal" || port != leftPort {
			return nil, fmt.Errorf("client local dial %s:%d", hostname, port)
		}
		return net.Dial("tcp", leftListener.Addr().String())
	})
	server := NewServerPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
		if hostname != "client.shop.internal" || port != rightPort {
			return nil, fmt.Errorf("server local dial %s:%d", hostname, port)
		}
		return net.Dial("tcp", rightListener.Addr().String())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	go func() { _ = client.Serve(ctx) }()
	go func() { _ = server.Serve(ctx) }()

	type result struct {
		conn net.Conn
		err  error
	}
	clientC := make(chan result, 1)
	serverC := make(chan result, 1)
	go func() {
		conn, err := client.Dial(ctx, "client.shop.internal", rightPort)
		clientC <- result{conn, err}
	}()
	go func() {
		conn, err := server.Dial(ctx, "server.shop.internal", leftPort)
		serverC <- result{conn, err}
	}()
	clientDial := <-clientC
	serverDial := <-serverC
	if clientDial.err != nil {
		t.Fatalf("client Dial: %v", clientDial.err)
	}
	if serverDial.err != nil {
		t.Fatalf("server Dial: %v", serverDial.err)
	}
	t.Cleanup(func() {
		_ = clientDial.conn.Close()
		_ = serverDial.conn.Close()
	})

	leftBackend := <-leftAccepted
	rightBackend := <-rightAccepted
	t.Cleanup(func() {
		_ = leftBackend.Close()
		_ = rightBackend.Close()
	})
	if _, err := clientDial.conn.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := serverDial.conn.Write([]byte("cd")); err != nil {
		t.Fatal(err)
	}
	leftBuf := make([]byte, 2)
	rightBuf := make([]byte, 2)
	if _, err := io.ReadFull(rightBackend, rightBuf); err != nil || string(rightBuf) != "ab" {
		t.Fatalf("server local read = %q %v", rightBuf, err)
	}
	if _, err := io.ReadFull(leftBackend, leftBuf); err != nil || string(leftBuf) != "cd" {
		t.Fatalf("client local read = %q %v", leftBuf, err)
	}
}

func startPeerListener(t *testing.T) (net.Listener, chan net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn
	}()
	return listener, accepted
}
