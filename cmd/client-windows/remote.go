package main

import (
	"context"
	"image"
	"strconv"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	coreclient "github.com/poouo/BeaconDesk/internal/client"
	"github.com/poouo/BeaconDesk/internal/input"
)

type remoteWindow struct {
	app           *nativeApp
	window        *app.Window
	ops           op.Ops
	theme         *material.Theme
	pointerTag    struct{}
	keyTag        struct{}
	closeBtn      widget.Clickable
	syncBtn       widget.Clickable
	screenToggle  widget.Bool
	fpsPreset     widget.Enum
	resPreset     widget.Enum
	qualityPreset widget.Enum
	bitratePreset widget.Enum
	staticPreset  widget.Enum
	controlsReady bool
	lastControl   remoteStreamControl
	lastButton    string
	lastMoveSent  time.Time
}

type remoteStreamControl struct {
	SendScreenFrames bool
	CaptureFPS       int
	CaptureMaxWidth  int
	CaptureMaxHeight int
	CaptureQuality   int
	BandwidthKbps    int
	StaticSeconds    int
}

func (rw *remoteWindow) run() {
	defer func() {
		if r := recover(); r != nil {
			rw.app.logger.Error("remote window panic", "panic", r)
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
			rw.app.renderMu.Lock()
			rw.ops.Reset()
			gtx := app.NewContext(&rw.ops, e)
			rw.layout(gtx)
			e.Frame(gtx.Ops)
			rw.app.renderMu.Unlock()
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
	title := rw.app.tr("远程画面", "Remote Screen")
	return layout.UniformInset(10).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return labelWithTheme(gtx, rw.theme, title, 17, rgb(0xffffff), font.SemiBold)
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

	if img.Size().X > 0 {
		defer op.Offset(screenRect.Min).Push(gtx.Ops).Pop()
		gtx2 := gtx
		gtx2.Constraints = layout.Exact(screenRect.Size())
		widget.Image{Src: img, Fit: widget.Contain}.Layout(gtx2)
	} else {
		layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return labelWithTheme(gtx, rw.theme, "等待远程画面...", 15, rgb(0xa7b0c0), font.Normal)
		})
	}

	rw.handleRemoteInput(gtx, screenRect, size, state)
	return layout.Dimensions{Size: bounds.Size()}
}

func (rw *remoteWindow) handleRemoteInput(gtx layout.Context, rect image.Rectangle, source image.Point, state coreclient.State) {
	defer clip.Rect(rect).Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &rw.pointerTag)
	event.Op(gtx.Ops, &rw.keyTag)

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

		x := (p.X - rect.Min.X) * source.X / rect.Dx()
		y := (p.Y - rect.Min.Y) * source.Y / rect.Dy()

		event := input.MouseEvent{X: x, Y: y, SourceWidth: source.X, SourceHeight: source.Y}
		switch pe.Kind {
		case pointer.Press:
			event.Button = "left"
			event.Action = "down"
		case pointer.Release:
			event.Button = "left"
			event.Action = "up"
		case pointer.Move, pointer.Drag:
			event.Action = "move"
		}
		rw.sendMouse(event)
	}
}

func (rw *remoteWindow) sendMouse(event input.MouseEvent) {
	c := rw.app.currentClient()
	if c == nil {
		return
	}
	rw.app.safeGo("send-mouse", func() {
		ctx, cancel := context.WithTimeout(rw.app.ctx, 2*time.Second)
		defer cancel()
		c.SendMouse(ctx, event)
	})
}

func (rw *remoteWindow) initControls(settings uiSettings) {
	rw.screenToggle.Value = settings.SendScreenFrames
	rw.fpsPreset.Value = strconv.Itoa(settings.CaptureFPS)
	rw.lastControl = rw.currentControl()
	rw.controlsReady = true
}

func (rw *remoteWindow) currentControl() remoteStreamControl {
	return remoteStreamControl{
		SendScreenFrames: rw.screenToggle.Value,
		CaptureFPS:       15,
	}
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
