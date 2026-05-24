//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	coreclient "github.com/poouo/BeaconDesk/internal/client"
	"github.com/poouo/BeaconDesk/pkg/version"
)

func main() {
	headless := flag.Bool("headless", false, "run without native GUI for relay smoke tests")
	server := flag.String("server", "127.0.0.1:8443", "relay TCP address")
	transportName := flag.String("transport", "tcp", "transport: tcp or websocket")
	useTLS := flag.Bool("tls", false, "connect to the relay with native TLS")
	webSocketPath := flag.String("ws-path", "/ws", "relay WebSocket path")
	tlsServerName := flag.String("tls-server-name", "", "TLS server name override")
	tlsSkipVerify := flag.Bool("tls-skip-verify", false, "skip TLS certificate verification for local self-signed testing")
	name := flag.String("name", "beacondesk-windows", "device name")
	role := flag.String("role", "peer", "device role: controller, controlled, peer")
	requestMode := flag.String("request-mode", "view-control", "requested session mode: view or view-control")
	target := flag.String("target", "", "target device id to request after registration")
	targetCode := flag.String("target-code", "", "target temporary verification code")
	publishCode := flag.Bool("publish-code", false, "generate and publish a temporary verification code after registration")
	token := flag.String("token", "", "shared relay token")
	autoAccept := flag.Bool("auto-accept", false, "automatically accept incoming session requests")
	enableInput := flag.Bool("enable-input", false, "allow remote mouse and keyboard input after session approval")
	mockFrames := flag.Bool("mock-frames", false, "send one mock frame per second once a session is ready")
	screenFrames := flag.Bool("screen-frames", false, "send authorized real screen frames after a session is ready")
	captureFPS := flag.Int("capture-fps", 2, "screen capture frames per second")
	captureMaxWidth := flag.Int("capture-width", 1280, "maximum captured frame width")
	captureMaxHeight := flag.Int("capture-height", 720, "maximum captured frame height")
	captureQuality := flag.Int("capture-quality", 55, "JPEG quality for screen frames")
	bandwidthLimit := flag.Int("bandwidth-limit-kbps", 2048, "capture sender bandwidth target in kbps")
	staticFrameSeconds := flag.Int("static-frame-seconds", 5, "seconds between unchanged screen keepalive frames")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("beacondesk-client %s commit=%s date=%s\n", version.Version, version.Commit, version.Date)
		return
	}
	if *headless {
		runHeadless(*server, *transportName, *useTLS, *webSocketPath, *tlsServerName, *tlsSkipVerify, *name, *role, *requestMode, *target, *targetCode, *publishCode, *token, *autoAccept, *enableInput, *mockFrames, *screenFrames, *captureFPS, *captureMaxWidth, *captureMaxHeight, *captureQuality, *bandwidthLimit, *staticFrameSeconds)
		return
	}

	if err := runNativeClient(); err != nil {
		fmt.Fprintln(os.Stderr, "native app failed:", err)
		os.Exit(1)
	}
}

func runHeadless(server string, transportName string, useTLS bool, webSocketPath string, tlsServerName string, tlsSkipVerify bool, name string, role string, requestMode string, target string, targetCode string, publishCode bool, token string, autoAccept bool, enableInput bool, mockFrames bool, screenFrames bool, captureFPS int, captureMaxWidth int, captureMaxHeight int, captureQuality int, bandwidthLimit int, staticFrameSeconds int) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := coreclient.New(coreclient.Options{
		ServerAddress:      server,
		Transport:          transportName,
		UseTLS:             useTLS,
		WebSocketPath:      webSocketPath,
		TLSServerName:      tlsServerName,
		TLSSkipVerify:      tlsSkipVerify,
		DeviceName:         name,
		Role:               role,
		RequestMode:        requestMode,
		TargetDeviceID:     target,
		TargetAuthCode:     targetCode,
		Token:              token,
		IdentityPath:       ".beacondesk/" + name + ".device.json",
		AuditLogPath:       ".beacondesk/audit-log.json",
		AutoAccept:         autoAccept,
		EnableInput:        enableInput,
		SendMockFrames:     mockFrames,
		SendScreenFrames:   screenFrames,
		CaptureFPS:         captureFPS,
		CaptureMaxWidth:    captureMaxWidth,
		CaptureMaxHeight:   captureMaxHeight,
		CaptureQuality:     captureQuality,
		BandwidthLimitKbps: bandwidthLimit,
		StaticFrameSeconds: staticFrameSeconds,
		HeartbeatInterval:  5 * time.Second,
	}, logger)
	if err := c.Start(ctx); err != nil {
		logger.Error("client failed", "error", err)
		os.Exit(1)
	}
	if publishCode {
		codeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		code, err := c.GenerateTemporaryCode(codeCtx, 10*time.Minute)
		cancel()
		if err != nil {
			logger.Error("temporary code failed", "error", err)
			os.Exit(1)
		}
		logger.Info("temporary verification code generated", "code", code, "ttl", "10m")
	}

	for {
		select {
		case <-ctx.Done():
			c.Close()
			return
		case event := <-c.Events():
			state := c.State()
			fmt.Printf("%s %-20s %s device=%s session=%s rtt=%dms loss=%d/10000 kbps=%d reconnects=%d sent=%d recv=%d\n",
				event.Time.Format(time.RFC3339),
				event.Type,
				event.Message,
				state.DeviceID,
				state.SessionID,
				state.RTTMillis,
				state.PacketLossPermy,
				state.BitrateKbps,
				state.ReconnectCount,
				state.FramesSent,
				state.FramesReceived,
			)
		}
	}
}
