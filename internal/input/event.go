package input

type MouseEvent struct {
	X            int    `json:"x"`
	Y            int    `json:"y"`
	SourceWidth  int    `json:"source_width,omitempty"`
	SourceHeight int    `json:"source_height,omitempty"`
	Button       string `json:"button,omitempty"`
	Action       string `json:"action"`
	WheelDelta   int    `json:"wheel_delta,omitempty"`
}

type KeyboardEvent struct {
	Key       string   `json:"key"`
	Code      string   `json:"code,omitempty"`
	KeyCode   int      `json:"key_code,omitempty"`
	ScanCode  int      `json:"scan_code,omitempty"`
	Action    string   `json:"action"`
	Modifiers []string `json:"modifiers,omitempty"`
}

type Injector interface {
	Mouse(MouseEvent) error
	Keyboard(KeyboardEvent) error
	Close() error
}
