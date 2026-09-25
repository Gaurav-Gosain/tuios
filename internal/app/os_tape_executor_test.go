package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

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

// TestParseKeyToMessageSpaceModifier verifies a modified space keeps its
// modifier: Text must stay empty so String() does not drop Ctrl/Alt.
func TestParseKeyToMessageSpaceModifier(t *testing.T) {
	m := &OS{Settings: config.Global}

	msg := m.parseKeyToMessage("ctrl+space")

	if msg.Mod != tea.ModCtrl {
		t.Errorf("parseKeyToMessage(\"ctrl+space\").Mod = %v, want %v", msg.Mod, tea.ModCtrl)
	}

	if msg.Text != "" {
		t.Errorf("parseKeyToMessage(\"ctrl+space\").Text = %q, want empty string", msg.Text)
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
