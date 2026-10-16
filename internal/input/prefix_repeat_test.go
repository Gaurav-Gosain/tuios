package input

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// repeatOS is a client with the prefix repeat window on.
func repeatOS(t *testing.T) *app.OS {
	t.Helper()
	m := app.NewOS(app.OSOptions{UserConfig: config.DefaultConfig()})
	// NewOS does not build one, and every lookup here goes through it.
	m.KeybindRegistry = config.NewKeybindRegistry(config.DefaultConfig())
	m.Settings.PrefixRepeatTime = config.PrefixRepeatTimeDefault
	return m
}

// TestARepeatableCommandLeavesThePrefixArmed.
//
// One press of the prefix per pane is the difference between navigating with
// the prefix and giving up on it, and on macOS the prefix is the path that
// always works, because the direct chords are the ones a terminal swallows.
//
// Negative control: dropping the armIfRepeatable call in HandlePrefixCommand
// leaves the window closed and this fails.
func TestARepeatableCommandLeavesThePrefixArmed(t *testing.T) {
	m := repeatOS(t)
	m.PrefixActive = true

	// The prefix binding for walking right. Taken from the registry rather
	// than assumed, so a rebind does not quietly make this test vacuous.
	key, ok := prefixKeyFor(t, m, "terminal_focus_right")
	if !ok {
		t.Fatal("ASSERTION: nothing walks right from the prefix, so this proves nothing")
	}
	HandlePrefixCommand(key, m)

	if !m.PrefixRepeatLive() {
		t.Error("a repeatable prefix command closed the window straight away")
	}
}

// TestAOneShotCommandDoesNotLeaveThePrefixArmed. A second press of something
// destructive, or of something that opens a panel, is either a mistake or a
// dismissal, and neither should be made easier by accident.
func TestAOneShotCommandDoesNotLeaveThePrefixArmed(t *testing.T) {
	m := repeatOS(t)
	m.PrefixActive = true

	key, ok := prefixKeyFor(t, m, "prefix_new_window")
	if !ok {
		t.Fatal("ASSERTION: no prefix binding for making a window, so this proves nothing")
	}
	HandlePrefixCommand(key, m)

	if m.PrefixRepeatLive() {
		t.Error("a one-shot prefix command left the prefix armed")
	}
}

// TestTheWindowClosesOnAKeyThatIsNotTheCommand. The window exists to make one
// command easy to repeat, not to swallow whatever is typed next.
func TestTheWindowClosesOnAKeyThatIsNotTheCommand(t *testing.T) {
	m := repeatOS(t)
	m.ArmPrefixRepeat()
	if !m.PrefixRepeatLive() {
		t.Fatal("ASSERTION: the window is not open, so there is nothing to close")
	}

	// A plain letter, which is not a repeatable prefix command.
	if _, _, ok := tryPrefixRepeat(tea.KeyPressMsg{Code: 'z', Text: "z"}, m); ok {
		t.Error("a letter was taken as a repeat of the last prefix command")
	}
	if m.PrefixRepeatLive() {
		t.Error("the window stayed open after a key that was not the command")
	}
}

// TestTheWindowExpires, so a key struck a minute later is an ordinary key.
func TestTheWindowExpires(t *testing.T) {
	m := repeatOS(t)
	m.Settings.PrefixRepeatTime = 1
	m.ArmPrefixRepeat()
	time.Sleep(5 * time.Millisecond)

	if m.PrefixRepeatLive() {
		t.Error("the window outlived the time it was given")
	}
}

// TestTurningItOffArmsNothing. Zero is a real value for this setting and means
// every prefix command takes its own prefix, which is what tuios did before
// the window existed.
func TestTurningItOffArmsNothing(t *testing.T) {
	m := repeatOS(t)
	m.Settings.PrefixRepeatTime = 0
	m.ArmPrefixRepeat()

	if m.PrefixRepeatLive() {
		t.Error("the window opened with the setting off")
	}
}

// prefixKeyFor is the key bound to a prefix action in this client's registry.
func prefixKeyFor(t *testing.T, m *app.OS, action string) (tea.KeyPressMsg, bool) {
	t.Helper()
	if m.KeybindRegistry == nil {
		return tea.KeyPressMsg{}, false
	}
	for _, k := range []string{
		"left", "right", "up", "down", "h", "j", "k", "l",
		"n", "p", "a", "c", "z", "s", "w", "q", "ctrl+p",
	} {
		if m.KeybindRegistry.GetPrefixAction(k) == action {
			if len(k) == 1 {
				return tea.KeyPressMsg{Code: rune(k[0]), Text: k}, true
			}
			return keyByName(k), true
		}
	}
	return tea.KeyPressMsg{}, false
}

// keyByName builds a named key press.
func keyByName(name string) tea.KeyPressMsg {
	switch name {
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{}
}

// TestEveryRepeatableActionExists. The set is keyed by action name, and a name
// nothing registers is never looked up, so a typo or a rename leaves that
// command one-shot with no error anywhere. resize_left and its siblings sat
// in the set for months while the resize actions were named resize_pane_*.
//
// Negative control: putting "resize_left" back in the set fails this.
func TestEveryRepeatableActionExists(t *testing.T) {
	d := GetDispatcher()
	for _, action := range config.DefaultPrefixRepeat {
		if !d.HasAction(action) && !app.PrefixWorkActions[action] {
			t.Errorf("%q is repeatable but no action of that name is registered", action)
		}
	}
}

// TestARepeatListReplacesTheDefault: keybindings.repeat is the whole set, not
// an addition to it. A tmux user who binds next_workspace to n leaves it out,
// so the n of a word typed right after ctrl+b n reaches the shell. An empty
// list turns repeat off.
//
// Negative control: merging the list into DefaultPrefixRepeat keeps
// terminal_focus_right armed in both rows.
func TestARepeatListReplacesTheDefault(t *testing.T) {
	for _, tc := range []struct {
		name   string
		repeat []string
		arms   map[string]bool
	}{
		{"own list", []string{"prefix_next_window"}, map[string]bool{"prefix_next_window": true, "terminal_focus_right": false}},
		{"empty", []string{}, map[string]bool{"prefix_next_window": false, "terminal_focus_right": false}},
		{"unset", nil, map[string]bool{"prefix_next_window": false, "terminal_focus_right": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Keybindings.Repeat = tc.repeat
			m := app.NewOS(app.OSOptions{UserConfig: cfg})
			m.KeybindRegistry = config.NewKeybindRegistry(cfg)
			m.Settings.PrefixRepeatTime = config.PrefixRepeatTimeDefault
			for action, want := range tc.arms {
				m.ClearPrefixRepeat()
				m.PrefixActive = true
				key, ok := prefixKeyFor(t, m, action)
				if !ok {
					t.Fatalf("ASSERTION: no prefix key runs %s", action)
				}
				HandlePrefixCommand(key, m)
				if got := m.PrefixRepeatLive(); got != want {
					t.Errorf("after %s the prefix is armed = %v, want %v", action, got, want)
				}
			}
		})
	}
}

// TestEveryPresetActionIsRegistered: each action a preset binds, and each it
// marks to repeat, is one the dispatcher runs. A preset that names an action
// by a wrong name binds a key that does nothing, with no error anywhere.
//
// Negative control: renaming last_workspace to last_workspaces in the tmux
// preset fails it.
func TestEveryPresetActionIsRegistered(t *testing.T) {
	d := GetDispatcher()
	known := func(a string) bool { return d.HasAction(a) || app.PrefixWorkActions[a] }
	def := config.DefaultConfig().Keybindings
	defaults := map[string]map[string][]string{
		"prefix_mode": def.PrefixMode, "terminal_mode": def.TerminalMode, "tuios_prefix": def.TuiosPrefix,
		"window_prefix": def.WindowPrefix, "workspace_prefix": def.WorkspacePrefix,
	}
	for _, name := range config.Presets()[1:] {
		cfg, err := config.ParseUserConfig([]byte("[keybindings]\npreset = \"" + name + "\"\n"))
		if err != nil {
			t.Fatalf("preset %s: %v", name, err)
		}
		kb := cfg.Keybindings
		for section, m := range map[string]map[string][]string{
			"prefix_mode": kb.PrefixMode, "terminal_mode": kb.TerminalMode, "tuios_prefix": kb.TuiosPrefix,
			"window_prefix": kb.WindowPrefix, "workspace_prefix": kb.WorkspacePrefix,
		} {
			for action, keys := range m {
				// Only what the preset changed: a few tuios terminal-mode
				// keys are read by the input path, not the dispatcher.
				if slices.Equal(keys, defaults[section][action]) {
					continue
				}
				if len(keys) > 0 && !known(action) {
					t.Errorf("preset %s binds %v to %s [%s], which no handler runs", name, keys, action, section)
				}
			}
		}
		for _, action := range kb.Repeat {
			if !known(action) {
				t.Errorf("preset %s repeats %s, which no handler runs", name, action)
			}
		}
	}
}
