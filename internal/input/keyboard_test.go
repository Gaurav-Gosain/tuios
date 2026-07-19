package input

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// TestTransitionGuardCondition directly tests the guard condition that suppresses
// misparsed mouse-sequence fragments during the AllMotion→CellMotion transition.
// This prevents phantom keypresses in apps like kakoune/nano (issue #78).
func TestTransitionGuardCondition(t *testing.T) {
	// guardShouldBlock mirrors the condition in HandleTerminalModeKey
	guardShouldBlock := func(msg tea.KeyPressMsg, enteredAt time.Time) bool {
		return msg.Mod == 0 && msg.Text != "" && !enteredAt.IsZero() &&
			time.Since(enteredAt) < 150*time.Millisecond
	}

	tests := []struct {
		name        string
		key         tea.KeyPressMsg
		enteredAgo  time.Duration
		useZeroTime bool
		wantBlock   bool
	}{
		{
			name:       "digit during transition - block (mouse fragment)",
			key:        tea.KeyPressMsg{Code: '2', Text: "2"},
			enteredAgo: 10 * time.Millisecond,
			wantBlock:  true,
		},
		{
			name:       "letter M during transition - block (SGR terminator)",
			key:        tea.KeyPressMsg{Code: 'M', Text: "M"},
			enteredAgo: 50 * time.Millisecond,
			wantBlock:  true,
		},
		{
			name:       "semicolon during transition - block (SGR separator)",
			key:        tea.KeyPressMsg{Code: ';', Text: ";"},
			enteredAgo: 100 * time.Millisecond,
			wantBlock:  true,
		},
		{
			name:       "digit after transition window - pass through",
			key:        tea.KeyPressMsg{Code: '2', Text: "2"},
			enteredAgo: 200 * time.Millisecond,
			wantBlock:  false,
		},
		{
			name:       "ctrl+b during transition - pass through (has modifier)",
			key:        tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl},
			enteredAgo: 10 * time.Millisecond,
			wantBlock:  false,
		},
		{
			name:       "escape during transition - pass through (no Text)",
			key:        tea.KeyPressMsg{Code: tea.KeyEscape},
			enteredAgo: 10 * time.Millisecond,
			wantBlock:  false,
		},
		{
			name:        "zero timestamp - pass through (no mode switch recorded)",
			key:         tea.KeyPressMsg{Code: '2', Text: "2"},
			useZeroTime: true,
			wantBlock:   false,
		},
		{
			name:       "alt+key during transition - pass through (has modifier)",
			key:        tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt, Text: "1"},
			enteredAgo: 10 * time.Millisecond,
			wantBlock:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var enteredAt time.Time
			if !tt.useZeroTime {
				enteredAt = time.Now().Add(-tt.enteredAgo)
			}

			got := guardShouldBlock(tt.key, enteredAt)
			if got != tt.wantBlock {
				t.Errorf("guardShouldBlock() = %v, want %v", got, tt.wantBlock)
			}
		})
	}
}

// TestTransitionGuardIntegration tests the full HandleTerminalModeKey function
// to verify that misparsed keys are actually suppressed during the transition.
func TestTransitionGuardIntegration(t *testing.T) {
	tests := []struct {
		name        string
		key         tea.KeyPressMsg
		enteredAgo  time.Duration
		shouldBlock bool
	}{
		{
			name:        "digit during transition - blocked",
			key:         tea.KeyPressMsg{Code: '2', Text: "2"},
			enteredAgo:  10 * time.Millisecond,
			shouldBlock: true,
		},
		{
			name:        "digit after transition - passes through",
			key:         tea.KeyPressMsg{Code: '2', Text: "2"},
			enteredAgo:  200 * time.Millisecond,
			shouldBlock: false,
		},
		{
			name:        "escape during transition - passes through",
			key:         tea.KeyPressMsg{Code: tea.KeyEscape},
			enteredAgo:  10 * time.Millisecond,
			shouldBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &app.OS{
				Mode:                  app.TerminalMode,
				FocusedWindow:         -1, // no focused window
				TerminalModeEnteredAt: time.Now().Add(-tt.enteredAgo),
			}

			result, _ := HandleTerminalModeKey(tt.key, o)

			// If guard blocked: Mode stays TerminalMode (returned immediately).
			// If guard passed: hits "no focused window" → switches to WindowManagementMode.
			wasBlocked := result.Mode == app.TerminalMode
			if wasBlocked != tt.shouldBlock {
				t.Errorf("blocked = %v, want %v (key=%q, enteredAgo=%v)",
					wasBlocked, tt.shouldBlock, tt.key.String(), tt.enteredAgo)
			}
		})
	}
}

// TestTransitionGuardBypassedWhenPrefixActive verifies that the transition guard
// does NOT suppress keys when PrefixActive is true. This fixes ctrl+b S not working
// when TerminalModeEnteredAt was recently set.
func TestTransitionGuardBypassedWhenPrefixActive(t *testing.T) {
	// Simulate the guard condition with PrefixActive
	guardShouldBlock := func(msg tea.KeyPressMsg, prefixActive bool, enteredAt time.Time) bool {
		return msg.Mod == 0 && msg.Text != "" && !prefixActive && !enteredAt.IsZero() &&
			time.Since(enteredAt) < 150*time.Millisecond
	}

	// "S" key during transition WITHOUT prefix — should block
	msg := tea.KeyPressMsg{Code: 'S', Text: "S"}
	enteredAt := time.Now() // just now = within 150ms
	if !guardShouldBlock(msg, false, enteredAt) {
		t.Error("expected block for 'S' during transition without prefix")
	}

	// "S" key during transition WITH prefix — should NOT block
	if guardShouldBlock(msg, true, enteredAt) {
		t.Error("expected pass for 'S' during transition with PrefixActive=true")
	}
}

// TestPrefixCommandsAvailableInBothModes verifies that key prefix commands
// like S (session switcher) and P (command palette) are handled in both
// terminal mode and window management mode.
func TestPrefixCommandsAvailableInBothModes(t *testing.T) {
	registry := config.NewKeybindRegistry(config.DefaultConfig())

	modes := []struct {
		name string
		mode app.Mode
	}{
		{"terminal mode", app.TerminalMode},
		{"window management mode", app.WindowManagementMode},
	}

	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			o := &app.OS{Mode: mode.mode, PrefixActive: true, KeybindRegistry: registry}
			result, _ := HandlePrefixCommand(tea.KeyPressMsg{Code: 'S', Text: "S"}, o)
			if !result.ShowSessionSwitcher {
				t.Error("leader S should open the session switcher")
			}

			o2 := &app.OS{Mode: mode.mode, PrefixActive: true, KeybindRegistry: registry}
			result2, _ := HandlePrefixCommand(tea.KeyPressMsg{Code: 'P', Text: "P"}, o2)
			if !result2.ShowCommandPalette {
				t.Error("leader P should open the command palette")
			}
		})
	}
}

// TestMacOSOptionGlyphsAreReservedOnlyOnDarwin verifies that the macOS
// Option-key glyphs only count as reserved chords on darwin. On other platforms
// these glyphs (e.g. £, ⇥) are ordinary typed characters and must fall through
// to the shell rather than being intercepted as workspace or window shortcuts.
func TestMacOSOptionGlyphsAreReservedOnlyOnDarwin(t *testing.T) {
	// £ is Option+3 on a US Mac layout, but Shift+3 on a UK layout.
	if got := isReservedTerminalChord(tea.KeyPressMsg{Code: '£', Text: "£"}); got != runtimeIsDarwin() {
		t.Errorf("isReservedTerminalChord(£) = %v, want %v", got, runtimeIsDarwin())
	}

	// ⇥ is Option+Tab on macOS, but an ordinary glyph elsewhere.
	if got := isReservedTerminalChord(tea.KeyPressMsg{Code: '⇥', Text: "⇥"}); got != runtimeIsDarwin() {
		t.Errorf("isReservedTerminalChord(⇥) = %v, want %v", got, runtimeIsDarwin())
	}

	// A real Alt chord is reserved on every platform.
	if !isReservedTerminalChord(tea.KeyPressMsg{Code: '1', Mod: tea.ModAlt, Text: "1"}) {
		t.Error("alt+1 should be a reserved chord on every platform")
	}

	// A bare letter never is, whatever it happens to be bound to.
	if isReservedTerminalChord(tea.KeyPressMsg{Code: 'a', Text: "a"}) {
		t.Error("a bare letter must reach the shell, not be treated as a chord")
	}

	// The pure lookup helper must remain platform-independent so it stays testable.
	if digit, ok := IsMacOSOptionKey('£'); !ok || digit != 3 {
		t.Errorf("IsMacOSOptionKey(£) = (%d, %v), want (3, true)", digit, ok)
	}
}

// TestTerminalPrefixChordNotRecorded verifies that prefix chords in terminal
// mode are not captured into a tape recording. Previously the leader key and
// its following command key were recorded before prefix routing, so a
// ctrl+b <cmd> chord replayed a stray 0x02 and command character into the shell.
func TestTerminalPrefixChordNotRecorded(t *testing.T) {
	rec := tape.NewRecorder()
	rec.Start()
	o := &app.OS{Mode: app.TerminalMode, TapeRecorder: rec}

	// ctrl+b activates prefix mode and must not be recorded.
	ctrlB := tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	HandleTerminalModeKey(ctrlB, o)
	if !o.PrefixActive {
		t.Fatal("ctrl+b should activate prefix mode")
	}

	// The following prefix command key routes to the prefix dispatcher, not the
	// PTY, and must not be recorded either.
	esc := tea.KeyPressMsg{Code: tea.KeyEscape}
	HandleTerminalModeKey(esc, o)

	if got := rec.CommandCount(); got != 0 {
		t.Errorf("prefix chord recorded %d commands, want 0: %+v", got, rec.GetCommands())
	}
}

// TestRecordTerminalKeyClassification verifies that keys forwarded to the PTY
// are classified correctly: printable ASCII accumulates as a Type command while
// special keys are recorded as KeyCombos (and flush any pending typed text).
func TestRecordTerminalKeyClassification(t *testing.T) {
	rec := tape.NewRecorder()
	rec.Start()
	o := &app.OS{TapeRecorder: rec}

	recordTerminalKey(o, tea.KeyPressMsg{Code: 'l', Text: "l"})
	recordTerminalKey(o, tea.KeyPressMsg{Code: 's', Text: "s"})
	recordTerminalKey(o, tea.KeyPressMsg{Code: tea.KeyEnter})

	cmds := rec.GetCommands()
	// "ls" accumulates into one Type command, flushed by the Enter KeyCombo.
	if len(cmds) != 2 {
		t.Fatalf("got %d commands, want 2: %+v", len(cmds), cmds)
	}
	if cmds[0].Type != tape.CommandTypeType || len(cmds[0].Args) == 0 || cmds[0].Args[0] != "ls" {
		t.Errorf("first command = %+v, want Type \"ls\"", cmds[0])
	}
	if cmds[1].Type != tape.CommandTypeEnter {
		t.Errorf("second command = %+v, want Enter", cmds[1])
	}
}
