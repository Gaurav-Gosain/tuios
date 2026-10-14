package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestToggleMouseTurnsHostReportingOffAndOn binds toggle_mouse and watches what
// tuios writes to its terminal. The client asks for any-event mouse reports
// (mode 1003) while it runs; toggle_mouse takes the request back, so the host
// selects text itself, and a second press asks again.
//
// The positive half is the first wait: 1003 is turned on at start, so the off
// sequence that follows is the toggle and not a mode that was never on.
//
// Negative control: delete the `if m.MouseOff {` block in View()
// (internal/app/render.go), or the d.Register("toggle_mouse", ...) call in
// internal/input/prefix_tree_actions.go. 1003 is never turned off and the
// first wait for it times out.
func TestToggleMouseTurnsHostReportingOffAndOn(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, `
[keybindings.workspaces]
toggle_mouse = ["alt+m"]
`)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 120, rows: 40, out: out})
	waitBoot(t, term)
	const on, off = "\x1b[?1003h", "\x1b[?1003l"
	if err := waitOutput(out, on, uiTimeout); err != nil {
		t.Fatalf("tuios never asked its terminal for mouse reports: %v", err)
	}

	mark := len(out.String())
	if err := term.SendKeys(tuitest.Alt('m')); err != nil {
		t.Fatalf("press alt+m: %v", err)
	}
	if err := waitOutputSince(out, mark, off, uiTimeout); err != nil {
		t.Fatalf("toggle_mouse never turned mouse reports off: %v\n%s", err, term.Snapshot())
	}

	mark = len(out.String())
	if err := term.SendKeys(tuitest.Alt('m')); err != nil {
		t.Fatalf("press alt+m again: %v", err)
	}
	if err := waitOutputSince(out, mark, on, uiTimeout); err != nil {
		t.Fatalf("the second toggle_mouse never turned mouse reports back on: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(out.String()[mark:], off) {
		t.Errorf("the second toggle wrote the off sequence again")
	}
}

// waitOutputSince waits for s in what the client wrote after the first from
// bytes of out.
func waitOutputSince(out *lockedBuffer, from int, s string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if all := out.String(); len(all) > from && strings.Contains(all[from:], s) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("%q not written within %v", s, timeout)
}
