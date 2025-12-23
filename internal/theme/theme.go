// Package theme provides color themes and styling for the TUIOS terminal.
package theme

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
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

// BorderUnfocused returns the color for unfocused window borders.
func BorderUnfocused() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#FAAAAA")
	}
	// Light pinkish red - use theme's red (or bright red depending on theme)
	// Using regular Red gives a softer, more muted tone for unfocused windows
	return t.Red
}

// BorderFocusedWindow returns the color for focused window borders in window management mode.
func BorderFocusedWindow() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#AFFFFF")
	}
	// Light cyan for window mode - use bright cyan
	return t.BrightCyan
}

// BorderFocusedTerminal returns the color for focused window borders in terminal mode.
func BorderFocusedTerminal() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#AAFFAA")
	}
	// Light green for terminal mode - use bright green
	return t.BrightGreen
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
		return lipgloss.Color("#00ff00")
	}
	return t.BrightGreen
}

// DockColorCopy returns the dock indicator color for copy mode.
func DockColorCopy() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ffff00")
	}
	return t.Yellow
}

// CopyModeCursor returns background and foreground colors for the copy mode cursor.
func CopyModeCursor() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ffff"), lipgloss.Color("#000000")
	}
	return t.BrightCyan, t.Black
}

// CopyModeVisualSelection returns colors for visually selected text in copy mode.
func CopyModeVisualSelection() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cd00cd"), lipgloss.Color("#ffffff")
	}
	return t.Purple, t.BrightWhite
}

// CopyModeSearchCurrent returns colors for the current search match in copy mode.
func CopyModeSearchCurrent() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ff00ff"), lipgloss.Color("#000000")
	}
	return t.BrightPurple, t.Black
}

// CopyModeSearchOther returns colors for other search matches in copy mode.
func CopyModeSearchOther() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ffff00"), lipgloss.Color("#000000")
	}
	return t.Yellow, t.Black
}

// CopyModeTextSelection returns background and foreground colors for text selection in copy mode.
func CopyModeTextSelection() (bg color.Color, fg color.Color) {
	return lipgloss.Color("62"), lipgloss.Color("15")
}

// CopyModeSelectionCursor returns background and foreground colors for the selection cursor in copy mode.
func CopyModeSelectionCursor() (bg color.Color, fg color.Color) {
	return lipgloss.Color("208"), lipgloss.Color("0")
}

// CopyModeSearchBar returns colors for the search bar in copy mode.
func CopyModeSearchBar() (bg color.Color, fg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#ffff00"), lipgloss.Color("#000000")
	}
	return t.Yellow, t.Black
}

// TerminalCursorColors returns foreground and background colors for the terminal cursor rendering.
func TerminalCursorColors() (fg color.Color, bg color.Color) {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00"), lipgloss.Color("#000000")
	}
	return t.Cursor, t.Black
}

// ButtonFg returns the foreground color for buttons.
func ButtonFg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return t.Black
}

// TimeOverlayBg returns the background color for the time overlay.
func TimeOverlayBg() color.Color {
	return lipgloss.Color("#1a1a2e")
}

// TimeOverlayFg returns the foreground color for the time overlay.
func TimeOverlayFg() color.Color {
	return lipgloss.Color("#a0a0b0")
}

// TimeOverlayPrefixActive returns the color for active prefix commands in the time overlay.
func TimeOverlayPrefixActive() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cd0000")
	}
	return t.Red
}

// TimeOverlayPrefixInactive returns the color for inactive prefix commands in the time overlay.
func TimeOverlayPrefixInactive() color.Color {
	return lipgloss.Color("#ffffff")
}

// WelcomeTitle returns the color for welcome screen titles.
func WelcomeTitle() color.Color {
	return lipgloss.Color("14") // Bright cyan
}

// WelcomeSubtitle returns the color for welcome screen subtitles.
func WelcomeSubtitle() color.Color {
	return lipgloss.Color("11") // Bright yellow
}

// WelcomeText returns the color for welcome screen text.
func WelcomeText() color.Color {
	return lipgloss.Color("7") // White
}

// WelcomeHighlight returns the color for highlighted elements on the welcome screen.
func WelcomeHighlight() color.Color {
	return lipgloss.Color("6") // Cyan
}

// CacheStatsTitle returns the color for cache stats overlay titles.
func CacheStatsTitle() color.Color {
	return lipgloss.Color("14")
}

// CacheStatsLabel returns the color for cache stats overlay labels.
func CacheStatsLabel() color.Color {
	return lipgloss.Color("11")
}

// CacheStatsValue returns the color for cache stats overlay values.
func CacheStatsValue() color.Color {
	return lipgloss.Color("10")
}

// CacheStatsAccent returns the accent color for cache stats overlay.
func CacheStatsAccent() color.Color {
	return lipgloss.Color("13")
}

// LogViewerTitle returns the color for log viewer titles.
func LogViewerTitle() color.Color {
	return lipgloss.Color("14")
}

// LogViewerError returns the color for error messages in the log viewer.
func LogViewerError() color.Color {
	return lipgloss.Color("9")
}

// LogViewerWarn returns the color for warning messages in the log viewer.
func LogViewerWarn() color.Color {
	return lipgloss.Color("11")
}

// LogViewerInfo returns the color for info messages in the log viewer.
func LogViewerInfo() color.Color {
	return lipgloss.Color("10")
}

// LogViewerDebug returns the color for debug messages in the log viewer.
func LogViewerDebug() color.Color {
	return lipgloss.Color("12")
}

// LogViewerBg returns the background color for the log viewer.
func LogViewerBg() color.Color {
	return lipgloss.Color("#1a1a2a")
}

// WhichKeyTitle returns the color for which-key overlay titles.
func WhichKeyTitle() color.Color {
	return lipgloss.Color("11")
}

// WhichKeyText returns the color for which-key overlay text.
func WhichKeyText() color.Color {
	return lipgloss.Color("7")
}

// WhichKeyHighlight returns the highlight color for which-key overlay.
func WhichKeyHighlight() color.Color {
	return lipgloss.Color("#ff6b6b")
}

// WhichKeyBg returns the background color for which-key overlay.
func WhichKeyBg() color.Color {
	return lipgloss.Color("#1a1a2e")
}

// NotificationError returns the color for error notifications.
func NotificationError() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cd0000")
	}
	return t.Red
}

// NotificationWarning returns the color for warning notifications.
func NotificationWarning() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#cdcd00")
	}
	return t.Yellow
}

// NotificationSuccess returns the color for success notifications.
func NotificationSuccess() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00cd00")
	}
	return t.Green
}

// NotificationInfo returns the color for info notifications.
func NotificationInfo() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#0000ee")
	}
	return t.Blue
}

// NotificationBg returns the background color for notifications.
func NotificationBg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#000000")
	}
	return t.Bg
}

// NotificationFg returns the foreground color for notifications.
func NotificationFg() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#e5e5e5")
	}
	return t.Fg
}

// DockBg returns the background color for the dock.
func DockBg() color.Color {
	return lipgloss.Color("#2a2a3e")
}

// DockFg returns the foreground color for the dock.
func DockFg() color.Color {
	return lipgloss.Color("#a0a0a8")
}

// DockHighlight returns the highlight color for the dock.
func DockHighlight() color.Color {
	t := Current()
	if t == nil {
		return lipgloss.Color("#00ff00")
	}
	return t.BrightGreen
}

// DockDimmed returns the dimmed color for the dock.
func DockDimmed() color.Color {
	return lipgloss.Color("#808090")
}

// DockAccent returns the accent color for the dock.
func DockAccent() color.Color {
	return lipgloss.Color("#a0a0b0")
}

// DockSeparator returns the separator color for the dock.
func DockSeparator() color.Color {
	return lipgloss.Color("#303040")
}

// HelpKeyBadge returns the color for key badges in help menu.
func HelpKeyBadge() color.Color {
	return lipgloss.Color("5") // Purple/magenta
}

// HelpKeyBadgeBg returns the background color for key badges in help menu.
func HelpKeyBadgeBg() color.Color {
	return lipgloss.Color("0") // Black
}

// HelpGray returns the gray color for help menu elements.
func HelpGray() color.Color {
	return lipgloss.Color("8")
}

// HelpBorder returns the border color for help menu.
func HelpBorder() color.Color {
	return lipgloss.Color("14")
}

// HelpTabActive returns the color for active tabs in help menu.
func HelpTabActive() color.Color {
	return lipgloss.Color("12")
}

// HelpTabInactive returns the color for inactive tabs in help menu.
func HelpTabInactive() color.Color {
	return lipgloss.Color("8")
}

// HelpTabBg returns the background color for tabs in help menu.
func HelpTabBg() color.Color {
	return lipgloss.Color("0")
}

// HelpSearchFg returns the foreground color for search in help menu.
func HelpSearchFg() color.Color {
	return lipgloss.Color("11")
}

// HelpSearchBg returns the background color for search in help menu.
func HelpSearchBg() color.Color {
	return lipgloss.Color("15")
}

// HelpTableHeader returns the color for table headers in help menu.
func HelpTableHeader() color.Color {
	return lipgloss.Color("12")
}

// HelpTableRow returns the color for table rows in help menu.
func HelpTableRow() color.Color {
	return lipgloss.Color("8")
}

// CLITableHeader returns the color for CLI table headers.
func CLITableHeader() color.Color {
	return lipgloss.Color("12")
}

// CLITableBorder returns the color for CLI table borders.
func CLITableBorder() color.Color {
	return lipgloss.Color("14")
}

// CLITableKey returns the color for CLI table keys.
func CLITableKey() color.Color {
	return lipgloss.Color("11")
}

// CLITableDim returns the dimmed color for CLI table elements.
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
