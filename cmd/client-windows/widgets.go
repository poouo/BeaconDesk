package main

import (
	"image"
	"image/color"

	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

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
	style.CornerRadius = 12
	style.Inset = layout.Inset{Top: 12, Bottom: 12, Left: 20, Right: 20}
	style.TextSize = 13
	style.Font.Weight = font.SemiBold
	switch kind {
	case buttonSecondary:
		style.Background = rgb(0xf1f5f9)
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
	return roundedPanel(gtx, rgb(0xffffff), 12, func(gtx layout.Context) layout.Dimensions {
		drawStroke(gtx, gtx.Constraints.Min, palette.border, 12, 1)
		return layout.Inset{Top: 12, Bottom: 12, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			st := material.Editor(a.theme, ed, hint)
			st.TextSize = 14
			st.Color = palette.text
			st.HintColor = palette.muted
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
	return roundedPanel(gtx, palette.panel2, 12, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 16, Bottom: 16, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical, Gap: gtx.Dp(6)}.Layout(gtx,
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
