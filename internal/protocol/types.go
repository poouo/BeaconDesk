package protocol

const Version = 1

const (
	TypeDeviceRegister     = "device.register"
	TypeDeviceRegistered   = "device.registered"
	TypeAuthCodePublish    = "auth.code.publish"
	TypeAuthCodePublished  = "auth.code.published"
	TypeWebShareCreate     = "web.share.create"
	TypeWebShareCreated    = "web.share.created"
	TypeWebShareList       = "web.share.list"
	TypeWebShareListResult = "web.share.list.result"
	TypeWebShareRevoke     = "web.share.revoke"
	TypeWebShareRevoked    = "web.share.revoked"
	TypeWebShareStatus     = "web.share.status"
	TypeHeartbeatPing      = "heartbeat.ping"
	TypeHeartbeatPong      = "heartbeat.pong"
	TypeSessionRequest     = "session.request"
	TypeSessionConfirm     = "session.confirm"
	TypeSessionReady       = "session.ready"
	TypeSessionClose       = "session.close"
	TypeStreamFrame        = "stream.frame"
	TypeStreamControl      = "stream.control"
	TypeInputMouse         = "input.mouse"
	TypeInputKeyboard      = "input.keyboard"
	TypeTelemetryStats     = "telemetry.stats"
	TypeError              = "error"
)

const (
	StreamKindJPEG   = "jpeg"
	StreamKindStatus = "status"
	StreamKindError  = "error"
)

const (
	RoleController = "controller"
	RoleControlled = "controlled"
	RolePeer       = "peer"
)

const (
	SessionModeView        = "view"
	SessionModeViewControl = "view-control"
)

func NormalizeSessionMode(mode string) string {
	switch mode {
	case SessionModeViewControl:
		return SessionModeViewControl
	case SessionModeView, "":
		return SessionModeView
	default:
		return SessionModeView
	}
}

func SessionModeAllowsInput(mode string) bool {
	return NormalizeSessionMode(mode) == SessionModeViewControl
}
