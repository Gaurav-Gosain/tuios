package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// focusedOS builds a minimal OS with a single focused window whose terminal
// renders the given screen content.
func focusedOS(t *testing.T, screen string) *OS {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	if _, err := em.Write([]byte(screen)); err != nil {
		t.Fatalf("write to emulator: %v", err)
	}
	win := &terminal.Window{Terminal: em, Workspace: 1}
	return &OS{
		Settings:         config.Global,
		Windows:          []*terminal.Window{win},
		FocusedWindow:    0,
		CurrentWorkspace: 1,
	}
}

func TestStartScriptWaitRegexBadPattern(t *testing.T) {
	m := &OS{Settings: config.Global}
	m.startScriptWaitRegex(&tape.Command{Type: tape.CommandTypeWaitUntilRegex, Args: []string{"("}})
	if m.ScriptWaitRegex != nil {
		t.Error("expected no wait to be armed for an invalid pattern")
	}

	m.startScriptWaitRegex(&tape.Command{Type: tape.CommandTypeWaitUntilRegex})
	if m.ScriptWaitRegex != nil {
		t.Error("expected no wait to be armed for a missing pattern")
	}
}

func TestCheckScriptWaitRegexBlocksThenTimesOut(t *testing.T) {
	m := focusedOS(t, "still running\n")
	m.startScriptWaitRegex(&tape.Command{
		Type: tape.CommandTypeWaitUntilRegex,
		Args: []string{"never appears", "5000"},
	})
	if m.checkScriptWaitRegex() {
		t.Error("expected to keep waiting while pattern is absent")
	}

	// Force the deadline into the past to exercise the timeout path.
	m.ScriptWaitDeadline = time.Now().Add(-time.Millisecond)
	if !m.checkScriptWaitRegex() {
		t.Error("expected timeout to resume playback")
	}
	if m.ScriptWaitRegex != nil {
		t.Error("expected wait state cleared after timeout")
	}
}

// TestParseKeyToMessage tests the key parsing function
func TestParseKeyToMessage(t *testing.T) {
	m := &OS{Settings: config.Global}

	tests := []struct {
		name           string
		input          string
		expectedString string
		expectedMod    tea.KeyMod
	}{
		// Basic keys
		{"single letter", "a", "a", 0},
		{"uppercase letter", "A", "a", 0}, // normalized to lowercase
		{"number", "5", "5", 0},

		// Modifier combos
		{"ctrl+b", "ctrl+b", "ctrl+b", tea.ModCtrl},
		{"ctrl+c", "ctrl+c", "ctrl+c", tea.ModCtrl},
		{"alt+1", "alt+1", "alt+1", tea.ModAlt},
		{"shift+a", "shift+a", "shift+a", tea.ModShift},
		{"ctrl+shift+a", "ctrl+shift+a", "ctrl+shift+a", tea.ModCtrl | tea.ModShift},

		// Special keys
		{"enter", "Enter", "enter", 0},
		{"return", "return", "enter", 0},
		{"space", "Space", "space", 0},
		{"tab", "Tab", "tab", 0},
		{"escape", "Escape", "esc", 0},
		{"esc", "esc", "esc", 0},
		{"backspace", "Backspace", "backspace", 0},

		// Arrow keys
		{"up", "Up", "up", 0},
		{"down", "Down", "down", 0},
		{"left", "Left", "left", 0},
		{"right", "Right", "right", 0},

		// Function keys
		{"f1", "F1", "f1", 0},
		{"f12", "F12", "f12", 0},

		// Modifier with special key
		{"ctrl+enter", "ctrl+Enter", "ctrl+enter", tea.ModCtrl},
		{"alt+tab", "alt+Tab", "alt+tab", tea.ModAlt},
		// A modified space keeps its modifier: Text must stay empty so
		// String() does not drop Ctrl.
		{"ctrl+space", "ctrl+space", "ctrl+space", tea.ModCtrl},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := m.parseKeyToMessage(tt.input)

			if msg.String() != tt.expectedString {
				t.Errorf("parseKeyToMessage(%q).String() = %q, want %q",
					tt.input, msg.String(), tt.expectedString)
			}

			if msg.Mod != tt.expectedMod {
				t.Errorf("parseKeyToMessage(%q).Mod = %v, want %v",
					tt.input, msg.Mod, tt.expectedMod)
			}
		})
	}
}

// TestParseKeysToMessages tests parsing multiple keys
func TestParseKeysToMessages(t *testing.T) {
	m := &OS{Settings: config.Global}

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"single key", "a", []string{"a"}},
		{"space separated", "a b c", []string{"a", "b", "c"}},
		{"comma separated", "a,b,c", []string{"a", "b", "c"}},
		{"mixed separators", "a, b c", []string{"a", "b", "c"}},
		{"with modifiers", "ctrl+b q", []string{"ctrl+b", "q"}},
		{"special keys", "Enter Space Tab", []string{"enter", "space", "tab"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := m.parseKeysToMessages(tt.input)

			if len(msgs) != len(tt.expected) {
				t.Errorf("parseKeysToMessages(%q) returned %d messages, want %d",
					tt.input, len(msgs), len(tt.expected))
				return
			}

			for i, msg := range msgs {
				if msg.String() != tt.expected[i] {
					t.Errorf("parseKeysToMessages(%q)[%d].String() = %q, want %q",
						tt.input, i, msg.String(), tt.expected[i])
				}
			}
		})
	}
}

// TestApplyStateSyncSkipsInvalidWindows tests that windows with empty IDs are skipped
func TestApplyStateSyncSkipsInvalidWindows(t *testing.T) {
	m := &OS{
		Settings:         config.Global,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
	}

	// Sync with an invalid window (empty ID)
	state := &session.SessionState{
		Windows: []session.WindowState{
			{ID: "", PTYID: ""},         // Invalid: empty ID
			{ID: "valid-id", PTYID: ""}, // Invalid: empty PTYID
		},
	}

	err := m.ApplyStateSync(state)
	if err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	// Should have 0 windows, since both were invalid
	if len(m.Windows) != 0 {
		t.Errorf("Windows count = %d, want 0", len(m.Windows))
	}
}

// TestSplitWithoutTilingIsLoud pins that the BSP commands report tiling being
// off instead of returning nil. They used to do nothing at all, so a tape whose
// EnableTiling had not taken effect skipped every Split silently and then typed
// the next command into whatever pane was still focused, producing a layout that
// looked built and was not.
func TestSplitWithoutTilingIsLoud(t *testing.T) {
	m := focusedOS(t, "")
	m.AutoTiling = false

	for name, call := range map[string]func() error{
		"SplitVertical":   m.SplitVertical,
		"SplitHorizontal": m.SplitHorizontal,
		"RotateSplit":     m.RotateSplit,
		"EqualizeSplits":  m.EqualizeSplitsExec,
		"SmartSplit":      m.SmartSplitFocusedExec,
	} {
		if err := call(); err == nil {
			t.Errorf("%s with tiling off returned nil; it must say why it did nothing", name)
		}
	}
}

// TestScriptPaneReadyWaitsThenGivesUp covers the pane-readiness gate playback
// holds the next command on. A pane a tape asked for arrives asynchronously in a
// daemon session, and until it does the focused window is still the pane the
// tape split away from.
func TestScriptPaneReadyWaitsThenGivesUp(t *testing.T) {
	m := focusedOS(t, "")

	// Nothing pending: playback runs.
	if !m.scriptPaneReady() {
		t.Fatal("playback blocked with no pane pending")
	}

	// A pane was asked for and has not arrived: playback waits.
	m.awaitNewWindow(len(m.Windows))
	if m.scriptPaneReady() {
		t.Fatal("playback ran on while the pane it asked for did not exist")
	}

	// The wait is bounded, and running out of it is reported, not swallowed.
	m.ScriptAwaitDeadline = time.Now().Add(-time.Millisecond)
	if !m.scriptPaneReady() {
		t.Fatal("playback stayed blocked past the deadline")
	}
	if len(m.Notifications) != 1 {
		t.Fatalf("a pane that never arrived produced %d notifications, want 1", len(m.Notifications))
	}

	// The pane arriving clears the gate without a complaint.
	m.awaitNewWindow(len(m.Windows))
	m.Windows = append(m.Windows, m.Windows[0])
	if !m.scriptPaneReady() {
		t.Fatal("playback stayed blocked after the pane arrived")
	}
	if m.ScriptAwaitWindows != 0 {
		t.Error("the gate was not disarmed once the pane arrived")
	}
}
