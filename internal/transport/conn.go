package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/poouo/BeaconDesk/internal/protocol"
)

var ErrClosed = errors.New("connection closed")

// Conn is the transport abstraction used by clients and the relay. WebSocket
// and QUIC implementations can be added behind this interface later.
type Conn interface {
	Read(context.Context) (protocol.Envelope, error)
	Write(context.Context, protocol.Envelope) error
	Close() error
	RemoteAddr() string
}

type DialOptions struct {
	Address               string
	Transport             string
	EnableTLS             bool
	WebSocketPath         string
	TLSServerName         string
	TLSInsecureSkipVerify bool
}

type TCPConn struct {
	conn  net.Conn
	codec *protocol.LineCodec
	mu    sync.Mutex
}

func DialTCP(ctx context.Context, address string) (*TCPConn, error) {
	return dialTCP(ctx, DialOptions{Address: address})
}

func Dial(ctx context.Context, opts DialOptions) (Conn, error) {
	if opts.Address == "" {
		return nil, fmt.Errorf("address is required")
	}
	if opts.Transport == "" {
		opts.Transport = "tcp"
	}
	switch opts.Transport {
	case "tcp":
		return dialTCP(ctx, opts)
	case "websocket", "ws":
		return DialWebSocket(ctx, opts)
	default:
		return nil, fmt.Errorf("unsupported transport %q", opts.Transport)
	}
}

func dialTCP(ctx context.Context, opts DialOptions) (*TCPConn, error) {
	var dialer net.Dialer
	if opts.EnableTLS {
		tlsDialer := tls.Dialer{
			NetDialer: &dialer,
			Config: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				ServerName:         opts.TLSServerName,
				InsecureSkipVerify: opts.TLSInsecureSkipVerify, //nolint:gosec // Explicit local/self-signed testing option.
			},
		}
		conn, err := tlsDialer.DialContext(ctx, "tcp", opts.Address)
		if err != nil {
			return nil, err
		}
		return NewTCPConn(conn), nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", opts.Address)
	if err != nil {
		return nil, err
	}
	return NewTCPConn(conn), nil
}

func NewTCPConn(conn net.Conn) *TCPConn {
	return &TCPConn{
		conn:  conn,
		codec: protocol.NewLineCodec(conn, conn),
	}
}

func (c *TCPConn) Read(ctx context.Context) (protocol.Envelope, error) {
	type result struct {
		msg protocol.Envelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := c.codec.Read()
		ch <- result{msg: msg, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = c.conn.SetReadDeadline(time.Now())
		return protocol.Envelope{}, ctx.Err()
	case res := <-ch:
		return res.msg, res.err
	}
}

func (c *TCPConn) Write(ctx context.Context, msg protocol.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- c.codec.Write(msg)
	}()

	select {
	case <-ctx.Done():
		_ = c.conn.SetWriteDeadline(time.Now())
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (c *TCPConn) Close() error {
	return c.conn.Close()
}

func (c *TCPConn) RemoteAddr() string {
	return c.conn.RemoteAddr().String()
}

type WebSocketConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func DialWebSocket(ctx context.Context, opts DialOptions) (*WebSocketConn, error) {
	path := opts.WebSocketPath
	if path == "" {
		path = "/ws"
	}
	scheme := "ws"
	if opts.EnableTLS {
		scheme = "wss"
	}
	url := fmt.Sprintf("%s://%s%s", scheme, opts.Address, path)
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         opts.TLSServerName,
			InsecureSkipVerify: opts.TLSInsecureSkipVerify, //nolint:gosec // Explicit local/self-signed testing option.
		},
	}
	conn, _, err := dialer.DialContext(ctx, url, http.Header{})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(protocol.MaxLineBytes)
	return NewWebSocketConn(conn), nil
}

func NewWebSocketConn(conn *websocket.Conn) *WebSocketConn {
	return &WebSocketConn{conn: conn}
}

func (c *WebSocketConn) Read(ctx context.Context) (protocol.Envelope, error) {
	type result struct {
		msg protocol.Envelope
		err error
	}
	ch := make(chan result, 1)
	go func() {
		messageType, b, err := c.conn.ReadMessage()
		if err != nil {
			ch <- result{err: err}
			return
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			ch <- result{err: fmt.Errorf("unsupported websocket message type %d", messageType)}
			return
		}
		var msg protocol.Envelope
		if err := json.Unmarshal(b, &msg); err != nil {
			ch <- result{err: err}
			return
		}
		if msg.Version != protocol.Version {
			ch <- result{err: fmt.Errorf("unsupported protocol version %d", msg.Version)}
			return
		}
		if msg.Type == "" {
			ch <- result{err: errors.New("message type is required")}
			return
		}
		ch <- result{msg: msg}
	}()

	select {
	case <-ctx.Done():
		_ = c.conn.SetReadDeadline(time.Now())
		return protocol.Envelope{}, ctx.Err()
	case res := <-ch:
		return res.msg, res.err
	}
}

func (c *WebSocketConn) Write(ctx context.Context, msg protocol.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.Version == 0 {
		msg.Version = protocol.Version
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- c.conn.WriteMessage(websocket.TextMessage, b)
	}()
	select {
	case <-ctx.Done():
		_ = c.conn.SetWriteDeadline(time.Now())
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (c *WebSocketConn) Close() error {
	return c.conn.Close()
}

func (c *WebSocketConn) RemoteAddr() string {
	return c.conn.RemoteAddr().String()
}
