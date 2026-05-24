package relay

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/poouo/BeaconDesk/internal/config"
	"github.com/poouo/BeaconDesk/internal/protocol"
	"github.com/poouo/BeaconDesk/internal/transport"
)

func TestValidateTargetAuthCode(t *testing.T) {
	target := &Client{
		authCode:   "123456",
		authExpiry: time.Now().Add(time.Minute),
	}

	if err := validateTargetAuthCode(target, "000000"); err == nil {
		t.Fatal("expected invalid code error")
	}
	if target.authCode == "" {
		t.Fatal("invalid attempt should not consume code")
	}
	if err := validateTargetAuthCode(target, "123456"); err != nil {
		t.Fatal(err)
	}
	if target.authCode != "" {
		t.Fatal("valid code should be consumed")
	}
}

func TestValidateTargetAuthCodeExpired(t *testing.T) {
	target := &Client{
		authCode:   "123456",
		authExpiry: time.Now().Add(-time.Second),
	}

	if err := validateTargetAuthCode(target, "123456"); err == nil {
		t.Fatal("expected expired code error")
	}
	if target.authCode != "" {
		t.Fatal("expired code should be cleared")
	}
}

func TestIsSixDigitCode(t *testing.T) {
	for _, code := range []string{"000000", "123456", "999999"} {
		if !isSixDigitCode(code) {
			t.Fatalf("%q should be valid", code)
		}
	}
	for _, code := range []string{"", "12345", "1234567", "abcdef", "12345x"} {
		if isSixDigitCode(code) {
			t.Fatalf("%q should be invalid", code)
		}
	}
}

func TestBandwidthLimiterDisabled(t *testing.T) {
	limiter := NewBandwidthLimiter(0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := limiter.Wait(ctx, 1<<20); err != nil {
		t.Fatal(err)
	}
}

func TestCloseIdleClients(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(config.RelayConfig{HeartbeatTimeout: time.Millisecond}, logger)
	c := newClient(s, transport.NewTCPConn(serverSide))
	c.deviceID = "dev_test"

	if err := s.register(c); err != nil {
		t.Fatal(err)
	}
	c.stats.Touch()
	time.Sleep(5 * time.Millisecond)
	s.closeIdleClients(time.Millisecond)

	if got := s.getClient("dev_test"); got != nil {
		t.Fatal("idle client should be unregistered")
	}
}

func TestPendingRequestControlsSessionInputPermission(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(config.RelayConfig{}, logger)
	controller := &Client{deviceID: "dev_controller"}
	controlled := &Client{deviceID: "dev_controlled"}

	s.clients[controller.deviceID] = controller
	s.clients[controlled.deviceID] = controlled
	s.storePendingRequest(controller.deviceID, controlled.deviceID, protocol.SessionModeViewControl)

	request := s.consumePendingRequest(controller.deviceID, controlled.deviceID)
	if request == nil {
		t.Fatal("pending request should be returned")
	}
	session, err := s.createSession(controller.deviceID, controlled.deviceID, request.Mode, true)
	if err != nil {
		t.Fatal(err)
	}
	if !session.InputAllowed {
		t.Fatal("input should be allowed for approved view-control request")
	}

	viewSession, err := s.createSession(controller.deviceID, controlled.deviceID, protocol.SessionModeView, true)
	if err != nil {
		t.Fatal(err)
	}
	if viewSession.InputAllowed {
		t.Fatal("input must not be allowed for view-only sessions")
	}
}
