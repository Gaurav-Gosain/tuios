// Package theme provides color themes and styling for the TUIOS terminal.
package theme

import (
	"image/color"
	"log"
	"math"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
	tint "github.com/lrstanley/bubbletint/v2"
)

var enabled bool

var (
	borderMu        sync.RWMutex
	borderOverrides [borderKinds]ColorSpec
)

type borderKind int

const (
	borderUnfocused borderKind = iota
	borderFocused
	borderFocusedWindow
	borderFocusedTerminal
	borderKinds
)

// BorderOverrides are the border colours a config sets, each in any form
// ParseColor reads. Empty leaves that border to the theme.
type BorderOverrides struct {
	// Focused is the focused border in both modes.
	Focused string
	// FocusedWindow and FocusedTerminal are the focused border in one mode.
	// Each wins over Focused in its mode.
	FocusedWindow, FocusedTerminal string
	Unfocused                      string
}

// SetBorderOverrides sets the border colours from config values. An empty or
// unparseable value clears that override and restores the theme colour; the
// config validator has said why.
func SetBorderOverrides(o BorderOverrides) {
	borderMu.Lock()
	defer borderMu.Unlock()
	borderOverrides = [borderKinds]ColorSpec{
		borderUnfocused:       SpecOf(o.Unfocused),
		borderFocused:         SpecOf(o.Focused),
		borderFocusedWindow:   SpecOf(o.FocusedWindow),
		borderFocusedTerminal: SpecOf(o.FocusedTerminal),
	}
}

func borderOverride(kind borderKind) color.Color {
	borderMu.RLock()
	c := borderOverrides[kind]
	borderMu.RUnlock()
	return c.Resolve()
}

func focusedOverride(mode borderKind) color.Color {
	if c := borderOverride(mode); c != nil {
		return c
	}
	return borderOverride(borderFocused)
}

// Initialize makes the named theme the active one.
//
// "none" disables theming, so the terminal's own colours are used. An empty
// name follows the applied Omarchy theme on a machine with Omarchy, and
// disables theming elsewhere. "omarchy" follows Omarchy too, and returns
// ErrOmarchyNotFound, with theming disabled, on a machine without it.
func Initialize(themeName string) error {
	wasEnabled, was := enabled, activeTint()
	defer func() {
		if enabled != wasEnabled || activeTint() != was {
			generation.Add(1)
		}
	}()
	matchHost.Store(themeName == NoneID)
	switch themeName {
	case NoneID:
		enabled = false
		return nil
	case "", OmarchyID:
		err := initOmarchy()
		enabled = err == nil
		if themeName == "" {
			return nil
		}
		return err
	}

	enabled = true

	EnsureRegistry()

	if ok := tint.SetTintID(themeName); !ok {
		log.Printf("Warning: theme %q not found; using default theme colors", themeName)
	}

	return nil
}

func activeTint() *tint.Tint {
	if !enabled {
		return nil
	}
	return tint.Current()
}

// IsEnabled returns true if theming is enabled
func IsEnabled() bool {
	return enabled
}

// Current returns the currently active theme.
// Returns nil if theming is disabled.
func Current() *tint.Tint {
	if !enabled {
		return nil
	}
	return tint.Current()
}

// GetANSIPalette returns the 16 ANSI colors (0-15) from the current theme.
//
// With no theme the sixteen are the user's terminal's, and tuios does not know
// what they are. It returns the indices themselves rather than a guess: painted
// with one of these, a swatch leaves as SGR 31 or 91 and the host fills it in
// from the user's own palette, so the row really is the user's sixteen. The
// xterm defaults here would make the picker show colours nobody had chosen.
//
// A caller that needs channel values gets whatever RGBA the index resolves to
// in this process, which is the xterm default. That is a guess, and only the
// terminal can settle it.
func GetANSIPalette() [16]color.Color {
	t := Current()
	if t == nil {
		var pal [16]color.Color
		for i := range pal {
			// #nosec G115 - i is a loop index over [0, 16)
			pal[i] = ansi.BasicColor(uint8(i))
		}
		return pal
	}
	var pal [16]color.Color
	for i, c := range ANSIOrder(t) {
		pal[i] = AsColor(c)
	}
	return pal
}

// EmulatorColors is the theme as a pane's emulator is given it, at the depth
// the frame is drawn at. ok is false when no theme is in force, and at 16
// colours, where the sixteen are the terminal's own slots and fg and bg its
// default colours, as slotColor has them.
//
// At 256 colours each colour is already the palette entry the chrome would
// pick (overlay.To256). Left in truecolor, a cell reached the frame writer as
// RGB and was stepped down by its cube-first rule: nord's #2e3440 ground
// became the teal 23 and Latte's red the grey 241. Converted here, it is done
// once per theme or depth change and never per cell.
//
// The colours a pane is told in answer to OSC 4, 10 and 11 are not these: a
// program asking for the palette gets the theme's own values.
func EmulatorColors() (fg, bg, cur color.Color, pal [16]color.Color, ok bool) {
	if !IsEnabled() {
		return nil, nil, nil, pal, false
	}
	d := Depth()
	if d == overlay.Depth16 {
		return nil, nil, nil, pal, false
	}
	fg, bg, cur, pal = TerminalFg(), TerminalBg(), TerminalCursor(), GetANSIPalette()
	if d == overlay.Depth256 {
		fg, bg, cur = to256(fg), to256(bg), to256(cur)
		for i := range pal {
			pal[i] = to256(pal[i])
		}
	}
	return fg, bg, cur, pal, true
}

func to256(c color.Color) color.Color {
	if c == nil {
		return nil
	}
	return overlay.To256(c)
}

// ANSIOrder returns a theme's sixteen ANSI colours in index order, 0 to 15:
// the eight normal colours then the eight bright ones.
//
// A colour the theme never set comes back nil. GetANSIPalette cannot say that,
// because a nil *tint.Color put in a color.Color is not a nil interface and
// panics the moment anything reads its channels. A caller that has to tell
// "unset" from "black" asks here.
//
// It is also the one place the order is written down. bubbletint calls index 5
// Purple and xterm calls it magenta, so a second hand-written list is how a
// palette ends up one slot out.
func ANSIOrder(t *tint.Tint) [16]*tint.Color {
	if t == nil {
		return [16]*tint.Color{}
	}
	return [16]*tint.Color{
		t.Black,
		t.Red,
		t.Green,
		t.Yellow,
		t.Blue,
		t.Purple,
		t.Cyan,
		t.White,
		t.BrightBlack,
		t.BrightRed,
		t.BrightGreen,
		t.BrightYellow,
		t.BrightBlue,
		t.BrightPurple,
		t.BrightCyan,
		t.BrightWhite,
	}
}

// TerminalFg returns the foreground color for terminal text.
func TerminalFg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#e5e5e5")
	}
	return AsColor(t.Fg)
}

// TerminalBg returns the background color for terminal emulator.
func TerminalBg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return AsColor(t.Bg)
}

// TerminalCursor returns the color for the terminal cursor. It is nil when
// the theme names no cursor colour, which some built-in themes do not.
func TerminalCursor() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00")
	}
	return AsColor(t.Cursor)
}

// AsColor returns c as a color.Color, and a nil interface when c is nil.
//
// A tint leaves a colour it does not name as a nil *tint.Color. Put into a
// color.Color as it is, that nil pointer makes an interface that is not nil,
// so a caller's nil check passes and the RGBA call on it panics. An OSC 12
// query in a pane under a theme with no cursor colour did exactly that.
func AsColor(c *tint.Color) color.Color {
	if c == nil {
		return nil
	}
	return c
}

func borderInk(c color.Color) color.Color {
	return ReadableAt(c, TerminalBg(), MarkFloor)
}

// BorderUnfocused returns the color for unfocused window borders.
func BorderUnfocused() color.Color { return BorderUnfocusedOn(nil) }

// BorderUnfocusedOn is BorderUnfocused for a client that knows its host
// terminal's background. With no theme the default is a fixed pale colour
// picked for a dark terminal, which all but vanishes on a light one, so it is
// measured against the host's ground the way a theme's border is measured
// against the theme's. A nil ground keeps the fixed colour.
func BorderUnfocusedOn(hostBg color.Color) color.Color {
	return bordersOn(hostBg).unfocused
}

// BorderFocusedWindow returns the color for focused window borders in window management mode.
func BorderFocusedWindow() color.Color { return BorderFocusedWindowOn(nil) }

// BorderFocusedWindowOn is BorderFocusedWindow on a known host background.
// See BorderUnfocusedOn.
func BorderFocusedWindowOn(hostBg color.Color) color.Color {
	return bordersOn(hostBg).window
}

// BorderFocusedTerminal returns the color for focused window borders in terminal mode.
func BorderFocusedTerminal() color.Color { return BorderFocusedTerminalOn(nil) }

// BorderFocusedTerminalOn is BorderFocusedTerminal on a known host
// background. See BorderUnfocusedOn.
func BorderFocusedTerminalOn(hostBg color.Color) color.Color {
	return bordersOn(hostBg).terminal
}

// SessionBorderModes is the focused border in each mode when
// appearance.session_border puts the session's colour, tint, on the window
// mode border.
//
// Terminal mode is the louder of the two, because it says the keys go into
// the pane: it stands modeLightnessGap further from the ground than window
// mode, and has at least its chroma. The terminal mode default keeps its
// lightness and takes as much chroma as the session colour has, where sRGB
// holds it. The session colour moves toward the ground to make the gap, and
// gives up chroma it has over terminal mode. When the mark floor stops the
// session colour, terminal mode moves out instead. A configured terminal mode
// colour is returned as chosen, with the session colour as it is.
func SessionBorderModes(tint, hostBg color.Color) (window, terminal color.Color) {
	if focused := focusedOverride(borderFocusedTerminal); focused != nil {
		return tint, focused
	}
	b := bordersOn(hostBg)
	if Depth() == overlay.Depth16 || tint == nil {
		return tint, b.terminal
	}
	key := [3][4]uint32{rgbaKey(tint), rgbaKey(b.terminal), rgbaKey(b.ground)}
	m := &sessionModesMemo
	m.Lock()
	defer m.Unlock()
	for i, k := range m.keys {
		if m.out[i][0] != nil && k == key {
			return m.out[i][0], m.out[i][1]
		}
	}

	lg := overlay.Lightness(b.ground)
	out := 1.0
	if lg > 0.5 {
		out = -1
	}
	dist := func(c color.Color) float64 { return (overlay.Lightness(c) - lg) * out }
	ok := func(c color.Color) bool { return ContrastRatio(c, b.ground) >= MarkFloor }
	at := func(c color.Color, d, ch float64) color.Color {
		return overlay.Vivid(c, lg+d*out, ch)
	}

	terminal = at(b.terminal, dist(b.terminal), max(overlay.Chroma(tint), overlay.Chroma(b.terminal)))
	window = tint
	if want := dist(terminal) - modeLightnessGap; dist(tint) > want {
		window = nil
		for d := want; d < dist(tint); d += 0.01 {
			if c := at(tint, d, overlay.Chroma(terminal)); ok(c) {
				window = c
				break
			}
		}
		if window == nil {
			window = tint
		}
	}
	if need := dist(window) + modeLightnessGap; dist(terminal) < need {
		terminal = at(b.terminal, need, max(overlay.Chroma(tint), overlay.Chroma(b.terminal)))
	}
	if overlay.Chroma(window) > overlay.Chroma(terminal) {
		window = at(window, dist(window), overlay.Chroma(terminal))
	}

	m.keys[m.next], m.out[m.next] = key, [2]color.Color{window, terminal}
	m.next = (m.next + 1) % len(m.out)
	return window, terminal
}

var sessionModesMemo struct {
	sync.Mutex
	keys [4][3][4]uint32
	out  [4][2]color.Color
	next int
}

type borderSet struct {
	window, terminal, unfocused, ground color.Color
}

func bordersOn(hostBg color.Color) borderSet {
	t := Current()
	var b borderSet
	var wFixed, tFixed, uFixed bool
	if c := focusedOverride(borderFocusedWindow); c != nil {
		b.window, wFixed = c, true
	} else if t == nil {
		b.window = slotAt16(14, hostBorderInk(lipgloss.Color("#AFFFFF"), hostBg))
	} else {
		b.window = slotAt16(14, borderInk(chromeOr(func(c *Chrome) color.Color { return c.AccentBright }, t.BrightCyan)))
	}
	switch c := focusedOverride(borderFocusedTerminal); {
	case c != nil:
		b.terminal, tFixed = c, true
	case Depth() == overlay.Depth16:
		b.terminal = overlay.Slot(10)
	case t == nil:
		b.terminal = hostBorderInk(lipgloss.Color("#AAFFAA"), hostBg)
	default:
		b.terminal = borderInk(chromeOr(func(c *Chrome) color.Color { return c.Success }, t.BrightGreen))
	}
	switch c := borderOverride(borderUnfocused); {
	case c != nil:
		b.unfocused, uFixed = c, true
	case t == nil:
		b.unfocused = slotAt16(8, neutralBorderOn(hostBg))
	default:
		if c := chromeFurniture(RoleBorderUnfocused); c != nil {
			b.unfocused, uFixed = borderInk(c), true
		} else {
			b.unfocused = slotAt16(1, borderInk(t.Red))
		}
	}
	if t == nil {
		b.ground = hostBg
	} else {
		b.ground = TerminalBg()
	}
	if b.ground == nil {
		b.ground = color.Black
	}
	if Depth() == overlay.Depth16 {
		return b
	}
	return spaceBorders(b, wFixed, tFixed, uFixed, t == nil)
}

const modeLightnessGap = 0.1

const focusLightnessGap = 0.12

const focusContrastFactor = 2.0

var spaceBordersMemo struct {
	sync.Mutex
	keys [4][5][4]uint32
	out  [4]borderSet
	next int
}

func spaceBorders(b borderSet, wFixed, tFixed, uFixed, vivid bool) borderSet {
	fixed := uint32(0)
	for i, f := range []bool{wFixed, tFixed, uFixed, vivid} {
		if f {
			fixed |= 1 << i
		}
	}
	key := [5][4]uint32{rgbaKey(b.window), rgbaKey(b.terminal), rgbaKey(b.unfocused), rgbaKey(b.ground), {fixed}}
	m := &spaceBordersMemo
	m.Lock()
	defer m.Unlock()
	for i, k := range m.keys {
		if m.out[i].window != nil && k == key {
			return m.out[i]
		}
	}

	lg := overlay.Lightness(b.ground)
	out := 1.0
	if lg > 0.5 {
		out = -1
	}
	dist := func(c color.Color) float64 { return (overlay.Lightness(c) - lg) * out }
	at := func(c color.Color, d float64) (color.Color, bool) {
		l := lg + d*out
		moved := overlay.WithLightness(c, l)
		return moved, math.Abs(overlay.Lightness(moved)-l) <= 0.02 && ContrastRatio(moved, b.ground) >= MarkFloor
	}
	pull := func(limit float64) {
		if uFixed || dist(b.unfocused) <= limit {
			return
		}
		for d := limit; d < dist(b.unfocused); d += 0.01 {
			if c, ok := at(b.unfocused, d); ok {
				b.unfocused = c
				return
			}
		}
	}

	reserve := 0.0
	if !tFixed && math.Abs(overlay.Lightness(b.window)-overlay.Lightness(b.terminal)) < modeLightnessGap {
		reserve = modeLightnessGap
	}
	pull(min(dist(b.window)-reserve, dist(b.terminal)) - focusLightnessGap)
	if need := dist(b.unfocused) + focusLightnessGap; !wFixed && dist(b.window) < need {
		if c, ok := at(b.window, need); ok {
			b.window = c
		}
	}
	least := dist(b.unfocused) + focusLightnessGap
	if wantCR := ContrastRatio(b.unfocused, b.ground) * focusContrastFactor; !wFixed {
		for d := max(dist(b.window), least); d <= 1; d += 0.01 {
			c, ok := at(b.window, d)
			if !ok {
				continue
			}
			if ContrastRatio(c, b.ground) >= wantCR {
				b.window = c
				break
			}
		}
		if ContrastRatio(b.window, b.ground) >= wantCR {
			least = max(least, dist(b.window))
		}
	}
	if !tFixed {
		b.terminal = modesApart(b.window, b.terminal, b.ground, least)
	}
	pull(min(dist(b.window), dist(b.terminal)) - focusLightnessGap)
	if vivid {
		if !wFixed {
			b.window = vividAt(b.window, noThemeChroma)
		}
		if !tFixed {
			b.terminal = vividAt(b.terminal, noThemeChroma)
		}
	}

	m.keys[m.next], m.out[m.next] = key, b
	m.next = (m.next + 1) % len(m.out)
	return b
}

const noThemeChroma = 0.15

func vividAt(c color.Color, limit float64) color.Color {
	return overlay.Vivid(c, overlay.Lightness(c), max(limit, overlay.Chroma(c)))
}

func modesApart(window, terminal, ground color.Color, least float64) color.Color {
	lw, lg := overlay.Lightness(window), overlay.Lightness(ground)
	out := 1.0
	if lg > 0.5 {
		out = -1
	}
	far := func(c color.Color) bool { return least < 0 || (overlay.Lightness(c)-lg)*out >= least-0.01 }
	if math.Abs(lw-overlay.Lightness(terminal)) >= modeLightnessGap && far(terminal) {
		return terminal
	}
	var fallback color.Color
	for _, l := range []float64{lw - modeLightnessGap*out, lw + modeLightnessGap*out} {
		c := overlay.WithLightness(terminal, l)
		if math.Abs(overlay.Lightness(c)-l) > 0.02 || ContrastRatio(c, ground) < MarkFloor {
			continue
		}
		if far(c) {
			return c
		}
		if fallback == nil {
			fallback = c
		}
	}
	if fallback != nil {
		return fallback
	}
	return terminal
}

func rgbaKey(c color.Color) [4]uint32 {
	if c == nil {
		return [4]uint32{}
	}
	r, g, b, a := c.RGBA()
	return [4]uint32{r, g, b, a}
}

// BorderFocusedCopy is the focused pane's border while it is in copy mode:
// the theme's border.copy, or nil when the theme names none. A configured
// border_focused_color wins, so it is nil then too. The keys for one mode do
// not: copy mode is a third state, and keeps its own colour.
func BorderFocusedCopy() color.Color {
	if borderOverride(borderFocused) != nil {
		return nil
	}
	if c := chromeFurniture(RoleBorderCopy); c != nil {
		return borderInk(c)
	}
	return nil
}

// BorderFocusedAttention is the focused pane's border while its agent waits
// for input: the theme's status.attention, or nil as BorderFocusedCopy.
func BorderFocusedAttention() color.Color {
	if borderOverride(borderFocused) != nil {
		return nil
	}
	if c := chromeFurniture(RoleAttention); c != nil {
		return borderInk(c)
	}
	return nil
}

const neutralBorderFloor = MarkFloor + 0.3

var neutralBorderMemo struct {
	sync.Mutex
	key [4]uint32
	ink color.Color
}

func neutralBorderOn(ground color.Color) color.Color {
	if ground == nil {
		ground = color.Black
	}
	key := rgbaKey(ground)
	m := &neutralBorderMemo
	m.Lock()
	defer m.Unlock()
	if m.ink != nil && m.key == key {
		return m.ink
	}
	ink := ContrastText(ground)
	out := ink
	const steps = 50
	for i := 1; i <= steps; i++ {
		c := overlay.MixColors(ground, ink, float64(i)/steps)
		if ContrastRatio(c, ground) >= neutralBorderFloor {
			out = c
			break
		}
	}
	m.key, m.ink = key, out
	return out
}

var hostBorderInkMemo struct {
	sync.Mutex
	keys [6][2][4]uint32
	ink  [6]color.Color
	next int
}

func hostBorderInk(c, hostBg color.Color) color.Color {
	if hostBg == nil {
		return c
	}
	cr, cg, cb, ca := c.RGBA()
	gr, gg, gb, ga := hostBg.RGBA()
	key := [2][4]uint32{{cr, cg, cb, ca}, {gr, gg, gb, ga}}
	m := &hostBorderInkMemo
	m.Lock()
	defer m.Unlock()
	for i, k := range m.keys {
		if m.ink[i] != nil && k == key {
			return m.ink[i]
		}
	}
	ink := ReadableAt(c, hostBg, MarkFloor)
	m.keys[m.next], m.ink[m.next] = key, ink
	m.next = (m.next + 1) % len(m.ink)
	return ink
}

// DockColorWindow returns the dock indicator color for window management mode.
func DockColorWindow() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(12, lipgloss.Color("#5c5cff"))
	}
	return slotAt16(12, chromeOr(func(c *Chrome) color.Color { return c.Accent }, t.BrightBlue))
}

// DockColorTerminal returns the dock indicator color for terminal mode.
func DockColorTerminal() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(10, lipgloss.Color("#7aa2f7"))
	}
	return slotAt16(10, chromeOr(func(c *Chrome) color.Color { return c.Success }, t.BrightGreen))
}

// DockColorCopy returns the dock indicator color for copy mode: the theme's
// border.copy, else its warning.
func DockColorCopy() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(3, lipgloss.Color("#e0af68"))
	}
	if c := chromeFurniture(RoleBorderCopy); c != nil {
		return c
	}
	return slotAt16(3, chromeOr(func(c *Chrome) color.Color { return c.Warning }, t.Yellow))
}

// DockColorMultifocus returns the dock indicator color while several panes
// take the keys: the theme's border.multifocus, else the copy mode colour.
func DockColorMultifocus() color.Color {
	if c := chromeFurniture(RoleBorderMultifocus); c != nil {
		return c
	}
	return DockColorCopy()
}

func furnitureRole(r Role) color.Color {
	switch r {
	case RoleBorderUnfocused:
		return BorderUnfocused()
	case RoleBorderCopy:
		return DockColorCopy()
	case RoleBorderMultifocus:
		return BorderMultifocus()
	case RoleRule:
		return RailRule()
	case RoleDotClose, RoleDotMinimize, RoleDotZoom:
		closeDot, minimise, zoom := WindowDots()
		return [...]color.Color{closeDot, minimise, zoom}[r-RoleDotClose]
	case RoleDockGround, RoleRailGround:
		return chromeFurniture(r)
	}
	return nil
}

// NotificationError returns the color for error notifications.
//
// The no-theme fallbacks for the four severities are ink colors, not the raw
// ANSI primaries: these are drawn as a one-cell mark and a sliver of cap on a
// dark bar, where #0000ee blue on #1a1a2e is a smudge. A
// theme, when one is active, still wins outright.
func NotificationError() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(1, lipgloss.Color("#dc2626"))
	}
	return slotAt16(1, chromeOr(func(c *Chrome) color.Color { return c.Error }, t.Red))
}

// NotificationWarning returns the color for warning notifications.
func NotificationWarning() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(3, lipgloss.Color("#d97706"))
	}
	return slotAt16(3, chromeOr(func(c *Chrome) color.Color { return c.Warning }, t.Yellow))
}

// NotificationSuccess returns the color for success notifications.
func NotificationSuccess() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(2, lipgloss.Color("#16a34a"))
	}
	return slotAt16(2, chromeOr(func(c *Chrome) color.Color { return c.Success }, t.Green))
}

// NotificationInfo returns the color for info notifications.
func NotificationInfo() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(4, lipgloss.Color("#2563eb"))
	}
	return slotAt16(4, chromeOr(func(c *Chrome) color.Color { return c.Info }, t.Blue))
}

// RailRule returns the ink for the chrome's structure: the rail's edge, the
// dock's separator, the unburnt remainder of a notification's burn, the
// collapsed strip's hairline and its group divider. All of it is one class,
// held to StructureTarget rather than to either floor.
//
// It was the same ink as the labels, and that is the whole of "the rail looks
// busy": the rail's edge and the dock's separator together are more cells than
// every label in the frame, so the largest object on screen was furniture. At
// StructureTarget the same structure is a whisper and the labels have not
// moved.
//
// The ground is the theme's when a theme is on, since that is the one the panes
// beside the rule are painted in. Without a theme tuios paints nothing and
// cannot ask, so the rule is measured against the chrome ramp's own canvas,
// which is the ground every other constant ink in the rail is measured against
// and lands within a channel step of charmtone Iron.
func RailRule() color.Color { return RailRuleOn(RailGround()) }

// NotificationSeverity maps a notification type string to its color. The type
// strings are the ones every ShowNotification call site already passes, so this
// is the single place the renderer turns one into a color and the only place
// that has to know "warning" and "warn" are the same thing.
func NotificationSeverity(notifType string) color.Color {
	switch notifType {
	case "error":
		return NotificationError()
	case "warning", "warn":
		return NotificationWarning()
	case "success":
		return NotificationSuccess()
	default:
		return NotificationInfo()
	}
}

// ColorToString converts a color.Color to a lowercase #rrggbb string, with nil
// reading as #000000. dock_helpers.go uses it where colors are stored as
// strings.
func ColorToString(c color.Color) string {
	if c == nil {
		return "#000000"
	}
	r, g, b, _ := c.RGBA()
	return overlay.Hex(color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff})
}
