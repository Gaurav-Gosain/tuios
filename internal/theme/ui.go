package theme

import (
	"image/color"
	"sync"

	"charm.land/tone"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	tint "github.com/lrstanley/bubbletint/v2"
)

// The contrast math lives in the overlay package, which owns the palette these
// ratios are measured on and is meant to stand on its own. These are the names
// the rest of tuios already calls it by.
const (
	// ContrastFloor is the ratio a chrome label has to clear against the
	// ground it is drawn on.
	ContrastFloor = overlay.ContrastFloor
	// MarkFloor is the ratio a non-text mark has to clear: a cap, a glyph, a
	// cursor block.
	MarkFloor = overlay.MarkFloor
)

// ContrastRatio returns the WCAG 2.1 contrast ratio between two colours.
func ContrastRatio(a, b color.Color) float64 { return overlay.ContrastRatio(a, b) }

// Readable returns c lifted toward the ground's text end until it clears
// ContrastFloor against bg, and c untouched when it already does.
func Readable(c, bg color.Color) color.Color { return overlay.Readable(c, bg) }

// ReadableAt is Readable against a chosen floor.
func ReadableAt(c, bg color.Color, floor float64) color.Color {
	return overlay.ReadableAt(c, bg, floor)
}

var uiCanvas = tone.Pepper

var railRuleMemo struct {
	sync.Mutex
	bg   [4][4]uint32
	ink  [4]color.Color
	next int
}

// RailRuleOn is the structure ink for a rule drawn on a ground its caller
// paints itself, like the collapsed strip's bands.
func RailRuleOn(bg color.Color) color.Color {
	if c := chromeFurniture(RoleRule); c != nil {
		return c
	}
	r, g, b, a := bg.RGBA()
	key := [4]uint32{r, g, b, a}

	railRuleMemo.Lock()
	defer railRuleMemo.Unlock()
	for i, held := range railRuleMemo.ink {
		if held != nil && railRuleMemo.bg[i] == key {
			return held
		}
	}
	ink := overlay.Structure(bg)
	railRuleMemo.bg[railRuleMemo.next] = key
	railRuleMemo.ink[railRuleMemo.next] = ink
	railRuleMemo.next = (railRuleMemo.next + 1) % len(railRuleMemo.ink)
	return ink
}

// ContrastText picks a foreground that reads on the given (usually saturated)
// background: near-white on a dark or mid accent, near-black on a light one.
func ContrastText(bg color.Color) color.Color { return overlay.ContrastText(bg) }

var chromeRamp = struct {
	canvas, panel, card float64
	fg, fgDim, fgMute   float64
}{
	canvas: overlay.ContrastRatio(tone.Char, tone.Pepper),
	panel:  overlay.ContrastRatio(tone.Char, tone.BBQ),
	card:   overlay.ContrastRatio(tone.Iron, tone.Char),
	fg:     overlay.ContrastRatio(tone.Butter, tone.Char),
	fgDim:  overlay.ContrastRatio(tone.Smoke, tone.Char),
	fgMute: overlay.ContrastRatio(tone.Squid, tone.Char),
}

// UI returns the active chrome palette. Neutrals and semantic status colors come
// from the charmtone palette so overlays read consistently regardless of the
// terminal theme; the accent follows the active terminal theme when one is
// enabled, falling back to charmtone Charple.
//
// Chrome is intentionally kept on a constant neutral ramp (like a real window
// manager keeps its chrome constant) so overlays stay legible over any terminal
// content, while a themed session still tints its tabs, selection and badges.
// On a light theme the ramp is built from the theme's background instead, so
// a dialog is a light panel on a light screen (see lightDialogChrome).
// A theme that names a chrome Surface moves the ramp, and the ink tiers move
// with it: every ink was chosen against the ground it is written on, so
// opening the ground up without re-deriving the inks would trade off-accent
// chrome for unreadable chrome. See Chrome.
//
// The palette is built for the colour depth the terminal has (see Depth): the
// designed ramp in truecolor, a hand-set grey ramp at 256 colours, and the
// terminal's own colours, slots and attributes at 16. Every derived token and
// every contrast check goes through overlay.Derive.
//
// It is memoised on what it is built from, because the derivation measures
// every ink on every ground it is promised on and render code asks for the
// palette many times a frame.
func UI() overlay.Palette {
	key := currentUIKey()
	uiMemo.Lock()
	defer uiMemo.Unlock()
	if uiMemo.valid && uiMemo.key == key {
		return uiMemo.pal
	}
	p := buildUI(key.depth)
	uiMemo.key, uiMemo.valid, uiMemo.pal = key, true, p
	return p
}

type uiKey struct {
	depth   overlay.Depth
	tint    *tint.Tint
	colours [6]*tint.Color
	chrome  *Chrome
	host    *hostGround
	framed  bool
}

func currentUIKey() uiKey {
	k := uiKey{depth: Depth(), framed: overlay.FramePanels()}
	if t := Current(); t != nil {
		k.tint = t
		k.colours = [6]*tint.Color{t.BrightBlue, t.BrightCyan, t.BrightRed, t.BrightGreen, t.Yellow, t.Bg}
		k.chrome = CurrentChrome()
	} else {
		k.host = hostInForce()
	}
	return k
}

var uiMemo struct {
	sync.Mutex
	key   uiKey
	valid bool
	pal   overlay.Palette
}

// PaneUI is UI for an overlay drawn like a pane window: filled with ground,
// the colour an empty pane cell shows, rather than with the chrome's own
// surface. The surface is ground, and every step a cursor row, a hovered row,
// a card or a status tint takes, and every ink, is derived from it, so a
// selected row is a step of the pane's colour and not of the chrome's. At 16
// colours, where every ground is the terminal's own, and for a nil ground, it
// is UI.
//
// The palette is kept per ground beside UI's, not in place of it: the clients
// of one process can each have their own pane ground, and UI is what every
// other caller, the rail and the dock among them, is drawn with.
func PaneUI(ground color.Color) overlay.Palette {
	if ground == nil || Depth() == overlay.Depth16 {
		return UI()
	}
	r, g, b, a := ground.RGBA()
	key := paneUIKey{ui: currentUIKey(), ground: [4]uint32{r, g, b, a}}
	paneUIMemo.Lock()
	defer paneUIMemo.Unlock()
	if p, ok := paneUIMemo.m[key]; ok {
		return p
	}
	p := buildUIOn(key.ui.depth, Current(), CurrentChrome(), ground)
	if paneUIMemo.m == nil || len(paneUIMemo.m) >= paneUIMemoMax {
		paneUIMemo.m = make(map[paneUIKey]overlay.Palette, paneUIMemoMax)
	}
	paneUIMemo.m[key] = p
	return p
}

type paneUIKey struct {
	ui     uiKey
	ground [4]uint32
}

var paneUIMemo struct {
	sync.Mutex
	m map[paneUIKey]overlay.Palette
}

const paneUIMemoMax = 16

func buildUI(d overlay.Depth) overlay.Palette {
	return buildUIOn(d, Current(), CurrentChrome(), nil)
}

func buildUIWith(d overlay.Depth, cur *tint.Tint, ch *Chrome) overlay.Palette {
	return buildUIOn(d, cur, ch, nil)
}

func buildUIOn(d overlay.Depth, cur *tint.Tint, ch *Chrome, pane color.Color) overlay.Palette {
	p := overlay.Palette{
		Canvas:   uiCanvas,
		Panel:    tone.BBQ,
		Surface:  tone.Char,
		RowSel:   tone.BBQ,
		Card:     tone.Iron,
		Selected: tone.Charple,

		Fg:     tone.Butter,
		FgDim:  tone.Smoke,
		FgMute: tone.Squid,

		Accent:       tone.Charple,
		AccentBright: tone.Hazy,
		PillFg:       tone.Pepper,

		Warn:    tone.Cherry,
		Success: tone.Julep,
		Info:    tone.Malibu,
		Warning: tone.Tang,
	}
	customRamp := false

	if t := cur; t != nil {
		p.Accent = t.BrightBlue
		p.AccentBright = t.BrightCyan
		p.Selected = t.BrightBlue
		p.Warn = t.BrightRed
		p.Success = t.BrightGreen
		p.Info = t.BrightBlue
		p.Warning = t.Yellow

		if c := ch; c != nil {
			if c.Accent != nil {
				p.Accent, p.Selected, p.Info = c.Accent, c.Accent, c.Accent
			}
			if c.AccentBright != nil {
				p.AccentBright = c.AccentBright
			}
			if c.Success != nil {
				p.Success = c.Success
			}
			if c.Warning != nil {
				p.Warning = c.Warning
			}
			if c.Error != nil {
				p.Warn = c.Error
			}
			if c.Info != nil {
				p.Info = c.Info
			}
			if c.Canvas != nil {
				p.Canvas, customRamp = c.Canvas, true
			}
			if c.Panel != nil {
				p.Panel, p.RowSel, customRamp = c.Panel, c.Panel, true
			}
			if c.Surface != nil {
				p.Surface, customRamp = c.Surface, true
				p.Fg, p.FgDim, p.FgMute = c.fg, c.fgDim, c.fgMute
			}
			if c.Card != nil {
				p.Card, customRamp = c.Card, true
			}
			p.Attention, p.Busy = c.Furniture(RoleAttention), c.Furniture(RoleBusy)
		}
	}

	light := false
	var ground color.Color
	groundLight := false
	if t := cur; t != nil {
		ground, groundLight = AsColor(t.Bg), GroundIsLight(t.Bg)
	} else if h := hostInForce(); h != nil {
		ground, groundLight = h.bg, h.light
	}
	if pane != nil && d != overlay.Depth16 {
		c := paneChrome(pane)
		light, customRamp = GroundIsLight(pane), true
		p.Canvas, p.Panel, p.RowSel, p.Surface, p.Card = c.Canvas, c.Panel, c.Panel, c.Surface, c.Card
		p.Fg, p.FgDim, p.FgMute = c.fg, c.fgDim, c.fgMute
	} else if ground != nil && !customRamp && d != overlay.Depth16 {
		var c Chrome
		if groundLight {
			light = true
			c = lightDialogChrome(ground)
		} else if cur == nil {
			customRamp = true
			c = hostDarkChrome(ground)
		}
		if light || customRamp {
			p.Canvas, p.Panel, p.RowSel, p.Surface, p.Card = c.Canvas, c.Panel, c.Panel, c.Surface, c.Card
			p.Fg, p.FgDim, p.FgMute = c.fg, c.fgDim, c.fgMute
		}
	}

	switch d {
	case overlay.Depth16:
		p.Accent, p.Selected, p.Info = overlay.Slot(12), overlay.Slot(12), overlay.Slot(12)
		p.AccentBright = overlay.Slot(14)
		p.Warn, p.Success, p.Warning = overlay.Slot(9), overlay.Slot(10), overlay.Slot(3)
		p.Fg, p.FgDim, p.FgMute = overlay.NoColor, overlay.NoColor, overlay.Slot(8)
		p.Attention, p.Busy = nil, nil
	case overlay.Depth256:
		if !customRamp && !light {
			p.Canvas, p.Panel, p.Surface = overlay.Grey(2), overlay.Grey(3), overlay.Grey(5)
			p.RowSel, p.RowSelQuiet, p.Hover, p.Card = overlay.Grey(3), overlay.Grey(4), overlay.Grey(6), overlay.Grey(7)
			p.Fg, p.FgDim, p.FgMute = overlay.Grey(23), overlay.Grey(18), overlay.Grey(13)
		}
		p.Accent, p.AccentBright, p.Selected = overlay.To256(p.Accent), overlay.To256(p.AccentBright), overlay.To256(p.Selected)
		p.Warn, p.Success = overlay.To256(p.Warn), overlay.To256(p.Success)
		p.Info, p.Warning = overlay.To256(p.Info), overlay.To256(p.Warning)
		if p.Attention != nil {
			p.Attention = overlay.To256(p.Attention)
		}
		if p.Busy != nil {
			p.Busy = overlay.To256(p.Busy)
		}
	}
	if light {
		liftOnLight(&p, d)
	}

	return overlay.Derive(p)
}

const lightDialogStep = 1.2

func lightDialogChrome(ground color.Color) Chrome {
	c := Chrome{Canvas: ground, Surface: overlay.Darker(ground, lightDialogStep)}
	c.deriveRamp()
	return c
}

func paneChrome(ground color.Color) Chrome {
	c := Chrome{
		Canvas:  ground,
		Surface: ground,
		Panel:   stepOff(ground, chromeRamp.panel, overlay.Darker, overlay.Lighter),
		Card:    stepOff(ground, chromeRamp.card, overlay.Lighter, overlay.Darker),
	}
	c.deriveRamp()
	return c
}

func stepOff(ground color.Color, ratio float64, step, other func(color.Color, float64) color.Color) color.Color {
	if c := step(ground, ratio); overlay.ContrastRatio(c, ground) >= 1+(ratio-1)/2 {
		return c
	}
	return other(ground, ratio)
}

func liftOnLight(p *overlay.Palette, d overlay.Depth) {
	surface := []color.Color{overlay.Shown(p.Surface)}
	rows := []color.Color{overlay.Shown(p.Surface), overlay.Shown(p.Panel)}
	if d == overlay.Depth256 {
		rows[1] = overlay.Apart256(p.Panel, p.Surface)
	}
	inks := []struct {
		ink     *color.Color
		grounds []color.Color
		floor   float64
	}{
		{&p.AccentBright, surface, ContrastFloor},
		{&p.Accent, rows, ContrastFloor}, {&p.Selected, rows, ContrastFloor},
		{&p.Info, rows, MarkFloor}, {&p.Warn, rows, MarkFloor},
		{&p.Success, rows, MarkFloor}, {&p.Warning, rows, MarkFloor},
	}
	for _, it := range inks {
		if d == overlay.Depth256 {
			if q := overlay.ReadableEntry256(*it.ink, it.grounds, it.floor); q != nil {
				*it.ink = q
			}
			continue
		}
		reads := func(c color.Color) bool {
			for _, g := range it.grounds {
				if overlay.ContrastRatio(c, g) < it.floor {
					return false
				}
			}
			return true
		}
		c := *it.ink
		for step := 1.0; step < 21 && !reads(c); step *= 1.05 {
			c = overlay.Darker(*it.ink, step)
		}
		*it.ink = c
	}
	if d == overlay.Depth256 {
		p.Warning = overlay.WarningApart256(p.Warning, p.Warn, rows, MarkFloor)
	}
}

// GroundUI is UI for chrome written straight on the terminal's own
// background rather than on a dialog's surface: the rail and the dock.
//
// UI's neutrals and inks are a dark ramp, which is right for the dialogs that
// paint their own ground and right beside a dark theme. The rail and the dock
// paint nothing, so their labels land on the theme's background, and on a
// light theme the ramp's near-white inks were written on near-white: the
// rail's session names, the dock's notice and the dock's buttons could not be
// read. On a light ground the ramp is rebuilt from that ground, with the
// ground as its canvas and a band one step darker as its surface, and the ink
// tiers are measured on it the way a theme's own chrome surface is. On a dark
// ground this is UI unchanged, and so it is at 16 colours, where the ground is
// the terminal's own whatever it is.
func GroundUI() overlay.Palette {
	ground := RailGround()
	return GroundUIOn(ground, GroundIsLight(ground))
}

// GroundUIOn is GroundUI for a ground the caller knows: the host terminal's
// own background, which only a client can ask for and which differs between
// the clients of one process. light is the caller's verdict on the ground,
// so a client that holds its verdict with hysteresis keeps it here too.
func GroundUIOn(ground color.Color, light bool) overlay.Palette {
	p := UI()
	if p.Depth == overlay.Depth16 || !light || ground == nil {
		return p
	}
	if c := CurrentChrome(); c != nil && c.Surface != nil {
		return p
	}
	key := currentUIKey()
	c := groundChrome(ground, p.Depth)
	if c.derived && c.derivedFor == key {
		return c.pal
	}
	p.Canvas, p.Panel, p.RowSel, p.Surface, p.Card = c.Canvas, c.Panel, c.Panel, c.Surface, c.Card
	p.Fg, p.FgDim, p.FgMute = c.fg, c.fgDim, c.fgMute
	p.RowSelQuiet, p.Hover = nil, nil
	p = overlay.Derive(p)
	groundChromeMemo.Lock()
	groundChromeMemo.c.pal, groundChromeMemo.c.derived, groundChromeMemo.c.derivedFor = p, true, key
	groundChromeMemo.Unlock()
	return p
}

// GroundIsLight reports whether dark ink reads better on ground than light.
func GroundIsLight(ground color.Color) bool {
	return overlay.ContrastRatio(ground, color.Black) > overlay.ContrastRatio(ground, color.White)
}

var groundChromeMemo struct {
	sync.Mutex
	key   [4]uint32
	depth overlay.Depth
	valid bool
	c     groundRamp
}

type groundRamp struct {
	Chrome
	pal        overlay.Palette
	derived    bool
	derivedFor uiKey
}

func groundChrome(ground color.Color, d overlay.Depth) groundRamp {
	r, g, b, a := ground.RGBA()
	key := [4]uint32{r, g, b, a}
	groundChromeMemo.Lock()
	defer groundChromeMemo.Unlock()
	if groundChromeMemo.valid && groundChromeMemo.key == key && groundChromeMemo.depth == d {
		return groundChromeMemo.c
	}
	surface := overlay.Darker(ground, chromeRamp.canvas)
	c := Chrome{
		Canvas:  ground,
		Surface: surface,
		Panel:   overlay.Darker(surface, chromeRamp.panel),
	}
	c.deriveRamp()
	groundChromeMemo.key, groundChromeMemo.depth, groundChromeMemo.valid = key, d, true
	groundChromeMemo.c = groundRamp{Chrome: c}
	return groundChromeMemo.c
}
