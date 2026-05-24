package client

import (
	"time"

	"github.com/poouo/BeaconDesk/internal/protocol"
)

type Options struct {
	ServerAddress      string
	Transport          string
	UseTLS             bool
	WebSocketPath      string
	TLSServerName      string
	TLSSkipVerify      bool
	TLSCertSHA256      string
	DeviceName         string
	Role               string
	RequestMode        string
	TargetDeviceID     string
	TargetAuthCode     string
	Token              string
	IdentityPath       string
	TrustStorePath     string
	AuditLogPath       string
	AutoAccept         bool
	EnableInput        bool
	SendMockFrames     bool
	SendScreenFrames   bool
	CaptureFPS         int
	CaptureMaxWidth    int
	CaptureMaxHeight   int
	CaptureQuality     int
	BandwidthLimitKbps int
	StaticFrameSeconds int
	HeartbeatInterval  time.Duration
	ReconnectMinDelay  time.Duration
	ReconnectMaxDelay  time.Duration
	DisableReconnect   bool
}

func (o Options) withDefaults() Options {
	if o.ServerAddress == "" {
		o.ServerAddress = "127.0.0.1:8443"
	}
	if o.Transport == "" {
		o.Transport = "tcp"
	}
	if o.WebSocketPath == "" {
		o.WebSocketPath = "/ws"
	}
	if o.DeviceName == "" {
		o.DeviceName = "beacondesk-client"
	}
	if o.Role == "" {
		o.Role = "peer"
	}
	if o.RequestMode == "" {
		o.RequestMode = protocol.SessionModeViewControl
	} else {
		o.RequestMode = protocol.NormalizeSessionMode(o.RequestMode)
	}
	if o.HeartbeatInterval == 0 {
		o.HeartbeatInterval = 10 * time.Second
	}
	if o.ReconnectMinDelay == 0 {
		o.ReconnectMinDelay = time.Second
	}
	if o.ReconnectMaxDelay == 0 {
		o.ReconnectMaxDelay = 30 * time.Second
	}
	if o.ReconnectMaxDelay < o.ReconnectMinDelay {
		o.ReconnectMaxDelay = o.ReconnectMinDelay
	}
	if o.CaptureFPS <= 0 {
		o.CaptureFPS = 2
	}
	if o.CaptureMaxWidth <= 0 {
		o.CaptureMaxWidth = 1280
	}
	if o.CaptureMaxHeight <= 0 {
		o.CaptureMaxHeight = 720
	}
	if o.CaptureQuality <= 0 {
		o.CaptureQuality = 55
	}
	if o.BandwidthLimitKbps <= 0 {
		o.BandwidthLimitKbps = 2048
	}
	if o.StaticFrameSeconds <= 0 {
		o.StaticFrameSeconds = 5
	}
	return o
}
