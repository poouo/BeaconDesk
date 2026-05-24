package desktop

import "context"

type CaptureOptions struct {
	MaxWidth  int
	MaxHeight int
	FPS       int
	Quality   int
}

type Frame struct {
	ID        int64
	Width     int
	Height    int
	Codec     string
	Keyframe  bool
	Timestamp int64
	Data      []byte
}

type Capturer interface {
	Capture(ctx context.Context) (Frame, error)
	Close() error
}

type QualitySetter interface {
	SetQuality(quality int)
}
