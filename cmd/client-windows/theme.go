package main

import (
	"image/color"
	"os"

	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/font/opentype"
	"gioui.org/text"
	"gioui.org/widget/material"
)

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
	bg:       rgb(0xf8fafc),
	panel:    rgb(0xffffff),
	panel2:   rgb(0xf1f5f9),
	card:     rgb(0xffffff),
	text:     rgb(0x0f172a),
	muted:    rgb(0x64748b),
	border:   rgb(0xe2e8f0),
	primary:  rgb(0x2563eb),
	primary2: rgb(0x10b981),
	success:  rgb(0x22c55e),
	warning:  rgb(0xf59e0b),
	danger:   rgb(0xef4444),
	ink:      rgb(0x0b1220),
}

func newAppTheme() *material.Theme {
	th := material.NewTheme()
	th.Palette.Bg = palette.bg
	th.Palette.Fg = palette.text
	th.Palette.ContrastBg = palette.primary
	th.Palette.ContrastFg = rgb(0xffffff)
	th.TextSize = 15
	th.Face = font.Typeface("Segoe UI, Microsoft YaHei UI, Microsoft YaHei, Noto Sans CJK SC, sans-serif")
	th.Shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(appFontCollection()))
	return th
}

func appFontCollection() []font.FontFace {
	collection := append([]font.FontFace(nil), gofont.Collection()...)
	for _, path := range []string{
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\msyhbd.ttc`,
		`C:\Windows\Fonts\simsun.ttc`,
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		faces, err := opentype.ParseCollection(b)
		if err != nil {
			continue
		}
		collection = append(collection, faces...)
	}
	return collection
}

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{A: 255, R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}

func withAlpha(c color.NRGBA, alpha uint8) color.NRGBA {
	c.A = alpha
	return c
}
