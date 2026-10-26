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

// Border color overrides from user config. When non-nil they take precedence
// over the theme-derived border colors. The focused border has one override per
// mode and one for both: a mode's own wins, then the shared one, then the
// theme.
//
// They are process-global and are written whenever a session applies its
// appearance config, which is once per client. Under the ssh server that is
// once per connection, on that connection's own goroutine, so two people
// connecting at the same moment wrote these at the same moment. The race
// detector caught it in CI; what a user would have seen is one session
// briefly wearing another's border colour.
//
// The lock makes the writes and reads safe. It does not make them right: the
// values are still shared, so two ssh sessions with different border colours
// still overwrite each other rather than each keeping their own. Making them
// per-session is a larger change than a data race deserves, and the lock is
// the part that has to be true either way.
var (
	borderMu        sync.RWMutex
	borderOverrides [borderKinds]ColorSpec
)

// borderKind names one border override.
type borderKind int

const (
	borderUnfocused borderKind = iota
	// borderFocused is appearance.border_focused_color, for both modes.
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

// borderOverride reads one override under the lock and resolves it, nil when
// it is unset. A role or a slot resolves on the theme in force, so an override
// written as "accent" follows a theme change.
func borderOverride(kind borderKind) color.Color {
	borderMu.RLock()
	c := borderOverrides[kind]
	borderMu.RUnlock()
	return c.Resolve()
}

// focusedOverride is the configured focused border for one mode: the mode's
// own key, then the key for both modes, nil when neither is set.
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
	// Generation moves only when the theme in force changes, so a config
	// reload that names the same theme keeps every memo keyed on it.
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

	// Build the tint registry (built-ins plus custom themes) exactly once for
	// the process, via the same sync.Once EnsureRegistry uses. Calling
	// tint.NewDefaultRegistry() directly here would let a later EnsureRegistry()
	// (fired when the settings page or theme picker first opens) rebuild the
	// global registry and reset the active tint to the library default,
	// silently discarding the configured theme.
	EnsureRegistry()

	// Try to set the theme by ID. An unknown name leaves the registry on its
	// current tint; warn so a typo is visible instead of silently applying the
	// wrong palette. Behavior is otherwise unchanged (theming stays enabled).
	if ok := tint.SetTintID(themeName); !ok {
		log.Printf("Warning: theme %q not found; using default theme colors", themeName)
	}

	return nil
}

// activeTint is the tint in force, or nil with theming off. It does not
// build the registry.
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

// to256 is overlay.To256 that leaves a nil colour nil.
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
		t.Black,        // 0
		t.Red,          // 1
		t.Green,        // 2
		t.Yellow,       // 3
		t.Blue,         // 4
		t.Purple,       // 5
		t.Cyan,         // 6
		t.White,        // 7
		t.BrightBlack,  // 8
		t.BrightRed,    // 9
		t.BrightGreen,  // 10
		t.BrightYellow, // 11
		t.BrightBlue,   // 12
		t.BrightPurple, // 13
		t.BrightCyan,   // 14
		t.BrightWhite,  // 15
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

// borderInk measures a theme-derived border against the pane it frames. A
// border and the pane's background come from the same theme and nothing was
// checking that they differ: 72 of the registry's tints put an unfocused border
// under 3:1 on its own pane, the worst at 1.19:1, which is a window with no
// visible edge. A border is a shape, so the mark floor is what it has to clear.
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

// BorderFocusedTerminalBeside is the terminal mode border for a pane whose
// window mode border is window rather than the default, as it is when
// appearance.session_border puts the session's colour there. The default is
// moved in lightness away from window, so the two modes stay apart whatever
// the session's colour is. A configured colour is returned as chosen.
func BorderFocusedTerminalBeside(window, hostBg color.Color) color.Color {
	if focused := focusedOverride(borderFocusedTerminal); focused != nil {
		return focused
	}
	b := bordersOn(hostBg)
	if Depth() == overlay.Depth16 || window == nil {
		return b.terminal
	}
	return modesApart(window, b.terminal, b.ground, -1)
}

// borderSet is the three pane borders and the ground they are measured on.
type borderSet struct {
	window, terminal, unfocused, ground color.Color
}

// bordersOn resolves the three borders together, because each default is
// placed against the other two. Configured colours are returned as chosen and
// only the defaults move.
func bordersOn(hostBg color.Color) borderSet {
	t := Current()
	var b borderSet
	var wFixed, tFixed, uFixed bool
	if c := focusedOverride(borderFocusedWindow); c != nil {
		b.window, wFixed = c, true
	} else if t == nil {
		b.window = slotAt16(14, hostBorderInk(lipgloss.Color("#AFFFFF"), hostBg))
	} else {
		// Light cyan for window mode: use bright cyan
		b.window = slotAt16(14, borderInk(chromeOr(func(c *Chrome) color.Color { return c.AccentBright }, t.BrightCyan)))
	}
	switch c := focusedOverride(borderFocusedTerminal); {
	case c != nil:
		b.terminal, tFixed = c, true
	case Depth() == overlay.Depth16:
		// Bright green against the window mode's bright cyan: two slots, so
		// the terminal's palette keeps them apart.
		b.terminal = overlay.Slot(10)
	case t == nil:
		b.terminal = hostBorderInk(lipgloss.Color("#AAFFAA"), hostBg)
	default:
		// Light green for terminal mode: use bright green
		b.terminal = borderInk(chromeOr(func(c *Chrome) color.Color { return c.Success }, t.BrightGreen))
	}
	switch c := borderOverride(borderUnfocused); {
	case c != nil:
		// A configured colour is returned as chosen: measurement was
		// overridden on purpose, the same way the scrollbar treats a
		// configured tint.
		b.unfocused, uFixed = c, true
	case t == nil:
		b.unfocused = slotAt16(1, hostBorderInk(lipgloss.Color("#FAAAAA"), hostBg))
	default:
		if c := chromeFurniture(RoleBorderUnfocused); c != nil {
			b.unfocused = borderInk(c)
		} else {
			// Light pinkish red: the theme's regular red, which gives a softer,
			// more muted tone for unfocused windows than bright red.
			b.unfocused = slotAt16(1, borderInk(t.Red))
		}
	}
	if t == nil {
		b.ground = hostBg
	} else {
		b.ground = TerminalBg()
	}
	if b.ground == nil {
		// A nil ground is taken as dark, the way the no-theme colours are
		// picked.
		b.ground = color.Black
	}
	if Depth() == overlay.Depth16 {
		return b
	}
	return spaceBorders(b, wFixed, tFixed, uFixed)
}

// modeLightnessGap is the least OKLab lightness between the two default
// focused borders. On 231 of the 344 bundled themes the accent and the success
// colour sit within 0.08 of each other, catppuccin mocha at 0.000 and nord at
// 0.005. Hue alone then tells the modes apart, which fails a person who cannot
// tell cyan from green and anyone who sees the border from the corner of an
// eye.
const modeLightnessGap = 0.1

// focusLightnessGap is how much further from the ground, in OKLab lightness,
// each default focused border stands than the unfocused one. Unspaced, the
// terminal mode border sat within 0.06 of the unfocused border on no theme
// over a Nord terminal, on nord and on catppuccin mocha, so focus was told by
// hue alone, the failure modeLightnessGap describes.
const focusLightnessGap = 0.12

// spaceBordersMemo keeps the last few answers of spaceBorders, which is asked
// on every frame and converts to OKLab and measures contrast many times.
var spaceBordersMemo struct {
	sync.Mutex
	keys [4][5][4]uint32
	out  [4]borderSet
	next int
}

// spaceBorders moves the default borders apart in lightness, hue and chroma
// kept. The unfocused border goes toward the ground until each focused border
// stands focusLightnessGap further out, but never under the mark floor. When
// the floor stops it, the window mode border goes further out instead. The
// terminal mode border is then placed modeLightnessGap from the window mode
// one, on the side that keeps it clear of the unfocused border.
func spaceBorders(b borderSet, wFixed, tFixed, uFixed bool) borderSet {
	fixed := uint32(0)
	for i, f := range []bool{wFixed, tFixed, uFixed} {
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
	// out is +1 when standing out from the ground means lighter.
	out := 1.0
	if lg > 0.5 {
		out = -1
	}
	dist := func(c color.Color) float64 { return (overlay.Lightness(c) - lg) * out }
	at := func(c color.Color, d float64) (color.Color, bool) {
		l := lg + d*out
		moved := overlay.WithLightness(c, l)
		// A colour out of gamut at l comes back clamped, at another lightness.
		return moved, math.Abs(overlay.Lightness(moved)-l) <= 0.02 && ContrastRatio(moved, b.ground) >= MarkFloor
	}
	// pull moves the unfocused border toward the ground until it is at most
	// limit from it, or as near the limit as the mark floor allows.
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

	// Room is kept for the terminal mode border on the ground's side of the
	// window mode one when it will have to move, since that is where a
	// saturated colour keeps its chroma.
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
	if !tFixed {
		b.terminal = modesApart(b.window, b.terminal, b.ground, dist(b.unfocused)+focusLightnessGap)
	}
	pull(min(dist(b.window), dist(b.terminal)) - focusLightnessGap)

	m.keys[m.next], m.out[m.next] = key, b
	m.next = (m.next + 1) % len(m.out)
	return b
}

// modesApart is the terminal mode border moved in lightness, hue and chroma
// kept, until it sits modeLightnessGap from the window mode border. It tries
// the ground's side first: on a dark ground that is where a saturated green
// keeps its chroma, and on a light ground it is the side with more contrast.
// It tries the far side when the near side would crowd the unfocused border. A side is taken only when the colour stays in
// gamut, clears the mark floor on ground, and stands at least least from the
// ground; a negative least asks for no distance. When no side does all of
// that, the first side that keeps the gamut and the floor is taken, and when
// none keeps those either, terminal stays as it is.
func modesApart(window, terminal, ground color.Color, least float64) color.Color {
	lw, lg := overlay.Lightness(window), overlay.Lightness(ground)
	out := 1.0
	if lg > 0.5 {
		out = -1
	}
	// The epsilon keeps a colour placed exactly at least, by the same
	// arithmetic, from failing on rounding.
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

// rgbaKey is c's channels as a memo key, all zero for nil.
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

// hostBorderInkMemo keeps the last few answers of hostBorderInk. The border
// colours are asked for on every pane of every frame, and a lift is a walk of
// up to sixteen contrast measurements.
var hostBorderInkMemo struct {
	sync.Mutex
	keys [6][2][4]uint32
	ink  [6]color.Color
	next int
}

// hostBorderInk lifts a no-theme border colour until it clears the mark floor
// on the host's ground, and leaves it alone when the ground is not known.
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
		return slotAt16(10, lipgloss.Color("#7aa2f7")) // Soft blue
	}
	return slotAt16(10, chromeOr(func(c *Chrome) color.Color { return c.Success }, t.BrightGreen))
}

// DockColorCopy returns the dock indicator color for copy mode: the theme's
// border.copy, else its warning.
func DockColorCopy() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(3, lipgloss.Color("#e0af68")) // Soft amber
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

// furnitureRole is the colour a config value naming one of the roles after
// RoleCard draws: what that piece of chrome draws now, theme or not. A ground
// is the theme's only, nil when it names none.
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
	// RGBA returns values in range 0-65535, convert to 0-255
	return overlay.Hex(color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff})
}
