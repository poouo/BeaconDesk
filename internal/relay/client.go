package relay

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/poouo/BeaconDesk/internal/protocol"
	"github.com/poouo/BeaconDesk/internal/transport"
)

type Client struct {
	conn       transport.Conn
	server     *Server
	logger     *slog.Logger
	send       chan protocol.Envelope
	closeOnce  sync.Once
	closed     chan struct{}
	stats      *transport.Stats
	deviceID   string
	deviceName string
	role       string
	authMu     sync.Mutex
	authCode   string
	authExpiry time.Time
	limiter    *BandwidthLimiter
}

func newClient(server *Server, conn transport.Conn) *Client {
	return &Client{
		conn:    conn,
		server:  server,
		logger:  server.logger.With("remote", conn.RemoteAddr()),
		send:    make(chan protocol.Envelope, 128),
		closed:  make(chan struct{}),
		stats:   transport.NewStats(),
		limiter: NewBandwidthLimiter(server.cfg.BandwidthLimitKbps),
	}
}

func (c *Client) run(ctx context.Context) {
	defer c.close()
	go c.writeLoop(ctx)

	for {
		msg, err := c.conn.Read(ctx)
		if err != nil {
			c.logger.Info("client read stopped", "error", err)
			return
		}
		c.stats.Touch()
		if err := c.server.handleMessage(ctx, c, msg); err != nil {
			c.logger.Warn("message handling failed", "type", msg.Type, "error", err)
			c.trySend(protocol.MustEnvelope(protocol.TypeError, "relay", c.deviceID, protocol.ErrorPayload{
				Code:    "message_failed",
				Message: err.Error(),
			}))
		}
	}
}

func (c *Client) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.closed:
			return
		case msg := <-c.send:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := c.limiter.Wait(writeCtx, len(msg.Payload)); err != nil {
				cancel()
				if writeCtx.Err() != nil {
					return
				}
				c.logger.Warn("bandwidth limiter failed", "error", err)
				continue
			}
			err := c.conn.Write(writeCtx, msg)
			cancel()
			if err != nil {
				c.logger.Warn("client write failed", "error", err)
				c.close()
				return
			}
		}
	}
}

func (c *Client) trySend(msg protocol.Envelope) bool {
	select {
	case c.send <- msg:
		return true
	case <-c.closed:
		return false
	default:
		c.logger.Warn("dropping message because send queue is full", "type", msg.Type)
		return false
	}
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		c.server.unregister(c)
	})
}
