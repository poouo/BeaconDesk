package relay

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/poouo/BeaconDesk/internal/auth"
	"github.com/poouo/BeaconDesk/internal/config"
	"github.com/poouo/BeaconDesk/internal/protocol"
	"github.com/poouo/BeaconDesk/internal/transport"
)

type Server struct {
	cfg      config.RelayConfig
	auth     auth.TokenValidator
	logger   *slog.Logger
	clients  map[string]*Client
	webPeers map[string]*WebPeer
	sessions map[string]*Session
	pending  map[string]*PendingRequest
	shares   map[string]*WebShare
	mu       sync.RWMutex
}

func NewServer(cfg config.RelayConfig, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:      cfg,
		auth:     auth.NewTokenValidator(cfg.SharedToken),
		logger:   logger,
		clients:  make(map[string]*Client),
		webPeers: make(map[string]*WebPeer),
		sessions: make(map[string]*Session),
		pending:  make(map[string]*PendingRequest),
		shares:   make(map[string]*WebShare),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if s.cfg.Transport == "websocket" || s.cfg.Transport == "ws" {
		return s.serveWebSocket(ctx)
	}
	if s.cfg.WebControlEnabled && s.cfg.WebListen != "" {
		go func() {
			if err := s.serveWebControl(ctx); err != nil {
				s.logger.Error("web control server stopped", "error", err)
			}
		}()
	}
	listener, err := s.listen()
	if err != nil {
		return err
	}
	defer listener.Close()

	s.logger.Info("relay server listening", "address", listener.Addr().String())
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	go s.reaperLoop(ctx)

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.logger.Warn("accept failed", "error", err)
			continue
		}
		client := newClient(s, transport.NewTCPConn(conn))
		go client.run(ctx)
	}
}

func (s *Server) serveWebSocket(ctx context.Context) error {
	path := s.cfg.WebSocketPath
	if path == "" {
		path = "/ws"
	}
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			s.logger.Warn("websocket upgrade failed", "error", err)
			return
		}
		conn.SetReadLimit(protocol.MaxLineBytes)
		client := newClient(s, transport.NewWebSocketConn(conn))
		go client.run(ctx)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	s.mountWebControl(mux, upgrader)

	server := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	go s.reaperLoop(ctx)

	s.logger.Info("relay websocket server listening", "address", s.cfg.Listen, "path", path)
	if s.cfg.TLSCertFile != "" || s.cfg.TLSKeyFile != "" {
		if s.cfg.TLSCertFile == "" || s.cfg.TLSKeyFile == "" {
			return fmt.Errorf("both tls_cert_file and tls_key_file are required for TLS")
		}
		if err := server.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	if !s.cfg.AllowInsecurePlaintext {
		return fmt.Errorf("plain WebSocket is disabled but tls_cert_file/tls_key_file are not configured")
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) serveWebControl(ctx context.Context) error {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	s.mountWebControl(mux, upgrader)
	server := &http.Server{
		Addr:              s.cfg.WebListen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	s.logger.Info("relay web control server listening", "address", s.cfg.WebListen, "path", s.webControlPath())
	if s.cfg.TLSCertFile != "" || s.cfg.TLSKeyFile != "" {
		if s.cfg.TLSCertFile == "" || s.cfg.TLSKeyFile == "" {
			return fmt.Errorf("both tls_cert_file and tls_key_file are required for TLS")
		}
		if err := server.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	if !s.cfg.AllowInsecurePlaintext {
		return fmt.Errorf("plain web control is disabled but tls_cert_file/tls_key_file are not configured")
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) reaperLoop(ctx context.Context) {
	timeout := s.cfg.HeartbeatTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	interval := timeout / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.closeIdleClients(timeout)
		}
	}
}

func (s *Server) closeIdleClients(timeout time.Duration) {
	now := time.Now()
	var stale []*Client
	s.mu.RLock()
	for _, client := range s.clients {
		if now.Sub(client.stats.LastMessageAt()) > timeout {
			stale = append(stale, client)
		}
	}
	s.mu.RUnlock()

	for _, client := range stale {
		client.logger.Warn("closing idle client", "device_id", client.deviceID, "timeout", timeout)
		client.close()
	}
}

func (s *Server) listen() (net.Listener, error) {
	if s.cfg.TLSCertFile == "" && s.cfg.TLSKeyFile == "" {
		if !s.cfg.AllowInsecurePlaintext {
			return nil, fmt.Errorf("plain TCP is disabled but tls_cert_file/tls_key_file are not configured")
		}
		return net.Listen("tcp", s.cfg.Listen)
	}
	if s.cfg.TLSCertFile == "" || s.cfg.TLSKeyFile == "" {
		return nil, fmt.Errorf("both tls_cert_file and tls_key_file are required for TLS")
	}

	cert, err := tls.LoadX509KeyPair(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	return tls.Listen("tcp", s.cfg.Listen, cfg)
}

func (s *Server) handleMessage(ctx context.Context, client *Client, msg protocol.Envelope) error {
	switch msg.Type {
	case protocol.TypeDeviceRegister:
		return s.handleRegister(ctx, client, msg)
	case protocol.TypeAuthCodePublish:
		return s.handleAuthCodePublish(ctx, client, msg)
	case protocol.TypeWebShareCreate:
		return s.handleWebShareCreate(ctx, client, msg)
	case protocol.TypeWebShareList:
		return s.handleWebShareList(ctx, client, msg)
	case protocol.TypeWebShareRevoke:
		return s.handleWebShareRevoke(ctx, client, msg)
	case protocol.TypeHeartbeatPing:
		return s.handlePing(ctx, client, msg)
	case protocol.TypeHeartbeatPong:
		return nil
	case protocol.TypeSessionRequest:
		return s.handleSessionRequest(client, msg)
	case protocol.TypeSessionConfirm:
		return s.handleSessionConfirm(client, msg)
	case protocol.TypeStreamFrame:
		return s.forwardSessionMessage(client, msg)
	case protocol.TypeInputMouse:
		return s.forwardInputMessage(client, msg)
	case protocol.TypeInputKeyboard:
		return s.forwardInputMessage(client, msg)
	case protocol.TypeTelemetryStats:
		return s.forwardSessionMessage(client, msg)
	case protocol.TypeSessionClose:
		return s.handleSessionClose(client, msg)
	default:
		return fmt.Errorf("unsupported message type %q", msg.Type)
	}
}

func (s *Server) handleAuthCodePublish(ctx context.Context, client *Client, msg protocol.Envelope) error {
	if client.deviceID == "" {
		return fmt.Errorf("device is not registered")
	}
	payload, err := protocol.DecodePayload[protocol.AuthCodePublishPayload](msg)
	if err != nil {
		return err
	}
	if payload.Code == "" {
		return fmt.Errorf("auth code is required")
	}
	if !isSixDigitCode(payload.Code) {
		return fmt.Errorf("auth code must be 6 digits")
	}
	ttl := time.Duration(payload.TTLMillis) * time.Millisecond
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if ttl > 30*time.Minute {
		ttl = 30 * time.Minute
	}

	expiresAt := time.Now().Add(ttl)
	client.authMu.Lock()
	client.authCode = payload.Code
	client.authExpiry = expiresAt
	client.authMu.Unlock()

	published := protocol.MustEnvelope(protocol.TypeAuthCodePublished, "relay", client.deviceID, protocol.AuthCodePublishedPayload{
		ExpiresAt: expiresAt.UnixMilli(),
		Message:   "temporary code published",
	})
	return client.conn.Write(ctx, published)
}

func (s *Server) handleRegister(ctx context.Context, client *Client, msg protocol.Envelope) error {
	payload, err := protocol.DecodePayload[protocol.RegisterPayload](msg)
	if err != nil {
		return err
	}
	if payload.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if !s.auth.Validate(payload.Token) {
		return fmt.Errorf("invalid token")
	}

	client.deviceID = payload.DeviceID
	client.deviceName = payload.DeviceName
	client.role = payload.Role
	if client.role == "" {
		client.role = protocol.RolePeer
	}
	if err := s.register(client); err != nil {
		return err
	}

	registered := protocol.MustEnvelope(protocol.TypeDeviceRegistered, "relay", client.deviceID, protocol.RegisteredPayload{
		DeviceID:   client.deviceID,
		ServerTime: time.Now().UnixMilli(),
		Message:    "registered",
	})
	return client.conn.Write(ctx, registered)
}

func (s *Server) handlePing(ctx context.Context, client *Client, msg protocol.Envelope) error {
	payload, err := protocol.DecodePayload[protocol.HeartbeatPayload](msg)
	if err != nil {
		return err
	}
	pong := protocol.MustEnvelope(protocol.TypeHeartbeatPong, "relay", client.deviceID, protocol.HeartbeatPayload{
		Sequence: payload.Sequence,
		SentAt:   payload.SentAt,
	})
	return client.conn.Write(ctx, pong)
}

func (s *Server) handleSessionRequest(client *Client, msg protocol.Envelope) error {
	if client.deviceID == "" {
		return fmt.Errorf("device is not registered")
	}
	payload, err := protocol.DecodePayload[protocol.SessionRequestPayload](msg)
	if err != nil {
		return err
	}
	if payload.TargetDeviceID == "" {
		return fmt.Errorf("target_device_id is required")
	}
	target := s.getClient(payload.TargetDeviceID)
	if target == nil {
		return fmt.Errorf("target device %s is offline", payload.TargetDeviceID)
	}
	if err := validateTargetAuthCode(target, payload.AuthCode); err != nil {
		return err
	}
	mode := protocol.NormalizeSessionMode(payload.Mode)
	s.storePendingRequest(client.deviceID, payload.TargetDeviceID, mode)
	forwarded := msg
	forwarded.From = client.deviceID
	forwarded.To = payload.TargetDeviceID
	forwarded.Payload = protocol.MustEnvelope(protocol.TypeSessionRequest, client.deviceID, payload.TargetDeviceID, protocol.SessionRequestPayload{
		TargetDeviceID: payload.TargetDeviceID,
		Mode:           mode,
		AuthCode:       "",
		RequesterName:  client.deviceName,
		RequesterRole:  client.role,
		InputRequested: protocol.SessionModeAllowsInput(mode),
	}).Payload
	return sendOrError(target, forwarded)
}

func validateTargetAuthCode(target *Client, code string) error {
	target.authMu.Lock()
	defer target.authMu.Unlock()
	if target.authCode == "" {
		return nil
	}
	if time.Now().After(target.authExpiry) {
		target.authCode = ""
		target.authExpiry = time.Time{}
		return fmt.Errorf("target temporary code expired")
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(target.authCode)) != 1 {
		return fmt.Errorf("invalid target temporary code")
	}
	target.authCode = ""
	target.authExpiry = time.Time{}
	return nil
}

func isSixDigitCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func (s *Server) handleSessionConfirm(client *Client, msg protocol.Envelope) error {
	if client.deviceID == "" {
		return fmt.Errorf("device is not registered")
	}
	payload, err := protocol.DecodePayload[protocol.SessionConfirmPayload](msg)
	if err != nil {
		return err
	}
	controllerID := msg.To
	if controllerID == "" {
		controllerID = msg.From
	}
	controller := s.getClient(controllerID)
	webPeer := s.getWebPeer(controllerID)
	if controller == nil && webPeer == nil {
		return fmt.Errorf("controller %s is offline", controllerID)
	}
	if !payload.Accepted {
		s.consumePendingRequest(controllerID, client.deviceID)
		declined := msg
		declined.From = client.deviceID
		declined.To = controllerID
		return s.sendToPeer(controllerID, declined)
	}

	request := s.consumePendingRequest(controllerID, client.deviceID)
	mode := protocol.NormalizeSessionMode(payload.AcceptedMode)
	if request != nil {
		mode = request.Mode
	}
	inputAllowed := payload.InputAllowed && protocol.SessionModeAllowsInput(mode)
	session, err := s.createSession(controllerID, client.deviceID, mode, inputAllowed)
	if err != nil {
		return err
	}

	toController := protocol.MustEnvelope(protocol.TypeSessionReady, "relay", controllerID, protocol.SessionReadyPayload{
		SessionID:    session.ID,
		PeerID:       client.deviceID,
		PeerName:     client.deviceName,
		Mode:         session.Mode,
		RelayRoute:   true,
		InputAllowed: session.InputAllowed,
	})
	toController.SessionID = session.ID
	var targetPeerID, targetPeerName string
	if controller != nil {
		targetPeerID = controller.deviceID
		targetPeerName = controller.deviceName
	} else {
		targetPeerID = webPeer.id
		targetPeerName = webPeer.name
	}
	toTarget := protocol.MustEnvelope(protocol.TypeSessionReady, "relay", client.deviceID, protocol.SessionReadyPayload{
		SessionID:    session.ID,
		PeerID:       targetPeerID,
		PeerName:     targetPeerName,
		Mode:         session.Mode,
		RelayRoute:   true,
		InputAllowed: session.InputAllowed,
	})
	toTarget.SessionID = session.ID

	_ = s.sendToPeer(controllerID, toController)
	return sendOrError(client, toTarget)
}

func (s *Server) forwardSessionMessage(client *Client, msg protocol.Envelope) error {
	session := s.getSession(msg.SessionID)
	if session == nil {
		return fmt.Errorf("session %s not found", msg.SessionID)
	}
	var targetID string
	switch client.deviceID {
	case session.ControllerID:
		targetID = session.ControlledID
	case session.ControlledID:
		targetID = session.ControllerID
	default:
		return fmt.Errorf("device %s is not part of session %s", client.deviceID, session.ID)
	}
	target := s.getClient(targetID)
	if target == nil {
		if webPeer := s.getWebPeer(targetID); webPeer != nil {
			msg.From = client.deviceID
			msg.To = targetID
			s.touchSession(session.ID)
			return sendWebOrError(webPeer, msg)
		}
		return fmt.Errorf("session peer %s is offline", targetID)
	}
	msg.From = client.deviceID
	msg.To = targetID
	s.touchSession(session.ID)
	return sendOrError(target, msg)
}

func (s *Server) forwardInputMessage(client *Client, msg protocol.Envelope) error {
	session := s.getSession(msg.SessionID)
	if session == nil {
		return fmt.Errorf("session %s not found", msg.SessionID)
	}
	if !session.InputAllowed {
		return fmt.Errorf("remote input is not allowed for session %s", session.ID)
	}
	if client.deviceID != session.ControllerID {
		return fmt.Errorf("only controller can send input for session %s", session.ID)
	}
	return s.forwardSessionMessage(client, msg)
}

func (s *Server) handleSessionClose(client *Client, msg protocol.Envelope) error {
	if msg.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	session := s.sessions[msg.SessionID]
	if session != nil {
		delete(s.sessions, msg.SessionID)
		s.notifySessionClosedLocked(session, "closed by peer")
	}
	s.mu.Unlock()
	return nil
}

func (s *Server) sendToPeer(deviceID string, msg protocol.Envelope) error {
	if client := s.getClient(deviceID); client != nil {
		return sendOrError(client, msg)
	}
	if peer := s.getWebPeer(deviceID); peer != nil {
		return sendWebOrError(peer, msg)
	}
	return fmt.Errorf("peer %s is offline", deviceID)
}

func sendOrError(client *Client, msg protocol.Envelope) error {
	if client.trySend(msg) {
		return nil
	}
	return fmt.Errorf("failed to queue message for %s", client.deviceID)
}

func sendWebOrError(peer *WebPeer, msg protocol.Envelope) error {
	if peer.trySend(msg) {
		return nil
	}
	return fmt.Errorf("failed to queue message for %s", peer.id)
}
