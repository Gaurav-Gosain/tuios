package tuios_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/pkg/tuios"
)

// =============================================================================
// Model Creation Tests
// =============================================================================

func TestNew_Default(t *testing.T) {
	model := tuios.New()
	if model == nil {
		t.Fatal("New() returned nil")
	}

	// Check default state
	if model.FocusedWindow != -1 {
		t.Errorf("Expected no focused window (-1), got %d", model.FocusedWindow)
	}

	if model.NumWorkspaces != 9 {
		t.Errorf("Expected 9 workspaces, got %d", model.NumWorkspaces)
	}

	if model.CurrentWorkspace != 1 {
		t.Errorf("Expected current workspace 1, got %d", model.CurrentWorkspace)
	}
}

func TestNew_WithWorkspaces(t *testing.T) {
	model := tuios.New(tuios.WithWorkspaces(4))
	if model.NumWorkspaces != 4 {
		t.Errorf("Expected 4 workspaces, got %d", model.NumWorkspaces)
	}
}

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

func TestNew_WithShowKeys(t *testing.T) {
	model := tuios.New(tuios.WithShowKeys(true))
	if !model.ShowKeys {
		t.Error("Expected ShowKeys to be true")
	}
}

func TestNew_WithSSHMode(t *testing.T) {
	model := tuios.New(tuios.WithSSHMode(true))
	if !model.IsSSHMode {
		t.Error("Expected IsSSHMode to be true")
	}
}

func TestNew_WithSize(t *testing.T) {
	model := tuios.New(tuios.WithSize(120, 40))
	if model.Width != 120 {
		t.Errorf("Expected width 120, got %d", model.Width)
	}
	if model.Height != 40 {
		t.Errorf("Expected height 40, got %d", model.Height)
	}
}

func TestNew_MultipleOptions(t *testing.T) {
	model := tuios.New(
		tuios.WithWorkspaces(5),
		tuios.WithShowKeys(true),
		tuios.WithSize(100, 30),
	)

	if model.NumWorkspaces != 5 {
		t.Errorf("Expected 5 workspaces, got %d", model.NumWorkspaces)
	}
	if !model.ShowKeys {
		t.Error("Expected ShowKeys to be true")
	}
	if model.Width != 100 || model.Height != 30 {
		t.Errorf("Expected size 100x30, got %dx%d", model.Width, model.Height)
	}
}

// =============================================================================
// DefaultOptions Tests
// =============================================================================

func TestDefaultOptions(t *testing.T) {
	opts := tuios.DefaultOptions()

	if !opts.Animations {
		t.Error("Expected animations to be enabled by default")
	}

	if opts.Workspaces != 9 {
		t.Errorf("Expected 9 workspaces by default, got %d", opts.Workspaces)
	}

	if opts.ScrollbackLines != 10000 {
		t.Errorf("Expected 10000 scrollback lines, got %d", opts.ScrollbackLines)
	}
}

// =============================================================================
// ProgramOptions Tests
// =============================================================================

func TestProgramOptions(t *testing.T) {
	opts := tuios.ProgramOptions()

	if len(opts) == 0 {
		t.Error("Expected ProgramOptions to return at least one option")
	}
}

// =============================================================================
// Config Access Tests
// =============================================================================

func TestConfig_DefaultConfig(t *testing.T) {
	cfg := tuios.Config.DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}
}

func TestConfig_GetConfigPath(t *testing.T) {
	path, err := tuios.Config.GetConfigPath()
	// In Nix sandbox or restricted environments, this may fail
	// which is expected behavior - just verify it doesn't panic
	if err != nil {
		// Acceptable in restricted environments (e.g., Nix sandbox)
		t.Logf("GetConfigPath error (expected in restricted env): %v", err)
		return
	}
	if path == "" {
		t.Error("Expected non-empty config path")
	}
}

// =============================================================================
// Mode Constants Tests
// =============================================================================

func TestModeConstants(t *testing.T) {
	// Just verify constants are accessible
	_ = tuios.WindowManagementMode
	_ = tuios.TerminalMode

	// They should be different
	if tuios.WindowManagementMode == tuios.TerminalMode {
		t.Error("WindowManagementMode and TerminalMode should be different")
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
