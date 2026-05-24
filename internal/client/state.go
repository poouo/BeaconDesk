package client

import (
	"time"

	"github.com/poouo/BeaconDesk/internal/protocol"
)

type State struct {
	Connected        bool                       `json:"connected"`
	Reconnecting     bool                       `json:"reconnecting"`
	ReconnectCount   int64                      `json:"reconnect_count"`
	Registered       bool                       `json:"registered"`
	DeviceID         string                     `json:"device_id"`
	DeviceName       string                     `json:"device_name"`
	Role             string                     `json:"role"`
	SessionID        string                     `json:"session_id"`
	PeerID           string                     `json:"peer_id"`
	PeerName         string                     `json:"peer_name,omitempty"`
	SessionMode      string                     `json:"session_mode,omitempty"`
	SessionLocalRole string                     `json:"session_local_role,omitempty"`
	ShouldSendView   bool                       `json:"should_send_view,omitempty"`
	InputAllowed     bool                       `json:"input_allowed"`
	PendingPeerID    string                     `json:"pending_peer_id,omitempty"`
	PendingPeerName  string                     `json:"pending_peer_name,omitempty"`
	PendingMode      string                     `json:"pending_mode,omitempty"`
	PendingInput     bool                       `json:"pending_input,omitempty"`
	PendingTrusted   bool                       `json:"pending_trusted,omitempty"`
	LocalAuthCode    string                     `json:"local_auth_code,omitempty"`
	AuthCodeExpiry   int64                      `json:"auth_code_expiry,omitempty"`
	WebShareLinks    []protocol.WebSharePayload `json:"web_share_links,omitempty"`
	LastEvent        string                     `json:"last_event"`
	RTTMillis        int64                      `json:"rtt_ms"`
	PacketLossPermy  int64                      `json:"packet_loss_permyriad"`
	FramesSent       int64                      `json:"frames_sent"`
	FramesReceived   int64                      `json:"frames_received"`
	BitrateKbps      int64                      `json:"bitrate_kbps"`
	CaptureQuality   int                        `json:"capture_quality,omitempty"`
	CurrentFPS       int                        `json:"current_fps,omitempty"`
	LastFrameData    string                     `json:"last_frame_data,omitempty"`
	LastFrameKind    string                     `json:"last_frame_kind,omitempty"`
	LastFrameStatus  string                     `json:"last_frame_status,omitempty"`
	LastFrameError   string                     `json:"last_frame_error,omitempty"`
	LastFrameWidth   int                        `json:"last_frame_width,omitempty"`
	LastFrameHeight  int                        `json:"last_frame_height,omitempty"`
	LastMessageAt    time.Time                  `json:"last_message_at"`
	ConnectedAt      time.Time                  `json:"connected_at"`
}

type Event struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
}
