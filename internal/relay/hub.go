package relay

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/poouo/BeaconDesk/internal/protocol"
)

func newID(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

func (s *Server) register(client *Client) error {
	if client.deviceID == "" {
		return errors.New("client has no device id")
	}

	var old *Client
	s.mu.Lock()
	if s.cfg.MaxClients > 0 && len(s.clients) >= s.cfg.MaxClients {
		s.mu.Unlock()
		return fmt.Errorf("max clients reached: %d", s.cfg.MaxClients)
	}
	if existing := s.clients[client.deviceID]; existing != nil && existing != client {
		old = existing
		delete(s.clients, client.deviceID)
	}
	s.clients[client.deviceID] = client
	s.mu.Unlock()

	if old != nil {
		old.close()
	}
	s.logger.Info("device registered", "device_id", client.deviceID, "name", client.deviceName, "role", client.role)
	return nil
}

func (s *Server) unregister(client *Client) {
	if client.deviceID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients[client.deviceID] == client {
		delete(s.clients, client.deviceID)
		s.logger.Info("device unregistered", "device_id", client.deviceID)
	}
	for id, session := range s.sessions {
		if session.ControllerID == client.deviceID || session.ControlledID == client.deviceID {
			delete(s.sessions, id)
			s.notifySessionClosedLocked(session, "peer disconnected")
		}
	}
	for key, request := range s.pending {
		if request.ControllerID == client.deviceID || request.ControlledID == client.deviceID {
			delete(s.pending, key)
		}
	}
	for _, share := range s.shares {
		if share.ControlledID == client.deviceID {
			share.Revoked = true
			share.RevokedAt = time.Now()
		}
	}
}

func (s *Server) getClient(deviceID string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clients[deviceID]
}

func (s *Server) getWebPeer(deviceID string) *WebPeer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webPeers[deviceID]
}

func (s *Server) createSession(controllerID string, controlledID string, mode string, inputAllowed bool) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clients[controllerID] == nil && s.webPeers[controllerID] == nil {
		return nil, fmt.Errorf("controller %s is offline", controllerID)
	}
	if s.clients[controlledID] == nil {
		return nil, fmt.Errorf("controlled device %s is offline", controlledID)
	}
	session := &Session{
		ID:             newID("sess_", 12),
		ControllerID:   controllerID,
		ControlledID:   controlledID,
		Mode:           protocol.NormalizeSessionMode(mode),
		InputAllowed:   inputAllowed && protocol.SessionModeAllowsInput(mode),
		CreatedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}
	s.sessions[session.ID] = session
	return session, nil
}

func pendingKey(controllerID string, controlledID string) string {
	return controllerID + "\x00" + controlledID
}

func (s *Server) storePendingRequest(controllerID string, controlledID string, mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[pendingKey(controllerID, controlledID)] = &PendingRequest{
		ControllerID: controllerID,
		ControlledID: controlledID,
		Mode:         protocol.NormalizeSessionMode(mode),
		RequestedAt:  time.Now(),
	}
}

func (s *Server) consumePendingRequest(controllerID string, controlledID string) *PendingRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pendingKey(controllerID, controlledID)
	request := s.pending[key]
	delete(s.pending, key)
	return request
}

func (s *Server) getSession(sessionID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[sessionID]
}

func (s *Server) touchSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session := s.sessions[sessionID]; session != nil {
		session.Touch()
	}
}

func (s *Server) notifySessionClosedLocked(session *Session, reason string) {
	payload := map[string]string{
		"session_id": session.ID,
		"reason":     reason,
	}
	if c := s.clients[session.ControllerID]; c != nil {
		c.trySend(protocolEnvelopeSessionClose(session.ID, "relay", session.ControllerID, payload))
	} else if p := s.webPeers[session.ControllerID]; p != nil {
		p.trySend(protocolEnvelopeSessionClose(session.ID, "relay", session.ControllerID, payload))
	}
	if c := s.clients[session.ControlledID]; c != nil {
		c.trySend(protocolEnvelopeSessionClose(session.ID, "relay", session.ControlledID, payload))
	}
}

func protocolEnvelopeSessionClose(sessionID string, from string, to string, payload any) protocol.Envelope {
	msg := protocol.MustEnvelope(protocol.TypeSessionClose, from, to, payload)
	msg.SessionID = sessionID
	return msg
}
