package protocol

import (
	"encoding/json"
	"time"
)

// Envelope is the single message wrapper used across all transports.
// Payload stays as JSON so the relay can route most messages without knowing
// desktop or input implementation details.
type Envelope struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	From      string          `json:"from,omitempty"`
	To        string          `json:"to,omitempty"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(messageType string, from string, to string, payload any) (Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}

	return Envelope{
		Version:   Version,
		Type:      messageType,
		From:      from,
		To:        to,
		Timestamp: time.Now().UnixMilli(),
		Payload:   body,
	}, nil
}

func MustEnvelope(messageType string, from string, to string, payload any) Envelope {
	msg, err := NewEnvelope(messageType, from, to, payload)
	if err != nil {
		panic(err)
	}
	return msg
}

func DecodePayload[T any](msg Envelope) (T, error) {
	var out T
	if len(msg.Payload) == 0 {
		return out, nil
	}
	err := json.Unmarshal(msg.Payload, &out)
	return out, err
}

type RegisterPayload struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Role       string `json:"role"`
	Token      string `json:"token,omitempty"`
}

type RegisteredPayload struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name,omitempty"`
	ServerTime int64  `json:"server_time"`
	Message    string `json:"message"`
}

type AuthCodePublishPayload struct {
	Code      string `json:"code"`
	TTLMillis int64  `json:"ttl_ms"`
}

type AuthCodePublishedPayload struct {
	ExpiresAt int64  `json:"expires_at"`
	Message   string `json:"message"`
}

type WebShareCreatePayload struct {
	TTLMillis int64  `json:"ttl_ms"`
	Mode      string `json:"mode"`
	Label     string `json:"label,omitempty"`
}

type WebSharePayload struct {
	ID             string `json:"id"`
	Token          string `json:"token,omitempty"`
	URL            string `json:"url,omitempty"`
	ControlledID   string `json:"controlled_id"`
	ControlledName string `json:"controlled_name,omitempty"`
	Mode           string `json:"mode"`
	Label          string `json:"label,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	ExpiresAt      int64  `json:"expires_at"`
	Revoked        bool   `json:"revoked,omitempty"`
}

type WebShareCreatedPayload struct {
	Share   WebSharePayload `json:"share"`
	Message string          `json:"message"`
}

type WebShareListPayload struct{}

type WebShareListResultPayload struct {
	Shares []WebSharePayload `json:"shares"`
}

type WebShareRevokePayload struct {
	ID    string `json:"id,omitempty"`
	Token string `json:"token,omitempty"`
}

type WebShareRevokedPayload struct {
	ID      string `json:"id,omitempty"`
	Token   string `json:"token,omitempty"`
	Message string `json:"message"`
}

type WebShareStatusPayload struct {
	ID      string `json:"id,omitempty"`
	Token   string `json:"token,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type HeartbeatPayload struct {
	Sequence int64 `json:"sequence"`
	SentAt   int64 `json:"sent_at"`
}

type SessionRequestPayload struct {
	TargetDeviceID string `json:"target_device_id"`
	Mode           string `json:"mode"`
	AuthCode       string `json:"auth_code,omitempty"`
	RequesterName  string `json:"requester_name,omitempty"`
	RequesterRole  string `json:"requester_role,omitempty"`
	InputRequested bool   `json:"input_requested,omitempty"`
}

type SessionConfirmPayload struct {
	Accepted     bool   `json:"accepted"`
	Reason       string `json:"reason,omitempty"`
	AcceptedMode string `json:"accepted_mode,omitempty"`
	InputAllowed bool   `json:"input_allowed,omitempty"`
}

type SessionReadyPayload struct {
	SessionID      string `json:"session_id"`
	PeerID         string `json:"peer_id"`
	PeerName       string `json:"peer_name,omitempty"`
	Mode           string `json:"mode"`
	RelayRoute     bool   `json:"relay_route"`
	InputAllowed   bool   `json:"input_allowed"`
	LocalRole      string `json:"local_role,omitempty"`
	ShouldSendView bool   `json:"should_send_view,omitempty"`
}

type StreamFramePayload struct {
	FrameID   int64  `json:"frame_id"`
	Kind      string `json:"kind"`
	Data      string `json:"data"`
	MimeType  string `json:"mime_type,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

type InputMousePayload struct {
	X            int    `json:"x"`
	Y            int    `json:"y"`
	SourceWidth  int    `json:"source_width,omitempty"`
	SourceHeight int    `json:"source_height,omitempty"`
	Button       string `json:"button,omitempty"`
	Action       string `json:"action"`
	WheelDelta   int    `json:"wheel_delta,omitempty"`
}

type InputKeyboardPayload struct {
	Key       string   `json:"key"`
	Code      string   `json:"code,omitempty"`
	KeyCode   int      `json:"key_code,omitempty"`
	Action    string   `json:"action"`
	Modifiers []string `json:"modifiers,omitempty"`
}

type TelemetryPayload struct {
	RTTMillis       int64 `json:"rtt_ms"`
	FramesSent      int64 `json:"frames_sent"`
	FramesReceived  int64 `json:"frames_received"`
	BitrateKbps     int64 `json:"bitrate_kbps"`
	PacketLossPermy int64 `json:"packet_loss_permyriad"`
}

type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
