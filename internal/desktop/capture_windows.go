//go:build windows

package desktop

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	gdi32                      = syscall.NewLazyDLL("gdi32.dll")
	procGetDC                  = user32.NewProc("GetDC")
	procGetWindowDC            = user32.NewProc("GetWindowDC")
	procGetDesktopWindow       = user32.NewProc("GetDesktopWindow")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
)

const (
	smCXScreen   = 0
	smCYScreen   = 1
	srccopy      = 0x00CC0020
	captureBlt   = 0x40000000
	dibRGBColors = 0
	biRGB        = 0
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

type windowsCapturer struct {
	opts CaptureOptions
	id   atomic.Int64
}

func NewCapturer(opts CaptureOptions) (Capturer, error) {
	if opts.MaxWidth <= 0 {
		opts.MaxWidth = 1280
	}
	if opts.MaxHeight <= 0 {
		opts.MaxHeight = 720
	}
	if opts.Quality <= 0 {
		opts.Quality = 55
	}
	return &windowsCapturer{opts: opts}, nil
}

func (c *windowsCapturer) Capture(ctx context.Context) (Frame, error) {
	select {
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	default:
	}

	img, err := captureScreenBGRA()
	if err != nil {
		return Frame{}, err
	}
	scaled := scaleImage(img, c.opts.MaxWidth, c.opts.MaxHeight)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: c.opts.Quality}); err != nil {
		return Frame{}, err
	}
	return Frame{
		ID:        c.id.Add(1),
		Width:     scaled.Bounds().Dx(),
		Height:    scaled.Bounds().Dy(),
		Codec:     "jpeg",
		Keyframe:  true,
		Timestamp: time.Now().UnixMilli(),
		Data:      buf.Bytes(),
	}, nil
}

func (c *windowsCapturer) Close() error {
	return nil
}

func (c *windowsCapturer) SetQuality(quality int) {
	if quality < 20 {
		quality = 20
	}
	if quality > 90 {
		quality = 90
	}
	c.opts.Quality = quality
}

func captureScreenBGRA() (*image.RGBA, error) {
	width, _, _ := procGetSystemMetrics.Call(smCXScreen)
	height, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if width == 0 || height == 0 {
		return nil, errors.New("failed to read screen dimensions")
	}

	desktopWindow, _, _ := procGetDesktopWindow.Call()
	screenDC, _, err := procGetWindowDC.Call(desktopWindow)
	if screenDC == 0 {
		screenDC, _, err = procGetDC.Call(0)
		desktopWindow = 0
	}
	if screenDC == 0 {
		return nil, winErr("GetDC", err)
	}
	defer procReleaseDC.Call(desktopWindow, screenDC)

	memDC, _, err := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return nil, winErr("CreateCompatibleDC", err)
	}
	defer procDeleteDC.Call(memDC)

	bitmap, _, err := procCreateCompatibleBitmap.Call(screenDC, width, height)
	if bitmap == 0 {
		return nil, winErr("CreateCompatibleBitmap", err)
	}
	defer procDeleteObject.Call(bitmap)

	oldObject, _, err := procSelectObject.Call(memDC, bitmap)
	if oldObject == 0 {
		return nil, winErr("SelectObject", err)
	}
	defer procSelectObject.Call(memDC, oldObject)

	ok, _, err := procBitBlt.Call(memDC, 0, 0, width, height, screenDC, 0, 0, srccopy|captureBlt)
	if ok == 0 {
		return nil, winErr("BitBlt", err)
	}

	stride := int(width) * 4
	pixels := make([]byte, stride*int(height))
	info := bitmapInfo{
		Header: bitmapInfoHeader{
			Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			Width:       int32(width),
			Height:      -int32(height),
			Planes:      1,
			BitCount:    32,
			Compression: biRGB,
			SizeImage:   uint32(len(pixels)),
		},
	}
	lines, _, err := procGetDIBits.Call(
		memDC,
		bitmap,
		0,
		height,
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&info)),
		dibRGBColors,
	)
	if lines == 0 {
		return nil, winErr("GetDIBits", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		for x := 0; x < int(width); x++ {
			src := y*stride + x*4
			dst := y*img.Stride + x*4
			img.Pix[dst+0] = pixels[src+2]
			img.Pix[dst+1] = pixels[src+1]
			img.Pix[dst+2] = pixels[src+0]
			img.Pix[dst+3] = 255
		}
	}
	return img, nil
}

func scaleImage(src image.Image, maxWidth int, maxHeight int) image.Image {
	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if maxWidth <= 0 || maxHeight <= 0 || (width <= maxWidth && height <= maxHeight) {
		return src
	}
	scale := math.Min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height))
	dstWidth := max(1, int(math.Round(float64(width)*scale)))
	dstHeight := max(1, int(math.Round(float64(height)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))

	for y := 0; y < dstHeight; y++ {
		srcY := bounds.Min.Y + int(float64(y)/float64(dstHeight)*float64(height))
		for x := 0; x < dstWidth; x++ {
			srcX := bounds.Min.X + int(float64(x)/float64(dstWidth)*float64(width))
			r, g, b, a := src.At(srcX, srcY).RGBA()
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}
	return dst
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func winErr(name string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return errors.New(name + " failed")
	}
	return errors.New(name + " failed: " + err.Error())
}
