package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// closeWindows tears down the real PTYs spawned by AddWindow so a test does not
// leak shell processes.
func closeWindows(m *OS) {
	for _, w := range m.Windows {
		w.Close()
	}
}

// newStartupOS builds a local (non-daemon) OS with the given [startup] options
// and a known screen size.
func newStartupOS(t *testing.T, openWindow, tiled bool) *OS {
	t.Helper()
	// Disable animations so the tiling layout is applied to the window geometry
	// instantly instead of easing into place over ticks the test never runs.
	prev := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prev })

	cfg := config.DefaultConfig()
	cfg.Startup.OpenDefaultWindow = openWindow
	cfg.Startup.Tiled = tiled
	m := NewOS(OSOptions{UserConfig: cfg})
	m.Width, m.Height = 120, 40
	return m
}

// TestStartupPreferences_TerminalModeNeedsWindow confirms the guard: with
// start_in_terminal_mode on but no window to focus, the session stays in
// window-management mode rather than becoming a dead end that swallows keys.
func TestStartupPreferences_TerminalModeNeedsWindow(t *testing.T) {
	m := newStartupOS(t, false, false)
	m.UserConfig.Startup.StartInTerminalMode = true
	defer closeWindows(m)

	m.applyStartupPreferences()

	if len(m.Windows) != 0 {
		t.Fatalf("expected no windows, got %d", len(m.Windows))
	}
	if m.Mode != WindowManagementMode {
		t.Fatalf("expected to stay in window-management mode with no window, got mode %v", m.Mode)
	}
}

// TestStartupPreferences_SkipsNonEmptySession confirms that a session which
// already has windows (an attach that restored them) is left alone: no extra
// window is opened and its tiling state is not overridden.
func TestStartupPreferences_SkipsNonEmptySession(t *testing.T) {
	m := newStartupOS(t, true, true)
	defer closeWindows(m)

	// Simulate a restored, floating session with one existing window.
	m.AddWindow("")
	if len(m.Windows) != 1 {
		t.Fatalf("setup: expected 1 window, got %d", len(m.Windows))
	}

	m.applyStartupPreferences()

	if len(m.Windows) != 1 {
		t.Fatalf("expected the existing session to be left alone, got %d windows", len(m.Windows))
	}
	if m.AutoTiling {
		t.Fatal("expected tiling not to be forced onto an existing session")
	}
}
