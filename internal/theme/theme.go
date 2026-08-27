// Package theme provides color themes and styling for the TUIOS terminal.
package theme

import (
	"fmt"
	"image/color"
	"log"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	tint "github.com/lrstanley/bubbletint/v2"
)

var enabled bool

// Border color overrides from user config. When non-nil they take precedence
// over the theme-derived border colors. A single focused override applies to
// both window-mode and terminal-mode focused borders.
var (
	borderFocusedOverride   color.Color
	borderUnfocusedOverride color.Color
)

// SetBorderOverrides sets custom border colors from hex strings (e.g. "#89b4fa").
// An empty string clears the corresponding override and restores the theme color.
func SetBorderOverrides(focusedHex, unfocusedHex string) {
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
	return [16]color.Color{
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
	return t.Fg
}

// TerminalBg returns the background color for terminal emulator.
func TerminalBg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return t.Bg
}

// TerminalCursor returns the color for the terminal cursor.
func TerminalCursor() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00")
	}
	return t.Cursor
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
func BorderUnfocused() color.Color {
	// A configured colour is returned as chosen: measurement was overridden on
	// purpose, the same way the scrollbar treats a configured tint.
	if borderUnfocusedOverride != nil {
		return borderUnfocusedOverride
	}
	t := Current()
	if t == nil {
		return lipgloss.Color("#FAAAAA")
	}
	// Light pinkish red - use theme's red (or bright red depending on theme)
	// Using regular Red gives a softer, more muted tone for unfocused windows
	return borderInk(t.Red)
}

// BorderFocusedWindow returns the color for focused window borders in window management mode.
func BorderFocusedWindow() color.Color {
	if borderFocusedOverride != nil {
		return borderFocusedOverride
	}
	t := Current()
	if t == nil {
		return lipgloss.Color("#AFFFFF")
	}
	// Light cyan for window mode - use bright cyan
	return borderInk(t.BrightCyan)
}

// BorderFocusedTerminal returns the color for focused window borders in terminal mode.
func BorderFocusedTerminal() color.Color {
	if borderFocusedOverride != nil {
		return borderFocusedOverride
	}
	t := Current()
	if t == nil {
		return lipgloss.Color("#AAFFAA")
	}
	// Light green for terminal mode - use bright green
	return borderInk(t.BrightGreen)
}

// DockColorWindow returns the dock indicator color for window management mode.
func DockColorWindow() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#5c5cff")
	}
	return t.BrightBlue
}

// DockColorTerminal returns the dock indicator color for terminal mode.
func DockColorTerminal() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#7aa2f7") // Soft blue
	}
	return t.BrightGreen
}

// DockColorCopy returns the dock indicator color for copy mode.
func DockColorCopy() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#e0af68") // Soft amber
	}
	return t.Yellow
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
		return lipgloss.Color("#dc2626")
	}
	return t.Red
}

// NotificationWarning returns the color for warning notifications.
func NotificationWarning() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#d97706")
	}
	return t.Yellow
}

// NotificationSuccess returns the color for success notifications.
func NotificationSuccess() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#16a34a")
	}
	return t.Green
}

// NotificationInfo returns the color for info notifications.
func NotificationInfo() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#2563eb")
	}
	return t.Blue
}

// NotificationGround is the ground a message's inks are measured against: the
// bare bar the dock draws on, the same ground the strip's overflow arrows are
// measured on. The block carries no fill of its own. It sat on the chrome's
// Surface step once, and the slab was the largest ink object in the bar; worse,
// every severity ink had to be lifted toward the text colour to clear the mark
// floor on the raised grey, which washed the hue out of exactly the marks that
// carry the message. On the bare canvas the four severities clear the floor as
// themselves.
func NotificationGround() color.Color {
	return UI().Canvas
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

// ColorToString converts a color.Color to a hex string
// Used for dock_helpers.go where colors need to be stored as strings
func ColorToString(c color.Color) string {
	if c == nil {
		return "#000000"
	}
	r, g, b, _ := c.RGBA()
	// RGBA returns values in range 0-65535, convert to 0-255
	r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
	// Format as hex string
	return fmt.Sprintf("#%02x%02x%02x", r8, g8, b8)
}
