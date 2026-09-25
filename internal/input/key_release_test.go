package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// releaseToPane runs a key release through the real input coordinator against
// a focused window whose emulator has been fed flagsSeq, and returns the bytes
// that reached the pane. daemon picks the DaemonWriteFunc transport over a
// local PTY; the encoding decision is shared and must not drift between them.
func releaseToPane(t *testing.T, flagsSeq string, mode app.Mode, daemon bool, msg tea.KeyReleaseMsg) string {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if flagsSeq != "" {
		_, _ = em.Write([]byte(flagsSeq))
	}
	var got []byte
	win := &terminal.Window{ID: "release-0001", Terminal: em, X: 0, Y: 0, Width: 82, Height: 26}
	pty := &capturePty{}
	if daemon {
		win.DaemonMode = true
		win.DaemonWriteFunc = func(b []byte) error { got = append(got, b...); return nil }
	} else {
		win.Pty = pty
	}
	o := &app.OS{Settings: config.Global, Mode: mode, FocusedWindow: 0, Windows: []*terminal.Window{win}}
	HandleInput(msg, o)
	if daemon {
		return string(got)
	}
	return string(pty.got)
}

// pushEventTypes is what a compositor in a pane pushes: disambiguation, event
// types and all-keys-as-escape-codes (CSI >11u). wlterm pushes exactly this.
const pushEventTypes = "\x1b[>11u"

// TestForwardKeyReleaseToPane pins the bytes a key release produces for a pane
// that asked for event types, and proves the panes that did not ask still see
// nothing. A press with no matching release is what left one Enter repeating
// forever inside a compositor running in a pane.
func TestForwardKeyReleaseToPane(t *testing.T) {
	tests := []struct {
		name   string
		flags  string
		mode   app.Mode
		daemon bool
		msg    tea.KeyReleaseMsg
		want   string
	}{
		{"letter", pushEventTypes, app.TerminalMode, false, tea.KeyReleaseMsg{Code: 'a', Text: "a"}, "\x1b[97;1:3u"},
		{"letter, daemon", pushEventTypes, app.TerminalMode, true, tea.KeyReleaseMsg{Code: 'a', Text: "a"}, "\x1b[97;1:3u"},
		{"ctrl+letter", pushEventTypes, app.TerminalMode, false, tea.KeyReleaseMsg{Code: 'b', Mod: tea.ModCtrl}, "\x1b[98;5:3u"},
		{"enter", pushEventTypes, app.TerminalMode, false, tea.KeyReleaseMsg{Code: tea.KeyEnter}, "\x1b[13;1:3u"},
		{"up arrow", pushEventTypes, app.TerminalMode, false, tea.KeyReleaseMsg{Code: tea.KeyUp}, "\x1b[1;1:3A"},
		{"delete", pushEventTypes, app.TerminalMode, false, tea.KeyReleaseMsg{Code: tea.KeyDelete}, "\x1b[3;1:3~"},
		// A pane that only asked for disambiguation gets no releases: the flag it
		// pushed says nothing about them, and sending them anyway would double
		// every keystroke it reads.
		{"disambiguate only", "\x1b[>1u", app.TerminalMode, false, tea.KeyReleaseMsg{Code: 'a', Text: "a"}, ""},
		{"no kitty keyboard", "", app.TerminalMode, false, tea.KeyReleaseMsg{Code: 'a', Text: "a"}, ""},
		// Window management owns the keyboard, so the press never reached the
		// pane; delivering the release alone would report a key coming up that
		// the pane was never told went down.
		{"window mode", pushEventTypes, app.WindowManagementMode, false, tea.KeyReleaseMsg{Code: 'a', Text: "a"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseToPane(t, tt.flags, tt.mode, tt.daemon, tt.msg); got != tt.want {
				t.Errorf("release to pane = %q, want %q", got, tt.want)
			}
		})
	}
}
