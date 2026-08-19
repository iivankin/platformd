package hosttunnel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	frameOpen      byte = 1
	frameAck       byte = 2
	frameData      byte = 3
	frameClose     byte = 4
	frameError     byte = 5
	frameKeepalive byte = 6

	maxFrameBytes     = 64 << 10
	maxStreams        = 256
	headerSize        = 5
	dialTimeout       = 10 * time.Second
	streamIdleTimeout = 2 * time.Minute
)

const keepaliveWriteTimeout = 10 * time.Second

var keepaliveInterval = 20 * time.Second

var errPeerClosed = errors.New("internal tunnel is closed")

type MessageConn interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
	Close() error
}

type DialLocal func(ctx context.Context, hostname string, port uint16) (net.Conn, error)

type Peer struct {
	conn    MessageConn
	dial    DialLocal
	mu      sync.Mutex
	nextID  uint32
	streams map[uint32]*stream
	closed  atomic.Bool
	writeMu sync.Mutex
}

type openPayload struct {
	Hostname string `json:"hostname"`
	Port     uint16 `json:"port"`
}

type stream struct {
	id       uint32
	pipe     net.Conn
	ready    chan error
	incoming chan []byte
	done     chan struct{}
	once     sync.Once
}

func NewPeer(conn MessageConn, dial DialLocal) *Peer {
	if dial == nil {
		dial = func(context.Context, string, uint16) (net.Conn, error) {
			return nil, errors.New("local .internal dialer is not configured")
		}
	}
	return &Peer{conn: conn, dial: dial, streams: map[uint32]*stream{}}
}

func (peer *Peer) Close() error {
	if peer.closed.Swap(true) {
		return nil
	}
	peer.mu.Lock()
	streams := make([]*stream, 0, len(peer.streams))
	for _, current := range peer.streams {
		streams = append(streams, current)
	}
	peer.streams = map[uint32]*stream{}
	peer.mu.Unlock()
	for _, current := range streams {
		current.shutdown()
	}
	return peer.conn.Close()
}

func (peer *Peer) Serve(ctx context.Context) error {
	defer peer.Close()
	go peer.keepalive(ctx)
	for {
		frame, err := peer.conn.Read(ctx)
		if err != nil {
			if peer.closed.Load() || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if err := peer.handle(ctx, frame); err != nil {
			return err
		}
	}
}

func (peer *Peer) Dial(ctx context.Context, hostname string, port uint16) (net.Conn, error) {
	if peer.closed.Load() {
		return nil, errPeerClosed
	}
	caller, endpoint := net.Pipe()
	current, err := peer.allocate(0, endpoint)
	if err != nil {
		_ = caller.Close()
		_ = endpoint.Close()
		return nil, err
	}
	body, err := json.Marshal(openPayload{Hostname: hostname, Port: port})
	if err != nil {
		peer.drop(current.id)
		_ = caller.Close()
		return nil, err
	}
	if err := peer.writeFrame(ctx, frameOpen, current.id, body); err != nil {
		peer.drop(current.id)
		_ = caller.Close()
		return nil, err
	}
	select {
	case <-ctx.Done():
		peer.drop(current.id)
		_ = caller.Close()
		return nil, ctx.Err()
	case err := <-current.ready:
		if err != nil {
			_ = caller.Close()
			return nil, err
		}
	}
	// The handshake ctx must not own the stream: acceptOpen cancels its
	// dial timeout as soon as Dial returns, and child-to-child relay
	// nests Dial inside that call.
	go peer.pump(context.Background(), current)
	return caller, nil
}

func (peer *Peer) handle(ctx context.Context, frame []byte) error {
	kind, id, payload, err := decodeFrame(frame)
	if err != nil {
		return err
	}
	switch kind {
	case frameOpen:
		return peer.acceptOpen(ctx, id, payload)
	case frameAck:
		if current := peer.stream(id); current != nil {
			current.signal(nil)
		}
	case frameError:
		if current := peer.stream(id); current != nil {
			message := string(payload)
			if message == "" {
				message = "internal tunnel open failed"
			}
			current.signal(errors.New(message))
			peer.drop(id)
		}
	case frameData:
		if current := peer.stream(id); current != nil {
			select {
			case current.incoming <- append([]byte(nil), payload...):
			case <-current.done:
			}
		}
	case frameClose:
		peer.drop(id)
	case frameKeepalive:
		return nil
	default:
		return fmt.Errorf("unknown internal tunnel frame %d", kind)
	}
	return nil
}

func (peer *Peer) acceptOpen(ctx context.Context, id uint32, payload []byte) error {
	var request openPayload
	if err := json.Unmarshal(payload, &request); err != nil || request.Hostname == "" || request.Port == 0 {
		_ = peer.writeFrame(ctx, frameError, id, []byte("tunnel open is invalid"))
		return nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	local, err := peer.dial(dialCtx, request.Hostname, request.Port)
	cancel()
	if err != nil {
		_ = peer.writeFrame(ctx, frameError, id, []byte(err.Error()))
		return nil
	}
	current, err := peer.allocate(id, local)
	if err != nil {
		_ = local.Close()
		_ = peer.writeFrame(ctx, frameError, id, []byte(err.Error()))
		return nil
	}
	if err := peer.writeFrame(ctx, frameAck, id, nil); err != nil {
		peer.drop(id)
		return err
	}
	go peer.pump(ctx, current)
	return nil
}

func (peer *Peer) pump(ctx context.Context, current *stream) {
	defer func() {
		_ = peer.writeFrame(context.Background(), frameClose, current.id, nil)
		peer.drop(current.id)
	}()
	go func() {
		for {
			select {
			case <-current.done:
				return
			case <-ctx.Done():
				return
			case payload, ok := <-current.incoming:
				if !ok {
					return
				}
				if _, err := current.pipe.Write(payload); err != nil {
					return
				}
			}
		}
	}()
	buffer := make([]byte, 32<<10)
	for {
		if err := current.pipe.SetReadDeadline(time.Now().Add(streamIdleTimeout)); err != nil {
			return
		}
		n, err := current.pipe.Read(buffer)
		if n > 0 {
			if writeErr := peer.writeFrame(ctx, frameData, current.id, buffer[:n]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (peer *Peer) allocate(id uint32, pipe net.Conn) (*stream, error) {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	if len(peer.streams) >= maxStreams {
		return nil, errors.New("internal tunnel has too many connections")
	}
	if id == 0 {
		for {
			peer.nextID++
			if peer.nextID == 0 {
				continue
			}
			if _, exists := peer.streams[peer.nextID]; !exists {
				id = peer.nextID
				break
			}
		}
	} else if _, exists := peer.streams[id]; exists {
		return nil, fmt.Errorf("internal tunnel id %d is already used", id)
	}
	current := &stream{
		id: id, pipe: pipe, ready: make(chan error, 1),
		incoming: make(chan []byte, 16), done: make(chan struct{}),
	}
	peer.streams[id] = current
	return current, nil
}

func (peer *Peer) stream(id uint32) *stream {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return peer.streams[id]
}

func (peer *Peer) drop(id uint32) {
	peer.mu.Lock()
	current := peer.streams[id]
	delete(peer.streams, id)
	peer.mu.Unlock()
	if current != nil {
		current.shutdown()
	}
}

func (peer *Peer) keepalive(ctx context.Context) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()
	for {
		writeCtx, cancel := context.WithTimeout(ctx, keepaliveWriteTimeout)
		err := peer.writeFrame(writeCtx, frameKeepalive, 0, nil)
		cancel()
		if err != nil {
			_ = peer.Close()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (peer *Peer) writeFrame(ctx context.Context, kind byte, id uint32, payload []byte) error {
	if len(payload) > maxFrameBytes-headerSize {
		return errors.New("internal tunnel frame is too large")
	}
	frame := make([]byte, headerSize+len(payload))
	frame[0] = kind
	binary.BigEndian.PutUint32(frame[1:5], id)
	copy(frame[5:], payload)
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	if peer.closed.Load() {
		return errPeerClosed
	}
	return peer.conn.Write(ctx, frame)
}

func decodeFrame(frame []byte) (byte, uint32, []byte, error) {
	if len(frame) < headerSize || len(frame) > maxFrameBytes {
		return 0, 0, nil, errors.New("internal tunnel frame is invalid")
	}
	return frame[0], binary.BigEndian.Uint32(frame[1:5]), frame[5:], nil
}

func (current *stream) signal(err error) {
	select {
	case current.ready <- err:
	default:
	}
}

func (current *stream) shutdown() {
	current.once.Do(func() {
		close(current.done)
		if current.pipe != nil {
			_ = current.pipe.Close()
		}
	})
}

var _ io.Closer = (*Peer)(nil)
