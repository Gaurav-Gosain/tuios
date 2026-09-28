package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// hintsFixture is one focused pane in terminal mode whose input is captured,
// with body printed in it, and hints mode ready to open.
type hintsFixture struct {
	o    *app.OS
	win  *terminal.Window
	sent *[]byte
}

func newHintsFixture(t *testing.T, body, alphabet string) hintsFixture {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	// The pane asks for key releases, so a release that is not dropped
	// reaches it.
	_, _ = em.Write([]byte(pushEventTypes + body))
	sent := &[]byte{}
	win := &terminal.Window{ID: "hints-0001", Terminal: em, X: 0, Y: 0, Width: 82, Height: 26}
	win.DaemonMode = true
	win.DaemonWriteFunc = func(b []byte) error { *sent = append(*sent, b...); return nil }
	cfg := config.DefaultConfig()
	if alphabet != "" {
		cfg.Hints.Alphabet = alphabet
	}
	o := &app.OS{Settings: config.Global, Mode: app.TerminalMode, FocusedWindow: 0,
		Windows: []*terminal.Window{win}, UserConfig: cfg}
	return hintsFixture{o: o, win: win, sent: sent}
}

func (f hintsFixture) open(t *testing.T) {
	t.Helper()
	*f.sent = (*f.sent)[:0]
	f.o.OpenHints()
	if !f.o.HintsOpen() {
		t.Fatal("hints did not open")
	}
}

// Nothing typed while the labels are up may reach the pane: not a paste, not
// an input method's commit, not the release of a key pressed before.
func TestHintsKeepInputFromThePane(t *testing.T) {
	f := newHintsFixture(t, "commit deadbee42\r\n", "")

	// A key pressed before hints opened, released while they are open.
	HandleInput(tea.KeyPressMsg{Code: 'x', Text: "x"}, f.o)
	f.open(t)

	HandleInput(tea.KeyReleaseMsg{Code: 'x', Text: "x"}, f.o)
	HandleInput(tea.PasteMsg{Content: "PASTED"}, f.o)
	HandleInput(tea.KeyPressMsg{Code: 'a', Text: "as"}, f.o) // an input method's commit
	HandleInput(tea.MouseMotionMsg{X: 5, Y: 5}, f.o)

	if got := string(*f.sent); got != "" {
		t.Errorf("input reached the pane while hints were open: %q", got)
	}
	if !f.o.HintsOpen() {
		t.Fatal("hints closed on input they should have dropped")
	}
	if typed := f.o.HintsTyped(); typed != "" {
		t.Errorf("a two-character commit was read as label letters: %q", typed)
	}
}

// A wheel scrolls the pane out from under the labels, so it closes hints.
func TestHintsCloseOnWheel(t *testing.T) {
	f := newHintsFixture(t, "commit deadbee42\r\n", "")
	f.open(t)
	HandleInput(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelUp}, f.o)
	if f.o.HintsOpen() {
		t.Error("hints stayed open after a wheel")
	}
}

// The leader closes hints even when it is Ctrl and a label letter, and never
// opens a match.
func TestHintsLeaderCloses(t *testing.T) {
	f := newHintsFixture(t, "commit deadbee42\r\n", "bs")
	f.open(t)
	if l := f.o.HintLabels()["deadbee42"]; l != "b" {
		t.Fatalf("the fixture needs the hash on label b, got %q", l)
	}
	HandleInput(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, f.o)
	if f.o.HintsOpen() {
		t.Fatal("the leader did not close hints")
	}
	for _, n := range f.o.Notifications {
		if strings.Contains(n.Message, "Copied") {
			t.Errorf("the leader acted on a label: %q", n.Message)
		}
	}
}

// Ctrl+C and Ctrl+G close unless c or g is a label letter; then they are Ctrl
// and a label like any other.
func TestHintsCtrlCAndGFollowTheAlphabet(t *testing.T) {
	t.Run("g is a label letter", func(t *testing.T) {
		f := newHintsFixture(t, "commit deadbee42\r\n", "gs")
		f.open(t)
		HandleInput(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, f.o)
		if f.o.HintsOpen() {
			t.Fatal("ctrl+g did not complete the label g")
		}
		last := f.o.Notifications[len(f.o.Notifications)-1].Message
		if !strings.Contains(last, "opens only links and paths") {
			t.Errorf("ctrl+g closed instead of acting on the label; last message %q", last)
		}
	})
	t.Run("c is not a label letter", func(t *testing.T) {
		f := newHintsFixture(t, "commit deadbee42\r\n", "")
		f.open(t)
		HandleInput(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, f.o)
		if f.o.HintsOpen() {
			t.Fatal("ctrl+c did not close hints")
		}
		for _, n := range f.o.Notifications {
			if strings.Contains(n.Message, "Copied") {
				t.Errorf("ctrl+c acted on a label: %q", n.Message)
			}
		}
	})
}

// A letter Caps Lock made upper case is a plain label letter, not Shift and a
// label, when the terminal reports Caps Lock. Shift still types the match.
func TestHintsCapsLockIsNotShift(t *testing.T) {
	f := newHintsFixture(t, "commit deadbee42\r\n", "")
	f.open(t)
	HandleInput(tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModCapsLock}, f.o)
	if f.o.HintsOpen() {
		t.Fatal("the label was not taken")
	}
	if got := string(*f.sent); got != "" {
		t.Errorf("Caps Lock typed the match into the pane: %q", got)
	}

	// The positive half: Shift types it.
	f.open(t)
	HandleInput(tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift}, f.o)
	if got := string(*f.sent); !strings.Contains(got, "deadbee42") {
		t.Errorf("Shift and the label did not type the match: %q", got)
	}
}
