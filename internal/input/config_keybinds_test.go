package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// osWithBindings builds an OS whose keybind registry comes from the default
// config with one section overridden, which is what a user editing config.toml
// ends up with after the defaults are filled in.
func osWithBindings(t *testing.T, override func(*config.KeybindingsConfig)) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	override(&cfg.Keybindings)
	return app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
}

func press(key string) tea.KeyPressMsg {
	switch key {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	}
	runes := []rune(key)
	return tea.KeyPressMsg{Code: runes[0], Text: key}
}

// TestRebindingAPrefixKeyTakesEffect is the test that would have caught the
// whole class of bug this change fixes: every prefix section was parsed,
// validated and written into the user's config while the handlers matched
// literal key strings, so rebinding anything under the leader key was a silent
// no-op. Each case rebinds one action onto a key that means something else by
// default, then asserts both that the new key runs the action and that the old
// key no longer does.
func TestRebindingAPrefixKeyTakesEffect(t *testing.T) {
	tests := []struct {
		name string
		// override installs the non-default binding.
		override func(*config.KeybindingsConfig)
		// arm puts the OS into the prefix state the key is pressed in.
		arm func(*app.OS)
		// route is the handler the routing layer would reach for that state.
		route func(tea.KeyPressMsg, *app.OS) (*app.OS, tea.Cmd)
		// newKey is the rebound key, oldKey the default it replaced.
		newKey, oldKey string
		// done reports whether the action ran.
		done func(*app.OS) bool
	}{
		{
			name: "prefix_mode",
			override: func(k *config.KeybindingsConfig) {
				k.PrefixMode["prefix_help"] = []string{"g"}
				delete(k.PrefixMode, "prefix_scrollback") // "s" would still be bound
			},
			arm:    func(o *app.OS) { o.PrefixActive = true },
			route:  HandlePrefixCommand,
			newKey: "g", oldKey: "?",
			done: func(o *app.OS) bool { return o.ShowHelp },
		},
		{
			name: "window_prefix",
			override: func(k *config.KeybindingsConfig) {
				k.WindowPrefix["window_prefix_tiling"] = []string{"g"}
			},
			arm:    func(o *app.OS) { o.TilingPrefixActive = true },
			route:  HandleTilingPrefixCommand,
			newKey: "g", oldKey: "t",
			done: func(o *app.OS) bool { return o.AutoTiling },
		},
		{
			name: "workspace_prefix",
			override: func(k *config.KeybindingsConfig) {
				k.WorkspacePrefix["workspace_prefix_switch_3"] = []string{"g"}
			},
			arm:    func(o *app.OS) { o.WorkspacePrefixActive = true },
			route:  HandleWorkspacePrefixCommand,
			newKey: "g", oldKey: "3",
			done: func(o *app.OS) bool { return o.CurrentWorkspace == 3 },
		},
		{
			name: "debug_prefix",
			override: func(k *config.KeybindingsConfig) {
				k.DebugPrefix["debug_prefix_logs"] = []string{"g"}
			},
			arm:    func(o *app.OS) { o.DebugPrefixActive = true },
			route:  HandleDebugPrefixCommand,
			newKey: "g", oldKey: "l",
			done: func(o *app.OS) bool { return o.ShowLogs },
		},
		{
			name: "tape_prefix",
			override: func(k *config.KeybindingsConfig) {
				k.TapePrefix["tape_prefix_manager"] = []string{"g"}
			},
			arm:    func(o *app.OS) { o.TapePrefixActive = true },
			route:  HandleTapePrefixCommand,
			newKey: "g", oldKey: "m",
			done: func(o *app.OS) bool { return o.ShowTapeManager },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := osWithBindings(t, tc.override)
			tc.arm(o)
			result, _ := tc.route(press(tc.newKey), o)
			if !tc.done(result) {
				t.Errorf("rebound key %q did not run the action", tc.newKey)
			}

			o2 := osWithBindings(t, tc.override)
			tc.arm(o2)
			result2, _ := tc.route(press(tc.oldKey), o2)
			if tc.done(result2) {
				t.Errorf("replaced default key %q still ran the action", tc.oldKey)
			}
		})
	}
}

// TestRebindingAMinimizePrefixKeyTakesEffect covers the minimize prefix, which
// needs a window to act on and so does not fit the table above.
func TestRebindingAMinimizePrefixKeyTakesEffect(t *testing.T) {
	newOS := func() *app.OS {
		o := osWithBindings(t, func(k *config.KeybindingsConfig) {
			k.MinimizePrefix["minimize_prefix_focused"] = []string{"g"}
		})
		o.Windows = append(o.Windows, &terminal.Window{ID: "w1", Workspace: o.CurrentWorkspace})
		o.FocusedWindow = 0
		o.MinimizePrefixActive = true
		return o
	}

	o := newOS()
	result, _ := HandleMinimizePrefixCommand(press("g"), o)
	if !result.Windows[0].Minimized {
		t.Error("rebound key did not minimize the focused window")
	}

	o2 := newOS()
	result2, _ := HandleMinimizePrefixCommand(press("m"), o2)
	if result2.Windows[0].Minimized {
		t.Error("replaced default key still minimized the focused window")
	}
}

// TestRebindingATerminalModeKeyTakesEffect covers the [keybindings.terminal_mode]
// section, whose whole point is binds that work while typing into a shell.
func TestRebindingATerminalModeKeyTakesEffect(t *testing.T) {
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.TerminalMode["terminal_exit_mode"] = []string{"alt+q"}
	})
	o.Mode = app.TerminalMode

	if !handleTerminalModeBinds(tea.KeyPressMsg{Code: 'q', Mod: tea.ModAlt}, o) {
		t.Fatal("rebound terminal-mode key was not consumed")
	}
	if o.Mode != app.WindowManagementMode {
		t.Error("rebound terminal-mode key did not leave terminal mode")
	}

	o2 := osWithBindings(t, func(k *config.KeybindingsConfig) {
		k.TerminalMode["terminal_exit_mode"] = []string{"alt+q"}
	})
	o2.Mode = app.TerminalMode
	if handleTerminalModeBinds(tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModAlt}, o2) {
		t.Error("replaced default alt+esc was still consumed")
	}
}

// TestUnboundKeysStillReachTheShell pins the rule that makes the terminal-mode
// dispatch safe: only reserved chords may be intercepted, so a plain letter is
// never swallowed no matter what the main keybind section binds it to.
func TestUnboundKeysStillReachTheShell(t *testing.T) {
	o := osWithBindings(t, func(k *config.KeybindingsConfig) {
		// A user who binds workspace switching to a bare letter must not lose
		// that letter while typing.
		k.Workspaces["switch_workspace_2"] = []string{"j"}
	})
	o.Mode = app.TerminalMode

	if handleTerminalModeBinds(press("j"), o) {
		t.Error("a bare letter was intercepted in terminal mode")
	}
	if o.CurrentWorkspace == 2 {
		t.Error("a bare letter switched workspaces instead of reaching the shell")
	}
}

// TestPrefixEscapeLeavesTerminalModeRatherThanDetaching pins the behaviour the
// prefix_detach/prefix_exit_mode split preserves. The old default bound esc to
// prefix_detach while the handler hard-coded esc to switch modes; now that the
// binding is what runs, esc must still switch modes.
func TestPrefixEscapeLeavesTerminalModeRatherThanDetaching(t *testing.T) {
	o := osWithBindings(t, func(*config.KeybindingsConfig) {})
	o.Mode = app.TerminalMode
	o.PrefixActive = true

	result, cmd := HandlePrefixCommand(press("esc"), o)
	if result.Mode != app.WindowManagementMode {
		t.Error("leader esc did not return to window-management mode")
	}
	if cmd != nil {
		t.Error("leader esc produced a command, which would mean it quit or detached")
	}
}
