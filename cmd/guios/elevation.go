package main

import (
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
)

// Surfaces stand out by their color and an edge, crisp, on whole pixels.
// In the light, the edge is a small soft shadow and no line, but for the
// panes', a hairline (paned); in the dark,
// where shadows hardly show, a hairline a little lighter than the
// surface, over a dark shadow, as a lit edge. These are kero's levels.

// raised lifts a surface off what is under it a little: the card, the
// selected row, buttons.
func raised(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return darkShadow(litEdge(e, pal))
	}
	return e.Shadow(0, 1, 2, 0, pal.cardShadow).Shadow(0, 1, 6, 0, pal.cardShadow2)
}

// paned edges a pane's card, and the settings', flat on the wash: in the
// light a hairline and no shadow, as Rex's panes; in the dark, as raised.
func paned(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return raised(e, pal)
	}
	// Measured from Rex's at 2x: a darker outer pixel, a lighter inner.
	return e.Border(0.5, ui.RGBA(0, 0, 0, 0.106)).InsetShadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.063))
}

// chosen lifts the chosen row of a list, the sidebars', a hair: an even
// ring a shade darker than what is under it, and a faint shadow close
// under it, as Rex's rows, lighter than a card's. In the dark, as raised.
func chosen(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return raised(e, pal)
	}
	return e.Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.06)).Shadow(0, 0.5, 2, 0, ui.RGBA(0, 0, 0, 0.05))
}

// menu lifts a menu as macOS 27 does, measured from its own: a fill a
// shade off white, lighter at the top; a dark hairline, a pixel of about
// 30% black, with a pixel of white inside it top and bottom; and a soft
// shadow, a little
// below, darkening what is under the menu's edge by about 9% at its sides
// and 16% under it, gone some 9 DIPs out.
func menu(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return e.Background(pal.popover).InsetShadow(0, 0, 0, 1, ui.RGBA(255, 255, 255, 0.12)).
			Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.25)).Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.4))
	}
	return e.LinearGradient(ui.LinearGradient{Angle: 180, From: ui.Hex("#fbfbfb"), To: ui.Hex("#f4f4f5")}).
		InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.9)).InsetShadow(0, -1, 0, 0, ui.RGBA(255, 255, 255, 0.9)).
		InsetShadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.3)).
		Shadow(0, 5, 10, 0, ui.RGBA(0, 0, 0, 0.22))
}

// floating lifts a surface above the window: the palette, dialogs.
func floating(e ui.Element, pal *palette) ui.Element {
	if pal.dark {
		return darkShadow(litEdge(e, pal)).Shadow(0, 16, 40, 0, pal.floatShadow)
	}
	return e.Shadow(0, 1, 3, 0, pal.cardShadow).Shadow(0, 16, 40, 0, pal.floatShadow)
}

// litEdge draws a dark surface's edge as Rex's docs draw their code
// blocks: a 1px border in the text's color, 18% at the top fading
// evenly to 6% at the bottom, as if lit from above.
func litEdge(e ui.Element, pal *palette) ui.Element {
	return e.Border(1, pal.text.Alpha(0.12)).BorderLinearGradient(ui.LinearGradient{
		Angle: 180, From: pal.text.Alpha(0.18), To: pal.text.Alpha(0.06),
	})
}

// darkShadow is Rex's surface shadow in the dark: three tight layers.
func darkShadow(e ui.Element) ui.Element {
	return e.Shadow(0, 1, 1, 0, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 2, 2, -1, ui.RGBA(0, 0, 0, 0.2)).Shadow(0, 4, 6, -3, ui.RGBA(0, 0, 0, 0.24))
}

// face styles a control's face: see palette.control.
func face(e ui.Element, pal *palette, bg ui.Color) ui.Element {
	e.Background(bg)
	if pal.controlShadow {
		return e.Shadow(0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.13)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.05))
	}
	return darkShadow(litEdge(e, pal))
}

// button is kero's button: a raised surface, or the accent's for the
// primary one, with a label and, optionally, its keys.
func button(c *ui.Context, key, label, keys string, primary bool, pal *palette) bool {
	return sizedButton(c, key, label, keys, primary, hControl, rControl, pal)
}

// fieldButton is a button beside a text field, of the field's height and
// corners, as a form's Add.
func fieldButton(c *ui.Context, key, label string, primary bool, pal *palette) bool {
	return sizedButton(c, key, label, "", primary, controlH+2, rControl, pal)
}

func sizedButton(c *ui.Context, key, label, keys string, primary bool, height, radius float32, pal *palette) bool {
	return sizedButtonAt(c, key, label, keys, primary, height, radius, pal, false)
}

func sizedButtonAt(c *ui.Context, key, label, keys string, primary bool, height, radius float32, pal *palette, focus bool) bool {
	kind := btnSecondary
	if primary {
		kind = btnPrimary
	}
	return kindButton(c, key, label, keys, kind, height, radius, pal, focus)
}

// The kinds of button (DESIGN.md section 6): a view has one primary at
// most, and a destructive one ends or deletes something.
const (
	btnSecondary = iota
	btnPrimary
	btnDanger
)

func kindButton(c *ui.Context, key, label, keys string, kind int, height, radius float32, pal *palette, focus bool) bool {
	pad := float32(sp3)
	if height < hControl {
		pad = 10
	}
	b := ui.ButtonBase(c.Key(key)).Height(height).Padding(0, pad).Gap(6).Radius(radius).AlignItems(ui.Center).Shrink(0).Label(label)
	if focus {
		b.AutoFocus()
	}
	bg, fg := pal.control, pal.text
	switch kind {
	case btnPrimary:
		bg, fg = pal.accent, onAccent(pal)
	case btnDanger:
		// The fill is deep enough for white text at 4.5:1, in both
		// themes (council round 1, visual 5).
		bg, fg = pal.dangerFill, ui.Hex("#ffffff")
	}
	switch {
	case b.Pressed():
		bg = bg.Mix(ui.Hex("#000000"), 0.08)
	case b.Hovered() && kind == btnDanger:
		// Lighter would take the white text under 4.5:1.
		bg = bg.Mix(ui.Hex("#000000"), 0.08)
	case b.Hovered() && kind != btnSecondary:
		bg = bg.Mix(ui.Hex("#ffffff"), 0.08)
	case b.Hovered():
		bg = pal.controlHover
	}
	face(b, pal, b.AnimateColor("bg", bg, ui.Animation{Duration: dPress})).TextColor(fg)
	b.Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).SingleLine()
		if keys != "" {
			ui.Text(c, keys).FontSize(fsSmall).TextColor(hintOn(fg, bg)).SingleLine()
		}
	})
	return b.Clicked()
}

// hintOn is a key hint's colour on a button: the label's colour faded
// toward the fill, no further than keeps 4.5:1 on it (council round 1,
// visual 33).
func hintOn(fg, bg ui.Color) ui.Color {
	for a := float32(0.55); a < 1; a += 0.05 {
		if contrast(fg.Alpha(a).Over(bg), bg) >= 4.5 {
			return fg.Alpha(a)
		}
	}
	return fg
}

// borderless returns the theme with kero's look: its accent, and no lines
// around controls, which stand out by their fill: white fields and
// buttons on soft panels.
func borderless(c *ui.Context, pal *palette) *ui.Theme {
	t := *c.Theme()
	t.Accent = pal.accent
	t.Selection = pal.accent.Alpha(0.25)
	t.Border = ui.Transparent
	t.Surface = pal.control
	t.SurfaceHover = pal.controlHover
	t.SurfacePressed = pal.controlHover.Mix(pal.text, 0.06)
	// Tooltips and toasts are raised surfaces, as menus are, rather than
	// the theme turned over (DESIGN.md section 6).
	t.Inverse, t.InverseText = pal.popover, pal.text
	if pal.dark {
		t.Inverse = pal.popover.Mix(pal.text, 0.06)
	}
	t.Warning, t.Success, t.Danger = pal.attention, pal.done, pal.danger
	return &t
}

// toggle is kero's switch: a track that shows on and off alike, the
// accent's when on, with a knob that springs across. It returns the
// switch, for OnChange.
func toggle(c *ui.Context, on *bool, pal *palette) ui.Element {
	sw := ui.SwitchBase(c, on).Size(38, 22).Radius(11).Padding(2).Shrink(0)
	track := pal.text.Alpha(0.14)
	if pal.dark {
		track = ui.RGBA(255, 255, 255, 0.2)
	}
	if *on {
		track = pal.accent
	}
	sw.Background(sw.AnimateColor("track", track, ui.Animation{Duration: dToggle}))
	x := sw.AnimateTo("knob", b2f(*on)*16, ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond, Bounce: 0.15}})
	sw.Children(func() {
		ui.Box(c).Size(18, 18).Radius(9).Background(ui.Hex("#ffffff")).Translate(x, 0).
			Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.22)).Shadow(0, 0, 0, 0.5, ui.RGBA(0, 0, 0, 0.06))
	})
	return sw
}
