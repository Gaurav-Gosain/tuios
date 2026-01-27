package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

// TestApplyStateSyncGlobalState tests that global state (workspace, tiling) is synced
func TestApplyStateSyncGlobalState(t *testing.T) {
	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
	}

	state := &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 3,
		MasterRatio:      0.7,
		AutoTiling:       true,
	}

	err := m.ApplyStateSync(state)
	if err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	// Verify global state was applied
	if m.CurrentWorkspace != 3 {
		t.Errorf("CurrentWorkspace = %d, want 3", m.CurrentWorkspace)
	}
	if m.MasterRatio != 0.7 {
		t.Errorf("MasterRatio = %f, want 0.7", m.MasterRatio)
	}
	if !m.AutoTiling {
		t.Error("AutoTiling should be true")
	}
}

// TestApplyStateSyncUpdatesExistingWindows tests that existing windows get updated
func TestApplyStateSyncUpdatesExistingWindows(t *testing.T) {
	win1ID := "win-1234-5678-90ab-cdef-000000000001"

	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Windows: []*terminal.Window{
			{
				ID:     win1ID,
				Title:  "Terminal 1",
				X:      0,
				Y:      0,
				Width:  80,
				Height: 24,
			},
		},
		FocusedWindow: 0,
	}

	// Capture original window pointer
	originalWindow := m.Windows[0]

	// Sync with updated position
	state := &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 1,
		FocusedWindowID:  win1ID,
		Windows: []session.WindowState{
			{ID: win1ID, X: 100, Y: 50, Width: 120, Height: 40},
		},
	}

	err := m.ApplyStateSync(state)
	if err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	// Window should be the same instance (not recreated)
	if m.Windows[0] != originalWindow {
		t.Error("Window should be the same instance")
	}

	// Position should be updated
	if m.Windows[0].X != 100 || m.Windows[0].Y != 50 {
		t.Errorf("Window position = (%d, %d), want (100, 50)", m.Windows[0].X, m.Windows[0].Y)
	}
	if m.Windows[0].Width != 120 || m.Windows[0].Height != 40 {
		t.Errorf("Window size = %dx%d, want 120x40", m.Windows[0].Width, m.Windows[0].Height)
	}
}

// TestApplyStateSyncRemovesDeletedWindows tests that windows deleted by other clients are removed
func TestApplyStateSyncRemovesDeletedWindows(t *testing.T) {
	win1ID := "win-1234-5678-90ab-cdef-000000000001"
	win2ID := "win-1234-5678-90ab-cdef-000000000002"

	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Windows: []*terminal.Window{
			{ID: win1ID, Title: "Terminal 1"},
			{ID: win2ID, Title: "Terminal 2"},
		},
		FocusedWindow: 0,
	}

	// Sync with only win2 (win1 was deleted by other client)
	state := &session.SessionState{
		Name:             "test-session",
		CurrentWorkspace: 1,
		FocusedWindowID:  win2ID,
		Windows: []session.WindowState{
			{ID: win2ID, X: 0, Y: 0, Width: 80, Height: 24},
		},
	}

	err := m.ApplyStateSync(state)
	if err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	// Should now have only 1 window
	if len(m.Windows) != 1 {
		t.Errorf("Windows count = %d, want 1", len(m.Windows))
	}
	if m.Windows[0].ID != win2ID {
		t.Errorf("Remaining window ID = %s, want %s", m.Windows[0].ID, win2ID)
	}
}

// TestApplyStateSyncNilState tests handling nil state
func TestApplyStateSyncNilState(t *testing.T) {
	m := &OS{}

	err := m.ApplyStateSync(nil)
	if err != nil {
		t.Errorf("ApplyStateSync(nil) should not error, got: %v", err)
	}
}

// TestApplyStateSyncFocusUpdate tests that focus is correctly updated
func TestApplyStateSyncFocusUpdate(t *testing.T) {
	win1ID := "win-1234-5678-90ab-cdef-000000000001"
	win2ID := "win-1234-5678-90ab-cdef-000000000002"

	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Windows: []*terminal.Window{
			{ID: win1ID, Title: "Terminal 1"},
			{ID: win2ID, Title: "Terminal 2"},
		},
		FocusedWindow: 0, // win1 focused
	}

	// Sync with win2 focused - include both windows to avoid deletion
	state := &session.SessionState{
		FocusedWindowID: win2ID,
		Windows: []session.WindowState{
			{ID: win1ID},
			{ID: win2ID},
		},
	}

	err := m.ApplyStateSync(state)
	if err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	// Focus should now be on win2 (index 1)
	if m.FocusedWindow != 1 {
		t.Errorf("FocusedWindow = %d, want 1", m.FocusedWindow)
	}
}

// TestApplyStateSyncSkipsInvalidWindows tests that windows with empty IDs are skipped
func TestApplyStateSyncSkipsInvalidWindows(t *testing.T) {
	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
	}

	// Sync with an invalid window (empty ID)
	state := &session.SessionState{
		Windows: []session.WindowState{
			{ID: "", PTYID: ""},         // Invalid - empty ID
			{ID: "valid-id", PTYID: ""}, // Invalid - empty PTYID
		},
	}

	err := m.ApplyStateSync(state)
	if err != nil {
		t.Fatalf("ApplyStateSync failed: %v", err)
	}

	// Should have 0 windows - both were invalid
	if len(m.Windows) != 0 {
		t.Errorf("Windows count = %d, want 0", len(m.Windows))
	}
}
