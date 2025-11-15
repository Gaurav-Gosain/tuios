// Package theme provides color themes and styling for the TUIOS terminal.
package theme

import (
	"fmt"
	"image/color"

	"github.com/charmbracelet/lipgloss/v2"
	tint "github.com/lrstanley/bubbletint/v2"
)

var enabled bool

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
	tint.NewDefaultRegistry()

	// Try to set the theme by ID
	ok := tint.SetTintID(themeName)
	if !ok {
		// Theme not found, set to default
		tint.SetTintID("default")
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
// These are injected into the terminal emulator.
func GetANSIPalette() [16]color.Color {
	t := Current()
	if t == nil {
		// Fallback to default xterm colors
		return [16]color.Color{
			lipgloss.Color("#000000"), lipgloss.Color("#cd0000"), lipgloss.Color("#00cd00"), lipgloss.Color("#cdcd00"),
			lipgloss.Color("#0000ee"), lipgloss.Color("#cd00cd"), lipgloss.Color("#00cdcd"), lipgloss.Color("#e5e5e5"),
			lipgloss.Color("#7f7f7f"), lipgloss.Color("#ff0000"), lipgloss.Color("#00ff00"), lipgloss.Color("#ffff00"),
			lipgloss.Color("#5c5cff"), lipgloss.Color("#ff00ff"), lipgloss.Color("#00ffff"), lipgloss.Color("#ffffff"),
		}
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

// Terminal colors for the emulator
func TerminalFg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#e5e5e5")
	}
	return t.Fg
}

func TerminalBg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return t.Bg
}

func TerminalCursor() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00")
	}
	return t.Cursor
}

// Window border colors
func BorderUnfocused() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#FAAAAA")
	}
	// Light pinkish red - use theme's red (or bright red depending on theme)
	// Using regular Red gives a softer, more muted tone for unfocused windows
	return t.Red
}

func BorderFocusedWindow() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#AFFFFF")
	}
	// Light cyan for window mode - use bright cyan
	return t.BrightCyan
}

func BorderFocusedTerminal() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#AAFFAA")
	}
	// Light green for terminal mode - use bright green
	return t.BrightGreen
}

// Dock mode indicator colors
func DockColorWindow() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#5c5cff")
	}
	return t.BrightBlue
}

func DockColorTerminal() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00")
	}
	return t.BrightGreen
}

func DockColorCopy() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ffff00")
	}
	return t.Yellow
}

// Copy mode colors
func CopyModeCursor() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ffff"), lipgloss.Color("#000000")
	}
	return t.BrightCyan, t.Black
}

func CopyModeVisualSelection() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cd00cd"), lipgloss.Color("#ffffff")
	}
	return t.Purple, t.BrightWhite
}

func CopyModeSearchCurrent() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ff00ff"), lipgloss.Color("#000000")
	}
	return t.BrightPurple, t.Black
}

func CopyModeSearchOther() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ffff00"), lipgloss.Color("#000000")
	}
	return t.Yellow, t.Black
}

func CopyModeTextSelection() (bg color.Color, fg color.Color) {
	return lipgloss.Color("62"), lipgloss.Color("15")
}

func CopyModeSelectionCursor() (bg color.Color, fg color.Color) {
	return lipgloss.Color("208"), lipgloss.Color("0")
}

func CopyModeSearchBar() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ffff00"), lipgloss.Color("#000000")
	}
	return t.Yellow, t.Black
}

// Terminal cursor for rendering
func TerminalCursorColors() (fg color.Color, bg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00"), lipgloss.Color("#000000")
	}
	return t.Cursor, t.Black
}

// Button colors
func ButtonFg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return t.Black
}

// Time overlay colors
func TimeOverlayBg() color.Color {
	return lipgloss.Color("#1a1a2e")
}

func TimeOverlayFg() color.Color {
	return lipgloss.Color("#a0a0b0")
}

func TimeOverlayPrefixActive() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cd0000")
	}
	return t.Red
}

func TimeOverlayPrefixInactive() color.Color {
	return lipgloss.Color("#ffffff")
}

// Welcome screen colors
func WelcomeTitle() color.Color {
	return lipgloss.Color("14") // Bright cyan
}

func WelcomeSubtitle() color.Color {
	return lipgloss.Color("11") // Bright yellow
}

func WelcomeText() color.Color {
	return lipgloss.Color("7") // White
}

func WelcomeHighlight() color.Color {
	return lipgloss.Color("6") // Cyan
}

// Cache stats overlay colors
func CacheStatsTitle() color.Color {
	return lipgloss.Color("14")
}

func CacheStatsLabel() color.Color {
	return lipgloss.Color("11")
}

func CacheStatsValue() color.Color {
	return lipgloss.Color("10")
}

func CacheStatsAccent() color.Color {
	return lipgloss.Color("13")
}

// Log viewer colors
func LogViewerTitle() color.Color {
	return lipgloss.Color("14")
}

func LogViewerError() color.Color {
	return lipgloss.Color("9")
}

func LogViewerWarn() color.Color {
	return lipgloss.Color("11")
}

func LogViewerInfo() color.Color {
	return lipgloss.Color("10")
}

func LogViewerDebug() color.Color {
	return lipgloss.Color("12")
}

func LogViewerBg() color.Color {
	return lipgloss.Color("#1a1a2a")
}

// Which-key overlay colors
func WhichKeyTitle() color.Color {
	return lipgloss.Color("11")
}

func WhichKeyText() color.Color {
	return lipgloss.Color("7")
}

func WhichKeyHighlight() color.Color {
	return lipgloss.Color("#ff6b6b")
}

func WhichKeyBg() color.Color {
	return lipgloss.Color("#1a1a2e")
}

// Notification colors
func NotificationError() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cd0000")
	}
	return t.Red
}

func NotificationWarning() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cdcd00")
	}
	return t.Yellow
}

func NotificationSuccess() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00cd00")
	}
	return t.Green
}

func NotificationInfo() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#0000ee")
	}
	return t.Blue
}

func NotificationBg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return t.Bg
}

func NotificationFg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#e5e5e5")
	}
	return t.Fg
}

// Dock styling colors
func DockBg() color.Color {
	return lipgloss.Color("#2a2a3e")
}

func DockFg() color.Color {
	return lipgloss.Color("#a0a0a8")
}

func DockHighlight() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00")
	}
	return t.BrightGreen
}

func DockDimmed() color.Color {
	return lipgloss.Color("#808090")
}

func DockAccent() color.Color {
	return lipgloss.Color("#a0a0b0")
}

func DockSeparator() color.Color {
	return lipgloss.Color("#303040")
}

// Help menu colors
func HelpKeyBadge() color.Color {
	return lipgloss.Color("5") // Purple/magenta
}

func HelpKeyBadgeBg() color.Color {
	return lipgloss.Color("0") // Black
}

func HelpGray() color.Color {
	return lipgloss.Color("8")
}

func HelpBorder() color.Color {
	return lipgloss.Color("14")
}

func HelpTabActive() color.Color {
	return lipgloss.Color("12")
}

func HelpTabInactive() color.Color {
	return lipgloss.Color("8")
}

func HelpTabBg() color.Color {
	return lipgloss.Color("0")
}

func HelpSearchFg() color.Color {
	return lipgloss.Color("11")
}

func HelpSearchBg() color.Color {
	return lipgloss.Color("15")
}

func HelpTableHeader() color.Color {
	return lipgloss.Color("12")
}

func HelpTableRow() color.Color {
	return lipgloss.Color("8")
}

// CLI table colors
func CLITableHeader() color.Color {
	return lipgloss.Color("12")
}

func CLITableBorder() color.Color {
	return lipgloss.Color("14")
}

func CLITableKey() color.Color {
	return lipgloss.Color("11")
}

func CLITableDim() color.Color {
	return lipgloss.Color("8")
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
