package terminaltransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/iivankin/platformd/internal/access"
)

const (
	maximumInputMessage = 64 << 10
	writeTimeout        = 10 * time.Second
)

type Size struct {
	Cols uint16
	Rows uint16
}

type Session interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(Size) error
	Wait() (int, error)
	Close(reason string) error
}

type OpenRequest struct {
	HTTP     *http.Request
	Identity access.Identity
}

type Open func(context.Context, OpenRequest, Size) (Session, error)
type Admission func(*http.Request) (release func(), err error)

type Handler struct {
	hostname string
	open     Open
	idle     time.Duration
	lifetime time.Duration
	admit    Admission
}

func (handler *Handler) SetAdmission(admit Admission) {
	handler.admit = admit
}

func New(hostname string, open Open, idle, lifetime time.Duration) (*Handler, error) {
	if hostname == "" || open == nil {
		return nil, errors.New("terminal transport hostname and opener are required")
	}
	if idle < 0 || lifetime < 0 {
		return nil, errors.New("terminal transport timeouts cannot be negative")
	}
	return &Handler{hostname: hostname, open: open, idle: idle, lifetime: lifetime}, nil
}

func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Origin") != "https://"+handler.hostname {
		http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	identity, ok := access.IdentityFromContext(request.Context())
	if !ok {
		http.Error(response, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	size, err := initialSize(request)
	if err != nil {
		http.Error(response, err.Error(), http.StatusBadRequest)
		return
	}
	var release func()
	if handler.admit != nil {
		release, err = handler.admit(request)
		if err != nil {
			http.Error(response, "platform update is in progress", http.StatusConflict)
			return
		}
		defer release()
	}

	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	// An upgraded request context is not a reliable lifetime boundary. The
	// socket reader owns cancellation and Close must terminate the PTY/process.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer connection.CloseNow()
	connection.SetReadLimit(maximumInputMessage)
	session, err := handler.open(ctx, OpenRequest{HTTP: request.Clone(ctx), Identity: identity}, size)
	if err != nil {
		_ = connection.Close(websocket.StatusTryAgainLater, "terminal unavailable")
		return
	}
	closeReason := "transport_closed"
	defer func() { _ = session.Close(closeReason) }()
	activity := make(chan struct{}, 1)
	type completion struct {
		source string
		err    error
	}
	done := make(chan completion, 3)
	go func() { done <- completion{source: "client", err: readClient(ctx, connection, session, activity)} }()
	go func() { done <- completion{source: "output", err: writeOutput(ctx, connection, session, activity)} }()
	go func() {
		_, waitErr := session.Wait()
		done <- completion{source: "process", err: waitErr}
	}()

	var idleTimer *time.Timer
	var idleC <-chan time.Time
	if handler.idle > 0 {
		idleTimer = time.NewTimer(handler.idle)
		idleC = idleTimer.C
		defer idleTimer.Stop()
	}
	var lifetimeTimer *time.Timer
	var lifetimeC <-chan time.Time
	if handler.lifetime > 0 {
		lifetimeTimer = time.NewTimer(handler.lifetime)
		lifetimeC = lifetimeTimer.C
		defer lifetimeTimer.Stop()
	}

	for {
		select {
		case <-activity:
			resetTimer(idleTimer, handler.idle)
		case <-idleC:
			closeReason = "idle_timeout"
			_ = connection.Close(websocket.StatusNormalClosure, "idle timeout")
			return
		case <-lifetimeC:
			closeReason = "absolute_lifetime"
			_ = connection.Close(websocket.StatusNormalClosure, "session lifetime reached")
			return
		case completed := <-done:
			closeReason = completed.source + "_closed"
			if completed.err != nil && !normalWebSocketClose(completed.err) {
				closeReason = completed.source + "_error"
			}
			return
		}
	}
}

func normalWebSocketClose(err error) bool {
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}

func initialSize(request *http.Request) (Size, error) {
	cols, err := boundedDimension(request.URL.Query().Get("cols"), "cols", 1000)
	if err != nil {
		return Size{}, err
	}
	rows, err := boundedDimension(request.URL.Query().Get("rows"), "rows", 500)
	if err != nil {
		return Size{}, err
	}
	return Size{Cols: cols, Rows: rows}, nil
}

func boundedDimension(value, name string, maximum uint16) (uint16, error) {
	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed < 1 || parsed > uint64(maximum) {
		return 0, fmt.Errorf("%s must be between 1 and %d", name, maximum)
	}
	return uint16(parsed), nil
}

func readClient(ctx context.Context, connection *websocket.Conn, session Session, activity chan<- struct{}) error {
	for {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			return err
		}
		switch messageType {
		case websocket.MessageBinary:
			if len(payload) == 0 {
				continue
			}
			if err := writeAll(session, payload); err != nil {
				return err
			}
			notifyActivity(activity)
		case websocket.MessageText:
			size, err := decodeResize(payload)
			if err != nil {
				_ = connection.Close(websocket.StatusPolicyViolation, "invalid control message")
				return err
			}
			if err := session.Resize(size); err != nil {
				return err
			}
			notifyActivity(activity)
		default:
			return errors.New("unsupported websocket message type")
		}
	}
}

func decodeResize(payload []byte) (Size, error) {
	var control struct {
		Type string `json:"type"`
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&control); err != nil {
		return Size{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Size{}, errors.New("control message contains trailing data")
	}
	if control.Type != "resize" || control.Cols < 1 || control.Cols > 1000 || control.Rows < 1 || control.Rows > 500 {
		return Size{}, errors.New("invalid terminal resize")
	}
	return Size{Cols: control.Cols, Rows: control.Rows}, nil
}

func writeOutput(ctx context.Context, connection *websocket.Conn, session Session, activity chan<- struct{}) error {
	buffer := make([]byte, 32<<10)
	for {
		count, err := session.Read(buffer)
		if count > 0 {
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			writeErr := connection.Write(writeCtx, websocket.MessageBinary, buffer[:count])
			cancel()
			if writeErr != nil {
				return writeErr
			}
			notifyActivity(activity)
		}
		if err != nil {
			return err
		}
	}
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		count, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		payload = payload[count:]
	}
	return nil
}

func notifyActivity(activity chan<- struct{}) {
	select {
	case activity <- struct{}{}:
	default:
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(duration)
}
