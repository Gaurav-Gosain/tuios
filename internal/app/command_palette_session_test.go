package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// sessionPaletteTestOS builds an OS with two windows and no daemon client, the
// same standalone shape session_tree_test.go exercises, ready for the palette
// builder and its actions.
func sessionPaletteTestOS(t *testing.T) (*OS, *terminal.Window, *terminal.Window) {
	t.Helper()
	first := &terminal.Window{ID: "w1", CustomName: "first"}
	second := &terminal.Window{ID: "w2", CustomName: "second"}
	return &OS{
		Settings:       config.Global,
		Windows:        []*terminal.Window{first, second},
		FocusedWindow:  0,
		WorkspaceFocus: map[int]int{},
		NumWorkspaces:  9,
	}, first, second
}

// TestSessionPaletteItemSelectCurrentSessionIsNoOp checks that selecting the
// palette entry for the session the client is already on shows an
// informational notice rather than calling SwitchToSession, which would error
// out immediately in standalone mode (no daemon client).
func TestSessionPaletteItemSelectCurrentSessionIsNoOp(t *testing.T) {
	m, _, _ := sessionPaletteTestOS(t)

	items := getSessionPaletteItems(m)
	items[0].Action(m)
	if len(m.Notifications) != 1 {
		t.Fatalf("Notifications = %d, want 1", len(m.Notifications))
	}
	if !strings.Contains(m.Notifications[0].Message, "Already on this session") {
		t.Errorf("notification = %q, want it to mention already being on this session", m.Notifications[0].Message)
	}
}
