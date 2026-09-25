package theme

import (
	"testing"

	"charm.land/lipgloss/v2"
)

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

// TestInitialize_RegistryEnsuredKeepsTheme is a regression test for the bug
// where opening the settings page or theme picker (which call EnsureRegistry)
// rebuilt the tint registry and reset the configured theme to the bubbletint
// default (dracula_plus). Initialize must build the registry through the same
// sync.Once EnsureRegistry uses, so a later EnsureRegistry() is a no-op and
// leaves the active theme untouched.
func TestInitialize_RegistryEnsuredKeepsTheme(t *testing.T) {
	if err := Initialize("nord"); err != nil {
		t.Fatalf("Initialize error: %v", err)
	}
	before := CurrentThemeID()
	if before != "nord" {
		t.Fatalf("expected active theme %q after Initialize, got %q", "nord", before)
	}

	// Simulate opening the settings page / theme picker.
	EnsureRegistry()

	if after := CurrentThemeID(); after != before {
		t.Fatalf("EnsureRegistry reset the active theme: before=%q after=%q", before, after)
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
