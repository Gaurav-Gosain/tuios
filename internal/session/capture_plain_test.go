package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// captureLineByLine is the plain capture the long way: every scrollback line
// decoded into cells and turned back into text, then the screen. The plain
// capture must give exactly these bytes whichever way it reads the ring.
func captureLineByLine(term vt.Terminal) string {
	var sb strings.Builder
	for i := range term.ScrollbackLen() {
		sb.WriteString(term.ScrollbackLine(i).String())
		sb.WriteByte('\n')
	}
	sb.WriteString(term.String())
	return sb.String()
}

// TestPlainCaptureMatchesLineByLine feeds a pane every kind of cell a program
// prints, lets a small ring wrap, and requires the plain capture, which reads
// the ring directly on the pure Go emulator, to match the line by line one.
func TestPlainCaptureMatchesLineByLine(t *testing.T) {
	inputs := []string{
		"plain line",
		"\x1b[31mred\x1b[0m and \x1b[1;44mbold on blue   \x1b[0m tail   ",
		"wide 日本語 text and 👍🏽 and 🇯🇵",
		"\x1b]8;;http://x.y\x1b\\link\x1b]8;;\x1b\\ after",
		"\x1b]8;;http://x.y\x1b\\   \x1b]8;;\x1b\\spaces in a link",
		"\x1b[44m          \x1b[0m",
		"",
		"   leading spaces\ttab",
		"\x1b[7m \x1b[0m",
		"combining é café",
		"wide at the edge 漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字漢字",
	}
	for _, width := range []int{7, 40, 80} {
		t.Run(fmt.Sprintf("w%d", width), func(t *testing.T) {
			term := vt.NewWithScrollback(width, 4, 30)
			for round := range 12 {
				for _, in := range inputs {
					_, _ = fmt.Fprintf(term, "%d %s\r\n", round, in)
				}
			}
			if term.ScrollbackLen() == 0 {
				t.Fatal("nothing reached the scrollback")
			}
			p := &PTY{terminal: term}
			want := captureLineByLine(term)
			if got := p.CaptureContent(true, false); got != want {
				t.Fatalf("plain capture differs from the line by line one:\n got %q\nwant %q", got, want)
			}
			got, _ := p.capturePlainAt(true)
			if got != want {
				t.Fatalf("capturePlainAt differs from the line by line one:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// TestWaitForOutputBackstopSeesUnannouncedChange changes a pane's content
// without publishing an output event, which is what a dropped event looks like
// to a waiter, and requires the backstop to notice. The second case changes the
// content through a resize, which does not advance the stream position, so a
// backstop keyed on output alone would never re-check.
func TestWaitForOutputBackstopSeesUnannouncedChange(t *testing.T) {
	cases := []struct {
		name   string
		change func(p *PTY)
	}{
		{"applied output", func(p *PTY) {
			_, _ = p.terminal.Write([]byte("\r\nBACKSTOPMARK\r\n"))
			p.vtSeq++
		}},
		{"resize", func(p *PTY) {
			_, _ = p.terminal.Write([]byte("\r\nBACKSTOPMARK\r\n"))
			p.terminal.Resize(p.terminal.Width()+1, p.terminal.Height())
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, sp := startTestDaemon(t)
			sess := makeSessionWithWindow(t, d, "work")
			pty, err := d.resolvePTYForTarget(sess, "")
			if err != nil {
				t.Fatalf("resolvePTYForTarget: %v", err)
			}

			c := dialVerb(t, sp)
			done := make(chan map[string]any, 1)
			go func() {
				done <- c.call(t, `{"id":1,"verb":"wait-for","params":{"condition":"window-output","session":"work","pattern":"BACKSTOPMARK","timeout":8000}}`)
			}()

			// Let the waiter take its first capture and settle on the
			// backstop before the content changes behind its back.
			time.Sleep(3 * waitOutputRecheck)
			pty.terminalMu.Lock()
			tc.change(pty)
			pty.terminalMu.Unlock()

			select {
			case resp := <-done:
				if res := result(t, resp); res["matched"] != true {
					t.Fatalf("wait result not matched: %v", res)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("the backstop never re-checked the changed pane")
			}
		})
	}
}
