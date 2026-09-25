package tuios_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/pkg/tuios"
)

// =============================================================================
// Model Creation Tests
// =============================================================================

func TestNew_WithWorkspaces_Bounds(t *testing.T) {
	// Test minimum bound
	model := tuios.New(tuios.WithWorkspaces(0))
	if model.NumWorkspaces != 1 {
		t.Errorf("Expected minimum 1 workspace, got %d", model.NumWorkspaces)
	}

	// Test maximum bound
	model = tuios.New(tuios.WithWorkspaces(100))
	if model.NumWorkspaces != 9 {
		t.Errorf("Expected maximum 9 workspaces, got %d", model.NumWorkspaces)
	}
}

// =============================================================================
// Option Validation Tests
// =============================================================================

func TestWithScrollbackLines_Bounds(t *testing.T) {
	// These options modify global state, but we can at least verify they don't panic
	opts := tuios.DefaultOptions()

	// Test minimum bound function
	minOpt := tuios.WithScrollbackLines(50)
	minOpt(&opts)
	if opts.ScrollbackLines != 100 {
		t.Errorf("Expected minimum 100 scrollback lines, got %d", opts.ScrollbackLines)
	}

	// Test maximum bound function
	maxOpt := tuios.WithScrollbackLines(2000000)
	maxOpt(&opts)
	if opts.ScrollbackLines != 1000000 {
		t.Errorf("Expected maximum 1000000 scrollback lines, got %d", opts.ScrollbackLines)
	}

	// Test valid value
	validOpt := tuios.WithScrollbackLines(5000)
	validOpt(&opts)
	if opts.ScrollbackLines != 5000 {
		t.Errorf("Expected 5000 scrollback lines, got %d", opts.ScrollbackLines)
	}
}

// TestNew_OptionsReachTheAppearanceGlobals checks the embed options land on
// the settings they name, layered over the config the way CLI flags are.
func TestNew_OptionsReachTheAppearanceGlobals(t *testing.T) {
	saved := config.Global
	t.Cleanup(func() { config.Global = saved })

	_ = tuios.New(
		tuios.WithASCIIOnly(true),
		tuios.WithBorderStyle("double"),
		tuios.WithDockbarPosition("bottom"),
		tuios.WithHideWindowButtons(true),
		tuios.WithWindowButtonStyle("pill"),
		tuios.WithWindowButtonPosition("right"),
		tuios.WithScrollbackLines(500),
		tuios.WithAnimations(false),
	)

	g := config.Global
	if !g.UseASCIIOnly || g.BorderStyle != "double" || g.DockbarPosition != "bottom" ||
		!g.HideWindowButtons || g.WindowButtonStyle != "pill" || g.WindowButtonPosition != "right" ||
		g.ScrollbackLines != 500 || g.AnimationsEnabled {
		t.Errorf("the options did not all reach the globals: ascii=%v border=%q dock=%q hideButtons=%v style=%q position=%q scrollback=%d animations=%v",
			g.UseASCIIOnly, g.BorderStyle, g.DockbarPosition, g.HideWindowButtons,
			g.WindowButtonStyle, g.WindowButtonPosition, g.ScrollbackLines, g.AnimationsEnabled)
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkNew_Default(b *testing.B) {
	for b.Loop() {
		_ = tuios.New()
	}
}

func BenchmarkNew_WithOptions(b *testing.B) {
	for b.Loop() {
		_ = tuios.New(
			tuios.WithWorkspaces(5),
			tuios.WithShowKeys(true),
			tuios.WithAnimations(false),
		)
	}
}
