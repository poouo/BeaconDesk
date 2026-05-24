package relay

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/poouo/BeaconDesk/internal/protocol"
)

const (
	defaultWebShareTTL = time.Hour
	maxWebShareTTL     = 7 * 24 * time.Hour
)

type WebShare struct {
	ID             string
	Token          string
	ControlledID   string
	ControlledName string
	Mode           string
	Label          string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	Revoked        bool
	RevokedAt      time.Time
}

func (s *Server) handleWebShareCreate(ctx context.Context, client *Client, msg protocol.Envelope) error {
	if client.deviceID == "" {
		return fmt.Errorf("device is not registered")
	}
	payload, err := protocol.DecodePayload[protocol.WebShareCreatePayload](msg)
	if err != nil {
		return err
	}
	ttl := normalizeWebShareTTL(time.Duration(payload.TTLMillis) * time.Millisecond)
	now := time.Now()
	share := &WebShare{
		ID:             newID("wshr_", 8),
		Token:          randomWebToken(),
		ControlledID:   client.deviceID,
		ControlledName: client.deviceName,
		Mode:           protocol.NormalizeSessionMode(payload.Mode),
		Label:          strings.TrimSpace(payload.Label),
		CreatedAt:      now,
		ExpiresAt:      now.Add(ttl),
	}

	s.mu.Lock()
	s.shares[share.Token] = share
	s.mu.Unlock()

	response := protocol.MustEnvelope(protocol.TypeWebShareCreated, "relay", client.deviceID, protocol.WebShareCreatedPayload{
		Share:   s.webSharePayload(share, nil),
		Message: "web control link created; visitors still require target-side approval",
	})
	return client.conn.Write(ctx, response)
}

func (s *Server) handleWebShareList(ctx context.Context, client *Client, _ protocol.Envelope) error {
	if client.deviceID == "" {
		return fmt.Errorf("device is not registered")
	}
	payload := protocol.WebShareListResultPayload{
		Shares: s.listWebShares(client.deviceID),
	}
	msg := protocol.MustEnvelope(protocol.TypeWebShareListResult, "relay", client.deviceID, payload)
	return client.conn.Write(ctx, msg)
}

func (s *Server) handleWebShareRevoke(ctx context.Context, client *Client, msg protocol.Envelope) error {
	if client.deviceID == "" {
		return fmt.Errorf("device is not registered")
	}
	payload, err := protocol.DecodePayload[protocol.WebShareRevokePayload](msg)
	if err != nil {
		return err
	}
	if payload.ID == "" && payload.Token == "" {
		return fmt.Errorf("web share id or token is required")
	}

	var revoked *WebShare
	now := time.Now()
	s.mu.Lock()
	for token, share := range s.shares {
		if share.ControlledID != client.deviceID {
			continue
		}
		if (payload.ID != "" && share.ID == payload.ID) || (payload.Token != "" && share.Token == payload.Token) {
			share.Revoked = true
			share.RevokedAt = now
			delete(s.shares, token)
			revoked = share
			break
		}
	}
	s.mu.Unlock()
	if revoked == nil {
		return fmt.Errorf("web share link not found")
	}

	response := protocol.MustEnvelope(protocol.TypeWebShareRevoked, "relay", client.deviceID, protocol.WebShareRevokedPayload{
		ID:      revoked.ID,
		Token:   revoked.Token,
		Message: "web control link revoked",
	})
	return client.conn.Write(ctx, response)
}

func (s *Server) getActiveWebShare(token string) (*WebShare, error) {
	if token == "" {
		return nil, fmt.Errorf("share token is required")
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	share := s.shares[token]
	if share == nil {
		return nil, fmt.Errorf("share link not found")
	}
	if share.Revoked || now.After(share.ExpiresAt) {
		delete(s.shares, token)
		return nil, fmt.Errorf("share link expired or revoked")
	}
	if s.clients[share.ControlledID] == nil {
		return nil, fmt.Errorf("controlled device is offline")
	}
	cp := *share
	return &cp, nil
}

func (s *Server) listWebShares(controlledID string) []protocol.WebSharePayload {
	now := time.Now()
	var shares []*WebShare
	s.mu.Lock()
	for token, share := range s.shares {
		if share.Revoked || now.After(share.ExpiresAt) {
			delete(s.shares, token)
			continue
		}
		if share.ControlledID == controlledID {
			cp := *share
			shares = append(shares, &cp)
		}
	}
	s.mu.Unlock()

	out := make([]protocol.WebSharePayload, 0, len(shares))
	for _, share := range shares {
		out = append(out, s.webSharePayload(share, nil))
	}
	slices.SortFunc(out, func(a, b protocol.WebSharePayload) int {
		switch {
		case a.CreatedAt > b.CreatedAt:
			return -1
		case a.CreatedAt < b.CreatedAt:
			return 1
		default:
			return 0
		}
	})
	return out
}

func (s *Server) webSharePayload(share *WebShare, r *http.Request) protocol.WebSharePayload {
	return protocol.WebSharePayload{
		ID:             share.ID,
		Token:          share.Token,
		URL:            s.webShareURL(share.Token, r),
		ControlledID:   share.ControlledID,
		ControlledName: share.ControlledName,
		Mode:           share.Mode,
		Label:          share.Label,
		CreatedAt:      share.CreatedAt.UnixMilli(),
		ExpiresAt:      share.ExpiresAt.UnixMilli(),
		Revoked:        share.Revoked,
	}
}

func normalizeWebShareTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultWebShareTTL
	}
	if ttl > maxWebShareTTL {
		return maxWebShareTTL
	}
	if ttl < time.Minute {
		return time.Minute
	}
	return ttl
}

func randomWebToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) webControlPath() string {
	p := s.cfg.WebControlPath
	if p == "" {
		p = "/web"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

func (s *Server) webShareURL(token string, r *http.Request) string {
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	if base == "" && r != nil {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		host := r.Host
		if forwardedHost := r.Header.Get("X-Forwarded-Host"); forwardedHost != "" {
			host = forwardedHost
		}
		base = scheme + "://" + host
	}
	if base == "" {
		scheme := "http"
		if s.cfg.TLSCertFile != "" {
			scheme = "https"
		}
		addr := s.cfg.WebListen
		if s.cfg.Transport == "websocket" || s.cfg.Transport == "ws" || addr == "" {
			addr = s.cfg.Listen
		}
		addr = strings.TrimPrefix(addr, ":")
		if addr == "" {
			addr = "127.0.0.1"
		}
		if strings.Contains(addr, ":") {
			base = scheme + "://" + addr
		} else {
			base = scheme + "://127.0.0.1:" + addr
		}
	}
	u, err := url.Parse(base)
	if err != nil {
		return path.Join(s.webControlPath(), "s", token)
	}
	u.Path = path.Join(strings.TrimRight(u.Path, "/"), s.webControlPath(), "s", token)
	return u.String()
}
