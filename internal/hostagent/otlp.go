package hostagent

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/iivankin/platformd/internal/hostconn"
)

type Forwarder struct {
	conn      *Conn
	server    *http.Server
	mu        sync.Mutex
	listeners []net.Listener
}

func NewForwarder(conn *Conn) *Forwarder {
	forwarder := &Forwarder{conn: conn}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/logs", forwarder.ingest)
	mux.HandleFunc("/v1/metrics", forwarder.ingest)
	mux.HandleFunc("/v1/traces", forwarder.ingest)
	forwarder.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	return forwarder
}

func (forwarder *Forwarder) Listen(ctx context.Context, addresses ...string) error {
	if err := forwarder.Add(addresses...); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = forwarder.Close()
	}()
	return nil
}

func (forwarder *Forwarder) Add(addresses ...string) error {
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return err
		}
		forwarder.mu.Lock()
		forwarder.listeners = append(forwarder.listeners, listener)
		forwarder.mu.Unlock()
		go func(listener net.Listener) { _ = forwarder.server.Serve(listener) }(listener)
	}
	return nil
}

func (forwarder *Forwarder) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return forwarder.server.Shutdown(ctx)
}

func (forwarder *Forwarder) ingest(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 4<<20))
	if err != nil {
		http.Error(response, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	if err := forwarder.conn.Write(request.Context(), hostconn.KindOTLP, hostconn.OTLPBatch{
		Path: request.URL.Path, ContentType: request.Header.Get("Content-Type"), Body: body,
	}); err != nil {
		http.Error(response, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
		return
	}
	response.WriteHeader(http.StatusOK)
}
