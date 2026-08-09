package input

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// capturePty is a fake xpty.Pty that records what SendInput writes on the
// native (non-daemon) path, where SendInput writes straight to the PTY.
type capturePty struct{ got []byte }

func (p *capturePty) Write(b []byte) (int, error) { p.got = append(p.got, b...); return len(b), nil }
func (p *capturePty) Read([]byte) (int, error)    { return 0, nil }
func (p *capturePty) Close() error                { return nil }
func (p *capturePty) Fd() uintptr                 { return 0 }
func (p *capturePty) Resize(_, _ int) error       { return nil }
func (p *capturePty) Size() (int, int, error)     { return 80, 24, nil }
func (p *capturePty) Name() string                { return "capture-pty" }
func (p *capturePty) Start(_ *exec.Cmd) error     { return nil }

// forwardNative runs a keypress through the real terminal-mode input handler
// against a focused, local-PTY window whose emulator has been fed flagsSeq, and
// returns the bytes that reached the PTY.
func forwardNative(t *testing.T, flagsSeq string, msg tea.KeyPressMsg) string {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if flagsSeq != "" {
		_, _ = em.Write([]byte(flagsSeq))
	}
	pty := &capturePty{}
	win := &terminal.Window{ID: "kitty-native-0001", Terminal: em, Pty: pty, X: 0, Y: 0, Width: 82, Height: 26}
	o := &app.OS{Mode: app.TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}
	HandleTerminalModeKey(msg, o)
	return string(pty.got)
}

// forwardDaemon is forwardNative for the daemon path, where SendInput hands the
// bytes to DaemonWriteFunc instead of a PTY. The encoding decision is shared, so
// both transports must observe identical bytes.
func forwardDaemon(t *testing.T, flagsSeq string, msg tea.KeyPressMsg) string {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	if flagsSeq != "" {
		_, _ = em.Write([]byte(flagsSeq))
	}
	var got []byte
	win := &terminal.Window{
		ID: "kitty-daemon-0001", Terminal: em, DaemonMode: true,
		DaemonWriteFunc: func(b []byte) error { got = append(got, b...); return nil },
		X:               0, Y: 0, Width: 82, Height: 26,
	}
	o := &app.OS{Mode: app.TerminalMode, FocusedWindow: 0, Windows: []*terminal.Window{win}}
	HandleTerminalModeKey(msg, o)
	return string(got)
}

// pushAll is the flag set awrit pushes at startup (CSI >31u): every kitty
// keyboard enhancement, including report-associated-keys. terminal-browser
// pushes the same minus alternate-keys (CSI >27u) once a text field is focused.
const pushAll = "\x1b[>31u"

// TestForwardKittyKeyboardPane pins the bytes a keypress produces for a pane
// that has enabled the kitty keyboard protocol, versus a plain pane that has
// not. The kitty column is exactly what terminal-browser and awrit parse back
// into the typed key; the legacy column is the untouched control-byte encoding
// a normal shell expects. It asserts the native and daemon transports agree.
func TestForwardKittyKeyboardPane(t *testing.T) {
	tests := []struct {
		name   string
		msg    tea.KeyPressMsg
		kitty  string // expected bytes to a pane with CSI >31u active
		legacy string // expected bytes to a pane with no kitty keyboard flags
	}{
		{"plain letter", tea.KeyPressMsg{Code: 'a', Text: "a"}, "\x1b[97;1;97u", "a"},
		{"capital letter", tea.KeyPressMsg{Code: 'x', ShiftedCode: 'X', Text: "X", Mod: tea.ModShift}, "\x1b[120;2;88u", "X"},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "\x1b[13u", "\r"},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, "\x1b[127u", "\x7f"},
		{"up arrow", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A", "\x1b[A"},
		{"ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, "\x1b[97;5u", "\x01"},
		{"bare escape", tea.KeyPressMsg{Code: tea.KeyEscape}, "\x1b[27u", "\x1b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := forwardNative(t, pushAll, tt.msg); got != tt.kitty {
				t.Errorf("native kitty pane: got %q, want %q", got, tt.kitty)
			}
			if got := forwardDaemon(t, pushAll, tt.msg); got != tt.kitty {
				t.Errorf("daemon kitty pane: got %q, want %q", got, tt.kitty)
			}
			if got := forwardNative(t, "", tt.msg); got != tt.legacy {
				t.Errorf("native legacy pane: got %q, want %q", got, tt.legacy)
			}
			if got := forwardDaemon(t, "", tt.msg); got != tt.legacy {
				t.Errorf("daemon legacy pane: got %q, want %q", got, tt.legacy)
			}
		})
	}
}
