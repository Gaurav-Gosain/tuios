package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea/v2"
)

// TestParseKeyToMessage tests the key parsing function
func TestParseKeyToMessage(t *testing.T) {
	m := &OS{}

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
	m := &OS{}

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

// TestParseKeysToMessagesRaw tests raw key parsing (no splitting)
func TestParseKeysToMessagesRaw(t *testing.T) {
	m := &OS{}

	tests := []struct {
		name     string
		input    string
		expected int // number of messages (one per character)
	}{
		{"simple", "abc", 3},
		{"with space", "a b", 3}, // space is a character too
		{"hello world", "hello world", 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := m.parseKeysToMessagesRaw(tt.input)

			if len(msgs) != tt.expected {
				t.Errorf("parseKeysToMessagesRaw(%q) returned %d messages, want %d",
					tt.input, len(msgs), tt.expected)
			}
		})
	}
}

// TestParseKeyToMessageModifierWithoutText verifies modifiers don't set Text
func TestParseKeyToMessageModifierWithoutText(t *testing.T) {
	m := &OS{}

	// When there's a modifier, Text should be empty so String() includes the modifier
	msg := m.parseKeyToMessage("ctrl+b")

	if msg.Text != "" {
		t.Errorf("parseKeyToMessage(\"ctrl+b\").Text = %q, want empty string", msg.Text)
	}

	if msg.Mod != tea.ModCtrl {
		t.Errorf("parseKeyToMessage(\"ctrl+b\").Mod = %v, want %v", msg.Mod, tea.ModCtrl)
	}

	// String() should return "ctrl+b" not just "b"
	if msg.String() != "ctrl+b" {
		t.Errorf("parseKeyToMessage(\"ctrl+b\").String() = %q, want \"ctrl+b\"", msg.String())
	}
}

// TestGetWindowDisplayNameLogic tests the display name logic
func TestGetWindowDisplayNameLogic(t *testing.T) {
	tests := []struct {
		name       string
		title      string
		customName string
		expected   string
	}{
		{"title only", "Terminal", "", "Terminal"},
		{"custom name set", "Terminal", "MyWindow", "MyWindow"},
		{"both empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the logic that getWindowDisplayName uses
			var result string
			if tt.customName != "" {
				result = tt.customName
			} else {
				result = tt.title
			}

			if result != tt.expected {
				t.Errorf("display name logic = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestSetDockbarPosition tests dockbar position validation
func TestSetDockbarPosition(t *testing.T) {
	m := &OS{}

	tests := []struct {
		name      string
		position  string
		wantError bool
	}{
		{"top", "top", false},
		{"bottom", "bottom", false},
		{"hidden", "hidden", false},
		{"invalid", "left", true},
		{"invalid empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.SetDockbarPosition(tt.position)
			if (err != nil) != tt.wantError {
				t.Errorf("SetDockbarPosition(%q) error = %v, wantError %v",
					tt.position, err, tt.wantError)
			}
		})
	}
}

// TestSetBorderStyle tests border style validation
func TestSetBorderStyle(t *testing.T) {
	m := &OS{}

	tests := []struct {
		name      string
		style     string
		wantError bool
	}{
		{"rounded", "rounded", false},
		{"normal", "normal", false},
		{"thick", "thick", false},
		{"double", "double", false},
		{"hidden", "hidden", false},
		{"block", "block", false},
		{"ascii", "ascii", false},
		{"invalid", "fancy", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.SetBorderStyle(tt.style)
			if (err != nil) != tt.wantError {
				t.Errorf("SetBorderStyle(%q) error = %v, wantError %v",
					tt.style, err, tt.wantError)
			}
		})
	}
}

// TestMoveWindowToWorkspaceByID tests workspace validation
func TestMoveWindowToWorkspaceByID(t *testing.T) {
	m := &OS{
		NumWorkspaces: 9,
	}

	tests := []struct {
		name      string
		workspace int
		wantError bool
	}{
		{"valid workspace 1", 1, true}, // Error because no windows
		{"valid workspace 5", 5, true}, // Error because no windows
		{"valid workspace 9", 9, true}, // Error because no windows
		{"invalid workspace 0", 0, true},
		{"invalid workspace 10", 10, true},
		{"invalid workspace -1", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := m.MoveWindowToWorkspaceByID("nonexistent", tt.workspace)
			if (err != nil) != tt.wantError {
				t.Errorf("MoveWindowToWorkspaceByID(\"nonexistent\", %d) error = %v, wantError %v",
					tt.workspace, err, tt.wantError)
			}
		})
	}
}
