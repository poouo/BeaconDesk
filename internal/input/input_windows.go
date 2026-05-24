//go:build windows

package input

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	procMapVirtualKeyW   = user32.NewProc("MapVirtualKeyW")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfMove       = 0x0001
	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfWheel      = 0x0800
	mouseeventfAbsolute   = 0x8000

	keyeventfKeyUp    = 0x0002
	keyeventfScancode = 0x0008

	smCXScreen = 0
	smCYScreen = 1
)

type windowsInjector struct{}

func NewInjector() (Injector, error) {
	return &windowsInjector{}, nil
}

func (windowsInjector) Mouse(event MouseEvent) error {
	var inputs []input
	if event.Action == "move" || event.X != 0 || event.Y != 0 {
		x, y := normalizeAbsolute(event.X, event.Y, event.SourceWidth, event.SourceHeight)
		inputs = append(inputs, newMouseInput(mouseInput{
			Dx:      x,
			Dy:      y,
			DwFlags: mouseeventfMove | mouseeventfAbsolute,
		}))
	}

	flags := mouseFlags(event.Button, event.Action)
	if flags != 0 {
		inputs = append(inputs, newMouseInput(mouseInput{DwFlags: flags}))
	}
	if event.Action == "wheel" && event.WheelDelta != 0 {
		inputs = append(inputs, newMouseInput(mouseInput{
			MouseData: uint32(int32(event.WheelDelta)),
			DwFlags:   mouseeventfWheel,
		}))
	}
	if len(inputs) == 0 {
		return nil
	}
	return sendInputs(inputs)
}

func (windowsInjector) Keyboard(event KeyboardEvent) error {
	vk := virtualKey(event)
	if vk == 0 {
		return fmt.Errorf("unsupported key %q code %q", event.Key, event.Code)
	}
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)
	flags := uint32(keyeventfScancode)
	if event.Action == "up" {
		flags |= keyeventfKeyUp
	}
	return sendInputs([]input{
		newKeyboardInput(keyboardInput{
			WScan:   uint16(scan),
			DwFlags: flags,
		}),
	})
}

func (windowsInjector) Close() error {
	return nil
}

type input struct {
	Type uint32
	U    inputUnion
}

type inputUnion struct {
	_    [0]uintptr
	Data [unsafe.Sizeof(mouseInput{})]byte
}

type mouseInput struct {
	Dx          int32
	Dy          int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type keyboardInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

func newMouseInput(mi mouseInput) input {
	var in input
	in.Type = inputMouse
	*(*mouseInput)(unsafe.Pointer(&in.U.Data[0])) = mi
	return in
}

func newKeyboardInput(ki keyboardInput) input {
	var in input
	in.Type = inputKeyboard
	*(*keyboardInput)(unsafe.Pointer(&in.U.Data[0])) = ki
	return in
}

func sendInputs(inputs []input) error {
	if len(inputs) == 0 {
		return nil
	}
	ret, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(input{}),
	)
	if ret != uintptr(len(inputs)) {
		if err != nil && err != syscall.Errno(0) {
			return fmt.Errorf("SendInput failed: %w", err)
		}
		return fmt.Errorf("SendInput sent %d of %d events", ret, len(inputs))
	}
	return nil
}

func normalizeAbsolute(x int, y int, sourceWidth int, sourceHeight int) (int32, int32) {
	screenW, _, _ := procGetSystemMetrics.Call(smCXScreen)
	screenH, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if sourceWidth <= 0 {
		sourceWidth = int(screenW)
	}
	if sourceHeight <= 0 {
		sourceHeight = int(screenH)
	}
	if sourceWidth <= 1 {
		sourceWidth = 1
	}
	if sourceHeight <= 1 {
		sourceHeight = 1
	}
	return int32(x * 65535 / (sourceWidth - 1)), int32(y * 65535 / (sourceHeight - 1))
}

func mouseFlags(button string, action string) uint32 {
	switch strings.ToLower(button) + ":" + strings.ToLower(action) {
	case "left:down":
		return mouseeventfLeftDown
	case "left:up":
		return mouseeventfLeftUp
	case "right:down":
		return mouseeventfRightDown
	case "right:up":
		return mouseeventfRightUp
	case "middle:down":
		return mouseeventfMiddleDown
	case "middle:up":
		return mouseeventfMiddleUp
	default:
		return 0
	}
}

func virtualKey(event KeyboardEvent) uint16 {
	if event.KeyCode > 0 {
		return uint16(event.KeyCode)
	}
	code := strings.ToUpper(event.Code)
	if strings.HasPrefix(code, "KEY") && len(code) == 4 {
		return uint16(code[3])
	}
	if strings.HasPrefix(code, "DIGIT") && len(code) == 6 {
		return uint16(code[5])
	}
	switch code {
	case "ENTER":
		return 0x0D
	case "ESCAPE":
		return 0x1B
	case "BACKSPACE":
		return 0x08
	case "TAB":
		return 0x09
	case "SPACE":
		return 0x20
	case "ARROWLEFT":
		return 0x25
	case "ARROWUP":
		return 0x26
	case "ARROWRIGHT":
		return 0x27
	case "ARROWDOWN":
		return 0x28
	case "DELETE":
		return 0x2E
	case "HOME":
		return 0x24
	case "END":
		return 0x23
	case "PAGEUP":
		return 0x21
	case "PAGEDOWN":
		return 0x22
	default:
		return 0
	}
}
