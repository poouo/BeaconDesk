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
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/poouo/BeaconDesk/internal/audit"
	coreclient "github.com/poouo/BeaconDesk/internal/client"
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
		app.Size(unit.Dp(1200), unit.Dp(800)),
		app.MinSize(unit.Dp(1000), unit.Dp(740)),
	)
	a.window = w

	if hasSavedConfig() {
		a.safeGo("auto-connect", func() {
			time.Sleep(150 * time.Millisecond)
			a.connect()
		})
	}

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
				col = palette.primary // 连通状态使用品牌主色
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
				col = palette.success // 注册成功使用绿色
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
	return roundedPanel(gtx, palette.card, 16, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.border, 12, 1)
		return layout.UniformInset(24).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(20)}.Layout(gtx,
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
	return roundedPanel(gtx, palette.card, 16, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.border, 16, 1)
		return layout.UniformInset(20).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(16)}.Layout(gtx,
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
	return roundedPanel(gtx, color.NRGBA{R: 255, G: 251, B: 235, A: 255}, 12, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.warning, 12, 1)
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
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
	return roundedPanel(gtx, palette.card, 16, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.border, 16, 1)
		return layout.UniformInset(20).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(18)}.Layout(gtx,
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
	return roundedPanel(gtx, palette.card, 16, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.border, 16, 1)
		return layout.UniformInset(20).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(12)}.Layout(gtx,
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
		c := coreclient.New(clientOptionsFromSettings(opts), a.logger)
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
	if a.client != nil {
		a.state = a.client.State()
		a.decodeLatestFrameLocked(a.state)
	}
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
	rw.initControls(a.settings)
	a.remote = rw
	a.mu.Unlock()
	a.safeGo("remote-window", rw.run)
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
		if a.remote != nil {
			a.remote.initControls(next)
		}
		c := a.client
		a.setNoticeLocked(a.tr("设置已保存，当前连接会立即应用可实时生效的参数。", "Settings saved. Runtime settings apply to the current connection immediately."), false)
		a.mu.Unlock()
		if c != nil {
			c.UpdateOptions(clientOptionsFromSettings(next))
			a.applyState(c.State())
		}
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

func hasSavedConfig() bool {
	info, err := os.Stat(appConfigPath())
	return err == nil && !info.IsDir()
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

func clientOptionsFromSettings(settings uiSettings) coreclient.Options {
	return coreclient.Options{
		ServerAddress:      settings.ServerAddress,
		Transport:          settings.Transport,
		UseTLS:             settings.UseTLS,
		WebSocketPath:      settings.WebSocketPath,
		TLSServerName:      settings.TLSServerName,
		TLSSkipVerify:      settings.TLSSkipVerify,
		TLSCertSHA256:      settings.TLSCertSHA256,
		DeviceName:         settings.DeviceName,
		Role:               settings.Role,
		RequestMode:        settings.RequestMode,
		Token:              settings.Token,
		IdentityPath:       defaultIdentityPath(settings.DeviceName),
		TrustStorePath:     defaultTrustStorePath(),
		AuditLogPath:       defaultAuditLogPath(),
		AutoAccept:         settings.AutoAccept,
		EnableInput:        settings.EnableInput,
		SendMockFrames:     settings.SendMockFrames,
		SendScreenFrames:   settings.SendScreenFrames,
		CaptureFPS:         settings.CaptureFPS,
		CaptureMaxWidth:    settings.CaptureMaxWidth,
		CaptureMaxHeight:   settings.CaptureMaxHeight,
		CaptureQuality:     settings.CaptureQuality,
		BandwidthLimitKbps: settings.BandwidthLimitKbps,
		StaticFrameSeconds: settings.StaticFrameSeconds,
	}
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

func sessionRoleLabel(role string, language string) string {
	switch role {
	case protocol.RoleController:
		if language == "en" {
			return "Controller"
		}
		return "主控"
	case protocol.RoleControlled:
		if language == "en" {
			return "Controlled"
		}
		return "被控"
	default:
		return "-"
	}
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
