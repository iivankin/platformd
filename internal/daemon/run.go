package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/iivankin/platformd/internal/server"
)

const shutdownTimeout = 10 * time.Second

// Run starts an HTTP-only development listener until bootstrap owns the
// installation TLS and state. The environment gate prevents this private mode
// from accidentally becoming a second public configuration surface.
func Run(ctx context.Context) error {
	if os.Getenv("PLATFORMD_DEV") != "1" {
		return errors.New("installation is not initialized")
	}

	address := os.Getenv("PLATFORMD_DEV_ADDR")
	if address == "" {
		address = "127.0.0.1:8080"
	}

	httpServer := &http.Server{
		Addr:              address,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errChannel := make(chan error, 1)
	go func() {
		errChannel <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve %s: %w", address, err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
