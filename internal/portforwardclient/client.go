package portforwardclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/coder/websocket"
	"github.com/iivankin/platformd/internal/portforwardprotocol"
)

type Config struct {
	URL       string
	Ticket    string
	LocalPort int
	Output    io.Writer
}

func Run(ctx context.Context, config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", config.LocalPort))
	if err != nil {
		return fmt.Errorf("listen on localhost: %w", err)
	}
	defer listener.Close()
	if config.Output != nil {
		_, _ = fmt.Fprintf(config.Output, "Forwarding %s to the platformd resource\n", listener.Addr())
	}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	var connections sync.WaitGroup
	defer connections.Wait()
	for {
		local, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept local connection: %w", err)
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			if err := forward(ctx, config, local); err != nil && config.Output != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(config.Output, "Port forward connection closed: %v\n", err)
			}
		}()
	}
}

func forward(ctx context.Context, config Config, local net.Conn) error {
	defer local.Close()
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+config.Ticket)
	connection, response, err := websocket.Dial(ctx, config.URL, &websocket.DialOptions{
		HTTPHeader: header, Subprotocols: []string{portforwardprotocol.WebSocketProtocol},
	})
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect WSS tunnel: server returned %s: %w", response.Status, err)
		}
		return fmt.Errorf("connect WSS tunnel: %w", err)
	}
	if connection.Subprotocol() != portforwardprotocol.WebSocketProtocol {
		_ = connection.Close(websocket.StatusProtocolError, "required protocol was not negotiated")
		return errors.New("server did not negotiate the platformd port forward protocol")
	}
	remote := websocket.NetConn(ctx, connection, websocket.MessageBinary)
	bridge(local, remote)
	return nil
}

func bridge(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyStream := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		done <- struct{}{}
	}
	go copyStream(left, right)
	go copyStream(right, left)
	<-done
	_ = left.Close()
	_ = right.Close()
	<-done
}

func validate(config Config) error {
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.Path != portforwardprotocol.EndpointPath || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("--url must be a wss:// URL for the platformd port forward endpoint")
	}
	if config.Ticket == "" {
		return errors.New("PLATFORMD_FORWARD_TICKET is required")
	}
	if config.LocalPort < 1 || config.LocalPort > 65535 {
		return errors.New("--local-port must be from 1 to 65535")
	}
	return nil
}
