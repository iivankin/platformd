package internaldns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	dnsRequestTimeout      = 4 * time.Second
	maxTCPQueriesPerClient = 16
)

type ServerConfig struct {
	Address  netip.Addr
	Port     uint16
	FreeBind bool
	View     *View
}

type Server struct {
	tcp       net.Listener
	udp       net.PacketConn
	port      uint16
	view      *View
	cancel    context.CancelFunc
	requests  chan struct{}
	clients   chan struct{}
	waitGroup sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

func Start(ctx context.Context, config ServerConfig) (*Server, error) {
	if !config.Address.IsValid() || !config.Address.Is4() || config.View == nil {
		return nil, errors.New("internal DNS server requires an IPv4 address and view")
	}
	serverContext, cancel := context.WithCancel(ctx)
	tcp, udp, port, err := listenDNS(serverContext, config.Address, config.Port, config.FreeBind)
	if err != nil {
		cancel()
		return nil, err
	}
	server := &Server{
		tcp: tcp, udp: udp, port: port, view: config.View, cancel: cancel,
		requests: make(chan struct{}, 128), clients: make(chan struct{}, 64),
	}
	server.waitGroup.Add(2)
	go server.serveUDP(serverContext)
	go server.serveTCP(serverContext)
	go func() {
		<-serverContext.Done()
		_ = server.Close()
	}()
	return server, nil
}

func (server *Server) Port() uint16 {
	return server.port
}

func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		server.cancel()
		server.closeErr = errors.Join(server.tcp.Close(), server.udp.Close())
		server.waitGroup.Wait()
	})
	return server.closeErr
}

func (server *Server) serveUDP(ctx context.Context) {
	defer server.waitGroup.Done()
	buffer := make([]byte, maxDNSMessageBytes)
	for {
		length, address, err := server.udp.ReadFrom(buffer)
		if err != nil {
			return
		}
		packet := append([]byte(nil), buffer[:length]...)
		select {
		case server.requests <- struct{}{}:
			server.waitGroup.Add(1)
			go func() {
				defer server.waitGroup.Done()
				defer func() { <-server.requests }()
				requestContext, cancel := context.WithTimeout(ctx, dnsRequestTimeout)
				defer cancel()
				response, resolveErr := server.view.Resolve(requestContext, packet)
				if resolveErr == nil {
					_, _ = server.udp.WriteTo(response, address)
				}
			}()
		default:
		}
	}
}

func (server *Server) serveTCP(ctx context.Context) {
	defer server.waitGroup.Done()
	for {
		connection, err := server.tcp.Accept()
		if err != nil {
			return
		}
		select {
		case server.clients <- struct{}{}:
			server.waitGroup.Add(1)
			go func() {
				defer server.waitGroup.Done()
				defer func() { <-server.clients }()
				server.handleTCP(ctx, connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (server *Server) handleTCP(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	for range maxTCPQueriesPerClient {
		if err := connection.SetDeadline(time.Now().Add(dnsRequestTimeout)); err != nil {
			return
		}
		var size [2]byte
		if _, err := io.ReadFull(connection, size[:]); err != nil {
			return
		}
		length := int(binary.BigEndian.Uint16(size[:]))
		if length < 12 || length > maxDNSMessageBytes {
			return
		}
		packet := make([]byte, length)
		if _, err := io.ReadFull(connection, packet); err != nil {
			return
		}
		requestContext, cancel := context.WithTimeout(ctx, dnsRequestTimeout)
		response, err := server.view.Resolve(requestContext, packet)
		cancel()
		if err != nil || len(response) > maxDNSMessageBytes {
			return
		}
		binary.BigEndian.PutUint16(size[:], uint16(len(response)))
		if err := writeAll(connection, append(size[:], response...)); err != nil {
			return
		}
	}
}

func (server *Server) Address() string {
	return fmt.Sprint(server.tcp.Addr())
}
