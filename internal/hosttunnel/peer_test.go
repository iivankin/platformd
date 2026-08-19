package hosttunnel

import (
	"context"
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
	server := NewPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
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
	server := NewPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
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
	server := NewPeer(pipeConn{incoming: leftFrames, outgoing: rightFrames, done: done}, func(_ context.Context, hostname string, port uint16) (net.Conn, error) {
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
