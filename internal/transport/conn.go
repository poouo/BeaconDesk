package transport

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
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
	TLSCertSHA256         string
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
		tlsConfig, err := clientTLSConfig(opts)
		if err != nil {
			return nil, err
		}
		tlsDialer := tls.Dialer{
			NetDialer: &dialer,
			Config:    tlsConfig,
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
	tlsConfig, err := clientTLSConfig(opts)
	if err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		TLSClientConfig:  tlsConfig,
	}
	conn, _, err := dialer.DialContext(ctx, url, http.Header{})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(protocol.MaxLineBytes)
	return NewWebSocketConn(conn), nil
}

func clientTLSConfig(opts DialOptions) (*tls.Config, error) {
	pin, hasPin, err := normalizeCertSHA256(opts.TLSCertSHA256)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: opts.TLSServerName,
		// This option is exposed only for explicit local/self-signed testing.
		InsecureSkipVerify: opts.TLSInsecureSkipVerify, //nolint:gosec
	}
	if hasPin {
		cfg.InsecureSkipVerify = true //nolint:gosec // The pinned SHA256 fingerprint is verified below.
		cfg.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("tls peer did not provide a certificate")
			}
			sum := sha256.Sum256(state.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(sum[:], pin) != 1 {
				return fmt.Errorf("tls certificate fingerprint mismatch: got %s", strings.ToUpper(hex.EncodeToString(sum[:])))
			}
			now := time.Now()
			leaf := state.PeerCertificates[0]
			if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
				return fmt.Errorf("tls certificate is outside its validity period")
			}
			return nil
		}
	}
	return cfg, nil
}

func normalizeCertSHA256(value string) ([]byte, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, nil
	}
	value = strings.TrimPrefix(value, "SHA256 Fingerprint=")
	value = strings.TrimPrefix(value, "sha256 Fingerprint=")
	value = strings.TrimPrefix(value, "sha256=")
	value = strings.TrimPrefix(value, "SHA256=")
	var b strings.Builder
	for _, r := range value {
		switch r {
		case ':', '-', ' ', '\t', '\r', '\n':
			continue
		default:
			b.WriteRune(r)
		}
	}
	hexValue := b.String()
	if len(hexValue) != sha256.Size*2 {
		return nil, false, fmt.Errorf("tls certificate SHA256 fingerprint must be 64 hex characters")
	}
	pin, err := hex.DecodeString(hexValue)
	if err != nil {
		return nil, false, fmt.Errorf("invalid tls certificate SHA256 fingerprint: %w", err)
	}
	return pin, true, nil
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
