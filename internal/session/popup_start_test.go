package session

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A popup's command starts at the size a client will place it at: the box
// resolved against the session less its agreed reserve, less the border.
func TestPopupContentSize(t *testing.T) {
	r := LayoutReserve{Top: 2, Right: 20}
	cols, rows := popupContentSize(120, 40, r, "80%", "80%")
	// Region 100x38, box 80x30, content 78x28.
	if cols != 78 || rows != 28 {
		t.Fatalf("content = %dx%d, want 78x28", cols, rows)
	}
	cols, rows = popupContentSize(120, 40, LayoutReserve{}, "", "")
	// Defaults 80% and 60% of 120x40: box 96x24, content 94x22.
	if cols != 94 || rows != 22 {
		t.Fatalf("default content = %dx%d, want 94x22", cols, rows)
	}
}

// The daemon refuses a frame edit the host cannot make, from the pane's own
// emulator, so the refusal is in order with its DA1 answer.
func TestKittyAnimationRefusal(t *testing.T) {
	frame := &vt.KittyCommand{Action: vt.KittyActionFrame, ImageID: 3}
	got := string(kittyAnimationRefusal(frame, false, false))
	if !strings.Contains(got, "i=3") || !strings.Contains(got, "ENOTSUPPORTED") {
		t.Fatalf("refusal = %q", got)
	}
	if kittyAnimationRefusal(frame, false, true) != nil {
		t.Fatal("refused a frame edit the host makes")
	}
	if kittyAnimationRefusal(frame, true, false) != nil {
		t.Fatal("answered for a pane on another machine")
	}
	if kittyAnimationRefusal(&vt.KittyCommand{Action: vt.KittyActionFrame, Quiet: 2}, false, false) != nil {
		t.Fatal("answered a command that asked for no answer")
	}
	if kittyAnimationRefusal(&vt.KittyCommand{Action: vt.KittyActionTransmit}, false, false) != nil {
		t.Fatal("refused a command that is not a frame edit")
	}
}

// A popup's process starts at the box a client will place it at, and its
// window carries that box, so nothing resizes it back to the session's.
func TestPopupStartsAtItsBox(t *testing.T) {
	m := NewManager()
	sess, err := m.CreateSession("pop", &SessionConfig{}, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Stop)
	sess.SettleLayout(120, 40, LayoutReserve{Top: 2})
	win, err := sess.AddDaemonWindowWith(NewWindowOptions{
		Command: []string{"sleep", "30"}, Popup: true, PopupWidth: "50%", PopupHeight: "40%",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Region 120x38: box 60x15, content 58x13.
	pty := sess.GetPTY(win.PTYID)
	if pty == nil {
		t.Fatal("no PTY for the popup")
	}
	if cols, rows := pty.Size(); cols != 58 || rows != 13 {
		t.Errorf("the popup's PTY started at %dx%d, want 58x13", cols, rows)
	}
	if win.Width != 60 || win.Height != 15 {
		t.Errorf("the popup's window box is %dx%d, want 60x15", win.Width, win.Height)
	}
}
