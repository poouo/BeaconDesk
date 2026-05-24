//go:build !windows

package desktop

import (
	"context"
	"errors"
)

func NewCapturer(opts CaptureOptions) (Capturer, error) {
	return nil, errors.New("desktop capture is only planned for Windows clients")
}

type noopCapturer struct{}

func (noopCapturer) Capture(ctx context.Context) (Frame, error) {
	return Frame{}, errors.New("desktop capture is not implemented")
}

func (noopCapturer) Close() error {
	return nil
}
