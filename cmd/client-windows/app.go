//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/poouo/BeaconDesk/internal/audit"
	coreclient "github.com/poouo/BeaconDesk/internal/client"
	"github.com/poouo/BeaconDesk/internal/input"
	"github.com/poouo/BeaconDesk/internal/protocol"
	"github.com/poouo/BeaconDesk/internal/trust"
	"github.com/poouo/BeaconDesk/internal/updatecheck"
	"github.com/poouo/BeaconDesk/pkg/version"
	"golang.org/x/sys/windows/registry"
)

type uiSettings struct {
	Language           string
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
	Token              string
	AutoStart          bool
	AutoAccept         bool
	EnableInput        bool
	WebShareTTLMinutes int
	SendMockFrames     bool
	SendScreenFrames   bool
	CaptureFPS         int
	CaptureMaxWidth    int
	CaptureMaxHeight   int
	CaptureQuality     int
	BandwidthLimitKbps int
	StaticFrameSeconds int
}

func defaultUISettings() uiSettings {
	return uiSettings{
		Language:           "zh-CN",
		ServerAddress:      "127.0.0.1:8443",
		Transport:          "websocket",
		UseTLS:             true,
		WebSocketPath:      "/ws",
		DeviceName:         "beacondesk-windows",
		Role:               "peer",
		RequestMode:        "view-control",
		WebShareTTLMinutes: 60,
		CaptureFPS:         15,
		CaptureMaxWidth:    1280,
		CaptureMaxHeight:   720,
		CaptureQuality:     55,
		BandwidthLimitKbps: 2048,
		StaticFrameSeconds: 5,
	}
}

type nativeApp struct {
	window *app.Window
	ops    op.Ops
	theme  *material.Theme

	ctx     context.Context
	cancel  context.CancelFunc
	logger  *slog.Logger
	logFile *os.File

	english atomic.Bool

	mu             sync.Mutex
	renderMu       sync.Mutex
	client         *coreclient.Client
	settings       uiSettings
	state          coreclient.State
	events         []string
	statusMessage  string
	statusIsError  bool
	requesting     bool
	updateChecking bool
	updateMessage  string
	updateURL      string
	updateIsError  bool
	lastFrameData  string
	lastFrameImage paint.ImageOp
	lastFrameSize  image.Point
	lastFrameError string

	targetEdit     widget.Editor
	targetCodeEdit widget.Editor
	connectButton  widget.Clickable
	codeButton     widget.Clickable
	disconnectBtn  widget.Clickable
	requestButton  widget.Clickable
	settingsButton widget.Clickable
	remoteButton   widget.Clickable
	approveButton  widget.Clickable
	rememberButton widget.Clickable
	rejectButton   widget.Clickable
	eventsList     widget.List

	showSettings   bool
	settingsPage   int
	settingsSaving bool
	webShareBusy   bool
	settingsDraft  uiSettings
	settingsUI     settingsWidgets

	remote *remoteWindow
}

type settingsWidgets struct {
	navButtons    [7]widget.Clickable
	saveButton    widget.Clickable
	cancelBtn     widget.Clickable
	refreshBtn    widget.Clickable
	revokeBtn     widget.Clickable
	clearBtn      widget.Clickable
	updateBtn     widget.Clickable
	releaseBtn    widget.Clickable
	webCreateBtn  widget.Clickable
	webRefreshBtn widget.Clickable
	webRevokeBtn  widget.Clickable
	webCopyBtn    widget.Clickable
	webOpenBtn    widget.Clickable

	language      widget.Enum
	transport     widget.Enum
	role          widget.Enum
	mode          widget.Enum
	fpsPreset     widget.Enum
	resPreset     widget.Enum
	qualityPreset widget.Enum
	bitratePreset widget.Enum
	staticPreset  widget.Enum

	serverEdit  widget.Editor
	wsPathEdit  widget.Editor
	tlsNameEdit widget.Editor
	tlsPinEdit  widget.Editor
	nameEdit    widget.Editor
	tokenEdit   widget.Editor
	webTTLEdit  widget.Editor

	tlsCheck          widget.Bool
	skipTLSCheck      widget.Bool
	autoStartCheck    widget.Bool
	autoAcceptCheck   widget.Bool
	inputCheck        widget.Bool
	mockFramesCheck   widget.Bool
	screenFramesCheck widget.Bool

	trustedList    widget.List
	auditList      widget.List
	trustedRows    []trust.Device
	auditRows      []audit.Entry
	selectedTrust  int
	trustRowClicks []widget.Clickable

	webShareList      widget.List
	webShareRows      []protocol.WebSharePayload
	selectedWebShare  int
	webShareRowClicks []widget.Clickable
}

type remoteWindow struct {
	app        *nativeApp
	window     *app.Window
	ops        op.Ops
	theme      *material.Theme
	pointerTag struct{}
	keyTag     struct{}
	closeBtn   widget.Clickable
	lastButton string
}

type appColors struct {
	bg       color.NRGBA
	panel    color.NRGBA
	panel2   color.NRGBA
	card     color.NRGBA
	text     color.NRGBA
	muted    color.NRGBA
	border   color.NRGBA
	primary  color.NRGBA
	primary2 color.NRGBA
	success  color.NRGBA
	warning  color.NRGBA
	danger   color.NRGBA
	ink      color.NRGBA
}

var palette = appColors{
	bg:       rgb(0xf3f6fb),
	panel:    rgb(0xffffff),
	panel2:   rgb(0xeef5ff),
	card:     rgb(0xffffff),
	text:     rgb(0x16202a),
	muted:    rgb(0x475467),
	border:   rgb(0xcbd5e1),
	primary:  rgb(0x2563eb),
	primary2: rgb(0x0f9f8e),
	success:  rgb(0x16a34a),
	warning:  rgb(0xd97706),
	danger:   rgb(0xdc2626),
	ink:      rgb(0x0b1220),
}

func runNativeClient() error {
	ctx, cancel := context.WithCancel(context.Background())
	a := newNativeApp(ctx, cancel)
	a.safeGo("main-window", func() {
		if err := a.run(); err != nil {
			a.logger.Error("native app failed", "error", err)
			os.Exit(1)
		}
		os.Exit(0)
	})
	app.Main()
	return nil
}

func newNativeApp(ctx context.Context, cancel context.CancelFunc) *nativeApp {
	logger, logFile := newClientLogger()
	a := &nativeApp{
		ctx:      ctx,
		cancel:   cancel,
		settings: loadUISettings(),
		theme:    newAppTheme(),
		logger:   logger,
		logFile:  logFile,
	}
	a.logger.Info("BeaconDesk client starting", "version", version.Version, "config", appConfigPath(), "log", appLogPath())
	a.english.Store(a.settings.Language == "en")
	a.settings.AutoStart = isAutoStartEnabled()
	a.updateMessage = a.tr("点击检查更新会连接 GitHub Releases。", "Click check for updates to query GitHub Releases.")
	a.targetEdit.SingleLine = true
	a.targetCodeEdit.SingleLine = true
	a.targetCodeEdit.MaxLen = 6
	a.eventsList.Axis = layout.Vertical
	a.eventsList.List.ScrollToEnd = false
	a.initSettingsEditors()
	a.setNotice(a.tr("准备就绪。远程控制必须经被控端明确授权。", "Ready. Remote control always requires explicit local approval."), false)
	return a
}

func newAppTheme() *material.Theme {
	th := material.NewTheme()
	th.Palette.Bg = palette.bg
	th.Palette.Fg = palette.text
	th.Palette.ContrastBg = palette.primary
	th.Palette.ContrastFg = rgb(0xffffff)
	th.TextSize = 15
	th.Face = font.Typeface("Segoe UI, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, sans-serif")
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	return th
}

func (a *nativeApp) initSettingsEditors() {
	editors := []*widget.Editor{
		&a.settingsUI.serverEdit, &a.settingsUI.wsPathEdit, &a.settingsUI.tlsNameEdit, &a.settingsUI.tlsPinEdit,
		&a.settingsUI.nameEdit, &a.settingsUI.tokenEdit, &a.settingsUI.webTTLEdit,
	}
	for _, ed := range editors {
		ed.SingleLine = true
	}
	a.settingsUI.tokenEdit.Mask = '*'
	a.settingsUI.webTTLEdit.Filter = "0123456789"
	a.settingsUI.trustedList.Axis = layout.Vertical
	a.settingsUI.auditList.Axis = layout.Vertical
	a.settingsUI.webShareList.Axis = layout.Vertical
}

func (a *nativeApp) run() error {
	defer func() {
		if r := recover(); r != nil {
			a.logger.Error("native app panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	w := new(app.Window)
	w.Option(
		app.Title("BeaconDesk"),
		app.Size(unit.Dp(1180), unit.Dp(760)),
		app.MinSize(unit.Dp(980), unit.Dp(660)),
	)
	a.window = w

	ticker := time.NewTicker(time.Second)
	a.safeGo("main-window-ticker", func() {
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-ticker.C:
				w.Invalidate()
			}
		}
	})

	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			ticker.Stop()
			a.shutdown()
			return e.Err
		case app.FrameEvent:
			func() {
				defer a.recoverFrame("main-frame")
				a.renderMu.Lock()
				defer a.renderMu.Unlock()
				a.ops.Reset()
				gtx := app.NewContext(&a.ops, e)
				a.layout(gtx)
				e.Frame(gtx.Ops)
			}()
		}
	}
}

func (a *nativeApp) layout(gtx layout.Context) layout.Dimensions {
	a.handleMainClicks(gtx)
	paint.Fill(gtx.Ops, palette.bg)
	return layout.Stack{Alignment: layout.Center}.Layout(gtx,
		layout.Expanded(a.layoutMain),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			if !a.showSettings {
				return layout.Dimensions{}
			}
			return a.layoutSettingsOverlay(gtx)
		}),
	)
}

func (a *nativeApp) layoutMain(gtx layout.Context) layout.Dimensions {
	compact := gtx.Constraints.Max.X < gtx.Dp(1040)
	return layout.Inset{Top: 18, Bottom: 18, Left: 18, Right: 18}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(14)}.Layout(gtx,
			layout.Rigid(a.layoutHeader),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if compact {
					return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(14)}.Layout(gtx,
						layout.Rigid(a.layoutDevicePanel),
						layout.Flexed(1, a.layoutWorkspace),
					)
				}
				return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(14)}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return withWidth(gtx, gtx.Dp(360), a.layoutDevicePanel)
					}),
					layout.Flexed(1, a.layoutWorkspace),
				)
			}),
		)
	})
}

func (a *nativeApp) layoutHeader(gtx layout.Context) layout.Dimensions {
	state := a.currentState()
	title := a.tr("透明授权的远程协助", "Transparent authorized remote assistance")
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(10)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return logoMark(gtx, image.Pt(gtx.Dp(38), gtx.Dp(38)))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.label(gtx, "BeaconDesk", 22, palette.text, font.SemiBold)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.label(gtx, title, 13, palette.muted, font.Normal)
						}),
					)
				}),
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Min} }),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text := a.tr("连接服务器离线", "Server offline")
			col := palette.muted
			if state.Connected {
				text = a.tr("连接服务器已连接", "Server connected")
				col = palette.success
			} else if state.Reconnecting {
				text = a.tr("正在重连", "Reconnecting")
				col = palette.warning
			}
			return statusPill(gtx, a.theme, text, col)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text := a.tr("未注册", "Unregistered")
			col := palette.muted
			if state.Registered {
				text = a.tr("已注册", "Registered")
				col = palette.primary2
			}
			return statusPill(gtx, a.theme, text, col)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.button(gtx, &a.settingsButton, a.tr("设置", "Settings"), buttonSecondary, true)
		}),
	)
}

func (a *nativeApp) layoutDevicePanel(gtx layout.Context) layout.Dimensions {
	state := a.currentState()
	settings := a.settingsSnapshot()
	connected := a.hasClient()
	return roundedPanel(gtx, palette.card, 8, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(18).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(14)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.label(gtx, a.tr("本机设备", "This device"), 18, palette.text, font.SemiBold)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.label(gtx, a.tr("给对方你的设备 ID 或临时验证码，对方发起请求后仍需要你本机确认。", "Share your device ID or temporary code. Incoming requests still require local approval."), 13, palette.muted, font.Normal)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return infoBlock(gtx, a.theme, a.tr("设备 ID", "Device ID"), valueOrDash(state.DeviceID), true)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					code := valueOr(state.LocalAuthCode, "------")
					if state.AuthCodeExpiry > 0 && state.LocalAuthCode != "" {
						code += "  " + a.tr("有效期至 ", "until ") + formatMillis(state.AuthCodeExpiry)
					}
					return infoBlock(gtx, a.theme, a.tr("临时验证码", "Temporary code"), code, true)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(8)}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return a.button(gtx, &a.connectButton, a.tr("连接服务器", "Connect server"), buttonPrimary, !connected)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return a.button(gtx, &a.codeButton, a.tr("生成验证码", "New code"), buttonAccent, state.Registered)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.disconnectBtn, a.tr("断开连接", "Disconnect"), buttonDanger, connected)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.label(gtx, fmt.Sprintf("%s / %s", roleLabel(settings.Role, settings.Language), mediaSummary(settings)), 12, palette.muted, font.Normal)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					msg, isErr := a.notice()
					col := palette.primary2
					if isErr {
						col = palette.danger
					}
					return noticeBox(gtx, a.theme, msg, col)
				}),
			)
		})
	})
}

func (a *nativeApp) layoutWorkspace(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(14)}.Layout(gtx,
		layout.Rigid(a.layoutAssistPanel),
		layout.Rigid(a.layoutApprovalPanel),
		layout.Rigid(a.layoutSessionPanel),
		layout.Flexed(1, a.layoutEventsPanel),
	)
}

func (a *nativeApp) layoutAssistPanel(gtx layout.Context) layout.Dimensions {
	state := a.currentState()
	requesting := a.isRequesting()
	requestLabel := a.tr("请求协助", "Request")
	if requesting {
		requestLabel = a.tr("请求中...", "Requesting...")
	}
	return roundedPanel(gtx, palette.card, 8, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sectionHeader(gtx, a.theme, a.tr("远程协助", "Remote assistance"), a.tr("输入对方设备 ID 和验证码，向对方发起透明授权请求。", "Enter the peer device ID and code to request an authorized session."))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(10), Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(2, func(gtx layout.Context) layout.Dimensions {
							return a.inputField(gtx, &a.targetEdit, a.tr("目标设备 ID", "Target device ID"))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return a.inputField(gtx, &a.targetCodeEdit, a.tr("验证码", "Code"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.button(gtx, &a.requestButton, requestLabel, buttonPrimary, state.Registered && !requesting)
						}),
					)
				}),
			)
		})
	})
}

func (a *nativeApp) layoutApprovalPanel(gtx layout.Context) layout.Dimensions {
	state := a.currentState()
	settings := a.settingsSnapshot()
	if state.PendingPeerID == "" {
		return layout.Dimensions{}
	}
	return roundedPanel(gtx, color.NRGBA{R: 255, G: 251, B: 235, A: 255}, 8, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(14).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			peer := peerNameFromPending(state)
			detail := fmt.Sprintf("%s · %s", peer, modeLabel(state.PendingMode, settings.Language))
			if state.PendingInput {
				detail += " · " + a.tr("请求鼠标键盘控制", "Requests mouse and keyboard control")
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(10)}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(4)}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.label(gtx, a.tr("收到连接请求", "Incoming request"), 16, palette.warning, font.SemiBold)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.label(gtx, detail, 13, palette.ink, font.Normal)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.approveButton, a.tr("允许", "Allow"), buttonAccent, true)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.rememberButton, a.tr("允许并记住", "Allow & remember"), buttonPrimary, true)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.rejectButton, a.tr("拒绝", "Reject"), buttonDanger, true)
				}),
			)
		})
	})
}

func (a *nativeApp) layoutSessionPanel(gtx layout.Context) layout.Dimensions {
	state := a.currentState()
	settings := a.settingsSnapshot()
	return roundedPanel(gtx, palette.card, 8, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(14)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					peer := valueOrDash(peerName(state))
					if state.SessionID == "" {
						peer = a.tr("暂无活动会话", "No active session")
					}
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(10)}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return sectionHeader(gtx, a.theme, a.tr("会话概览", "Session"), peer)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.button(gtx, &a.remoteButton, a.tr("远程画面", "Remote screen"), buttonSecondary, state.SessionID != "")
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(10)}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("延迟", "Latency"), formatMaybe(state.RTTMillis, "%d ms"))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("丢包", "Loss"), formatLoss(state.PacketLossPermy))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("码率", "Bitrate"), formatMaybe(state.BitrateKbps, "%d Kbps"))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("帧", "Frames"), fmt.Sprintf("%d / %d", state.FramesSent, state.FramesReceived))
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(10)}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("模式", "Mode"), modeLabel(state.SessionMode, settings.Language))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("输入权限", "Input"), stateInputText(state, settings.Language))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("画质 / FPS", "Quality / FPS"), formatQuality(state))
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return metricTile(gtx, a.theme, a.tr("重连", "Reconnects"), fmt.Sprintf("%d", state.ReconnectCount))
						}),
					)
				}),
			)
		})
	})
}

func (a *nativeApp) layoutEventsPanel(gtx layout.Context) layout.Dimensions {
	return roundedPanel(gtx, palette.card, 8, func(gtx layout.Context) layout.Dimensions {
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(10)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sectionHeader(gtx, a.theme, a.tr("事件", "Events"), a.tr("连接、授权、心跳、码流和输入事件会显示在这里。", "Connection, authorization, heartbeat, stream, and input events appear here."))
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					events := a.eventLines()
					if len(events) == 0 {
						events = []string{a.tr("暂无事件", "No events yet")}
					}
					return material.List(a.theme, &a.eventsList).Layout(gtx, len(events), func(gtx layout.Context, i int) layout.Dimensions {
						return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return a.label(gtx, events[i], 12, palette.muted, font.Normal)
						})
					})
				}),
			)
		})
	})
}

func (a *nativeApp) handleMainClicks(gtx layout.Context) {
	for a.connectButton.Clicked(gtx) {
		a.connect()
	}
	for a.codeButton.Clicked(gtx) {
		a.generateCode()
	}
	for a.disconnectBtn.Clicked(gtx) {
		a.disconnect()
	}
	for a.requestButton.Clicked(gtx) {
		a.requestSession()
	}
	for a.settingsButton.Clicked(gtx) {
		a.openSettings()
	}
	for a.remoteButton.Clicked(gtx) {
		a.openRemoteWindow()
	}
	for a.approveButton.Clicked(gtx) {
		a.approveSession(false)
	}
	for a.rememberButton.Clicked(gtx) {
		a.approveSession(true)
	}
	for a.rejectButton.Clicked(gtx) {
		a.rejectSession()
	}
}

func (a *nativeApp) connect() {
	a.mu.Lock()
	if a.client != nil {
		a.mu.Unlock()
		return
	}
	opts := a.settings
	a.setNoticeLocked(a.tr("正在连接服务器...", "Connecting to server..."), false)
	a.mu.Unlock()
	a.invalidate()

	a.safeGo("connect", func() {
		c := coreclient.New(coreclient.Options{
			ServerAddress:      opts.ServerAddress,
			Transport:          opts.Transport,
			UseTLS:             opts.UseTLS,
			WebSocketPath:      opts.WebSocketPath,
			TLSServerName:      opts.TLSServerName,
			TLSSkipVerify:      opts.TLSSkipVerify,
			TLSCertSHA256:      opts.TLSCertSHA256,
			DeviceName:         opts.DeviceName,
			Role:               opts.Role,
			RequestMode:        opts.RequestMode,
			Token:              opts.Token,
			IdentityPath:       defaultIdentityPath(opts.DeviceName),
			TrustStorePath:     defaultTrustStorePath(),
			AuditLogPath:       defaultAuditLogPath(),
			AutoAccept:         opts.AutoAccept,
			EnableInput:        opts.EnableInput,
			SendMockFrames:     opts.SendMockFrames,
			SendScreenFrames:   opts.SendScreenFrames,
			CaptureFPS:         opts.CaptureFPS,
			CaptureMaxWidth:    opts.CaptureMaxWidth,
			CaptureMaxHeight:   opts.CaptureMaxHeight,
			CaptureQuality:     opts.CaptureQuality,
			BandwidthLimitKbps: opts.BandwidthLimitKbps,
			StaticFrameSeconds: opts.StaticFrameSeconds,
		}, a.logger)
		if err := c.Start(a.ctx); err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("连接失败", "Connection failed"), err), true)
			a.invalidate()
			return
		}

		a.mu.Lock()
		if a.client != nil {
			a.mu.Unlock()
			c.Close()
			return
		}
		a.client = c
		a.state = c.State()
		a.setNoticeLocked(a.tr("已连接服务器。", "Connected to server."), false)
		a.mu.Unlock()
		a.invalidate()

		a.safeGo("client-events", func() { a.forwardEvents(c) })
	})
}

func (a *nativeApp) disconnect() {
	a.mu.Lock()
	c := a.client
	a.client = nil
	a.state = coreclient.State{}
	a.lastFrameData = ""
	a.lastFrameImage = paint.ImageOp{}
	a.lastFrameSize = image.Point{}
	a.lastFrameError = ""
	a.setNoticeLocked(a.tr("已断开连接。", "Disconnected."), false)
	a.mu.Unlock()
	if c != nil {
		c.Close()
	}
	a.invalidate()
}

func (a *nativeApp) requestSession() {
	c := a.currentClient()
	if c == nil {
		a.setNotice(a.tr("请先连接服务器。", "Connect to the server first."), true)
		a.invalidate()
		return
	}
	state := c.State()
	if !state.Registered {
		a.setNotice(a.tr("设备还未完成注册，请等待顶部显示“已注册”后再请求。", "Device is not registered yet. Wait until the header shows Registered."), true)
		a.invalidate()
		return
	}
	a.mu.Lock()
	if a.requesting {
		a.mu.Unlock()
		return
	}
	a.requesting = true
	a.mu.Unlock()
	a.invalidate()

	target := strings.TrimSpace(a.targetEdit.Text())
	if target == "" {
		a.mu.Lock()
		a.requesting = false
		a.mu.Unlock()
		a.setNotice(a.tr("请输入目标设备 ID。", "Enter a target device ID."), true)
		a.invalidate()
		return
	}
	code := strings.TrimSpace(a.targetCodeEdit.Text())
	a.safeGo("request-session", func() {
		defer func() {
			a.mu.Lock()
			a.requesting = false
			a.mu.Unlock()
			a.invalidate()
		}()
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		if err := c.RequestSessionWithCode(ctx, target, code); err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("请求失败", "Request failed"), err), true)
		} else {
			a.setNotice(a.tr("已发送协助请求，等待对方授权。", "Request sent. Waiting for peer approval."), false)
		}
		a.invalidate()
	})
}

func (a *nativeApp) generateCode() {
	c := a.currentClient()
	if c == nil {
		a.setNotice(a.tr("请先连接服务器。", "Connect to the server first."), true)
		a.invalidate()
		return
	}
	a.safeGo("generate-code", func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		code, err := c.GenerateTemporaryCode(ctx, 10*time.Minute)
		if err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("生成失败", "Code generation failed"), err), true)
		} else {
			a.setNotice(fmt.Sprintf("%s: %s", a.tr("临时验证码已生成", "Temporary code generated"), code), false)
		}
		a.applyState(c.State())
		a.invalidate()
	})
}

func (a *nativeApp) approveSession(remember bool) {
	c := a.currentClient()
	if c == nil {
		return
	}
	a.safeGo("approve-session", func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		var err error
		if remember {
			err = c.ApproveAndRememberSession(ctx)
		} else {
			err = c.ApproveSession(ctx)
		}
		if err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("授权失败", "Approval failed"), err), true)
		} else {
			a.setNotice(a.tr("已允许本次远程协助。", "Remote assistance approved."), false)
		}
		a.applyState(c.State())
		a.invalidate()
	})
}

func (a *nativeApp) rejectSession() {
	c := a.currentClient()
	if c == nil {
		return
	}
	a.safeGo("reject-session", func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		if err := c.RejectSession(ctx, "rejected by user"); err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("拒绝失败", "Reject failed"), err), true)
		} else {
			a.setNotice(a.tr("已拒绝连接请求。", "Request rejected."), false)
		}
		a.applyState(c.State())
		a.invalidate()
	})
}

func (a *nativeApp) forwardEvents(c *coreclient.Client) {
	for {
		select {
		case <-a.ctx.Done():
			return
		case event, ok := <-c.Events():
			if !ok {
				return
			}
			a.appendEvent(event)
			a.handleClientEvent(event)
			a.applyState(c.State())
			a.invalidate()
		}
	}
}

func (a *nativeApp) handleClientEvent(evt coreclient.Event) {
	switch evt.Type {
	case "device.registered":
		a.setNotice(a.tr("设备已注册，可以发起或接收远程协助。", "Device registered. You can request or receive assistance."), false)
	case "relay.error":
		a.setNotice(humanRelayError(evt.Message, a.english.Load()), true)
	case "screen.disabled":
		a.setNotice(a.tr("被控端未开启屏幕画面发送，控制端会一直等待画面。", "Screen sending is disabled on the controlled device, so the controller will keep waiting."), true)
	case "screen.error":
		a.setNotice(humanScreenError(evt.Message, a.english.Load()), true)
	case "stream.error":
		a.setNotice(humanScreenError(evt.Message, a.english.Load()), true)
	case "stream.status":
		a.setNotice(evt.Message, false)
	case "session.incoming":
		a.setNotice(a.tr("收到远程协助请求，请在本机确认。", "Incoming assistance request. Please approve locally."), false)
	case "session.ready":
		a.setNotice(a.tr("远程协助会话已建立。", "Remote assistance session is ready."), false)
		if c := a.currentClient(); c != nil {
			state := c.State()
			if state.SessionID != "" && state.SessionLocalRole != protocol.RoleControlled && !state.ShouldSendView {
				a.openRemoteWindow()
			}
		}
	case "session.declined":
		a.setNotice(a.tr("对方已拒绝本次远程协助。", "The peer declined this assistance request."), true)
	}
}

func (a *nativeApp) appendEvent(evt coreclient.Event) {
	line := fmt.Sprintf("[%s] %s: %s", evt.Time.Format("15:04:05"), evt.Type, evt.Message)
	a.mu.Lock()
	a.events = append([]string{line}, a.events...)
	if len(a.events) > 240 {
		a.events = a.events[:240]
	}
	a.mu.Unlock()
}

func (a *nativeApp) applyState(state coreclient.State) {
	a.mu.Lock()
	a.state = state
	if a.showSettings && a.settingsPage == 4 {
		a.settingsUI.webShareRows = append([]protocol.WebSharePayload(nil), state.WebShareLinks...)
		if a.settingsUI.selectedWebShare >= len(a.settingsUI.webShareRows) {
			a.settingsUI.selectedWebShare = -1
		}
	}
	a.decodeLatestFrameLocked(state)
	a.mu.Unlock()
}

func (a *nativeApp) decodeLatestFrameLocked(state coreclient.State) {
	if state.LastFrameData == "" || state.LastFrameData == a.lastFrameData {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			a.state.LastFrameError = fmt.Sprintf("画面解码异常：%v", r)
			a.lastFrameData = state.LastFrameData
			a.lastFrameError = a.state.LastFrameError
		}
	}()
	img, err := decodeFrameImage(state.LastFrameData)
	if err != nil {
		a.state.LastFrameError = "画面解码失败：" + err.Error()
		a.lastFrameData = state.LastFrameData
		a.lastFrameError = a.state.LastFrameError
		return
	}
	a.lastFrameData = state.LastFrameData
	a.lastFrameError = ""
	a.lastFrameImage = paint.NewImageOp(img)
	a.lastFrameSize = image.Pt(state.LastFrameWidth, state.LastFrameHeight)
	if a.lastFrameSize.X <= 0 || a.lastFrameSize.Y <= 0 {
		a.lastFrameSize = img.Bounds().Size()
	}
	if a.remote != nil && a.remote.window != nil {
		a.remote.window.Invalidate()
	}
}

func (a *nativeApp) currentClient() *coreclient.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

func (a *nativeApp) hasClient() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client != nil
}

func (a *nativeApp) settingsSnapshot() uiSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

func (a *nativeApp) currentState() coreclient.State {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		a.state = a.client.State()
		a.decodeLatestFrameLocked(a.state)
		if a.showSettings && a.settingsPage == 4 {
			a.settingsUI.webShareRows = append([]protocol.WebSharePayload(nil), a.state.WebShareLinks...)
			if a.settingsUI.selectedWebShare >= len(a.settingsUI.webShareRows) {
				a.settingsUI.selectedWebShare = -1
			}
		}
	}
	return a.state
}

func (a *nativeApp) frameSnapshot() (paint.ImageOp, image.Point, coreclient.State) {
	a.mu.Lock()
	defer a.mu.Unlock()
	state := a.state
	if state.LastFrameError == "" && a.lastFrameError != "" {
		state.LastFrameError = a.lastFrameError
	}
	return a.lastFrameImage, a.lastFrameSize, state
}

func (a *nativeApp) eventLines() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.events...)
}

func (a *nativeApp) setNotice(msg string, isError bool) {
	a.mu.Lock()
	a.setNoticeLocked(msg, isError)
	a.mu.Unlock()
}

func (a *nativeApp) setNoticeLocked(msg string, isError bool) {
	a.statusMessage = msg
	a.statusIsError = isError
}

func (a *nativeApp) notice() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusMessage, a.statusIsError
}

func (a *nativeApp) updateStatus() (string, string, bool, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.updateMessage, a.updateURL, a.updateChecking, a.updateIsError
}

func (a *nativeApp) settingsActionState() (saving bool, webBusy bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settingsSaving, a.webShareBusy
}

func (a *nativeApp) isRequesting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requesting
}

func (a *nativeApp) safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.logger.Error("goroutine panic", "name", name, "panic", r, "stack", string(debug.Stack()))
				a.invalidate()
			}
		}()
		fn()
	}()
}

func (a *nativeApp) recoverFrame(name string) {
	if r := recover(); r != nil {
		a.logger.Error("frame panic", "name", name, "panic", r, "stack", string(debug.Stack()))
		a.invalidate()
	}
}

func (a *nativeApp) invalidate() {
	if a.window != nil {
		a.window.Invalidate()
	}
	if a.remote != nil && a.remote.window != nil {
		a.remote.window.Invalidate()
	}
}

func (a *nativeApp) shutdown() {
	a.logger.Info("BeaconDesk client shutting down")
	a.cancel()
	a.mu.Lock()
	c := a.client
	a.client = nil
	f := a.logFile
	a.logFile = nil
	a.mu.Unlock()
	if c != nil {
		c.Close()
	}
	if f != nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

func (a *nativeApp) openRemoteWindow() {
	a.mu.Lock()
	if a.remote != nil && a.remote.window != nil {
		a.remote.window.Perform(system.ActionRaise)
		a.mu.Unlock()
		return
	}
	rw := &remoteWindow{
		app:    a,
		window: new(app.Window),
		theme:  newAppTheme(),
	}
	a.remote = rw
	a.mu.Unlock()
	a.safeGo("remote-window", rw.run)
}

func (rw *remoteWindow) run() {
	defer func() {
		if r := recover(); r != nil {
			rw.app.logger.Error("remote window panic", "panic", r, "stack", string(debug.Stack()))
			rw.app.mu.Lock()
			if rw.app.remote == rw {
				rw.app.remote = nil
			}
			rw.app.mu.Unlock()
			rw.app.invalidate()
		}
	}()
	title := rw.app.tr("BeaconDesk 远程画面", "BeaconDesk Remote Screen")
	rw.window.Option(app.Title(title), app.Size(unit.Dp(1024), unit.Dp(680)), app.MinSize(unit.Dp(720), unit.Dp(480)))
	for {
		switch e := rw.window.Event().(type) {
		case app.DestroyEvent:
			rw.app.mu.Lock()
			if rw.app.remote == rw {
				rw.app.remote = nil
			}
			rw.app.mu.Unlock()
			return
		case app.FrameEvent:
			func() {
				defer rw.app.recoverFrame("remote-frame")
				rw.app.renderMu.Lock()
				defer rw.app.renderMu.Unlock()
				rw.ops.Reset()
				gtx := app.NewContext(&rw.ops, e)
				rw.layout(gtx)
				e.Frame(gtx.Ops)
			}()
		}
	}
}

func (rw *remoteWindow) layout(gtx layout.Context) layout.Dimensions {
	for rw.closeBtn.Clicked(gtx) {
		rw.window.Perform(system.ActionClose)
	}
	paint.Fill(gtx.Ops, rgb(0x111827))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return rw.toolbar(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return rw.screen(gtx)
		}),
	)
}

func (rw *remoteWindow) toolbar(gtx layout.Context) layout.Dimensions {
	state := rw.app.currentState()
	title := rw.app.tr("远程画面", "Remote Screen")
	subtitle := rw.app.tr("仅在会话授权后显示画面和转发输入", "Video and input are available only after authorization")
	if state.PeerID != "" {
		subtitle = peerName(state)
	}
	return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(12)}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(2)}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return labelWithTheme(gtx, rw.theme, title, 17, rgb(0xffffff), font.SemiBold)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return labelWithTheme(gtx, rw.theme, subtitle, 12, rgb(0xa7b0c0), font.Normal)
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return buttonWithTheme(gtx, rw.theme, &rw.closeBtn, rw.app.tr("关闭", "Close"), buttonDark, true)
			}),
		)
	})
}

func (rw *remoteWindow) screen(gtx layout.Context) layout.Dimensions {
	img, size, state := rw.app.frameSnapshot()
	gtx.Constraints.Min = gtx.Constraints.Max
	bounds := image.Rectangle{Max: gtx.Constraints.Max}
	paint.FillShape(gtx.Ops, rgb(0x0b1020), clip.Rect(bounds).Op())
	area := image.Rectangle{
		Min: image.Pt(gtx.Dp(18), gtx.Dp(18)),
		Max: bounds.Max.Sub(image.Pt(gtx.Dp(18), gtx.Dp(18))),
	}
	if area.Dx() < 1 || area.Dy() < 1 {
		return layout.Dimensions{Size: bounds.Size()}
	}

	screenRect := area
	if size.X > 0 && size.Y > 0 {
		screenRect = fitRect(area, size)
	}
	frameErr := strings.TrimSpace(state.LastFrameError)
	frameStatus := strings.TrimSpace(state.LastFrameStatus)

	defer clip.Rect(area).Push(gtx.Ops).Pop()
	if img.Size().X > 0 {
		defer op.Offset(screenRect.Min).Push(gtx.Ops).Pop()
		gtx2 := gtx
		gtx2.Constraints = layout.Exact(screenRect.Size())
		widget.Image{
			Src:      img,
			Fit:      widget.Contain,
			Position: layout.Center,
			Scale:    1.0 / gtx.Metric.PxPerDp,
		}.Layout(gtx2)
	} else {
		paint.FillShape(gtx.Ops, rgb(0x182033), clip.UniformRRect(screenRect, gtx.Dp(8)).Op(gtx.Ops))
		defer op.Offset(screenRect.Min).Push(gtx.Ops).Pop()
		gtx2 := gtx
		gtx2.Constraints = layout.Exact(screenRect.Size())
		layout.Center.Layout(gtx2, func(gtx layout.Context) layout.Dimensions {
			msg := rw.app.tr("等待远程画面...", "Waiting for remote video...")
			col := rgb(0xa7b0c0)
			if state.SessionID == "" {
				msg = rw.app.tr("暂无活动会话", "No active session")
			} else if frameErr != "" {
				msg = frameErr + "\n" + rw.app.tr("请确认被控端未锁屏、当前用户桌面可见，并已开启“发送屏幕画面”。", "Make sure the controlled desktop is unlocked, visible, and screen sending is enabled.")
				col = rgb(0xfca5a5)
			} else if frameStatus != "" {
				msg = frameStatus
				col = rgb(0xfcd34d)
			} else if state.SessionLocalRole == protocol.RoleControlled {
				msg = rw.app.tr("本机是被控端，正在把画面发送给对方。", "This device is controlled and is sending its screen.")
			} else if state.FramesReceived == 0 {
				msg = rw.app.tr("等待被控端发送第一帧画面。请在被控端设置 -> 授权中开启“发送屏幕画面”。", "Waiting for the controlled device to send the first frame. Enable screen sending on the controlled device.")
			}
			return labelWithTheme(gtx, rw.theme, msg, 15, col, font.Normal)
		})
	}

	rw.handleRemoteInput(gtx, screenRect, size, state)
	return layout.Dimensions{Size: bounds.Size()}
}

func (rw *remoteWindow) handleRemoteInput(gtx layout.Context, rect image.Rectangle, source image.Point, state coreclient.State) {
	defer clip.Rect(rect).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &rw.pointerTag)
	event.Op(gtx.Ops, &rw.keyTag)
	if source.X <= 0 {
		source.X = max(1, rect.Dx())
	}
	if source.Y <= 0 {
		source.Y = max(1, rect.Dy())
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{
			Target: &rw.pointerTag,
			Kinds:  pointer.Press | pointer.Release | pointer.Move | pointer.Drag | pointer.Scroll,
		})
		if !ok {
			break
		}
		pe := ev.(pointer.Event)
		if pe.Kind == pointer.Press {
			gtx.Execute(key.FocusCmd{Tag: &rw.keyTag})
		}
		if state.SessionID == "" || !state.InputAllowed {
			continue
		}
		p := image.Pt(int(pe.Position.X), int(pe.Position.Y))
		if !p.In(rect) && pe.Kind != pointer.Scroll {
			continue
		}
		x := clampInt((p.X-rect.Min.X)*source.X/max(1, rect.Dx()), 0, source.X-1)
		y := clampInt((p.Y-rect.Min.Y)*source.Y/max(1, rect.Dy()), 0, source.Y-1)
		event := input.MouseEvent{X: x, Y: y, SourceWidth: source.X, SourceHeight: source.Y}
		switch pe.Kind {
		case pointer.Press:
			rw.lastButton = pointerButtonName(pe.Buttons)
			event.Button = valueOr(rw.lastButton, "left")
			event.Action = "down"
		case pointer.Release:
			event.Button = valueOr(rw.lastButton, "left")
			event.Action = "up"
			rw.lastButton = ""
		case pointer.Move, pointer.Drag:
			event.Action = "move"
		case pointer.Scroll:
			event.Action = "wheel"
			event.WheelDelta = int(-pe.Scroll.Y)
		}
		rw.sendMouse(event)
	}
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: &rw.keyTag},
			key.Filter{Focus: &rw.keyTag, Name: ""},
		)
		if !ok {
			break
		}
		ke, ok := ev.(key.Event)
		if !ok || state.SessionID == "" || !state.InputAllowed {
			continue
		}
		action := "down"
		if ke.State == key.Release {
			action = "up"
		}
		rw.sendKeyboard(input.KeyboardEvent{
			Key:       string(ke.Name),
			Code:      gioKeyCode(ke.Name),
			KeyCode:   gioVirtualKey(ke.Name),
			Action:    action,
			Modifiers: gioModifiers(ke.Modifiers),
		})
	}
	pointer.CursorCrosshair.Add(gtx.Ops)
}

func (rw *remoteWindow) sendMouse(event input.MouseEvent) {
	c := rw.app.currentClient()
	if c == nil {
		return
	}
	rw.app.safeGo("send-mouse", func() {
		ctx, cancel := context.WithTimeout(rw.app.ctx, 2*time.Second)
		defer cancel()
		if err := c.SendMouse(ctx, event); err != nil {
			rw.app.setNotice(fmt.Sprintf("%s: %v", rw.app.tr("鼠标事件发送失败", "Mouse event failed"), err), true)
			rw.app.invalidate()
		}
	})
}

func (rw *remoteWindow) sendKeyboard(event input.KeyboardEvent) {
	c := rw.app.currentClient()
	if c == nil {
		return
	}
	rw.app.safeGo("send-keyboard", func() {
		ctx, cancel := context.WithTimeout(rw.app.ctx, 2*time.Second)
		defer cancel()
		if err := c.SendKeyboard(ctx, event); err != nil {
			rw.app.setNotice(fmt.Sprintf("%s: %v", rw.app.tr("键盘事件发送失败", "Keyboard event failed"), err), true)
			rw.app.invalidate()
		}
	})
}

func (a *nativeApp) openSettings() {
	a.mu.Lock()
	a.settingsDraft = a.settings
	a.mu.Unlock()
	a.settingsPage = 0
	a.settingsUI.language.Value = a.settingsDraft.Language
	a.settingsUI.transport.Value = a.settingsDraft.Transport
	a.settingsUI.role.Value = a.settingsDraft.Role
	a.settingsUI.mode.Value = a.settingsDraft.RequestMode
	a.settingsUI.fpsPreset.Value = strconv.Itoa(a.settingsDraft.CaptureFPS)
	a.settingsUI.resPreset.Value = resolutionKey(a.settingsDraft.CaptureMaxWidth, a.settingsDraft.CaptureMaxHeight)
	a.settingsUI.qualityPreset.Value = strconv.Itoa(a.settingsDraft.CaptureQuality)
	a.settingsUI.bitratePreset.Value = strconv.Itoa(a.settingsDraft.BandwidthLimitKbps)
	a.settingsUI.staticPreset.Value = strconv.Itoa(a.settingsDraft.StaticFrameSeconds)
	setEditorText(&a.settingsUI.serverEdit, a.settingsDraft.ServerAddress)
	setEditorText(&a.settingsUI.wsPathEdit, a.settingsDraft.WebSocketPath)
	setEditorText(&a.settingsUI.tlsNameEdit, a.settingsDraft.TLSServerName)
	setEditorText(&a.settingsUI.tlsPinEdit, a.settingsDraft.TLSCertSHA256)
	setEditorText(&a.settingsUI.nameEdit, a.settingsDraft.DeviceName)
	setEditorText(&a.settingsUI.tokenEdit, a.settingsDraft.Token)
	setEditorText(&a.settingsUI.webTTLEdit, strconv.Itoa(a.settingsDraft.WebShareTTLMinutes))
	a.settingsUI.tlsCheck.Value = a.settingsDraft.UseTLS
	a.settingsUI.skipTLSCheck.Value = a.settingsDraft.TLSSkipVerify
	a.settingsUI.autoStartCheck.Value = isAutoStartEnabled()
	a.settingsUI.autoAcceptCheck.Value = a.settingsDraft.AutoAccept
	a.settingsUI.inputCheck.Value = a.settingsDraft.EnableInput
	a.settingsUI.mockFramesCheck.Value = a.settingsDraft.SendMockFrames
	a.settingsUI.screenFramesCheck.Value = a.settingsDraft.SendScreenFrames
	a.refreshWebShares()
	a.refreshSettingsLists()
	a.showSettings = true
	a.invalidate()
}

func (a *nativeApp) layoutSettingsOverlay(gtx layout.Context) layout.Dimensions {
	a.handleSettingsClicks(gtx)
	saving, _ := a.settingsActionState()
	gtx.Constraints.Min = gtx.Constraints.Max
	paint.Fill(gtx.Ops, color.NRGBA{R: 15, G: 23, B: 42, A: 110})
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		maxW := min(gtx.Constraints.Max.X-gtx.Dp(32), gtx.Dp(1040))
		maxH := min(gtx.Constraints.Max.Y-gtx.Dp(32), gtx.Dp(680))
		gtx.Constraints = layout.Exact(image.Pt(maxW, maxH))
		return roundedPanel(gtx, rgb(0xf8fbff), 8, func(gtx layout.Context) layout.Dimensions {
			drawStroke(gtx, gtx.Constraints.Min, rgb(0xe2e8f0), 8, 1)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 18, Bottom: 14, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return sectionHeader(gtx, a.theme, a.tr("设置", "Settings"), a.tr("连接、安全、显示、性能和网页控制配置。", "Connection, security, display, performance, and web control settings."))
							}),
						)
					})
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return roundedPanel(gtx, palette.panel, 8, func(gtx layout.Context) layout.Dimensions {
							drawStroke(gtx, gtx.Constraints.Min, rgb(0xe5e7eb), 8, 1)
							return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return withWidth(gtx, gtx.Dp(190), a.layoutSettingsNav)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									size := image.Pt(gtx.Dp(1), gtx.Constraints.Max.Y)
									paint.FillShape(gtx.Ops, rgb(0xe5e7eb), clip.Rect(image.Rectangle{Max: size}).Op())
									return layout.Dimensions{Size: size}
								}),
								layout.Flexed(1, a.layoutSettingsPage),
							)
						})
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 14, Bottom: 18, Left: 20, Right: 20}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(10)}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return a.button(gtx, &a.settingsUI.cancelBtn, a.tr("取消", "Cancel"), buttonSecondary, true)
							}),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								note := fmt.Sprintf("%s %s", a.tr("配置保存在本地文件：", "Config file:"), appConfigPath())
								return a.label(gtx, note, 12, palette.muted, font.Normal)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								label := a.tr("保存", "Save")
								if saving {
									label = a.tr("保存中...", "Saving...")
								}
								return a.button(gtx, &a.settingsUI.saveButton, label, buttonPrimary, !saving)
							}),
						)
					})
				}),
			)
		})
	})
}

func (a *nativeApp) layoutSettingsNav(gtx layout.Context) layout.Dimensions {
	items := settingsNavItems(a.draftLanguage())
	return layout.Inset{Top: 22, Bottom: 18, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(10)}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return a.label(gtx, a.tr("设置分类", "Settings"), 13, palette.muted, font.SemiBold)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 0, len(items))
				for i, name := range items {
					i, name := i, name
					children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						selected := a.settingsPage == i
						return navButton(gtx, a.theme, &a.settingsUI.navButtons[i], name, selected)
					}))
				}
				return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(6)}.Layout(gtx, children...)
			}),
		)
	})
}

func (a *nativeApp) layoutSettingsPage(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Top: 26, Bottom: 20, Left: 24, Right: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		switch a.settingsPage {
		case 1:
			return a.layoutRelaySettings(gtx)
		case 2:
			return a.layoutAuthSettings(gtx)
		case 3:
			return a.layoutMediaSettings(gtx)
		case 4:
			return a.layoutWebControlSettings(gtx)
		case 5:
			return a.layoutTrustedSettings(gtx)
		case 6:
			return a.layoutAuditSettings(gtx)
		default:
			return a.layoutGeneralSettings(gtx)
		}
	})
}

func (a *nativeApp) layoutGeneralSettings(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[0])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.layoutUpdateSettings(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.radioRow(gtx, a.tr("语言", "Language"), &a.settingsUI.language, []optionItem{{"zh-CN", "中文"}, {"en", "English"}})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.formRow(gtx, a.tr("本机名称", "Device name"), &a.settingsUI.nameEdit, a.tr("beacondesk-windows", "beacondesk-windows"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("随系统启动", "Start with Windows"), &a.settingsUI.autoStartCheck)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.radioRow(gtx, a.tr("角色", "Role"), &a.settingsUI.role, roleOptionItems(a.draftLanguage()))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.radioRow(gtx, a.tr("请求模式", "Request mode"), &a.settingsUI.mode, modeOptionItems(a.draftLanguage()))
		}),
	)
}

func (a *nativeApp) layoutUpdateSettings(gtx layout.Context) layout.Dimensions {
	msg, url, checking, isErr := a.updateStatus()
	current := fmt.Sprintf("%s  commit=%s", version.Version, version.Commit)
	col := palette.primary2
	if isErr {
		col = palette.danger
	}
	return roundedPanel(gtx, rgb(0xf8fafc), 8, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 12, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(10)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(10)}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(3)}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return a.label(gtx, a.tr("版本更新", "Updates"), 14, palette.text, font.SemiBold)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return a.label(gtx, a.tr("当前版本：", "Current version: ")+current, 12, palette.muted, font.Normal)
								}),
							)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							label := a.tr("检查更新", "Check")
							if checking {
								label = a.tr("检查中...", "Checking...")
							}
							return a.button(gtx, &a.settingsUI.updateBtn, label, buttonSecondary, !checking)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return a.button(gtx, &a.settingsUI.releaseBtn, a.tr("打开发布页", "Open release"), buttonPrimary, url != "")
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return noticeBox(gtx, a.theme, msg, col)
				}),
			)
		})
	})
}

func (a *nativeApp) layoutRelaySettings(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[1])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.formRow(gtx, a.tr("连接服务器地址", "Server address"), &a.settingsUI.serverEdit, "127.0.0.1:8443")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.radioRow(gtx, a.tr("传输协议", "Transport"), &a.settingsUI.transport, []optionItem{{"tcp", "TCP"}, {"websocket", "WebSocket"}})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.formRow(gtx, a.tr("访问令牌", "Access token"), &a.settingsUI.tokenEdit, a.tr("可选，建议通过安全渠道配置", "Optional, configure through a secure channel"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.formRow(gtx, "WebSocket Path", &a.settingsUI.wsPathEdit, "/ws")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.formRow(gtx, a.tr("TLS 服务名", "TLS server name"), &a.settingsUI.tlsNameEdit, "relay.example.com")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.formRow(gtx, a.tr("TLS 证书指纹", "TLS cert SHA256"), &a.settingsUI.tlsPinEdit, a.tr("自签证书可填 SHA256 指纹", "SHA256 fingerprint for self-signed certs"))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("使用 TLS", "Use TLS"), &a.settingsUI.tlsCheck)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("跳过 TLS 证书校验（仅本地测试）", "Skip TLS verification (local testing only)"), &a.settingsUI.skipTLSCheck)
		}),
	)
}

func (a *nativeApp) layoutAuthSettings(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[2])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return noticeBox(gtx, a.theme, a.tr("所有远程控制都必须经过被控端明确确认或预先配置授权；不会实现隐藏运行、静默控制或绕过权限。", "All remote control requires explicit local approval or preconfigured authorization. No hidden operation, silent control, or permission bypass is implemented."), palette.primary2)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("测试模式自动接受（仅用于本地 MVP 验证）", "Auto-accept for testing (local MVP only)"), &a.settingsUI.autoAcceptCheck)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("允许远程鼠标键盘输入", "Allow remote mouse and keyboard"), &a.settingsUI.inputCheck)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("发送模拟帧", "Send simulated frames"), &a.settingsUI.mockFramesCheck)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.switchRow(gtx, a.tr("发送屏幕画面", "Send screen frames"), &a.settingsUI.screenFramesCheck)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return noticeBox(gtx, a.theme, a.tr("被控端需要开启“发送屏幕画面”才会把远程画面传给控制端；锁屏、UAC 安全桌面或服务会话可能无法采集。", "The controlled device must enable screen frame sending. Lock screens, UAC secure desktop, or service sessions may block capture."), palette.warning)
		}),
	)
}

func (a *nativeApp) layoutMediaSettings(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[3])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.compactRadioRow(gtx, a.tr("帧率上限", "FPS limit"), &a.settingsUI.fpsPreset, fpsOptionItems())
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.compactRadioRow(gtx, a.tr("画质模式", "Quality mode"), &a.settingsUI.qualityPreset, qualityOptionItems(a.draftLanguage()))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.compactRadioRow(gtx, a.tr("分辨率", "Resolution"), &a.settingsUI.resPreset, resolutionOptionItems())
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.compactRadioRow(gtx, a.tr("码率上限", "Bitrate limit"), &a.settingsUI.bitratePreset, bitrateOptionItems())
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.compactRadioRow(gtx, a.tr("静态画面间隔", "Static frame interval"), &a.settingsUI.staticPreset, staticIntervalOptionItems(a.draftLanguage()))
		}),
	)
}

func (a *nativeApp) layoutWebControlSettings(gtx layout.Context) layout.Dimensions {
	shares := append([]protocol.WebSharePayload(nil), a.settingsUI.webShareRows...)
	selectedOK := a.settingsUI.selectedWebShare >= 0 && a.settingsUI.selectedWebShare < len(shares)
	_, webBusy := a.settingsActionState()
	connected := a.hasClient()
	createLabel := a.tr("生成链接", "Generate")
	refreshLabel := a.tr("刷新", "Refresh")
	revokeLabel := a.tr("删除/撤销", "Delete/Revoke")
	if webBusy {
		createLabel = a.tr("处理中...", "Working...")
		refreshLabel = a.tr("处理中...", "Working...")
		revokeLabel = a.tr("处理中...", "Working...")
	}
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[4])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return noticeBox(gtx, a.theme, a.tr("网页链接只允许访客发起控制请求。每次访问仍需本机明确允许，链接可随时撤销。", "A web link only lets a visitor request control. Every visit still needs local approval and links can be revoked at any time."), palette.primary2)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(10)}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return a.formRow(gtx, a.tr("有效期（分钟）", "Validity minutes"), &a.settingsUI.webTTLEdit, "60")
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.webCreateBtn, createLabel, buttonPrimary, connected && !webBusy)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(8)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.webRefreshBtn, refreshLabel, buttonSecondary, connected && !webBusy)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.webCopyBtn, a.tr("复制选中链接", "Copy selected"), buttonSecondary, selectedOK)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.webOpenBtn, a.tr("打开选中链接", "Open selected"), buttonAccent, selectedOK)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.webRevokeBtn, revokeLabel, buttonDanger, selectedOK && connected && !webBusy)
				}),
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(shares) == 0 {
				return emptyState(gtx, a.theme, a.tr("暂无网页控制链接", "No web control links"))
			}
			ensureClicks(&a.settingsUI.webShareRowClicks, len(shares))
			return material.List(a.theme, &a.settingsUI.webShareList).Layout(gtx, len(shares), func(gtx layout.Context, i int) layout.Dimensions {
				for a.settingsUI.webShareRowClicks[i].Clicked(gtx) {
					a.settingsUI.selectedWebShare = i
				}
				share := shares[i]
				line := fmt.Sprintf("%s · %s · %s\n%s", modeLabel(share.Mode, a.draftLanguage()), formatMillis(share.ExpiresAt), valueOrDash(share.ID), valueOrDash(share.URL))
				return rowButton(gtx, a.theme, &a.settingsUI.webShareRowClicks[i], line, i == a.settingsUI.selectedWebShare)
			})
		}),
	)
}

func (a *nativeApp) layoutTrustedSettings(gtx layout.Context) layout.Dimensions {
	devices := append([]trust.Device(nil), a.settingsUI.trustedRows...)
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[5])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(8)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.refreshBtn, a.tr("刷新", "Refresh"), buttonSecondary, true)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.revokeBtn, a.tr("撤销选中", "Revoke selected"), buttonDanger, a.settingsUI.selectedTrust >= 0 && a.settingsUI.selectedTrust < len(devices))
				}),
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(devices) == 0 {
				return emptyState(gtx, a.theme, a.tr("暂无可信设备", "No trusted devices"))
			}
			ensureClicks(&a.settingsUI.trustRowClicks, len(devices))
			return material.List(a.theme, &a.settingsUI.trustedList).Layout(gtx, len(devices), func(gtx layout.Context, i int) layout.Dimensions {
				for a.settingsUI.trustRowClicks[i].Clicked(gtx) {
					a.settingsUI.selectedTrust = i
				}
				d := devices[i]
				line := fmt.Sprintf("%s · %s · %s", d.DeviceID, modeLabel(d.Mode, a.draftLanguage()), formatMillis(d.LastUsedAt))
				return rowButton(gtx, a.theme, &a.settingsUI.trustRowClicks[i], line, i == a.settingsUI.selectedTrust)
			})
		}),
	)
}

func (a *nativeApp) layoutAuditSettings(gtx layout.Context) layout.Dimensions {
	rows := append([]audit.Entry(nil), a.settingsUI.auditRows...)
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pageTitle(gtx, a.theme, settingsNavItems(a.draftLanguage())[6])
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Gap: gtx.Dp(8)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.refreshBtn, a.tr("刷新", "Refresh"), buttonSecondary, true)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return a.button(gtx, &a.settingsUI.clearBtn, a.tr("清空", "Clear"), buttonDanger, len(rows) > 0)
				}),
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if len(rows) == 0 {
				return emptyState(gtx, a.theme, a.tr("暂无审计日志", "No audit entries"))
			}
			return material.List(a.theme, &a.settingsUI.auditList).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
				e := rows[i]
				line := fmt.Sprintf("%s  %s  %s  %s", formatMillis(e.Time), e.Event, valueOrDash(e.PeerID), e.Detail)
				return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return a.label(gtx, line, 12, palette.muted, font.Normal)
				})
			})
		}),
	)
}

func (a *nativeApp) handleSettingsClicks(gtx layout.Context) {
	for i := range a.settingsUI.navButtons {
		for a.settingsUI.navButtons[i].Clicked(gtx) {
			a.settingsPage = i
			if i == 4 {
				a.refreshWebShares()
			}
			if i == 5 || i == 6 {
				a.refreshSettingsLists()
			}
		}
	}
	for a.settingsUI.cancelBtn.Clicked(gtx) {
		a.showSettings = false
	}
	for a.settingsUI.saveButton.Clicked(gtx) {
		a.saveSettings()
	}
	for a.settingsUI.updateBtn.Clicked(gtx) {
		a.checkForUpdates()
	}
	for a.settingsUI.releaseBtn.Clicked(gtx) {
		a.openReleasePage()
	}
	for a.settingsUI.webCreateBtn.Clicked(gtx) {
		a.createWebShare()
	}
	for a.settingsUI.webRefreshBtn.Clicked(gtx) {
		a.refreshWebShares()
	}
	for a.settingsUI.webCopyBtn.Clicked(gtx) {
		a.copySelectedWebShare()
	}
	for a.settingsUI.webOpenBtn.Clicked(gtx) {
		a.openSelectedWebShare()
	}
	for a.settingsUI.webRevokeBtn.Clicked(gtx) {
		a.revokeSelectedWebShare()
	}
	for a.settingsUI.refreshBtn.Clicked(gtx) {
		a.refreshSettingsLists()
	}
	for a.settingsUI.revokeBtn.Clicked(gtx) {
		idx := a.settingsUI.selectedTrust
		if idx >= 0 && idx < len(a.settingsUI.trustedRows) {
			_ = trust.NewStore(defaultTrustStorePath()).Revoke(a.settingsUI.trustedRows[idx].DeviceID)
			a.settingsUI.selectedTrust = -1
			a.refreshSettingsLists()
		}
	}
	for a.settingsUI.clearBtn.Clicked(gtx) {
		_ = audit.NewStore(defaultAuditLogPath()).Clear()
		a.refreshSettingsLists()
	}
}

func (a *nativeApp) saveSettings() {
	a.mu.Lock()
	if a.settingsSaving {
		a.mu.Unlock()
		return
	}
	a.settingsSaving = true
	a.mu.Unlock()
	a.invalidate()

	language := a.settingsUI.language.Value
	if language == "" {
		language = "zh-CN"
	}
	width, height := parseResolutionKey(a.settingsUI.resPreset.Value, 1280, 720)
	next := uiSettings{
		Language:           language,
		ServerAddress:      textOr(a.settingsUI.serverEdit.Text(), "127.0.0.1:8443"),
		Transport:          textOr(a.settingsUI.transport.Value, "websocket"),
		UseTLS:             a.settingsUI.tlsCheck.Value,
		WebSocketPath:      textOr(a.settingsUI.wsPathEdit.Text(), "/ws"),
		TLSServerName:      strings.TrimSpace(a.settingsUI.tlsNameEdit.Text()),
		TLSSkipVerify:      a.settingsUI.skipTLSCheck.Value,
		TLSCertSHA256:      strings.TrimSpace(a.settingsUI.tlsPinEdit.Text()),
		DeviceName:         textOr(a.settingsUI.nameEdit.Text(), "beacondesk-windows"),
		Role:               textOr(a.settingsUI.role.Value, "peer"),
		RequestMode:        textOr(a.settingsUI.mode.Value, "view-control"),
		Token:              strings.TrimSpace(a.settingsUI.tokenEdit.Text()),
		AutoStart:          a.settingsUI.autoStartCheck.Value,
		AutoAccept:         a.settingsUI.autoAcceptCheck.Value,
		EnableInput:        a.settingsUI.inputCheck.Value,
		WebShareTTLMinutes: parseInt(a.settingsUI.webTTLEdit.Text(), 60, 1, 10080),
		SendMockFrames:     a.settingsUI.mockFramesCheck.Value,
		SendScreenFrames:   a.settingsUI.screenFramesCheck.Value,
		CaptureFPS:         parsePresetInt(a.settingsUI.fpsPreset.Value, 15, []int{15, 30, 45, 60, 90, 120}),
		CaptureMaxWidth:    width,
		CaptureMaxHeight:   height,
		CaptureQuality:     parsePresetInt(a.settingsUI.qualityPreset.Value, 55, []int{40, 55, 72, 85}),
		BandwidthLimitKbps: parsePresetInt(a.settingsUI.bitratePreset.Value, 4096, []int{512, 1024, 2048, 4096, 8192, 12288, 20000, 50000}),
		StaticFrameSeconds: parsePresetInt(a.settingsUI.staticPreset.Value, 5, []int{1, 3, 5, 10, 15, 30}),
	}

	a.safeGo("save-settings", func() {
		var err error
		var msg string
		if err = setAutoStart(next.AutoStart); err != nil {
			msg = fmt.Sprintf("%s: %v", a.tr("自启动设置失败", "Auto-start update failed"), err)
		} else if err = saveUISettings(next); err != nil {
			msg = fmt.Sprintf("%s: %v", a.tr("保存配置失败", "Failed to save settings"), err)
		}

		a.mu.Lock()
		a.settingsSaving = false
		if err != nil {
			a.setNoticeLocked(msg, true)
			a.mu.Unlock()
			a.invalidate()
			return
		}
		a.settings = next
		a.english.Store(next.Language == "en")
		a.showSettings = false
		a.setNoticeLocked(a.tr("设置已保存到本地配置文件，新的连接会使用这些配置。", "Settings saved to local config. New connections will use these values."), false)
		a.mu.Unlock()
		a.invalidate()
	})
}

func (a *nativeApp) checkForUpdates() {
	a.mu.Lock()
	if a.updateChecking {
		a.mu.Unlock()
		return
	}
	a.updateChecking = true
	a.updateIsError = false
	a.updateMessage = a.tr("正在连接 GitHub Releases 检查最新版本...", "Checking GitHub Releases for the latest version...")
	a.updateURL = ""
	a.mu.Unlock()
	a.invalidate()

	a.safeGo("check-updates", func() {
		ctx, cancel := context.WithTimeout(a.ctx, 12*time.Second)
		defer cancel()
		result, err := updatecheck.Latest(ctx, updatecheck.Options{
			Owner:          updatecheck.DefaultOwner,
			Repo:           updatecheck.DefaultRepo,
			CurrentVersion: version.Version,
			UserAgent:      "BeaconDesk/" + version.Version,
		})

		a.mu.Lock()
		defer a.mu.Unlock()
		a.updateChecking = false
		if err != nil {
			a.updateIsError = true
			a.updateMessage = fmt.Sprintf("%s: %v", a.tr("检查更新失败", "Update check failed"), err)
			a.updateURL = ""
			a.window.Invalidate()
			return
		}
		a.updateIsError = false
		a.updateURL = result.ReleaseURL
		published := ""
		if !result.PublishedAt.IsZero() {
			published = " · " + result.PublishedAt.Local().Format("2006-01-02")
		}
		switch {
		case !result.Comparable:
			a.updateMessage = fmt.Sprintf("%s %s%s。%s", a.tr("GitHub 最新版本：", "Latest GitHub release:"), result.LatestVersion, published, a.tr("当前是开发版或无法比较版本号。", "Current build is a development or non-comparable version."))
		case result.NeedsUpdate:
			a.updateMessage = fmt.Sprintf("%s %s%s。%s", a.tr("发现新版本：", "New version available:"), result.LatestVersion, published, a.tr("请打开发布页手动下载并校验来源。", "Open the release page to download manually and verify the source."))
		default:
			a.updateMessage = fmt.Sprintf("%s %s%s", a.tr("当前已是最新版本：", "You are up to date:"), result.LatestVersion, published)
		}
		a.window.Invalidate()
	})
}

func (a *nativeApp) openReleasePage() {
	_, url, _, _ := a.updateStatus()
	if url == "" {
		return
	}
	if err := openURL(url); err != nil {
		a.setNotice(fmt.Sprintf("%s: %v", a.tr("打开发布页失败", "Failed to open release page"), err), true)
		a.invalidate()
	}
}

func (a *nativeApp) createWebShare() {
	c := a.currentClient()
	if c == nil {
		a.setNotice(a.tr("请先连接服务器。", "Connect to the server first."), true)
		a.invalidate()
		return
	}
	a.mu.Lock()
	if a.webShareBusy {
		a.mu.Unlock()
		return
	}
	a.webShareBusy = true
	a.mu.Unlock()
	a.invalidate()

	settings := a.settingsSnapshot()
	ttlMinutes := parseInt(a.settingsUI.webTTLEdit.Text(), settings.WebShareTTLMinutes, 1, 10080)
	mode := a.settingsUI.mode.Value
	if mode == "" {
		mode = settings.RequestMode
	}
	a.safeGo("create-web-share", func() {
		defer a.finishWebShareAction()
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		if err := c.CreateWebShare(ctx, time.Duration(ttlMinutes)*time.Minute, mode, ""); err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("网页链接生成失败", "Web link generation failed"), err), true)
		} else {
			a.setNotice(a.tr("已请求生成网页控制链接。", "Requested web control link generation."), false)
		}
		a.applyState(c.State())
		a.refreshWebSharesFromState(c.State())
		a.invalidate()
	})
}

func (a *nativeApp) refreshWebShares() {
	if c := a.currentClient(); c != nil {
		a.mu.Lock()
		if a.webShareBusy {
			a.mu.Unlock()
			return
		}
		a.webShareBusy = true
		a.mu.Unlock()
		a.invalidate()

		a.safeGo("refresh-web-shares", func() {
			defer a.finishWebShareAction()
			ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
			defer cancel()
			if err := c.RefreshWebShares(ctx); err != nil {
				a.setNotice(fmt.Sprintf("%s: %v", a.tr("刷新网页链接失败", "Failed to refresh web links"), err), true)
			}
			a.applyState(c.State())
			a.refreshWebSharesFromState(c.State())
			a.invalidate()
		})
		return
	}
	a.mu.Lock()
	a.settingsUI.webShareRows = nil
	a.settingsUI.selectedWebShare = -1
	a.mu.Unlock()
}

func (a *nativeApp) finishWebShareAction() {
	a.mu.Lock()
	a.webShareBusy = false
	a.mu.Unlock()
	a.invalidate()
}

func (a *nativeApp) refreshWebSharesFromState(state coreclient.State) {
	a.mu.Lock()
	a.settingsUI.webShareRows = append([]protocol.WebSharePayload(nil), state.WebShareLinks...)
	if a.settingsUI.selectedWebShare >= len(a.settingsUI.webShareRows) {
		a.settingsUI.selectedWebShare = -1
	}
	a.mu.Unlock()
}

func (a *nativeApp) selectedWebShare() (protocol.WebSharePayload, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := a.settingsUI.selectedWebShare
	if idx < 0 || idx >= len(a.settingsUI.webShareRows) {
		return protocol.WebSharePayload{}, false
	}
	return a.settingsUI.webShareRows[idx], true
}

func (a *nativeApp) revokeSelectedWebShare() {
	share, ok := a.selectedWebShare()
	if !ok {
		return
	}
	c := a.currentClient()
	if c == nil {
		a.setNotice(a.tr("请先连接服务器。", "Connect to the server first."), true)
		a.invalidate()
		return
	}
	a.mu.Lock()
	if a.webShareBusy {
		a.mu.Unlock()
		return
	}
	a.webShareBusy = true
	a.mu.Unlock()
	a.invalidate()

	a.safeGo("revoke-web-share", func() {
		defer a.finishWebShareAction()
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		if err := c.RevokeWebShare(ctx, share.ID, share.Token); err != nil {
			a.setNotice(fmt.Sprintf("%s: %v", a.tr("撤销网页链接失败", "Failed to revoke web link"), err), true)
		} else {
			a.setNotice(a.tr("已撤销网页控制链接。", "Web control link revoked."), false)
		}
		a.applyState(c.State())
		a.refreshWebSharesFromState(c.State())
		a.invalidate()
	})
}

func (a *nativeApp) copySelectedWebShare() {
	share, ok := a.selectedWebShare()
	if !ok || share.URL == "" {
		return
	}
	if err := copyTextToClipboard(share.URL); err != nil {
		a.setNotice(fmt.Sprintf("%s: %v", a.tr("复制失败", "Copy failed"), err), true)
	} else {
		a.setNotice(a.tr("网页控制链接已复制。", "Web control link copied."), false)
	}
	a.invalidate()
}

func (a *nativeApp) openSelectedWebShare() {
	share, ok := a.selectedWebShare()
	if !ok || share.URL == "" {
		return
	}
	if err := openURL(share.URL); err != nil {
		a.setNotice(fmt.Sprintf("%s: %v", a.tr("打开链接失败", "Failed to open link"), err), true)
	}
	a.invalidate()
}

func (a *nativeApp) refreshSettingsLists() {
	devices, err := trust.NewStore(defaultTrustStorePath()).List()
	if err == nil {
		a.settingsUI.trustedRows = devices
	}
	entries, err := audit.NewStore(defaultAuditLogPath()).List(150)
	if err == nil {
		a.settingsUI.auditRows = entries
	}
	if a.settingsUI.selectedTrust >= len(a.settingsUI.trustedRows) {
		a.settingsUI.selectedTrust = -1
	}
}

func (a *nativeApp) draftLanguage() string {
	if a.settingsUI.language.Value != "" {
		return a.settingsUI.language.Value
	}
	return a.settingsSnapshot().Language
}

type optionItem struct {
	key   string
	label string
}

func (a *nativeApp) formRow(gtx layout.Context, label string, ed *widget.Editor, hint string) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return withWidth(gtx, gtx.Dp(170), func(gtx layout.Context) layout.Dimensions {
				return a.label(gtx, label, 13, palette.muted, font.SemiBold)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return a.inputField(gtx, ed, hint)
		}),
	)
}

func (a *nativeApp) compactRadioRow(gtx layout.Context, label string, group *widget.Enum, items []optionItem) layout.Dimensions {
	if group.Value == "" && len(items) > 0 {
		group.Value = items[0].key
	}
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(8)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.label(gtx, label, 13, palette.muted, font.SemiBold)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, 0, len(items))
			for _, item := range items {
				item := item
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return radioPill(gtx, a.theme, group, item.key, item.label)
				}))
			}
			return wrapFlex(gtx, children, gtx.Dp(8))
		}),
	)
}

func (a *nativeApp) switchRow(gtx layout.Context, label string, b *widget.Bool) layout.Dimensions {
	return roundedPanel(gtx, rgb(0xf8fafc), 8, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(12)}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return a.label(gtx, label, 13, palette.text, font.Normal)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					sw := material.Switch(a.theme, b, label)
					sw.Color.Enabled = palette.primary2
					return sw.Layout(gtx)
				}),
			)
		})
	})
}

func (a *nativeApp) radioRow(gtx layout.Context, label string, group *widget.Enum, items []optionItem) layout.Dimensions {
	if group.Value == "" && len(items) > 0 {
		group.Value = items[0].key
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle, Gap: gtx.Dp(12)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return withWidth(gtx, gtx.Dp(170), func(gtx layout.Context) layout.Dimensions {
				return a.label(gtx, label, 13, palette.muted, font.SemiBold)
			})
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, 0, len(items))
			for _, item := range items {
				item := item
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return radioPill(gtx, a.theme, group, item.key, item.label)
				}))
			}
			return wrapFlex(gtx, children, gtx.Dp(8))
		}),
	)
}

type buttonKind int

const (
	buttonPrimary buttonKind = iota
	buttonSecondary
	buttonAccent
	buttonDanger
	buttonDark
)

func (a *nativeApp) button(gtx layout.Context, b *widget.Clickable, txt string, kind buttonKind, enabled bool) layout.Dimensions {
	return buttonWithTheme(gtx, a.theme, b, txt, kind, enabled)
}

func buttonWithTheme(gtx layout.Context, th *material.Theme, b *widget.Clickable, txt string, kind buttonKind, enabled bool) layout.Dimensions {
	style := material.Button(th, b, txt)
	style.CornerRadius = 8
	style.Inset = layout.Inset{Top: 9, Bottom: 9, Left: 14, Right: 14}
	style.TextSize = 13
	style.Font.Weight = font.SemiBold
	switch kind {
	case buttonSecondary:
		style.Background = rgb(0xe9eef7)
		style.Color = palette.text
	case buttonAccent:
		style.Background = palette.primary2
		style.Color = rgb(0xffffff)
	case buttonDanger:
		style.Background = palette.danger
		style.Color = rgb(0xffffff)
	case buttonDark:
		style.Background = rgb(0x1f2937)
		style.Color = rgb(0xffffff)
	default:
		style.Background = palette.primary
		style.Color = rgb(0xffffff)
	}
	if !enabled {
		gtx = gtx.Disabled()
		style.Background = rgb(0xe5e7eb)
		style.Color = rgb(0x98a2b3)
	}
	return style.Layout(gtx)
}

func (a *nativeApp) inputField(gtx layout.Context, ed *widget.Editor, hint string) layout.Dimensions {
	return roundedPanel(gtx, rgb(0xffffff), 8, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.border, 8, 1)
		return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			st := material.Editor(a.theme, ed, hint)
			st.TextSize = 14
			st.Color = palette.text
			st.HintColor = rgb(0x667085)
			return st.Layout(gtx)
		})
	})
}

func (a *nativeApp) label(gtx layout.Context, txt string, size unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	return labelWithTheme(gtx, a.theme, txt, size, col, weight)
}

func labelWithTheme(gtx layout.Context, th *material.Theme, txt string, size unit.Sp, col color.NRGBA, weight font.Weight) layout.Dimensions {
	l := material.Label(th, size, txt)
	l.Color = col
	l.Font.Weight = weight
	l.WrapPolicy = text.WrapWords
	return l.Layout(gtx)
}

func (a *nativeApp) tr(zh, en string) string {
	if a.english.Load() {
		return en
	}
	return zh
}

func roundedPanel(gtx layout.Context, bg color.NRGBA, radius int, content layout.Widget) layout.Dimensions {
	return layout.Background{}.Layout(gtx,
		func(gtx layout.Context) layout.Dimensions {
			paint.FillShape(gtx.Ops, bg, clip.UniformRRect(image.Rectangle{Max: gtx.Constraints.Min}, radius).Op(gtx.Ops))
			return layout.Dimensions{Size: gtx.Constraints.Min}
		},
		content,
	)
}

func sectionHeader(gtx layout.Context, th *material.Theme, title, subtitle string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(3)}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, 16, title)
			l.Font.Weight = font.SemiBold
			l.Color = palette.text
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, 12, subtitle)
			l.Color = palette.muted
			l.WrapPolicy = text.WrapWords
			return l.Layout(gtx)
		}),
	)
}

func pageTitle(gtx layout.Context, th *material.Theme, title string) layout.Dimensions {
	l := material.Label(th, 20, title)
	l.Font.Weight = font.SemiBold
	l.Color = palette.text
	return l.Layout(gtx)
}

func statusPill(gtx layout.Context, th *material.Theme, txt string, col color.NRGBA) layout.Dimensions {
	return roundedPanel(gtx, withAlpha(col, 30), 8, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 7, Bottom: 7, Left: 10, Right: 10}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, 12, txt)
			l.Font.Weight = font.SemiBold
			l.Color = col
			return l.Layout(gtx)
		})
	})
}

func infoBlock(gtx layout.Context, th *material.Theme, title, value string, mono bool) layout.Dimensions {
	return roundedPanel(gtx, rgb(0xf8fafc), 8, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 12, Bottom: 12, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(4)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, 12, title)
					l.Color = palette.muted
					l.Font.Weight = font.SemiBold
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, 18, value)
					l.Color = palette.text
					l.Font.Weight = font.SemiBold
					if mono {
						l.Font.Typeface = font.Typeface("Cascadia Mono, Consolas, monospace")
					}
					l.WrapPolicy = text.WrapWords
					return l.Layout(gtx)
				}),
			)
		})
	})
}

func noticeBox(gtx layout.Context, th *material.Theme, msg string, col color.NRGBA) layout.Dimensions {
	return roundedPanel(gtx, withAlpha(col, 22), 8, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			l := material.Label(th, 12, msg)
			l.Color = col
			l.WrapPolicy = text.WrapWords
			return l.Layout(gtx)
		})
	})
}

func metricTile(gtx layout.Context, th *material.Theme, title, value string) layout.Dimensions {
	return roundedPanel(gtx, rgb(0xf8fafc), 8, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(4)}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, 11, title)
					l.Color = palette.muted
					l.Font.Weight = font.SemiBold
					return l.Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, 15, valueOrDash(value))
					l.Color = palette.text
					l.Font.Weight = font.SemiBold
					l.MaxLines = 1
					return l.Layout(gtx)
				}),
			)
		})
	})
}

func navButton(gtx layout.Context, th *material.Theme, b *widget.Clickable, txt string, selected bool) layout.Dimensions {
	bg := rgb(0xf8fafc)
	fg := rgb(0x334155)
	if selected {
		bg = palette.primary
		fg = rgb(0xffffff)
	}
	return material.Clickable(gtx, b, func(gtx layout.Context) layout.Dimensions {
		return roundedPanel(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
			if !selected {
				drawStroke(gtx, gtx.Constraints.Min, rgb(0xe2e8f0), 8, 1)
			}
			return layout.Inset{Top: 11, Bottom: 11, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, 14, txt)
				l.Color = fg
				l.Font.Weight = font.SemiBold
				l.MaxLines = 1
				return l.Layout(gtx)
			})
		})
	})
}

func clickableSurface(gtx layout.Context, b *widget.Clickable, bg color.NRGBA, radius int, content layout.Widget) layout.Dimensions {
	return material.Clickable(gtx, b, func(gtx layout.Context) layout.Dimensions {
		return roundedPanel(gtx, bg, radius, func(gtx layout.Context) layout.Dimensions {
			return content(gtx)
		})
	})
}

func radioPill(gtx layout.Context, th *material.Theme, group *widget.Enum, keyValue, label string) layout.Dimensions {
	group.Update(gtx)
	selected := group.Value == keyValue
	return group.Layout(gtx, keyValue, func(gtx layout.Context) layout.Dimensions {
		bg := rgb(0xf8fafc)
		fg := rgb(0x334155)
		if selected {
			bg = withAlpha(palette.primary, 28)
			fg = palette.primary
		}
		return roundedPanel(gtx, bg, 8, func(gtx layout.Context) layout.Dimensions {
			if !selected {
				drawStroke(gtx, gtx.Constraints.Min, rgb(0xe2e8f0), 8, 1)
			}
			return layout.Inset{Top: 8, Bottom: 8, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, 13, label)
				l.Color = fg
				l.Font.Weight = font.SemiBold
				l.MaxLines = 1
				return l.Layout(gtx)
			})
		})
	})
}

func rowButton(gtx layout.Context, th *material.Theme, b *widget.Clickable, txt string, selected bool) layout.Dimensions {
	bg := rgb(0xf8fafc)
	if selected {
		bg = withAlpha(palette.primary2, 30)
	}
	return layout.Inset{Bottom: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return clickableSurface(gtx, b, bg, 8, func(gtx layout.Context) layout.Dimensions {
			if !selected {
				drawStroke(gtx, gtx.Constraints.Min, rgb(0xe2e8f0), 8, 1)
			}
			return layout.Inset{Top: 10, Bottom: 10, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, 12, txt)
				l.Color = palette.text
				l.WrapPolicy = text.WrapWords
				return l.Layout(gtx)
			})
		})
	})
}

func emptyState(gtx layout.Context, th *material.Theme, txt string) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Label(th, 14, txt)
		l.Color = palette.muted
		return l.Layout(gtx)
	})
}

func logoMark(gtx layout.Context, size image.Point) layout.Dimensions {
	gtx.Constraints = layout.Exact(size)
	paint.FillShape(gtx.Ops, palette.primary, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Op(gtx.Ops))
	in := gtx.Dp(8)
	rect := image.Rectangle{Min: image.Pt(in, in), Max: size.Sub(image.Pt(in, in))}
	paint.FillShape(gtx.Ops, rgb(0xffffff), clip.UniformRRect(rect, gtx.Dp(4)).Op(gtx.Ops))
	inner := image.Rectangle{Min: rect.Min.Add(image.Pt(gtx.Dp(5), gtx.Dp(5))), Max: rect.Max.Sub(image.Pt(gtx.Dp(5), gtx.Dp(5)))}
	paint.FillShape(gtx.Ops, palette.primary2, clip.UniformRRect(inner, gtx.Dp(3)).Op(gtx.Ops))
	return layout.Dimensions{Size: size}
}

func drawStroke(gtx layout.Context, size image.Point, col color.NRGBA, radius int, width float32) {
	if size.X <= 0 || size.Y <= 0 {
		return
	}
	path := clip.UniformRRect(image.Rectangle{Max: size}, radius).Path(gtx.Ops)
	paint.FillShape(gtx.Ops, col, clip.Stroke{Path: path, Width: width}.Op())
}

func withWidth(gtx layout.Context, width int, child layout.Widget) layout.Dimensions {
	gtx.Constraints.Min.X = width
	gtx.Constraints.Max.X = width
	return child(gtx)
}

func wrapFlex(gtx layout.Context, children []layout.FlexChild, gap int) layout.Dimensions {
	const maxPerRow = 4
	rows := make([]layout.FlexChild, 0, (len(children)+maxPerRow-1)/maxPerRow)
	for start := 0; start < len(children); start += maxPerRow {
		end := min(start+maxPerRow, len(children))
		rowChildren := append([]layout.FlexChild(nil), children[start:end]...)
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Gap: gap}.Layout(gtx, rowChildren...)
		}))
	}
	return layout.Flex{Axis: layout.Vertical, Gap: gap}.Layout(gtx, rows...)
}

func decodeFrameImage(dataURL string) (image.Image, error) {
	_, raw, ok := strings.Cut(dataURL, ",")
	if !ok {
		raw = dataURL
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

func newClientLogger() (*slog.Logger, *os.File) {
	path := appLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})), nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})), nil
	}
	return slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})), f
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "."
	}
	return filepath.Dir(exe)
}

func appConfigPath() string {
	return filepath.Join(executableDir(), "beacondesk-client.json")
}

func appLogPath() string {
	return filepath.Join(executableDir(), "beacondesk-client.log")
}

func loadUISettings() uiSettings {
	settings := defaultUISettings()
	b, err := os.ReadFile(appConfigPath())
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return defaultUISettings()
	}
	return normalizeUISettings(settings)
}

func saveUISettings(settings uiSettings) error {
	settings = normalizeUISettings(settings)
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(appConfigPath()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(appConfigPath(), b, 0o600)
}

func normalizeUISettings(settings uiSettings) uiSettings {
	defaults := defaultUISettings()
	if settings.Language == "" {
		settings.Language = defaults.Language
	}
	if settings.ServerAddress == "" {
		settings.ServerAddress = defaults.ServerAddress
	}
	if settings.Transport == "" {
		settings.Transport = defaults.Transport
	}
	if settings.WebSocketPath == "" {
		settings.WebSocketPath = defaults.WebSocketPath
	}
	if settings.DeviceName == "" {
		settings.DeviceName = defaults.DeviceName
	}
	if settings.Role == "" {
		settings.Role = defaults.Role
	}
	if settings.RequestMode == "" {
		settings.RequestMode = defaults.RequestMode
	}
	if settings.WebShareTTLMinutes <= 0 {
		settings.WebShareTTLMinutes = defaults.WebShareTTLMinutes
	}
	settings.CaptureFPS = parsePresetInt(strconv.Itoa(settings.CaptureFPS), defaults.CaptureFPS, []int{15, 30, 45, 60, 90, 120})
	if resolutionKey(settings.CaptureMaxWidth, settings.CaptureMaxHeight) == "1280x720" && (settings.CaptureMaxWidth != 1280 || settings.CaptureMaxHeight != 720) {
		settings.CaptureMaxWidth = defaults.CaptureMaxWidth
		settings.CaptureMaxHeight = defaults.CaptureMaxHeight
	}
	settings.CaptureQuality = parsePresetInt(strconv.Itoa(settings.CaptureQuality), defaults.CaptureQuality, []int{40, 55, 72, 85})
	settings.BandwidthLimitKbps = parsePresetInt(strconv.Itoa(settings.BandwidthLimitKbps), defaults.BandwidthLimitKbps, []int{512, 1024, 2048, 4096, 8192, 12288, 20000, 50000})
	settings.StaticFrameSeconds = parsePresetInt(strconv.Itoa(settings.StaticFrameSeconds), defaults.StaticFrameSeconds, []int{1, 3, 5, 10, 15, 30})
	return settings
}

func autoStartValueName() string {
	return "BeaconDesk"
}

func isAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	value, _, err := k.GetStringValue(autoStartValueName())
	if err != nil {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return value != ""
	}
	return strings.Contains(strings.ToLower(value), strings.ToLower(exe))
}

func setAutoStart(enabled bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !enabled {
		_ = k.DeleteValue(autoStartValueName())
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(autoStartValueName(), fmt.Sprintf("%q", exe))
}

func defaultIdentityPath(deviceName string) string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	if deviceName == "" {
		deviceName = "default"
	}
	return filepath.Join(base, "BeaconDesk", deviceName+".device.json")
}

func defaultTrustStorePath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, "BeaconDesk", "trusted-devices.json")
}

func defaultAuditLogPath() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, "BeaconDesk", "audit-log.json")
}

func settingsNavItems(language string) []string {
	if language == "en" {
		return []string{"General", "Server", "Authorization", "Video & Network", "Web Control", "Trusted Devices", "Audit Log"}
	}
	return []string{"通用", "连接服务器", "授权", "画面与网络", "网页控制", "可信设备", "审计日志"}
}

func roleOptionItems(language string) []optionItem {
	if language == "en" {
		return []optionItem{{"peer", "Peer"}, {"controlled", "Controlled"}, {"controller", "Controller"}}
	}
	return []optionItem{{"peer", "双向"}, {"controlled", "被控端"}, {"controller", "控制端"}}
}

func fpsOptionItems() []optionItem {
	return []optionItem{{"15", "15"}, {"30", "30"}, {"45", "45"}, {"60", "60"}, {"90", "90"}, {"120", "120"}}
}

func qualityOptionItems(language string) []optionItem {
	if language == "en" {
		return []optionItem{{"40", "Smooth"}, {"55", "Balanced"}, {"72", "HD"}, {"85", "Ultra"}}
	}
	return []optionItem{{"40", "流畅"}, {"55", "均衡"}, {"72", "高清"}, {"85", "超清"}}
}

func resolutionOptionItems() []optionItem {
	return []optionItem{{"1280x720", "720p"}, {"1600x900", "900p"}, {"1920x1080", "1080p"}, {"2560x1440", "2K"}, {"3840x2160", "4K"}}
}

func bitrateOptionItems() []optionItem {
	return []optionItem{{"512", "512K"}, {"1024", "1M"}, {"2048", "2M"}, {"4096", "4M"}, {"8192", "8M"}, {"12288", "12M"}, {"20000", "20M"}, {"50000", "50M"}}
}

func staticIntervalOptionItems(language string) []optionItem {
	if language == "en" {
		return []optionItem{{"1", "1s"}, {"3", "3s"}, {"5", "5s"}, {"10", "10s"}, {"15", "15s"}, {"30", "30s"}}
	}
	return []optionItem{{"1", "1秒"}, {"3", "3秒"}, {"5", "5秒"}, {"10", "10秒"}, {"15", "15秒"}, {"30", "30秒"}}
}

func modeOptionItems(language string) []optionItem {
	if language == "en" {
		return []optionItem{{"view-control", "View and Control"}, {"view", "View Only"}}
	}
	return []optionItem{{"view-control", "观看并控制"}, {"view", "仅观看"}}
}

func roleLabel(role string, language string) string {
	switch role {
	case "controller":
		if language == "en" {
			return "Controller"
		}
		return "控制端"
	case "controlled":
		if language == "en" {
			return "Controlled"
		}
		return "被控端"
	default:
		if language == "en" {
			return "Peer"
		}
		return "双向"
	}
}

func modeLabel(mode string, language string) string {
	switch mode {
	case "view":
		if language == "en" {
			return "View Only"
		}
		return "仅观看"
	case "view-control":
		if language == "en" {
			return "View and Control"
		}
		return "观看并控制"
	default:
		return "-"
	}
}

func stateInputText(state coreclient.State, language string) string {
	if state.SessionID == "" {
		return "-"
	}
	if state.InputAllowed {
		if language == "en" {
			return "Allowed"
		}
		return "已允许"
	}
	if language == "en" {
		return "Not Allowed"
	}
	return "未允许"
}

func peerName(state coreclient.State) string {
	if state.PeerID == "" {
		return ""
	}
	if state.PeerName != "" {
		return state.PeerName + " (" + state.PeerID + ")"
	}
	return state.PeerID
}

func peerNameFromPending(state coreclient.State) string {
	if state.PendingPeerName != "" {
		return state.PendingPeerName + " (" + state.PendingPeerID + ")"
	}
	return state.PendingPeerID
}

func formatMaybe(v int64, format string) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf(format, v)
}

func formatQuality(state coreclient.State) string {
	if state.CaptureQuality == 0 {
		if state.LastFrameKind == protocol.StreamKindStatus || state.LastFrameKind == protocol.StreamKindError {
			return "等待画面"
		}
		return "-"
	}
	return fmt.Sprintf("%d / %d FPS", state.CaptureQuality, state.CurrentFPS)
}

func humanRelayError(message string, english bool) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "invalid token"):
		if english {
			return "Invalid access token. Make sure Settings -> Connection -> Access token matches the server shared_token, then save and reconnect."
		}
		return "访问令牌无效：请确认本机“设置 -> 连接服务器 -> 访问令牌”和服务器 shared_token 一致，然后保存并重新连接。"
	case strings.Contains(lower, "target device") && strings.Contains(lower, "offline"):
		if english {
			return "The target device is offline or the device ID is wrong. Make sure the peer also shows Registered and copy the full device ID."
		}
		return "目标设备不在线或设备 ID 填错：请确认对方也显示“已注册”，并复制完整设备 ID。"
	case strings.Contains(lower, "device is not registered") || strings.Contains(lower, "requesting device is not registered"):
		if english {
			return "This device is not registered yet. Check the access token and wait until the header shows Registered."
		}
		return "本机尚未注册成功：请检查访问令牌并等待顶部显示“已注册”。"
	case strings.Contains(lower, "invalid target temporary code"):
		if english {
			return "The temporary code is wrong or expired."
		}
		return "临时验证码错误或已失效。"
	case strings.Contains(lower, "target temporary code expired"):
		if english {
			return "The target temporary code has expired. Ask the peer to generate a new code."
		}
		return "目标临时验证码已过期，请让对方重新生成验证码。"
	case strings.Contains(lower, "session") && strings.Contains(lower, "not found"):
		if english {
			return "The remote session no longer exists. Please request assistance again."
		}
		return "远程会话已不存在，请重新发起协助请求。"
	default:
		if english {
			return "Relay error: " + message
		}
		return "中转错误：" + message
	}
}

func humanScreenError(message string, english bool) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "getdc") || strings.Contains(lower, "bitblt") || strings.Contains(lower, "getdibits") || strings.Contains(lower, "screen dimensions"):
		if english {
			return "Screen capture failed. Make sure the controlled device is unlocked, a user desktop is visible, and the app is running in that desktop session."
		}
		return "屏幕采集失败：请确认被控端未锁屏、当前用户桌面可见，并且客户端运行在该桌面会话内。"
	case strings.Contains(lower, "not implemented"):
		if english {
			return "Screen capture is not implemented on this platform yet."
		}
		return "当前平台暂未实现屏幕采集。"
	default:
		if english {
			return "Screen error: " + message
		}
		return "画面错误：" + message
	}
}

func mediaSummary(s uiSettings) string {
	return fmt.Sprintf("%d FPS / %dx%d / %s", s.CaptureFPS, s.CaptureMaxWidth, s.CaptureMaxHeight, bitrateLabel(s.BandwidthLimitKbps))
}

func bitrateLabel(kbps int) string {
	if kbps >= 1000 {
		if kbps%1000 == 0 {
			return fmt.Sprintf("%dM", kbps/1000)
		}
		return fmt.Sprintf("%.1fM", float64(kbps)/1000)
	}
	return fmt.Sprintf("%dK", kbps)
}

func formatLoss(v int64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("%.2f%%", float64(v)/100)
}

func formatMillis(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04:05")
}

func valueOrDash(v string) string {
	return valueOr(v, "-")
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func openURL(rawURL string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL).Start()
}

func copyTextToClipboard(text string) error {
	cmd := exec.Command("clip.exe")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func textOr(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func setEditorText(ed *widget.Editor, text string) {
	if ed.Text() != text {
		ed.SetText(text)
	}
}

func parseInt(v string, fallback, minValue, maxValue int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return clampInt(n, minValue, maxValue)
}

func parsePresetInt(value string, fallback int, allowed []int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	for _, allowedValue := range allowed {
		if n == allowedValue {
			return n
		}
	}
	return fallback
}

func resolutionKey(width, height int) string {
	key := fmt.Sprintf("%dx%d", width, height)
	for _, item := range resolutionOptionItems() {
		if item.key == key {
			return key
		}
	}
	return "1280x720"
}

func parseResolutionKey(value string, fallbackWidth, fallbackHeight int) (int, int) {
	for _, item := range resolutionOptionItems() {
		if item.key == value {
			w, h, ok := strings.Cut(item.key, "x")
			if !ok {
				break
			}
			width, errW := strconv.Atoi(w)
			height, errH := strconv.Atoi(h)
			if errW == nil && errH == nil {
				return width, height
			}
		}
	}
	return fallbackWidth, fallbackHeight
}

func ensureClicks(clicks *[]widget.Clickable, n int) {
	if len(*clicks) >= n {
		return
	}
	*clicks = append(*clicks, make([]widget.Clickable, n-len(*clicks))...)
}

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{A: 255, R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}

func withAlpha(c color.NRGBA, alpha uint8) color.NRGBA {
	c.A = alpha
	return c
}

func fitRect(area image.Rectangle, src image.Point) image.Rectangle {
	if src.X <= 0 || src.Y <= 0 || area.Dx() <= 0 || area.Dy() <= 0 {
		return area
	}
	w := area.Dx()
	h := w * src.Y / src.X
	if h > area.Dy() {
		h = area.Dy()
		w = h * src.X / src.Y
	}
	x := area.Min.X + (area.Dx()-w)/2
	y := area.Min.Y + (area.Dy()-h)/2
	return image.Rect(x, y, x+w, y+h)
}

func pointerButtonName(buttons pointer.Buttons) string {
	switch {
	case buttons.Contain(pointer.ButtonSecondary):
		return "right"
	case buttons.Contain(pointer.ButtonTertiary):
		return "middle"
	case buttons.Contain(pointer.ButtonPrimary):
		return "left"
	default:
		return "left"
	}
}

func gioKeyCode(name key.Name) string {
	s := string(name)
	if len(s) == 1 {
		ch := s[0]
		if ch >= 'A' && ch <= 'Z' {
			return "Key" + s
		}
		if ch >= '0' && ch <= '9' {
			return "Digit" + s
		}
	}
	switch name {
	case key.NameReturn, key.NameEnter:
		return "Enter"
	case key.NameEscape:
		return "Escape"
	case key.NameDeleteBackward:
		return "Backspace"
	case key.NameDeleteForward:
		return "Delete"
	case key.NameTab:
		return "Tab"
	case key.NameSpace:
		return "Space"
	case key.NameLeftArrow:
		return "ArrowLeft"
	case key.NameRightArrow:
		return "ArrowRight"
	case key.NameUpArrow:
		return "ArrowUp"
	case key.NameDownArrow:
		return "ArrowDown"
	case key.NameHome:
		return "Home"
	case key.NameEnd:
		return "End"
	case key.NamePageUp:
		return "PageUp"
	case key.NamePageDown:
		return "PageDown"
	default:
		return s
	}
}

func gioVirtualKey(name key.Name) int {
	s := string(name)
	if len(s) == 1 {
		ch := s[0]
		if ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			return int(ch)
		}
	}
	switch name {
	case key.NameReturn, key.NameEnter:
		return 0x0D
	case key.NameEscape:
		return 0x1B
	case key.NameDeleteBackward:
		return 0x08
	case key.NameDeleteForward:
		return 0x2E
	case key.NameTab:
		return 0x09
	case key.NameSpace:
		return 0x20
	case key.NameLeftArrow:
		return 0x25
	case key.NameUpArrow:
		return 0x26
	case key.NameRightArrow:
		return 0x27
	case key.NameDownArrow:
		return 0x28
	case key.NameHome:
		return 0x24
	case key.NameEnd:
		return 0x23
	case key.NamePageUp:
		return 0x21
	case key.NamePageDown:
		return 0x22
	default:
		return 0
	}
}

func gioModifiers(mods key.Modifiers) []string {
	out := []string{}
	if mods.Contain(key.ModCtrl) {
		out = append(out, "ctrl")
	}
	if mods.Contain(key.ModAlt) {
		out = append(out, "alt")
	}
	if mods.Contain(key.ModShift) {
		out = append(out, "shift")
	}
	if mods.Contain(key.ModSuper) {
		out = append(out, "super")
	}
	return out
}

func clampInt(v, minValue, maxValue int) int {
	if v < minValue {
		return minValue
	}
	if v > maxValue {
		return maxValue
	}
	return v
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func versionInfo() map[string]string {
	return map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	}
}
