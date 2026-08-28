package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// releaseNative runs a key release through the real input coordinator against a
// focused local-PTY window whose emulator has been fed flagsSeq, and returns the
// bytes that reached the PTY.
func releaseNative(t *testing.T, flagsSeq string, mode app.Mode, msg tea.KeyReleaseMsg) string {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if flagsSeq != "" {
		_, _ = em.Write([]byte(flagsSeq))
	}
	pty := &capturePty{}
	win := &terminal.Window{ID: "release-native-001", Terminal: em, Pty: pty, X: 0, Y: 0, Width: 82, Height: 26}
	o := &app.OS{Settings: config.Global, Mode: mode, FocusedWindow: 0, Windows: []*terminal.Window{win}}
	HandleInput(msg, o)
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
		name  string
		flags string
		msg   tea.KeyReleaseMsg
		want  string
	}{
		{"letter", pushEventTypes, tea.KeyReleaseMsg{Code: 'a', Text: "a"}, "\x1b[97;1:3u"},
		{"ctrl+letter", pushEventTypes, tea.KeyReleaseMsg{Code: 'b', Mod: tea.ModCtrl}, "\x1b[98;5:3u"},
		{"enter", pushEventTypes, tea.KeyReleaseMsg{Code: tea.KeyEnter}, "\x1b[13;1:3u"},
		{"up arrow", pushEventTypes, tea.KeyReleaseMsg{Code: tea.KeyUp}, "\x1b[1;1:3A"},
		{"delete", pushEventTypes, tea.KeyReleaseMsg{Code: tea.KeyDelete}, "\x1b[3;1:3~"},
		// A pane that only asked for disambiguation gets no releases: the flag it
		// pushed says nothing about them, and sending them anyway would double
		// every keystroke it reads.
		{"disambiguate only", "\x1b[>1u", tea.KeyReleaseMsg{Code: 'a', Text: "a"}, ""},
		{"no kitty keyboard", "", tea.KeyReleaseMsg{Code: 'a', Text: "a"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseNative(t, tt.flags, app.TerminalMode, tt.msg); got != tt.want {
				t.Errorf("release to pane = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestKeyReleaseStaysOutOfWindowMode checks the gate. Window management owns the
// keyboard, so the press never reached the pane; delivering the release alone
// would report a key coming up that the pane was never told went down.
func TestKeyReleaseStaysOutOfWindowMode(t *testing.T) {
	got := releaseNative(t, pushEventTypes, app.WindowManagementMode, tea.KeyReleaseMsg{Code: 'a', Text: "a"})
	if got != "" {
		t.Errorf("release forwarded from window management mode = %q, want nothing", got)
	}
}

// TestForwardKeyReleaseDaemonPath checks the daemon transport observes the same
// bytes as the native one; the encoding decision is shared and must not drift.
func TestForwardKeyReleaseDaemonPath(t *testing.T) {
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	_, _ = em.Write([]byte(pushEventTypes))
	var got []byte
	win := &terminal.Window{
		ID: "release-daemon-001", Terminal: em, DaemonMode: true,
		DaemonWriteFunc: func(b []byte) error { got = append(got, b...); return nil },
		X:               0, Y: 0, Width: 82, Height: 26,
	}
	o := &app.OS{Settings: config.Global, Mode: app.TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}
	HandleInput(tea.KeyReleaseMsg{Code: 'a', Text: "a"}, o)
	if string(got) != "\x1b[97;1:3u" {
		t.Errorf("daemon release = %q, want %q", got, "\x1b[97;1:3u")
	}
}
