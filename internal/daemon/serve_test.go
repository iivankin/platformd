package daemon

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPListenersServesAllAndShutsDown(t *testing.T) {
	first := testHTTPListener(t)
	second := testHTTPListener(t)
	server := func(body string) *http.Server {
		return &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(response, body)
		})}
	}
	firstServer := server("first")
	firstServer.Addr = first.Addr().String()
	secondServer := server("second")
	secondServer.Addr = second.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		done <- serveHTTPListeners(ctx, []httpListener{
			{server: firstServer, listener: first},
			{server: secondServer, listener: second},
		}, func() error {
			close(started)
			return nil
		})
	}()
	<-started

	client := &http.Client{Timeout: time.Second}
	for _, test := range []struct {
		address string
		want    string
	}{
		{address: first.Addr().String(), want: "first"},
		{address: second.Addr().String(), want: "second"},
	} {
		response, err := client.Get("http://" + test.address)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || string(body) != test.want {
			t.Fatalf("response from %s = %q, %v", test.address, body, readErr)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("servers did not shut down")
	}
}

func testHTTPListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
