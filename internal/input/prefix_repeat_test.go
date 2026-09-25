package input

import (
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
