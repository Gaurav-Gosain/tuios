// Package theme provides color themes and styling for the TUIOS terminal.
package theme

import (
	"image/color"
	"log"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
	tint "github.com/lrstanley/bubbletint/v2"
)

var enabled bool

// Border color overrides from user config. When non-nil they take precedence
// over the theme-derived border colors. A single focused override applies to
// both window-mode and terminal-mode focused borders.
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
	borderMu                sync.RWMutex
	borderFocusedOverride   color.Color
	borderUnfocusedOverride color.Color
)

// SetBorderOverrides sets custom border colors from hex strings (e.g. "#89b4fa").
// An empty string clears the corresponding override and restores the theme color.
func SetBorderOverrides(focusedHex, unfocusedHex string) {
	borderMu.Lock()
	defer borderMu.Unlock()
	if focusedHex != "" {
		borderFocusedOverride = lipgloss.Color(focusedHex)
	} else {
		borderFocusedOverride = nil
	}
	if unfocusedHex != "" {
		borderUnfocusedOverride = lipgloss.Color(unfocusedHex)
	} else {
		borderUnfocusedOverride = nil
	}
}

// borderOverrides reads the pair under the lock, so a caller cannot see one
// half of a change.
func borderOverrides() (focused, unfocused color.Color) {
	borderMu.RLock()
	defer borderMu.RUnlock()
	return borderFocusedOverride, borderUnfocusedOverride
}

// Initialize sets up the theme registry with the specified theme name.
// Call this once at application startup.
// If themeName is empty, theming will be disabled and standard terminal colors will be used.
func Initialize(themeName string) error {
	// If no theme specified, disable theming
	if themeName == "" {
		enabled = false
		return nil
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
// xterm defaults that used to stand here made the picker show colours nobody
// had chosen.
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
	// A configured colour is returned as chosen: measurement was overridden on
	// purpose, the same way the scrollbar treats a configured tint.
	if _, unfocused := borderOverrides(); unfocused != nil {
		return unfocused
	}
	t := Current()
	if t == nil {
		return slotAt16(1, hostBorderInk(lipgloss.Color("#FAAAAA"), hostBg))
	}
	// Light pinkish red: the theme's regular red, which gives a softer, more
	// muted tone for unfocused windows than bright red.
	return slotAt16(1, borderInk(t.Red))
}

// BorderFocusedWindow returns the color for focused window borders in window management mode.
func BorderFocusedWindow() color.Color { return BorderFocusedWindowOn(nil) }

// BorderFocusedWindowOn is BorderFocusedWindow on a known host background.
// See BorderUnfocusedOn.
func BorderFocusedWindowOn(hostBg color.Color) color.Color {
	if focused, _ := borderOverrides(); focused != nil {
		return focused
	}
	t := Current()
	if t == nil {
		return slotAt16(14, hostBorderInk(lipgloss.Color("#AFFFFF"), hostBg))
	}
	// Light cyan for window mode: use bright cyan
	return slotAt16(14, borderInk(chromeOr(func(c *Chrome) color.Color { return c.AccentBright }, t.BrightCyan)))
}

// BorderFocusedTerminal returns the color for focused window borders in terminal mode.
func BorderFocusedTerminal() color.Color { return BorderFocusedTerminalOn(nil) }

// BorderFocusedTerminalOn is BorderFocusedTerminal on a known host
// background. See BorderUnfocusedOn.
func BorderFocusedTerminalOn(hostBg color.Color) color.Color {
	if focused, _ := borderOverrides(); focused != nil {
		return focused
	}
	t := Current()
	if t == nil {
		return slotAt16(10, hostBorderInk(lipgloss.Color("#AAFFAA"), hostBg))
	}
	// Light green for terminal mode: use bright green
	return slotAt16(10, borderInk(chromeOr(func(c *Chrome) color.Color { return c.Success }, t.BrightGreen)))
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

// DockColorCopy returns the dock indicator color for copy mode.
func DockColorCopy() color.Color {
	t := Current()
	if t == nil {
		return slotAt16(3, lipgloss.Color("#e0af68")) // Soft amber
	}
	return slotAt16(3, chromeOr(func(c *Chrome) color.Color { return c.Warning }, t.Yellow))
}

// NotificationError returns the color for error notifications.
//
// The no-theme fallbacks for the four severities are ink colors, not the raw
// ANSI primaries they used to be: these are drawn as a one-cell mark and a
// sliver of cap on a dark bar, and #0000ee blue on #1a1a2e was a smudge. A
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
