package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// osState is the part of the model the window-manager bindings under test move.
// Comparing the whole struct means a key that fires anything at all is caught,
// not only the three actions that were demonstrated on screen.
type osState struct {
	windows       int
	focused       int
	mode          app.Mode
	autoTiling    bool
	showHelp      bool
	showSettings  bool
	showPalette   bool
	showLayout    bool
	showAggregate bool
	showSessions  bool
	workspace     int
	renaming      bool
	prefixActive  bool
}

func snapshotOS(o *app.OS) osState {
	return osState{
		windows:       len(o.Windows),
		focused:       o.FocusedWindow,
		mode:          o.Mode,
		autoTiling:    o.AutoTiling,
		showHelp:      o.ShowHelp,
		showSettings:  o.ShowSettings,
		showPalette:   o.ShowCommandPalette,
		showLayout:    o.ShowLayoutPicker,
		showAggregate: o.ShowAggregateView,
		showSessions:  o.ShowSessionSwitcher,
		workspace:     o.CurrentWorkspace,
		renaming:      o.Renaming(),
		prefixActive:  o.PrefixActive,
	}
}

// helpModalOS builds a window-management-mode model with the help overlay open
// and two daemon-backed windows, which need no PTY and so let a key that does
// reach the window manager close one without the test having spawned a shell.
func helpModalOS(t *testing.T) *app.OS {
	t.Helper()

	m := &app.OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		FocusedWindow:    0,
		Mode:             app.WindowManagementMode,
		ShowHelp:         true,
		HelpCategory:     -1,
		KeybindRegistry:  config.NewKeybindRegistry(config.DefaultConfig()),
	}

	for i := range 2 {
		id := "help-modal-window-" + string(rune('a'+i))
		ptyData := make(chan struct{}, 1)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-ptyData:
				case <-done:
					return
				}
			}
		}()
		t.Cleanup(func() { close(done) })

		win := terminal.NewDaemonWindow(id, "help", 0, 0, 40, 12, i, "pty-"+id, ptyData, config.DefaultScrollbackLines)
		if win == nil {
			t.Fatal("NewDaemonWindow returned nil")
		}
		win.Workspace = 1
		t.Cleanup(win.Close)
		m.Windows = append(m.Windows, win)
	}

	return m
}

// TestHelpOverlaySwallowsUnhandledKeys pins the help overlay being modal in
// window-management mode.
//
// The overlay handled esc/q/?/arrows/'/' and search typing and then fell
// through to the ordinary window-manager dispatch for everything else, from
// behind a panel covering most of the screen. n created and focused a window,
// x closed one, t toggled tiling, ',' stacked the settings page on top of help:
// state changed with nothing on screen to say so, and the same keystroke did
// two different things depending on which mode help had been opened from, since
// terminal mode already ignored what it did not handle.
//
// The table is deliberately wider than the three keys demonstrated on screen: a
// missing catch-all leaks every binding, so the guard should be against the
// whole bound set rather than against the examples.
func TestHelpOverlaySwallowsUnhandledKeys(t *testing.T) {
	keys := []string{
		"n", "x", "t", "f", "m", "z", "w", "s", "c", "d", "i", ",",
		"h", "j", "k", "l", "-", "|", ".", "<", ">", "=", "[", "]",
		"1", "2", "3", "4", "5", "6", "7", "8", "9",
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			m := helpModalOS(t)
			before := snapshotOS(m)

			msg := tea.KeyPressMsg{Code: rune(key[0]), Text: key}
			out, _ := HandleWindowManagementModeKey(msg, m)

			if got := snapshotOS(out); got != before {
				t.Errorf("%q changed the model with the help overlay open:\n got %+v\nwant %+v", key, got, before)
			}
		})
	}
}

// TestHelpOverlayStillHandlesItsOwnKeys is the other half of the pin: the
// catch-all must not swallow the keys the overlay itself is driven by, which is
// how a modality fix turns into a wedged overlay.
func TestHelpOverlayStillHandlesItsOwnKeys(t *testing.T) {
	t.Run("esc closes", func(t *testing.T) {
		m := helpModalOS(t)
		out, _ := HandleWindowManagementModeKey(tea.KeyPressMsg{Code: tea.KeyEscape}, m)
		if out.ShowHelp {
			t.Error("esc did not close the help overlay")
		}
	})

	t.Run("q closes", func(t *testing.T) {
		m := helpModalOS(t)
		out, _ := HandleWindowManagementModeKey(tea.KeyPressMsg{Code: 'q', Text: "q"}, m)
		if out.ShowHelp {
			t.Error("q did not close the help overlay")
		}
	})

	t.Run("slash enters search", func(t *testing.T) {
		m := helpModalOS(t)
		out, _ := HandleWindowManagementModeKey(tea.KeyPressMsg{Code: '/', Text: "/"}, m)
		if !out.HelpSearchMode {
			t.Error("'/' did not enter help search mode")
		}
		if !out.ShowHelp {
			t.Error("'/' closed the help overlay")
		}
	})

	t.Run("typing in search reaches the query", func(t *testing.T) {
		m := helpModalOS(t)
		m.HelpSearchMode = true
		out, _ := HandleWindowManagementModeKey(tea.KeyPressMsg{Code: 'n', Text: "n"}, m)
		if out.HelpSearchQuery != "n" {
			t.Errorf("search query is %q, want %q", out.HelpSearchQuery, "n")
		}
		if len(out.Windows) != 2 {
			t.Errorf("typing in search created a window: %d windows", len(out.Windows))
		}
	})

	t.Run("down scrolls", func(t *testing.T) {
		m := helpModalOS(t)
		out, _ := HandleWindowManagementModeKey(tea.KeyPressMsg{Code: tea.KeyDown}, m)
		if out.HelpScrollOffset == 0 {
			t.Error("down arrow did not scroll the help overlay")
		}
	})
}
