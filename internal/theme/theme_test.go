package theme

import (
	"image/color"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestInitialize_Empty tests initialization with empty theme name
func TestInitialize_Empty(t *testing.T) {
	err := Initialize("")
	if err != nil {
		t.Fatalf("Initialize with empty theme should not error: %v", err)
	}

	if IsEnabled() {
		t.Error("Theme should be disabled with empty name")
	}
}

// TestInitialize_WithTheme tests initialization with a theme name
func TestInitialize_WithTheme(t *testing.T) {
	err := Initialize("default")
	if err != nil {
		t.Fatalf("Initialize with 'default' theme should not error: %v", err)
	}

	if !IsEnabled() {
		t.Error("Theme should be enabled after initialization")
	}
}

// TestInitialize_InvalidTheme tests initialization with invalid theme
func TestInitialize_InvalidTheme(t *testing.T) {
	err := Initialize("nonexistent-theme-12345")
	if err != nil {
		t.Fatalf("Initialize should not error with invalid theme: %v", err)
	}

	// Should fallback to default
	if !IsEnabled() {
		t.Error("Theme should be enabled even with invalid theme")
	}
}

// TestCurrent tests getting current theme
func TestCurrent(t *testing.T) {
	// Test with disabled theme
	_ = Initialize("")
	if Current() != nil {
		t.Error("Current should return nil when theme is disabled")
	}

	// Test with enabled theme
	_ = Initialize("default")
	if Current() == nil {
		t.Error("Current should return non-nil when theme is enabled")
	}
}

// TestGetANSIPalette tests ANSI palette retrieval
func TestGetANSIPalette(t *testing.T) {
	// Test with disabled theme (fallback colors)
	_ = Initialize("")
	palette := GetANSIPalette()

	if len(palette) != 16 {
		t.Errorf("Expected palette of 16 colors, got %d", len(palette))
	}

	// Verify no nil colors
	for i, c := range palette {
		if c == nil {
			t.Errorf("Color at index %d is nil", i)
		}
	}

	// Test with enabled theme
	_ = Initialize("default")
	paletteWithTheme := GetANSIPalette()

	if len(paletteWithTheme) != 16 {
		t.Errorf("Expected palette of 16 colors with theme, got %d", len(paletteWithTheme))
	}

	// Verify no nil colors with theme
	for i, c := range paletteWithTheme {
		if c == nil {
			t.Errorf("Color at index %d is nil with theme", i)
		}
	}
}

// TestTerminalColors tests terminal color functions
func TestTerminalColors(t *testing.T) {
	_ = Initialize("")

	tests := []struct {
		name     string
		colorFn  func() color.Color
		expected bool // whether color should be non-nil
	}{
		{"TerminalFg", TerminalFg, true},
		{"TerminalBg", TerminalBg, true},
		{"TerminalCursor", TerminalCursor, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.colorFn()
			if tt.expected && c == nil {
				t.Errorf("%s returned nil", tt.name)
			}
		})
	}
}

// TestBorderColors tests border color functions
func TestBorderColors(t *testing.T) {
	_ = Initialize("")

	tests := []struct {
		name    string
		colorFn func() color.Color
	}{
		{"BorderUnfocused", BorderUnfocused},
		{"BorderFocusedWindow", BorderFocusedWindow},
		{"BorderFocusedTerminal", BorderFocusedTerminal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.colorFn()
			if c == nil {
				t.Errorf("%s returned nil", tt.name)
			}
		})
	}
}

// TestDockColors tests dock color functions
func TestDockColors(t *testing.T) {
	_ = Initialize("")

	tests := []struct {
		name    string
		colorFn func() color.Color
	}{
		{"DockColorWindow", DockColorWindow},
		{"DockColorTerminal", DockColorTerminal},
		{"DockColorCopy", DockColorCopy},
		{"DockBg", DockBg},
		{"DockFg", DockFg},
		{"DockHighlight", DockHighlight},
		{"DockDimmed", DockDimmed},
		{"DockAccent", DockAccent},
		{"DockSeparator", DockSeparator},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.colorFn()
			if c == nil {
				t.Errorf("%s returned nil", tt.name)
			}
		})
	}
}

// TestCopyModeColors tests copy mode color functions
func TestCopyModeColors(t *testing.T) {
	_ = Initialize("")

	tests := []struct {
		name    string
		colorFn func() (color.Color, color.Color)
	}{
		{"CopyModeCursor", CopyModeCursor},
		{"CopyModeVisualSelection", CopyModeVisualSelection},
		{"CopyModeSearchCurrent", CopyModeSearchCurrent},
		{"CopyModeSearchOther", CopyModeSearchOther},
		{"CopyModeTextSelection", CopyModeTextSelection},
		{"CopyModeSelectionCursor", CopyModeSelectionCursor},
		{"CopyModeSearchBar", CopyModeSearchBar},
		{"TerminalCursorColors", TerminalCursorColors},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bg, fg := tt.colorFn()
			if bg == nil {
				t.Errorf("%s returned nil background", tt.name)
			}
			if fg == nil {
				t.Errorf("%s returned nil foreground", tt.name)
			}
		})
	}
}

// TestNotificationColors tests notification color functions
func TestNotificationColors(t *testing.T) {
	_ = Initialize("")

	tests := []struct {
		name    string
		colorFn func() color.Color
	}{
		{"NotificationError", NotificationError},
		{"NotificationWarning", NotificationWarning},
		{"NotificationSuccess", NotificationSuccess},
		{"NotificationInfo", NotificationInfo},
		{"NotificationBg", NotificationBg},
		{"NotificationFg", NotificationFg},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.colorFn()
			if c == nil {
				t.Errorf("%s returned nil", tt.name)
			}
		})
	}
}

// TestColorToString tests color to string conversion
func TestColorToString(t *testing.T) {
	tests := []struct {
		name     string
		color    color.Color
		expected string
	}{
		{
			name:     "nil color",
			color:    nil,
			expected: "#000000",
		},
		{
			name:     "black color",
			color:    lipgloss.Color("#000000"),
			expected: "#000000",
		},
		{
			name:     "white color",
			color:    lipgloss.Color("#ffffff"),
			expected: "#ffffff",
		},
		{
			name:     "red color",
			color:    lipgloss.Color("#ff0000"),
			expected: "#ff0000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ColorToString(tt.color)
			if result != tt.expected {
				t.Errorf("ColorToString: expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// TestColorConsistency tests that colors are consistent across calls
func TestColorConsistency(t *testing.T) {
	_ = Initialize("default")

	// Call twice and ensure we get the same color
	c1 := TerminalFg()
	c2 := TerminalFg()

	r1, g1, b1, a1 := c1.RGBA()
	r2, g2, b2, a2 := c2.RGBA()

	if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
		t.Error("TerminalFg should return consistent colors")
	}
}

// BenchmarkGetANSIPalette benchmarks ANSI palette retrieval
func BenchmarkGetANSIPalette(b *testing.B) {
	_ = Initialize("default")

	b.ResetTimer()
	for b.Loop() {
		_ = GetANSIPalette()
	}
}

// BenchmarkColorToString benchmarks color to string conversion
func BenchmarkColorToString(b *testing.B) {
	c := lipgloss.Color("#ff0000")

	b.ResetTimer()
	for b.Loop() {
		_ = ColorToString(c)
	}
}
