package relay

import (
	"context"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/poouo/BeaconDesk/internal/protocol"
	"github.com/poouo/BeaconDesk/internal/transport"
)

type WebPeer struct {
	server     *Server
	conn       *transport.WebSocketConn
	id         string
	name       string
	shareToken string
	shareID    string
	controlled string
	mode       string
	send       chan protocol.Envelope
	closed     chan struct{}
}

func newWebPeer(server *Server, conn *websocket.Conn, share *WebShare) *WebPeer {
	return &WebPeer{
		server:     server,
		conn:       transport.NewWebSocketConn(conn),
		id:         newID("web_", 8),
		name:       "Web visitor",
		shareToken: share.Token,
		shareID:    share.ID,
		controlled: share.ControlledID,
		mode:       protocol.NormalizeSessionMode(share.Mode),
		send:       make(chan protocol.Envelope, 128),
		closed:     make(chan struct{}),
	}
}

func (p *WebPeer) run(ctx context.Context) {
	defer p.close()
	go p.writeLoop(ctx)
	if err := p.requestControlledApproval(); err != nil {
		p.trySend(protocol.MustEnvelope(protocol.TypeError, "relay", p.id, protocol.ErrorPayload{
			Code:    "web_request_failed",
			Message: err.Error(),
		}))
		return
	}
	p.trySend(protocol.MustEnvelope(protocol.TypeWebShareStatus, "relay", p.id, protocol.WebShareStatusPayload{
		ID:      p.shareID,
		Token:   p.shareToken,
		Status:  "waiting",
		Message: "waiting for controlled-side approval",
	}))

	for {
		msg, err := p.conn.Read(ctx)
		if err != nil {
			return
		}
		if err := p.handleMessage(msg); err != nil {
			p.trySend(protocol.MustEnvelope(protocol.TypeError, "relay", p.id, protocol.ErrorPayload{
				Code:    "web_message_failed",
				Message: err.Error(),
			}))
		}
	}
}

func (p *WebPeer) requestControlledApproval() error {
	if _, err := p.server.getActiveWebShare(p.shareToken); err != nil {
		return err
	}
	target := p.server.getClient(p.controlled)
	if target == nil {
		return fmt.Errorf("controlled device is offline")
	}
	p.server.mu.Lock()
	p.server.webPeers[p.id] = p
	p.server.mu.Unlock()

	p.server.storePendingRequest(p.id, p.controlled, p.mode)
	request := protocol.MustEnvelope(protocol.TypeSessionRequest, p.id, p.controlled, protocol.SessionRequestPayload{
		TargetDeviceID: p.controlled,
		Mode:           p.mode,
		RequesterName:  p.name,
		RequesterRole:  protocol.RoleController,
		InputRequested: protocol.SessionModeAllowsInput(p.mode),
	})
	return sendOrError(target, request)
}

func (p *WebPeer) handleMessage(msg protocol.Envelope) error {
	switch msg.Type {
	case protocol.TypeInputMouse, protocol.TypeInputKeyboard:
		return p.server.forwardWebInputMessage(p, msg)
	case protocol.TypeStreamControl:
		return p.server.forwardWebStreamControlMessage(p, msg)
	case protocol.TypeSessionClose:
		return p.server.handleWebSessionClose(p, msg)
	case protocol.TypeHeartbeatPing:
		payload, err := protocol.DecodePayload[protocol.HeartbeatPayload](msg)
		if err != nil {
			return err
		}
		return sendWebOrError(p, protocol.MustEnvelope(protocol.TypeHeartbeatPong, "relay", p.id, payload))
	default:
		return fmt.Errorf("unsupported web message type %q", msg.Type)
	}
}

func (p *WebPeer) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.closed:
			return
		case msg := <-p.send:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := p.conn.Write(writeCtx, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (p *WebPeer) trySend(msg protocol.Envelope) bool {
	select {
	case p.send <- msg:
		return true
	case <-p.closed:
		return false
	default:
		return false
	}
}

func (p *WebPeer) close() {
	select {
	case <-p.closed:
		return
	default:
		close(p.closed)
	}
	_ = p.conn.Close()
	p.server.unregisterWebPeer(p)
}

func (s *Server) unregisterWebPeer(peer *WebPeer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webPeers[peer.id] == peer {
		delete(s.webPeers, peer.id)
	}
	for id, session := range s.sessions {
		if session.ControllerID == peer.id || session.ControlledID == peer.id {
			delete(s.sessions, id)
			s.notifySessionClosedLocked(session, "web peer disconnected")
		}
	}
	for key, request := range s.pending {
		if request.ControllerID == peer.id || request.ControlledID == peer.id {
			delete(s.pending, key)
		}
	}
}

func (s *Server) forwardWebInputMessage(peer *WebPeer, msg protocol.Envelope) error {
	session := s.getSession(msg.SessionID)
	if session == nil {
		return fmt.Errorf("session %s not found", msg.SessionID)
	}
	if !session.InputAllowed {
		return fmt.Errorf("remote input is not allowed for session %s", session.ID)
	}
	if peer.id != session.ControllerID {
		return fmt.Errorf("only controller can send input for session %s", session.ID)
	}
	target := s.getClient(session.ControlledID)
	if target == nil {
		return fmt.Errorf("controlled device %s is offline", session.ControlledID)
	}
	msg.From = peer.id
	msg.To = session.ControlledID
	s.touchSession(session.ID)
	return sendOrError(target, msg)
}

func (s *Server) forwardWebStreamControlMessage(peer *WebPeer, msg protocol.Envelope) error {
	session := s.getSession(msg.SessionID)
	if session == nil {
		return fmt.Errorf("session %s not found", msg.SessionID)
	}
	if peer.id != session.ControllerID {
		return fmt.Errorf("only controller can update stream controls for session %s", session.ID)
	}
	target := s.getClient(session.ControlledID)
	if target == nil {
		return fmt.Errorf("controlled device %s is offline", session.ControlledID)
	}
	msg.From = peer.id
	msg.To = session.ControlledID
	s.touchSession(session.ID)
	return sendOrError(target, msg)
}

func (s *Server) handleWebSessionClose(peer *WebPeer, msg protocol.Envelope) error {
	if msg.SessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	s.mu.Lock()
	session := s.sessions[msg.SessionID]
	if session != nil {
		delete(s.sessions, msg.SessionID)
		s.notifySessionClosedLocked(session, "closed by web peer")
	}
	s.mu.Unlock()
	return nil
}
