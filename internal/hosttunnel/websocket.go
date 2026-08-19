package hosttunnel

import (
	"context"

	"github.com/coder/websocket"
)

type websocketConn struct {
	conn *websocket.Conn
}

func WrapWebsocket(conn *websocket.Conn) MessageConn {
	conn.SetReadLimit(maxFrameBytes)
	return websocketConn{conn: conn}
}

func (conn websocketConn) Read(ctx context.Context) ([]byte, error) {
	kind, payload, err := conn.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if kind != websocket.MessageBinary {
		return nil, errPeerClosed
	}
	return payload, nil
}

func (conn websocketConn) Write(ctx context.Context, payload []byte) error {
	return conn.conn.Write(ctx, websocket.MessageBinary, payload)
}

func (conn websocketConn) Close() error {
	return conn.conn.Close(websocket.StatusNormalClosure, "")
}
