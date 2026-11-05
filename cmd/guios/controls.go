package main

import (
	"slices"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// kero's form controls, as macOS 27 draws its own: a pop-up button, its
// choice beside the up and down chevrons in a soft disc, which the whole
// button takes on as the pointer comes, opening its menu over itself with
// the current item, checked, on it; a search menu, the same button with a
// search field atop its menu, for long lists; and a text field, rounded,
// with a faint stroke inside and the accent's glow around it while it has
// the focus.

var (
	updownIcon = glyph("lucide-chevrons-up-down")
	checkIcon  = glyph("lucide-check")
)

const (
	controlH = 24 // the height of macOS's regular controls
	popupH   = 28 // a pop-up button's, its disc 22
	// A menu's padding, its items' height, and where their labels start,
	// past the check.
	menuPad, menuItemH, menuTextX = 5, 24, 22
)

// popupFace draws a pop-up button on trig: its text, and the chevrons in
// a disc, which the button's own tint takes the place of as the pointer
// comes, deepening while its menu is open.
func popupFace(c *ui.Context, trig ui.Element, text string, open bool, pal *palette) {
	trig.Height(popupH).Padding(0, 3, 0, 10).Gap(6).Radius(popupH / 2).AlignItems(ui.Center).Cursor(ui.CursorDefault)
	tint := pal.text.Alpha(0.065)
	if pal.dark {
		tint = ui.RGBA(255, 255, 255, 0.09)
	}
	hover := trig.Hovered() || open
	bg, disc := ui.Transparent, tint
	switch {
	case open || trig.Pressed():
		bg, disc = tint.Mix(pal.text, 0.06), ui.Transparent
	case hover:
		bg, disc = tint, ui.Transparent
	}
	anim := ui.Animation{Duration: dHoverOut}
	trig.Background(trig.AnimateColor("bg", bg, anim))
	trig.Children(func() {
		ui.Text(c, text).FontSize(fsBody).TextColor(pal.text).SingleLine().Ellipsis("…").MinWidth(0).Shrink(1)
		d := ui.Box(c.Key("disc")).Size(22, 22).Radius(11).Center().Shrink(0)
		d.Background(d.AnimateColor("bg", disc, anim)).Children(func() {
			ui.Icon(c, updownIcon).Size(16, 16).TextColor(pal.text.Alpha(0.85))
		})
	})
}

// popup chooses *value among options, labeled by label, as a pop-up
// button no wider than width. It binds *value itself: the choice lands
// in it.
func popup[T comparable](c *ui.Context, key string, value *T, options []T, label func(T) string, width float32, pal *palette) ui.Element {
	sel := ui.SelectBase(c.Key(key), value)
	trig := sel.Trigger.MaxWidth(width)
	open := sel.Open()
	popupFace(c, trig, label(*value), open, pal)
	var spot ui.Element
	maxH := float32(0)
	if open {
		spot, maxH = menuSpot(c, key, trig.Bounds(), max(slices.Index(options, *value), 0), len(options))
	}
	sel.Popup(func(panel ui.Element) {
		menuPanel(panel, pal).AttachTo(spot, ui.AnchorTopLeft, ui.AnchorTopLeft)
		menuWidths[key] = panel.Bounds().W
		over := panel.Hovered()
		ui.Scroll(c.Key("items")).MaxHeight(maxH - 2*menuPad).Children(func() {
			for _, o := range options {
				item := sel.Item(o)
				// As macOS's: the item under the pointer, or the one the
				// arrows moved to; none while the pointer is off the menu,
				// as the current item, highlighted as the menu opens, is
				// then.
				hi := item.Highlighted() && (item.Hovered() || !over && o != *value)
				menuRow(c, item, label(o), o == *value, hi, pal)
			}
		})
	})
	return trig
}

// menuWidths are the pop-up menus' widths as last shown, by key, which
// keep a menu that opens again in the window.
var menuWidths = map[string]float32{}

// menuSpot places the menu of a pop-up button, whose bounds are b, as
// macOS does: over the button, its current item, the at'th of n, on the
// button, its label on the button's; then moved into the window, where a
// menu too tall for it scrolls. It returns an element to attach the menu
// to, at its top left, and the height the menu may take.
//
// The menu attaches to this spot rather than to the button: AttachTo
// moves what would leave the window to the other side of its target
// before Top and Left move it, which threw a menu under the bottom of
// the window above the button, and further up.
func menuSpot(c *ui.Context, key string, b ui.Rect, at, n int) (ui.Element, float32) {
	const edge = 8 // kept from the window's edges
	w, h := c.Size()
	maxH := h - 2*edge
	menuH := min(2*menuPad+float32(n)*menuItemH, maxH)
	y := b.Y - (menuPad + float32(at)*menuItemH + (menuItemH-popupH)/2)
	y = max(edge, min(y, h-edge-menuH))
	x := b.X - (menuPad + menuTextX - 10)
	if mw := max(menuWidths[key], b.W); x+mw > w-edge {
		x = w - edge - mw
	}
	x = max(edge, x)
	var spot ui.Element
	ui.Overlay(c, func() {
		spot = ui.Box(c.Key(key+"-spot")).Absolute().Left(x).Top(y).Size(0, 0).PassThrough()
	})
	return spot, maxH
}

// menuPanel styles a menu: rounded, lifted off the window.
func menuPanel(panel ui.Element, pal *palette) ui.Element {
	return menu(panel.Padding(menuPad).Radius(rControl+2), pal)
}

func menuRow(c *ui.Context, item ui.Element, label string, current, highlighted bool, pal *palette) {
	menuRowWith(c, item, label, current, highlighted, pal, nil)
}

// menuRowWith is menuRow with lead, when set, drawn between the check
// mark and the label: a theme's swatches.
func menuRowWith(c *ui.Context, item ui.Element, label string, current, highlighted bool, pal *palette, lead func()) {
	item.Height(menuItemH).Padding(0, 12, 0, 6).Gap(5).Radius(rRow).AlignItems(ui.Center)
	fg := pal.text
	if highlighted {
		item.Background(pal.accentFill)
		fg = pal.accentInk
	}
	item.TextColor(fg).Children(func() {
		mark := ui.Icon(c, checkIcon).Size(12, 12).Shrink(0)
		if !current {
			mark.Opacity(0)
		}
		if lead != nil {
			lead()
		}
		ui.Text(c, label).FontSize(fsBody).SingleLine().Ellipsis("…").MinWidth(0)
	})
}

// searchMenu is a search menu's state: open, what is typed, and the item
// the arrows or the pointer are on.
type searchMenu struct {
	open    bool
	query   string
	hi      int
	hovered string // the item the pointer came to last
	lit     string // the item highlighted as the menu was last built
	reveal  bool   // the highlighted item scrolls into view, once
	// label names an option for people where its value is an id, and
	// lead draws beside it in the list; both optional, set by the caller.
	label func(string) string
	lead  func(c *ui.Context, option string)
	at    ui.Rect // the button's bounds as the menu opened
}

// searchPopup chooses value among options as a pop-up button, whose menu
// opens below it with a search field atop, for lists too long to scroll
// through: typing filters them, the arrows move, Return chooses and
// Escape closes. With custom set, a query no option is offers itself, as
// custom labels it: "Use “%s”". choose gets the choice.
func (a *App) searchPopup(c *ui.Context, key, value string, options []string, width float32, custom func(string) string, pal *palette, choose func(string)) {
	if a.ss.menus == nil {
		a.ss.menus = map[string]*searchMenu{}
	}
	m := a.ss.menus[key]
	if m == nil {
		m = &searchMenu{}
		a.ss.menus[key] = m
	}
	trig := ui.ButtonBase(c.Key(key)).MaxWidth(width).Label(value)
	if trig.Clicked() {
		m.open = !m.open
		m.query, m.hovered, m.reveal = "", "", true
		m.hi = max(slices.Index(options, value), 0)
		m.at = trig.Bounds()
	}
	// The window resizing closes the menu, which would follow the button
	// off where it opened.
	if b := trig.Bounds(); m.open && (abs(b.X-m.at.X) > 0.5 || abs(b.Y-m.at.Y) > 0.5) {
		m.open = false
	}
	if m.open {
		// As a menu of macOS's, the menu holds the window while it is
		// open: a backdrop under it takes the wheel, so the page does not
		// scroll, and a press, which closes the menu and goes no further.
		ui.Overlay(c, func() {
			back := ui.ButtonBase(c.Key(key + "-back")).Absolute().Left(0).Top(0).Right(0).Bottom(0).Label(locales.CloseTheMenu())
			if back.Clicked() {
				m.open = false
			}
		})
	}
	face := value
	if m.label != nil {
		face = m.label(value)
	}
	popupFace(c, trig, face, m.open, pal)
	// The list takes what the window has below the button, or above it,
	// where the menu goes when there is more room there, less the search
	// field and the menu's padding.
	const edge, gap, inset = 8, 4, 3 // the window's edge kept; the gap to the button; the field's inset past menuPad
	b := trig.Bounds()
	_, h := c.Size()
	room := max(h-(b.Y+b.H)-gap-edge, b.Y-gap-edge)
	listH := min(300, room-(2*menuPad+2*inset+controlH+2+menuPad))
	ui.PopoverBase(c.Key(key+"-menu"), trig, &m.open, func(panel ui.Element) {
		menuPanel(panel, pal).Margin(gap, 0, 0, 0).Width(max(width, 260))
		q := strings.ToLower(strings.TrimSpace(m.query))
		var shown []string
		exact := false
		for _, o := range options {
			if q == "" || strings.Contains(strings.ToLower(o), q) || m.label != nil && strings.Contains(strings.ToLower(m.label(o)), q) {
				shown = append(shown, o)
			}
			exact = exact || strings.ToLower(o) == q
		}
		labels := slices.Clone(shown)
		if m.label != nil {
			for i, o := range shown {
				labels[i] = m.label(o)
			}
		}
		if custom != nil && q != "" && !exact {
			shown = append(shown, strings.TrimSpace(m.query))
			labels = append(labels, custom(strings.TrimSpace(m.query)))
		}
		m.hi = min(max(m.hi, 0), max(len(shown)-1, 0))
		if lit := shownAt(shown, m.hi); lit != m.lit {
			// What shows the highlighted item, built before the menu, builds
			// again with it.
			m.lit = lit
			c.After(0)
		}
		pick := func(i int) {
			if i >= 0 && i < len(shown) {
				m.open = false
				if shown[i] != value {
					choose(shown[i])
				}
			}
		}
		var in ui.Element
		// Inset past the items, so that its focus glow keeps clear of
		// the menu's edge.
		frame := ui.Row(c.Key("search")).Margin(inset, inset, menuPad+inset, inset).Cursor(ui.CursorText)
		frame.Children(func() {
			ui.Icon(c, searchIcon).Size(12, 12).TextColor(pal.textFaint).Shrink(0)
			in = ui.TextInputBase(c.Key("q"), &m.query).AutoFocus().Grow(1).MinWidth(0).AlignSelf(ui.Stretch).FontSize(fsBody).
				Placeholder(locales.CommonSearch()).Label(locales.CommonSearch())
		})
		field(c, frame, in.Focused(), pal)
		in.OnChange(func() { m.hi, m.reveal = 0, true })
		in.OnShortcut(0, ui.KeyDown, func() { m.hi, m.reveal = min(m.hi+1, len(shown)-1), true })
		in.OnShortcut(0, ui.KeyUp, func() { m.hi, m.reveal = max(m.hi-1, 0), true })
		in.OnSubmit(func() { pick(m.hi) })
		if len(shown) == 0 {
			ui.Text(c, locales.CommonNoResults()).FontSize(fsBody).TextColor(pal.textFaint).Padding(6, 12)
			return
		}
		reveal := m.reveal
		m.reveal = false
		// The pointer coming to an item moves the highlight once the items
		// are built, and the menu builds again: moved as a row learns it,
		// the rows built before it would keep the old one lit too.
		moved := -1
		ui.Scroll(c.Key("items")).MaxHeight(max(listH, menuItemH)).Children(func() {
			for i, o := range shown {
				row := ui.Row(c.Key(o)).Label(labels[i])
				if row.Hovered() && m.hovered != o {
					moved = i
				}
				if row.Clicked() {
					pick(i)
				}
				var lead func()
				if m.lead != nil {
					lead = func() { m.lead(c, o) }
				}
				menuRowWith(c, row, labels[i], o == value, i == m.hi, pal, lead)
				if reveal && i == m.hi {
					row.ScrollIntoView()
				}
			}
		})
		if moved >= 0 {
			m.hovered, m.hi = shown[moved], moved
			c.After(0)
		}
	})
}

func shownAt(shown []string, i int) string {
	if i < 0 || i >= len(shown) {
		return ""
	}
	return shown[i]
}

// field styles a text field's frame, around its input.
func field(c *ui.Context, frame ui.Element, focused bool, pal *palette) ui.Element {
	frame.Height(controlH+2).Padding(0, 8).Gap(6).Radius(rControl).AlignItems(ui.Center)
	return fieldFace(frame, focused, pal)
}

// fieldFace is a field's look on a frame of any height: the task box of
// the New agent sheet grows with its text.
func fieldFace(frame ui.Element, focused bool, pal *palette) ui.Element {
	stroke := ui.RGBA(0, 0, 0, 0.13)
	bg := ui.Hex("#ffffff")
	if pal.dark {
		stroke, bg = ui.RGBA(255, 255, 255, 0.13), ui.RGBA(255, 255, 255, 0.06)
	}
	frame.Background(bg).InsetShadow(0, 0, 0, 1, stroke)
	glow := frame.Animate("glow", b2f(focused), dFade)
	if glow > 0 {
		frame.Shadow(0, 0, 0, 3.5*glow, pal.accent.Alpha(0.32*glow)).InsetShadow(0, 0, 0, 1, pal.accent.Alpha(0.75*glow))
	}
	return frame
}

// textField edits *value in a field, returning its input, for
// Submitted, OnSubmit and Focused.
func textField(c *ui.Context, key string, value *string, placeholder string, width float32, pal *palette) ui.Element {
	var in ui.Element
	frame := ui.Row(c.Key(key)).Cursor(ui.CursorText)
	if width > 0 {
		frame.Width(width)
	} else {
		frame.Grow(1)
	}
	frame.Children(func() {
		// The input fills the field, its padding its own, so that a click
		// anywhere in the field lands in it.
		in = ui.TextInputBase(c.Key("in"), value).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).Padding(0, 8).FontSize(fsBody).Placeholder(placeholder).Label(placeholder)
	})
	field(c, frame, in.Focused(), pal).Padding(0)
	return in
}

// slider sets *value between lo and hi, as macOS's slider: a thin track,
// the accent's up to a white knob, which the pointer and the arrows move.
// Held, the knob grows into a lens of glass that the track shows through,
// as AppKit's does, and as MyGo's own slider draws it. The knob's half
// width pads the track, so that the value spans the knob's travel.
func slider(c *ui.Context, key string, value *float64, lo, hi float64, width float32, pal *palette) ui.Element {
	const knobW, knobH, trackH = 20, 16, 4
	s := ui.SliderBase(c.Key(key), value, lo, hi).Width(width).Height(controlH).Padding(0, knobW/2).FocusRing(false)
	track := pal.text.Alpha(0.14)
	if pal.dark {
		track = ui.RGBA(255, 255, 255, 0.18)
	}
	f := float32(0)
	if hi > lo {
		f = float32((*value - lo) / (hi - lo))
	}
	f = min(max(f, 0), 1)
	held := s.Animate("held", b2f(s.Pressed()), 150*time.Millisecond)
	accent, dark, focus := pal.accent, pal.dark, s.FocusVisible()
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		x0, x1 := r.X+knobW/2, r.X+r.W-knobW/2
		cy := r.Y + r.H/2
		x := x0 + (x1-x0)*f
		g := 1 + 0.35*held
		knob := ui.Rect{X: x - knobW*g/2, Y: cy - knobH*g/2, W: knobW * g, H: knobH * g}
		rad := knob.H / 2
		if held > 0 {
			// The lens lightens what is behind it, under the track.
			p.Fill(knob, ui.RGBA(255, 255, 255, 0.1*held), rad)
		}
		p.Fill(ui.Rect{X: x0 - trackH/2, Y: cy - trackH/2, W: x1 - x0 + trackH, H: trackH}, track, trackH/2)
		p.Fill(ui.Rect{X: x0 - trackH/2, Y: cy - trackH/2, W: x - x0 + trackH, H: trackH}, accent, trackH/2)
		face, rim, drop := ui.Hex("#ffffff"), ui.RGBA(0, 0, 0, 0.22), ui.RGBA(0, 0, 0, 0.14)
		if dark {
			face, rim, drop = ui.Hex("#e0e1e1"), ui.RGBA(0, 0, 0, 0.7), ui.RGBA(0, 0, 0, 0.3)
		}
		if held > 0 {
			p.Shadow(knob, rad, 0, 8, 14, -3, drop.Alpha(held))
		}
		if held < 1 {
			// A tight shadow edges the knob, a soft one lifts it.
			p.Shadow(knob, rad, 0, 0.5, 1, 0, ui.RGBA(0, 0, 0, 0.08*(1-held)))
			p.Shadow(knob, rad, 0, 1.5, 7, 0, ui.RGBA(0, 0, 0, 0.1*(1-held)))
			p.Fill(knob, face.Alpha(1-held), rad)
		}
		if held > 0 {
			// The lens's rim, lit on the inside.
			p.Stroke(knob, rim.Alpha(held), rad, 1)
			p.Stroke(ui.Rect{X: knob.X + 1, Y: knob.Y + 1, W: knob.W - 2, H: knob.H - 2}, ui.RGBA(255, 255, 255, 0.25*held), rad-1, 1)
		}
		if focus {
			p.FocusRing(knob, [4]float32{rad, rad, rad, rad})
		}
	})
	return s
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
