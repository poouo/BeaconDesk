package client

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/poouo/BeaconDesk/internal/audit"
	"github.com/poouo/BeaconDesk/internal/auth"
	"github.com/poouo/BeaconDesk/internal/desktop"
	"github.com/poouo/BeaconDesk/internal/input"
	"github.com/poouo/BeaconDesk/internal/protocol"
	"github.com/poouo/BeaconDesk/internal/transport"
	"github.com/poouo/BeaconDesk/internal/trust"
)

type Client struct {
	opts      Options
	logger    *slog.Logger
	rootCtx   context.Context
	conn      transport.Conn
	connMu    sync.RWMutex
	identity  auth.DeviceIdentity
	trust     *trust.Store
	audit     *audit.Store
	stateMu   sync.RWMutex
	state     State
	events    chan Event
	cancel    context.CancelFunc
	closed    chan struct{}
	closeOnce sync.Once

	framesSent     atomic.Int64
	framesReceived atomic.Int64
	bytesSent      atomic.Int64
	heartbeatSeq   atomic.Int64
	heartbeatPongs atomic.Int64
	reconnectCount atomic.Int64
}

func New(opts Options, logger *slog.Logger) *Client {
	opts = opts.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		opts:   opts,
		logger: logger,
		events: make(chan Event, 256),
		closed: make(chan struct{}),
		state: State{
			DeviceName: opts.DeviceName,
			Role:       opts.Role,
		},
	}
}

func (c *Client) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.rootCtx = ctx

	identityPath := c.opts.IdentityPath
	if identityPath == "" {
		identityPath = filepath.Join(".", ".beacondesk", "device.json")
	}
	trustPath := c.opts.TrustStorePath
	if trustPath == "" {
		trustPath = filepath.Join(filepath.Dir(identityPath), "trusted-devices.json")
	}
	c.trust = trust.NewStore(trustPath)
	auditPath := c.opts.AuditLogPath
	if auditPath == "" {
		auditPath = filepath.Join(filepath.Dir(identityPath), "audit-log.json")
	}
	c.audit = audit.NewStore(auditPath)
	identity, err := auth.LoadOrCreateDeviceIdentity(identityPath, c.opts.DeviceName)
	if err != nil {
		return err
	}
	c.identity = identity
	c.setState(func(s *State) {
		s.DeviceID = identity.DeviceID
		s.DeviceName = identity.DeviceName
		s.Role = c.opts.Role
	})

	if err := c.connect(ctx); err != nil {
		return err
	}

	go c.readLoop(ctx)
	go c.heartbeatLoop(ctx)
	go c.telemetryLoop(ctx)
	return nil
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		c.closeConn()
		close(c.closed)
	})
}

func (c *Client) Events() <-chan Event {
	return c.events
}

func (c *Client) State() State {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	out := c.state
	out.FramesSent = c.framesSent.Load()
	out.FramesReceived = c.framesReceived.Load()
	out.BitrateKbps = c.estimatedBitrateKbpsSince(out.ConnectedAt)
	out.ReconnectCount = c.reconnectCount.Load()
	out.PacketLossPermy = c.packetLossPermyriad()
	return out
}

func (c *Client) RequestSession(ctx context.Context, targetDeviceID string) error {
	return c.RequestSessionWithCode(ctx, targetDeviceID, "")
}

func (c *Client) RequestSessionWithCode(ctx context.Context, targetDeviceID string, authCode string) error {
	if targetDeviceID == "" {
		return fmt.Errorf("target device id is required")
	}
	msg := protocol.MustEnvelope(protocol.TypeSessionRequest, c.identity.DeviceID, targetDeviceID, protocol.SessionRequestPayload{
		TargetDeviceID: targetDeviceID,
		Mode:           c.opts.RequestMode,
		AuthCode:       authCode,
		RequesterName:  c.identity.DeviceName,
		RequesterRole:  c.opts.Role,
		InputRequested: protocol.SessionModeAllowsInput(c.opts.RequestMode),
	})
	c.emit("session.request", "requesting session with "+targetDeviceID)
	return c.write(ctx, msg)
}

func (c *Client) GenerateTemporaryCode(ctx context.Context, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	code, err := auth.NewTemporaryCode()
	if err != nil {
		return "", err
	}
	msg := protocol.MustEnvelope(protocol.TypeAuthCodePublish, c.identity.DeviceID, "relay", protocol.AuthCodePublishPayload{
		Code:      code,
		TTLMillis: ttl.Milliseconds(),
	})
	if err := c.write(ctx, msg); err != nil {
		return "", err
	}
	expiresAt := time.Now().Add(ttl).UnixMilli()
	c.setState(func(s *State) {
		s.LocalAuthCode = code
		s.AuthCodeExpiry = expiresAt
	})
	c.emit("auth.code.generated", "temporary code generated")
	return code, nil
}

func (c *Client) CreateWebShare(ctx context.Context, ttl time.Duration, mode string, label string) error {
	if ttl <= 0 {
		ttl = time.Hour
	}
	mode = protocol.NormalizeSessionMode(mode)
	msg := protocol.MustEnvelope(protocol.TypeWebShareCreate, c.identity.DeviceID, "relay", protocol.WebShareCreatePayload{
		TTLMillis: ttl.Milliseconds(),
		Mode:      mode,
		Label:     label,
	})
	c.emit("web.share.create", fmt.Sprintf("creating web control link ttl=%s mode=%s", ttl, mode))
	return c.write(ctx, msg)
}

func (c *Client) RefreshWebShares(ctx context.Context) error {
	msg := protocol.MustEnvelope(protocol.TypeWebShareList, c.identity.DeviceID, "relay", protocol.WebShareListPayload{})
	return c.write(ctx, msg)
}

func (c *Client) RevokeWebShare(ctx context.Context, id string, token string) error {
	if id == "" && token == "" {
		return fmt.Errorf("web share id or token is required")
	}
	msg := protocol.MustEnvelope(protocol.TypeWebShareRevoke, c.identity.DeviceID, "relay", protocol.WebShareRevokePayload{
		ID:    id,
		Token: token,
	})
	return c.write(ctx, msg)
}

func (c *Client) ApproveSession(ctx context.Context) error {
	return c.approveSession(ctx, false)
}

func (c *Client) ApproveAndRememberSession(ctx context.Context) error {
	return c.approveSession(ctx, true)
}

func (c *Client) approveSession(ctx context.Context, remember bool) error {
	pending := c.pendingPeerID()
	if pending == "" {
		return fmt.Errorf("no pending session request")
	}
	if remember && c.trust != nil {
		if err := c.trust.Remember(pending, c.pendingMode()); err != nil {
			return err
		}
	}
	confirm := protocol.MustEnvelope(protocol.TypeSessionConfirm, c.identity.DeviceID, pending, protocol.SessionConfirmPayload{
		Accepted:     true,
		AcceptedMode: c.pendingMode(),
		InputAllowed: c.pendingInput() && c.opts.EnableInput,
	})
	if err := c.write(ctx, confirm); err != nil {
		return err
	}
	c.recordAudit("session.accepted", pending, "", c.pendingMode(), fmt.Sprintf("remember=%t input_allowed=%t", remember, c.pendingInput() && c.opts.EnableInput))
	c.setState(func(s *State) {
		clearPending(s)
	})
	if remember {
		c.emit("session.accepted", "accepted and remembered request from "+pending)
	} else {
		c.emit("session.accepted", "accepted request from "+pending)
	}
	return nil
}

func (c *Client) RejectSession(ctx context.Context, reason string) error {
	pending := c.pendingPeerID()
	if pending == "" {
		return fmt.Errorf("no pending session request")
	}
	if reason == "" {
		reason = "rejected by user"
	}
	confirm := protocol.MustEnvelope(protocol.TypeSessionConfirm, c.identity.DeviceID, pending, protocol.SessionConfirmPayload{
		Accepted: false,
		Reason:   reason,
	})
	if err := c.write(ctx, confirm); err != nil {
		return err
	}
	c.recordAudit("session.rejected", pending, "", c.pendingMode(), reason)
	c.setState(func(s *State) {
		clearPending(s)
	})
	c.emit("session.rejected", "rejected request from "+pending)
	return nil
}

func (c *Client) SendMouse(ctx context.Context, event input.MouseEvent) error {
	sessionID := c.activeSessionID()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	payload := protocol.InputMousePayload{
		X:            event.X,
		Y:            event.Y,
		SourceWidth:  event.SourceWidth,
		SourceHeight: event.SourceHeight,
		Button:       event.Button,
		Action:       event.Action,
		WheelDelta:   event.WheelDelta,
	}
	msg := protocol.MustEnvelope(protocol.TypeInputMouse, c.identity.DeviceID, "", payload)
	msg.SessionID = sessionID
	return c.write(ctx, msg)
}

func (c *Client) SendKeyboard(ctx context.Context, event input.KeyboardEvent) error {
	sessionID := c.activeSessionID()
	if sessionID == "" {
		return fmt.Errorf("no active session")
	}
	payload := protocol.InputKeyboardPayload{
		Key:       event.Key,
		Code:      event.Code,
		KeyCode:   event.KeyCode,
		Action:    event.Action,
		Modifiers: event.Modifiers,
	}
	msg := protocol.MustEnvelope(protocol.TypeInputKeyboard, c.identity.DeviceID, "", payload)
	msg.SessionID = sessionID
	return c.write(ctx, msg)
}

func (c *Client) register(ctx context.Context) error {
	msg := protocol.MustEnvelope(protocol.TypeDeviceRegister, c.identity.DeviceID, "relay", protocol.RegisterPayload{
		DeviceID:   c.identity.DeviceID,
		DeviceName: c.identity.DeviceName,
		Role:       c.opts.Role,
		Token:      c.opts.Token,
	})
	return c.write(ctx, msg)
}

func (c *Client) connect(ctx context.Context) error {
	conn, err := transport.Dial(ctx, transport.DialOptions{
		Address:               c.opts.ServerAddress,
		Transport:             c.opts.Transport,
		EnableTLS:             c.opts.UseTLS,
		WebSocketPath:         c.opts.WebSocketPath,
		TLSServerName:         c.opts.TLSServerName,
		TLSInsecureSkipVerify: c.opts.TLSSkipVerify,
	})
	if err != nil {
		return err
	}

	c.connMu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = conn
	c.connMu.Unlock()

	now := time.Now()
	c.setState(func(s *State) {
		s.Connected = true
		s.Reconnecting = false
		s.Registered = false
		s.ConnectedAt = now
		s.LastMessageAt = now
	})
	c.emit("transport.connected", fmt.Sprintf("connected to relay %s via %s", c.opts.ServerAddress, c.opts.Transport))
	if err := c.register(ctx); err != nil {
		c.closeConn()
		return err
	}
	return nil
}

func (c *Client) reconnect(ctx context.Context) error {
	delay := c.opts.ReconnectMinDelay
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.closed:
			return context.Canceled
		default:
		}

		count := c.reconnectCount.Add(1)
		c.setState(func(s *State) {
			s.Connected = false
			s.Reconnecting = true
			s.ReconnectCount = count
			s.Registered = false
			clearSession(s)
			clearPending(s)
		})
		c.emit("transport.reconnecting", fmt.Sprintf("reconnecting in %s", delay))

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-c.closed:
			timer.Stop()
			return context.Canceled
		case <-timer.C:
		}

		if err := c.connect(ctx); err == nil {
			c.emit("transport.reconnected", fmt.Sprintf("reconnected to relay %s via %s", c.opts.ServerAddress, c.opts.Transport))
			return nil
		} else {
			c.emit("transport.reconnect_failed", err.Error())
		}
		delay *= 2
		if delay > c.opts.ReconnectMaxDelay {
			delay = c.opts.ReconnectMaxDelay
		}
	}
}

func (c *Client) currentConn() transport.Conn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *Client) write(ctx context.Context, msg protocol.Envelope) error {
	conn := c.currentConn()
	if conn == nil {
		return fmt.Errorf("client is not connected")
	}
	return conn.Write(ctx, msg)
}

func (c *Client) closeConn() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) markDisconnected() {
	c.closeConn()
	c.setState(func(s *State) {
		s.Connected = false
		s.Registered = false
		clearSession(s)
	})
}

func (c *Client) readLoop(ctx context.Context) {
	for {
		conn := c.currentConn()
		if conn == nil {
			if err := c.reconnect(ctx); err != nil {
				return
			}
			continue
		}
		msg, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				c.emit("transport.closed", err.Error())
				c.markDisconnected()
				if c.opts.DisableReconnect {
					c.Close()
					return
				}
				if err := c.reconnect(ctx); err != nil {
					return
				}
				continue
			}
			return
		}
		c.setState(func(s *State) {
			s.LastMessageAt = time.Now()
		})
		if err := c.handleMessage(ctx, msg); err != nil {
			c.emit("client.error", err.Error())
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(c.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			seq := c.heartbeatSeq.Add(1)
			msg := protocol.MustEnvelope(protocol.TypeHeartbeatPing, c.identity.DeviceID, "relay", protocol.HeartbeatPayload{
				Sequence: seq,
				SentAt:   time.Now().UnixMilli(),
			})
			if err := c.write(ctx, msg); err != nil {
				c.emit("heartbeat.error", err.Error())
				return
			}
		}
	}
}

func (c *Client) telemetryLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sessionID := c.activeSessionID()
			if sessionID == "" {
				continue
			}
			state := c.State()
			payload := protocol.TelemetryPayload{
				RTTMillis:       state.RTTMillis,
				FramesSent:      state.FramesSent,
				FramesReceived:  state.FramesReceived,
				BitrateKbps:     state.BitrateKbps,
				PacketLossPermy: state.PacketLossPermy,
			}
			msg := protocol.MustEnvelope(protocol.TypeTelemetryStats, c.identity.DeviceID, "", payload)
			msg.SessionID = sessionID
			if err := c.write(ctx, msg); err != nil {
				c.emit("telemetry.error", err.Error())
			}
		}
	}
}

func (c *Client) handleMessage(ctx context.Context, msg protocol.Envelope) error {
	switch msg.Type {
	case protocol.TypeDeviceRegistered:
		payload, err := protocol.DecodePayload[protocol.RegisteredPayload](msg)
		if err != nil {
			return err
		}
		c.setState(func(s *State) {
			s.Registered = true
			s.DeviceID = payload.DeviceID
		})
		c.emit("device.registered", "registered as "+payload.DeviceID)
		if c.opts.TargetDeviceID != "" {
			return c.RequestSessionWithCode(ctx, c.opts.TargetDeviceID, c.opts.TargetAuthCode)
		}
	case protocol.TypeAuthCodePublished:
		payload, err := protocol.DecodePayload[protocol.AuthCodePublishedPayload](msg)
		if err != nil {
			return err
		}
		c.setState(func(s *State) {
			s.AuthCodeExpiry = payload.ExpiresAt
		})
		c.emit("auth.code.published", payload.Message)
	case protocol.TypeWebShareCreated:
		payload, err := protocol.DecodePayload[protocol.WebShareCreatedPayload](msg)
		if err != nil {
			return err
		}
		c.setState(func(s *State) {
			upsertWebShare(s, payload.Share)
		})
		c.emit("web.share.created", payload.Message)
	case protocol.TypeWebShareListResult:
		payload, err := protocol.DecodePayload[protocol.WebShareListResultPayload](msg)
		if err != nil {
			return err
		}
		c.setState(func(s *State) {
			s.WebShareLinks = append([]protocol.WebSharePayload(nil), payload.Shares...)
		})
		c.emit("web.share.list", fmt.Sprintf("%d web control links", len(payload.Shares)))
	case protocol.TypeWebShareRevoked:
		payload, err := protocol.DecodePayload[protocol.WebShareRevokedPayload](msg)
		if err != nil {
			return err
		}
		c.setState(func(s *State) {
			removeWebShare(s, payload.ID, payload.Token)
		})
		c.emit("web.share.revoked", payload.Message)
	case protocol.TypeWebShareStatus:
		payload, err := protocol.DecodePayload[protocol.WebShareStatusPayload](msg)
		if err != nil {
			return err
		}
		if payload.Message != "" {
			c.emit("web.share."+payload.Status, payload.Message)
		} else {
			c.emit("web.share."+payload.Status, payload.Status)
		}
	case protocol.TypeHeartbeatPong:
		payload, err := protocol.DecodePayload[protocol.HeartbeatPayload](msg)
		if err != nil {
			return err
		}
		rtt := time.Now().UnixMilli() - payload.SentAt
		c.heartbeatPongs.Add(1)
		c.setState(func(s *State) {
			s.RTTMillis = rtt
		})
		c.emit("heartbeat.pong", fmt.Sprintf("rtt=%dms seq=%d", rtt, payload.Sequence))
	case protocol.TypeSessionRequest:
		return c.handleSessionRequest(ctx, msg)
	case protocol.TypeSessionConfirm:
		payload, err := protocol.DecodePayload[protocol.SessionConfirmPayload](msg)
		if err != nil {
			return err
		}
		if !payload.Accepted {
			c.emit("session.declined", payload.Reason)
		}
	case protocol.TypeSessionReady:
		payload, err := protocol.DecodePayload[protocol.SessionReadyPayload](msg)
		if err != nil {
			return err
		}
		c.setState(func(s *State) {
			s.SessionID = payload.SessionID
			s.PeerID = payload.PeerID
			s.PeerName = payload.PeerName
			s.SessionMode = payload.Mode
			s.InputAllowed = payload.InputAllowed
		})
		c.recordAudit("session.ready", payload.PeerID, payload.SessionID, payload.Mode, fmt.Sprintf("input_allowed=%t", payload.InputAllowed))
		c.emit("session.ready", fmt.Sprintf("session ready with %s mode=%s input=%t", payload.PeerID, payload.Mode, payload.InputAllowed))
		if c.opts.SendMockFrames {
			go c.mockFrameLoop(ctx, payload.SessionID)
		}
		if c.opts.SendScreenFrames {
			go c.screenFrameLoop(ctx, payload.SessionID)
		}
	case protocol.TypeStreamFrame:
		payload, err := protocol.DecodePayload[protocol.StreamFramePayload](msg)
		if err != nil {
			return err
		}
		c.framesReceived.Add(1)
		c.setState(func(s *State) {
			s.LastFrameKind = payload.Kind
			s.LastFrameWidth = payload.Width
			s.LastFrameHeight = payload.Height
			if payload.Kind == "jpeg" {
				s.LastFrameData = "data:image/jpeg;base64," + payload.Data
			}
		})
		if payload.Kind == "jpeg" {
			c.emit("stream.frame", fmt.Sprintf("received jpeg frame %d (%dx%d)", payload.FrameID, payload.Width, payload.Height))
		} else {
			c.emit("stream.frame", fmt.Sprintf("received frame %d: %s", payload.FrameID, payload.Data))
		}
	case protocol.TypeInputMouse:
		return c.handleInputMouse(msg)
	case protocol.TypeInputKeyboard:
		return c.handleInputKeyboard(msg)
	case protocol.TypeTelemetryStats:
		payload, err := protocol.DecodePayload[protocol.TelemetryPayload](msg)
		if err != nil {
			return err
		}
		c.emit("telemetry.stats", fmt.Sprintf("peer rtt=%dms bitrate=%dkbps loss=%d/10000", payload.RTTMillis, payload.BitrateKbps, payload.PacketLossPermy))
	case protocol.TypeSessionClose:
		c.setState(func(s *State) {
			clearSession(s)
		})
		c.emit("session.close", "session closed")
	case protocol.TypeError:
		payload, err := protocol.DecodePayload[protocol.ErrorPayload](msg)
		if err != nil {
			return err
		}
		c.emit("relay.error", payload.Code+": "+payload.Message)
	default:
		c.emit("message.ignored", msg.Type)
	}
	return nil
}

func (c *Client) handleInputMouse(msg protocol.Envelope) error {
	if !c.opts.EnableInput {
		c.emit("input.blocked", "blocked mouse input because remote input is disabled")
		c.recordAudit("input.blocked", msg.From, msg.SessionID, "", "mouse")
		return nil
	}
	payload, err := protocol.DecodePayload[protocol.InputMousePayload](msg)
	if err != nil {
		return err
	}
	injector, err := input.NewInjector()
	if err != nil {
		return err
	}
	defer injector.Close()
	c.recordAudit("input.mouse", msg.From, msg.SessionID, "", payload.Action)
	return injector.Mouse(input.MouseEvent{
		X:            payload.X,
		Y:            payload.Y,
		SourceWidth:  payload.SourceWidth,
		SourceHeight: payload.SourceHeight,
		Button:       payload.Button,
		Action:       payload.Action,
		WheelDelta:   payload.WheelDelta,
	})
}

func (c *Client) handleInputKeyboard(msg protocol.Envelope) error {
	if !c.opts.EnableInput {
		c.emit("input.blocked", "blocked keyboard input because remote input is disabled")
		c.recordAudit("input.blocked", msg.From, msg.SessionID, "", "keyboard")
		return nil
	}
	payload, err := protocol.DecodePayload[protocol.InputKeyboardPayload](msg)
	if err != nil {
		return err
	}
	injector, err := input.NewInjector()
	if err != nil {
		return err
	}
	defer injector.Close()
	c.recordAudit("input.keyboard", msg.From, msg.SessionID, "", payload.Action+" "+payload.Code)
	return injector.Keyboard(input.KeyboardEvent{
		Key:       payload.Key,
		Code:      payload.Code,
		KeyCode:   payload.KeyCode,
		Action:    payload.Action,
		Modifiers: payload.Modifiers,
	})
}

func (c *Client) handleSessionRequest(ctx context.Context, msg protocol.Envelope) error {
	c.emit("session.incoming", "incoming request from "+msg.From)
	payload, err := protocol.DecodePayload[protocol.SessionRequestPayload](msg)
	if err != nil {
		return err
	}
	c.recordAudit("session.incoming", msg.From, "", protocol.NormalizeSessionMode(payload.Mode), fmt.Sprintf("name=%s input_requested=%t", payload.RequesterName, payload.InputRequested))
	trusted := c.trust != nil && c.trust.IsTrusted(msg.From)
	c.setState(func(s *State) {
		s.PendingPeerID = msg.From
		s.PendingPeerName = payload.RequesterName
		s.PendingMode = protocol.NormalizeSessionMode(payload.Mode)
		s.PendingInput = payload.InputRequested
		s.PendingTrusted = trusted
	})
	if trusted {
		if err := c.trust.Touch(msg.From); err != nil {
			c.emit("trust.error", err.Error())
		}
		c.emit("session.trusted", "auto-accepting trusted device "+msg.From)
		return c.ApproveSession(ctx)
	}
	if !c.opts.AutoAccept {
		c.emit("session.waiting", "waiting for local approval from "+msg.From)
		return nil
	}
	return c.ApproveSession(ctx)
}

func (c *Client) screenFrameLoop(ctx context.Context, sessionID string) {
	quality := c.opts.CaptureQuality
	capturer, err := desktop.NewCapturer(desktop.CaptureOptions{
		FPS:       c.opts.CaptureFPS,
		MaxWidth:  c.opts.CaptureMaxWidth,
		MaxHeight: c.opts.CaptureMaxHeight,
		Quality:   quality,
	})
	if err != nil {
		c.emit("screen.error", err.Error())
		return
	}
	defer capturer.Close()
	currentFPS := c.opts.CaptureFPS
	c.setState(func(s *State) {
		s.CaptureQuality = quality
		s.CurrentFPS = currentFPS
	})
	detector := desktop.ChangeDetector{
		StaticFrameInterval: time.Duration(c.opts.StaticFrameSeconds) * time.Second,
	}

	ticker := time.NewTicker(frameInterval(currentFPS))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame, err := capturer.Capture(ctx)
			if err != nil {
				c.emit("screen.error", err.Error())
				continue
			}
			decision := detector.Observe(frame)
			if !decision.Send {
				continue
			}
			payload := protocol.StreamFramePayload{
				FrameID:   frame.ID,
				Kind:      frame.Codec,
				Data:      base64.StdEncoding.EncodeToString(frame.Data),
				MimeType:  "image/jpeg",
				Width:     frame.Width,
				Height:    frame.Height,
				Timestamp: frame.Timestamp,
			}
			msg := protocol.MustEnvelope(protocol.TypeStreamFrame, c.identity.DeviceID, "", payload)
			msg.SessionID = sessionID
			if err := c.write(ctx, msg); err != nil {
				c.emit("screen.error", err.Error())
				return
			}
			c.framesSent.Add(1)
			c.bytesSent.Add(int64(len(frame.Data)))
			quality = c.adjustCaptureQuality(capturer, quality)
			nextFPS := c.adjustCaptureFPS(currentFPS)
			if nextFPS != currentFPS {
				currentFPS = nextFPS
				ticker.Reset(frameInterval(currentFPS))
			}
			if decision.Changed {
				c.emit("screen.sent", fmt.Sprintf("sent jpeg frame %d (%dx%d)", frame.ID, frame.Width, frame.Height))
			} else {
				c.emit("screen.keepalive", fmt.Sprintf("sent static jpeg frame %d (%dx%d)", frame.ID, frame.Width, frame.Height))
			}
		}
	}
}

func (c *Client) mockFrameLoop(ctx context.Context, sessionID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var frameID int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frameID++
			payload := protocol.StreamFramePayload{
				FrameID:   frameID,
				Kind:      "mock-text",
				Data:      fmt.Sprintf("mock desktop frame %d from %s", frameID, c.identity.DeviceID),
				Width:     1280,
				Height:    720,
				Timestamp: time.Now().UnixMilli(),
			}
			msg := protocol.MustEnvelope(protocol.TypeStreamFrame, c.identity.DeviceID, "", payload)
			msg.SessionID = sessionID
			if err := c.write(ctx, msg); err != nil {
				c.emit("stream.error", err.Error())
				return
			}
			c.framesSent.Add(1)
			c.bytesSent.Add(int64(len(payload.Data)))
			c.emit("stream.sent", fmt.Sprintf("sent mock frame %d", frameID))
		}
	}
}

func (c *Client) estimatedBitrateKbpsSince(connectedAt time.Time) int64 {
	if connectedAt.IsZero() {
		return 0
	}
	elapsed := time.Since(connectedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return int64(float64(c.bytesSent.Load()*8) / elapsed / 1000)
}

func (c *Client) packetLossPermyriad() int64 {
	sent := c.heartbeatSeq.Load()
	if sent <= 0 {
		return 0
	}
	lost := sent - c.heartbeatPongs.Load()
	if lost < 0 {
		lost = 0
	}
	return lost * 10000 / sent
}

func (c *Client) connectedAt() time.Time {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.ConnectedAt
}

func (c *Client) currentRTTMillis() int64 {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.RTTMillis
}

func (c *Client) adjustCaptureQuality(capturer desktop.Capturer, current int) int {
	limit := int64(c.opts.BandwidthLimitKbps)
	if limit <= 0 {
		return current
	}
	bitrate := c.estimatedBitrateKbpsSince(c.connectedAt())
	next := current
	switch {
	case bitrate > limit && current > 25:
		next = current - 5
	case bitrate < limit*6/10 && current < c.opts.CaptureQuality:
		next = current + 2
	}
	if next == current {
		c.setState(func(s *State) {
			s.BitrateKbps = bitrate
			s.CaptureQuality = current
		})
		return current
	}
	if setter, ok := capturer.(desktop.QualitySetter); ok {
		setter.SetQuality(next)
	}
	c.setState(func(s *State) {
		s.BitrateKbps = bitrate
		s.CaptureQuality = next
	})
	c.emit("screen.quality", fmt.Sprintf("adjusted jpeg quality to %d at %dkbps", next, bitrate))
	return next
}

func (c *Client) adjustCaptureFPS(current int) int {
	maxFPS := c.opts.CaptureFPS
	if maxFPS <= 1 {
		c.setState(func(s *State) {
			s.CurrentFPS = max(1, current)
		})
		return max(1, current)
	}
	if current <= 0 {
		current = maxFPS
	}
	bitrate := c.estimatedBitrateKbpsSince(c.connectedAt())
	loss := c.packetLossPermyriad()
	rtt := c.currentRTTMillis()
	limit := int64(c.opts.BandwidthLimitKbps)
	congested := (limit > 0 && bitrate > limit) || loss >= 500 || rtt >= 350
	healthy := (limit <= 0 || bitrate < limit*6/10) && loss <= 100 && rtt < 180

	next := current
	switch {
	case congested && current > 1:
		next = current - 1
	case healthy && current < maxFPS:
		next = current + 1
	}
	c.setState(func(s *State) {
		s.CurrentFPS = next
	})
	if next != current {
		c.emit("screen.fps", fmt.Sprintf("adjusted capture fps to %d at rtt=%dms loss=%d/10000 bitrate=%dkbps", next, rtt, loss, bitrate))
	}
	return next
}

func frameInterval(fps int) time.Duration {
	if fps <= 0 {
		fps = 1
	}
	return time.Second / time.Duration(fps)
}

func (c *Client) setState(update func(*State)) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	update(&c.state)
}

func (c *Client) pendingPeerID() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.PendingPeerID
}

func (c *Client) pendingMode() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.PendingMode
}

func (c *Client) pendingInput() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.PendingInput
}

func (c *Client) activeSessionID() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.SessionID
}

func (c *Client) activeSessionMode() string {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.state.SessionMode
}

func clearPending(s *State) {
	s.PendingPeerID = ""
	s.PendingPeerName = ""
	s.PendingMode = ""
	s.PendingInput = false
	s.PendingTrusted = false
}

func clearSession(s *State) {
	s.SessionID = ""
	s.PeerID = ""
	s.PeerName = ""
	s.SessionMode = ""
	s.InputAllowed = false
}

func upsertWebShare(s *State, share protocol.WebSharePayload) {
	if share.ID == "" && share.Token == "" {
		return
	}
	for i := range s.WebShareLinks {
		if (share.ID != "" && s.WebShareLinks[i].ID == share.ID) || (share.Token != "" && s.WebShareLinks[i].Token == share.Token) {
			s.WebShareLinks[i] = share
			return
		}
	}
	s.WebShareLinks = append([]protocol.WebSharePayload{share}, s.WebShareLinks...)
}

func removeWebShare(s *State, id string, token string) {
	out := s.WebShareLinks[:0]
	for _, share := range s.WebShareLinks {
		if id != "" && share.ID == id {
			continue
		}
		if token != "" && share.Token == token {
			continue
		}
		out = append(out, share)
	}
	s.WebShareLinks = out
}

func (c *Client) recordAudit(event string, peerID string, sessionID string, mode string, detail string) {
	if c.audit == nil {
		return
	}
	if sessionID == "" {
		sessionID = c.activeSessionID()
	}
	if mode == "" {
		mode = c.activeSessionMode()
	}
	if err := c.audit.Append(audit.Entry{
		Event:     event,
		LocalID:   c.identity.DeviceID,
		PeerID:    peerID,
		SessionID: sessionID,
		Mode:      mode,
		Detail:    detail,
	}); err != nil {
		c.emit("audit.error", err.Error())
	}
}

func (c *Client) emit(eventType string, message string) {
	c.setState(func(s *State) {
		s.LastEvent = message
	})
	event := Event{Time: time.Now(), Type: eventType, Message: message}
	c.logger.Info(message, "event", eventType)
	select {
	case c.events <- event:
	default:
	}
}
