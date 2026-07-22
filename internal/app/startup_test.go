package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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
	prev := config.AnimationsEnabled
	config.AnimationsEnabled = false
	t.Cleanup(func() { config.AnimationsEnabled = prev })

	cfg := config.DefaultConfig()
	cfg.Startup.OpenDefaultWindow = openWindow
	cfg.Startup.Tiled = tiled
	m := NewOS(OSOptions{UserConfig: cfg})
	m.Width, m.Height = 120, 40
	return m
}

// TestStartupPreferences_BothOn is the combined behavior the feature exists for:
// a fresh session opens one terminal and comes up tiled. It also proves the
// tiling actually applied to the window rather than only flipping the flag.
func TestStartupPreferences_BothOn(t *testing.T) {
	m := newStartupOS(t, true, true)
	defer closeWindows(m)

	m.applyStartupPreferences()

	if len(m.Windows) != 1 {
		t.Fatalf("expected 1 window opened on start, got %d", len(m.Windows))
	}
	if !m.AutoTiling {
		t.Fatal("expected AutoTiling on after start-tiled")
	}
	if got := m.LayoutName(); got != LayoutModeBSP {
		t.Fatalf("expected BSP layout, got %q", got)
	}
	// Prove the layout was actually applied to the window (not just flagged on):
	// the single tiled window fills the BSP bounds.
	bounds := m.GetBSPBounds()
	w := m.Windows[0]
	if w.X != bounds.X || w.Y != bounds.Y || w.Width != bounds.W || w.Height != bounds.H {
		t.Fatalf("window was not tiled to the BSP bounds: window=(%d,%d %dx%d) bounds=(%d,%d %dx%d)",
			w.X, w.Y, w.Width, w.Height, bounds.X, bounds.Y, bounds.W, bounds.H)
	}
}

// TestStartupPreferences_AllThreeOn is the full intended combination: open a
// window, tile it, and land focused in terminal mode so typing reaches the
// shell straight away.
func TestStartupPreferences_AllThreeOn(t *testing.T) {
	m := newStartupOS(t, true, true)
	m.UserConfig.Startup.StartInTerminalMode = true
	defer closeWindows(m)

	m.applyStartupPreferences()

	if len(m.Windows) != 1 {
		t.Fatalf("expected 1 window opened on start, got %d", len(m.Windows))
	}
	if !m.AutoTiling {
		t.Fatal("expected AutoTiling on after start-tiled")
	}
	if m.FocusedWindow < 0 || m.FocusedWindow >= len(m.Windows) {
		t.Fatalf("expected a focused window for terminal mode, got index %d", m.FocusedWindow)
	}
	if m.Mode != TerminalMode {
		t.Fatalf("expected to start in terminal mode, got mode %v", m.Mode)
	}
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

// TestStartupPreferences_BothOff confirms the default: nothing is opened and
// tiling stays off.
func TestStartupPreferences_BothOff(t *testing.T) {
	m := newStartupOS(t, false, false)
	defer closeWindows(m)

	m.applyStartupPreferences()

	if len(m.Windows) != 0 {
		t.Fatalf("expected no windows with both options off, got %d", len(m.Windows))
	}
	if m.AutoTiling {
		t.Fatal("expected tiling to stay off with both options off")
	}
}

// TestStartupPreferences_TiledOnly enables tiling on an empty session without
// opening a window; tiling turns on so windows opened later tile.
func TestStartupPreferences_TiledOnly(t *testing.T) {
	m := newStartupOS(t, false, true)
	defer closeWindows(m)

	m.applyStartupPreferences()

	if len(m.Windows) != 0 {
		t.Fatalf("expected no windows opened, got %d", len(m.Windows))
	}
	if !m.AutoTiling {
		t.Fatal("expected AutoTiling on with tiled=true")
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

// TestStartupPreferences_WiredToFirstResize proves the wiring: the first
// WindowSizeMsg applies the preferences once, and a second one does not open a
// second window.
func TestStartupPreferences_WiredToFirstResize(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Startup.OpenDefaultWindow = true
	cfg.Startup.Tiled = true
	m := NewOS(OSOptions{UserConfig: cfg})
	defer closeWindows(m)

	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if len(m.Windows) != 1 {
		t.Fatalf("first WindowSizeMsg should open one window, got %d", len(m.Windows))
	}
	if !m.AutoTiling {
		t.Fatal("first WindowSizeMsg should have enabled tiling")
	}

	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if len(m.Windows) != 1 {
		t.Fatalf("second WindowSizeMsg must not re-run startup; want 1 window, got %d", len(m.Windows))
	}
}
