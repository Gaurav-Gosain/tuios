package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// leaderTwice presses the leader twice in terminal mode against a focused
// local-PTY window whose emulator was fed flagsSeq, and returns the bytes that
// reached the PTY.
func leaderTwice(t *testing.T, leader, flagsSeq string, msg tea.KeyPressMsg) string {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if flagsSeq != "" {
		_, _ = em.Write([]byte(flagsSeq))
	}
	pty := &capturePty{}
	win := &terminal.Window{ID: "leader-twice-0001", Terminal: em, Pty: pty, X: 0, Y: 0, Width: 82, Height: 26}
	settings := config.Global
	settings.LeaderKey = leader
	o := &app.OS{Settings: settings, Mode: app.TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}
	HandleTerminalModeKey(msg, o)
	if !o.PrefixActive {
		t.Fatalf("the first %s press did not start the prefix", leader)
	}
	if len(pty.got) != 0 {
		t.Fatalf("the first leader press reached the pane: %q", pty.got)
	}
	HandleTerminalModeKey(msg, o)
	if o.PrefixActive {
		t.Fatalf("the second %s press left the prefix active", leader)
	}
	return string(pty.got)
}

// TestLeaderTwiceSendsTheLeader is issue #213: pressing the leader twice sent
// Ctrl+B to the pane whatever the leader was. The pane must get the leader
// itself, encoded for its keyboard mode.
func TestLeaderTwiceSendsTheLeader(t *testing.T) {
	tests := []struct {
		leader string
		msg    tea.KeyPressMsg
		legacy string
		kitty  string
	}{
		{"ctrl+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, "\x02", "\x1b[98;5u"},
		{"ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, "\x01", "\x1b[97;5u"},
		{"alt+f12", tea.KeyPressMsg{Code: tea.KeyF12, Mod: tea.ModAlt}, "\x1b[24;3~", "\x1b[24;3~"},
	}
	for _, tt := range tests {
		t.Run(tt.leader, func(t *testing.T) {
			if got := leaderTwice(t, tt.leader, "", tt.msg); got != tt.legacy {
				t.Errorf("legacy pane: got %q, want %q", got, tt.legacy)
			}
			if got := leaderTwice(t, tt.leader, pushAll, tt.msg); got != tt.kitty {
				t.Errorf("kitty pane: got %q, want %q", got, tt.kitty)
			}
		})
	}
}
