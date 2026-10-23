package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A FIFO splits a guest's synchronized update across real PTY reads and daemon
// packets. The old screen and hardware cursor must stay together until 2026l.
// This deliberately forces fragmentation; it does not claim Neovim naturally
// produces this boundary on every keypress.
func TestSynchronizedOutputHoldsHostCursor(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "e2e-cursor-sync", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	stream := &hostStream{}
	term := startIn(t, base, startOpts{cols: 232, rows: 53, args: []string{"attach", "e2e-cursor-sync"}, out: stream, animations: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}
	pipe := filepath.Join(base, "guest-output")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := term.SendKeys("exec cat '"+pipe+"'", tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	writer := openFifo(t, pipe)
	t.Cleanup(func() { _ = writer.Close() })
	write := func(s string) {
		t.Helper()
		if _, err := writer.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	vis := func(hidden bool) string {
		if hidden {
			return "\x1b[?25l"
		}
		return "\x1b[?25h"
	}
	var evidence strings.Builder
	defer func() {
		t.Log(evidence.String())
		if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
			if err := os.WriteFile(filepath.Join(dir, "cursor-sync.ansi"), stream.bytes(), 0o600); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "cursor-sync.txt"), []byte(evidence.String()), 0o600); err != nil {
				t.Error(err)
			}
		}
	}()
	for _, tc := range []struct {
		name                       string
		beforeHidden, duringHidden bool
	}{
		{"visible", false, false}, {"hide-during", false, true}, {"show-during", true, false},
	} {
		// Baseline and positive controls are ordinary, unsynchronized output.
		write("\x1b[2J\x1b[HSYNCCOMMITTED\x1b[3;5H\x1b[6 q" + vis(tc.beforeHidden))
		if err := term.WaitForText("SYNCCOMMITTED", shellTimeout); err != nil {
			t.Fatal(err)
		}
		x, y, found := textCell(term.Screen(), "SYNCCOMMITTED")
		if !found {
			t.Fatal("baseline marker missing")
		}
		waitHostCursor(t, term, x+4, y+2, !tc.beforeHidden, tc.name+" baseline")
		waitCursorShape(t, stream, shapeBar, tc.name+" baseline")
		stream.mark(tc.name + "-hold")
		written := time.Now()
		write("\x1b[?2026h\x1b[HSYNCPARTIAL!!\x1b[13;37H\x1b[2 q" + vis(tc.duringHidden))
		// get-window is answered by the attached client and reads its raw VT
		// cursor, independently of the cursor cache presented to the host.
		// Acknowledge that the partial stream reached that VT before sampling
		// the hold, so a delayed producer cannot make broken code pass.
		for {
			out, err := tuiosCLI(t, base, "get-window", "--session", "e2e-cursor-sync", "--json")
			if err != nil {
				t.Fatalf("read partial-stream cursor: %v: %s", err, out)
			}
			var guest struct {
				X       int  `json:"cursor_x"`
				Y       int  `json:"cursor_y"`
				Visible bool `json:"cursor_visible"`
			}
			if err := json.Unmarshal([]byte(out), &guest); err != nil {
				t.Fatalf("decode partial-stream cursor: %v: %s", err, out)
			}
			if time.Since(written) >= 400*time.Millisecond {
				t.Fatalf("partial-stream acknowledgement exceeded sync test budget: %s", time.Since(written))
			}
			if guest.X == 36 && guest.Y == 12 && guest.Visible != tc.duringHidden {
				fmt.Fprintf(&evidence, "%s: client VT acknowledged partial cursor=(36,12) visible=%v after %s\n", tc.name, guest.Visible, time.Since(written))
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		started := time.Now()
		// Several render opportunities, within VT's one-second sync deadline.
		// Do not wait for the wrong cursor: that would outlive the hold on a
		// correct build and accidentally test the timeout instead.
		leaked := false
		for time.Since(started) < 300*time.Millisecond {
			if text := term.Screen().Text(); !strings.Contains(text, "SYNCCOMMITTED") || strings.Contains(text, "SYNCPARTIAL!!") {
				t.Errorf("%s: content did not hold", tc.name)
				break
			}
			col, row, visible := term.Screen().Cursor()
			shape := hostCursorShape(stream.bytes())
			if col != x+4 || row != y+2 || visible == tc.beforeHidden || shape != shapeBar {
				t.Errorf("%s: content held but host cursor leaked at %s: (%d,%d) visible=%v shape=%s; want (%d,%d) visible=%v bar", tc.name, time.Since(started), col, row, visible, shape, x+4, y+2, !tc.beforeHidden)
				leaked = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if time.Since(written) >= 900*time.Millisecond {
			t.Fatal("sampling exceeded sync test budget; cannot claim the update was held")
		}
		col, row, visible := term.Screen().Cursor()
		fmt.Fprintf(&evidence, "%s: held for %s leaked=%v; host=(%d,%d) visible=%v shape=%v\n", tc.name, time.Since(started), leaked, col, row, visible, hostCursorShape(stream.bytes()))
		saveFrame(t, term, "cursor-sync-"+tc.name+"-held")
		stream.mark(tc.name + "-commit")
		write("\x1b[HSYNCCOMPLETED\x1b[6;9H\x1b[3 q" + vis(tc.duringHidden) + "\x1b[?2026l")
		if err := term.WaitForText("SYNCCOMPLETED", uiTimeout); err != nil {
			t.Fatal(err)
		}
		waitHostCursor(t, term, x+8, y+5, !tc.duringHidden, tc.name+" committed")
		waitCursorShape(t, stream, shapeUnderline, tc.name+" committed")
		write("\x1b[7;10H\x1b[6 q\x1b[?25h")
		waitHostCursor(t, term, x+9, y+6, true, tc.name+" ordinary cursor-only update")
		waitCursorShape(t, stream, shapeBar, tc.name+" ordinary cursor-only update")
		fmt.Fprintf(&evidence, "%s: committed=(%d,%d) visible=%v underline; ordinary=(%d,%d) visible=true bar\n", tc.name, x+8, y+5, !tc.duringHidden, x+9, y+6)
	}
}
