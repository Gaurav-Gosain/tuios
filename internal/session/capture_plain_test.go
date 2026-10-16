package session

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"tuios.dev/vt"
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

// TestUnwrapCaptureJoinsWrappedRows writes lines longer than the pane, some
// with a space or a wide character at the wrap edge, and requires an unwrapped
// capture to give back each line as it was written. A line that fills the row
// exactly and then ends must stay a line of its own. Runs on whichever backend
// the build uses, so the ghostty workflow covers that backend too.
func TestUnwrapCaptureJoinsWrappedRows(t *testing.T) {
	const width = 10
	lines := []string{
		"short",
		"exactly10!",
		"a path/that/is/longer/than/the/pane/width.go:42",
		"edge-at-9 wraps here", // a space lands in the last column
		"wide 日本語日本語日本語 end",   // a wide character meets the edge
		"\x1b[31mred text that wraps around\x1b[0m",
		"last",
	}
	term := vt.NewWithScrollback(width, 4, 100)
	for _, l := range lines {
		_, _ = fmt.Fprintf(term, "%s\r\n", l)
	}
	p := &PTY{terminal: term}

	got, _ := p.CaptureContentMeta(true, false, true)
	got = trimCapture(got)
	var want strings.Builder
	for _, l := range lines {
		want.WriteString(ansi.Strip(l) + "\n")
	}
	if got != want.String() {
		t.Fatalf("unwrapped capture:\n got %q\nwant %q", got, want.String())
	}

	// Styled, the joined line keeps its escapes and still reads as one line.
	styled, _ := p.CaptureContentMeta(true, true, true)
	if n := strings.Count(trimCapture(styled), "\n"); n != len(lines) {
		t.Fatalf("styled unwrapped capture has %d lines, want %d:\n%q", n, len(lines), styled)
	}

	// Without unwrap the rows stay as the pane shows them.
	plain, _ := p.CaptureContentMeta(true, false, false)
	if strings.Contains(plain, "longer/than/the/pane") {
		t.Fatalf("a capture without unwrap joined rows: %q", plain)
	}
}

// TestTrimCaptureDropsTheBlankRowsBelow keeps every line with content and the
// blank lines between them, and drops only the blank lines at the end.
func TestTrimCaptureDropsTheBlankRowsBelow(t *testing.T) {
	for in, want := range map[string]string{
		"a\n\nb\n\n   \n\x1b[0m\n": "a\n\nb\n",
		"a":                        "a\n",
		"\n\n":                     "",
		"":                         "",
	} {
		if got := trimCapture(in); got != want {
			t.Errorf("trimCapture(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUnwrapStopsAtTheAlternateScreen opens the alternate screen over a line
// that wraps from the history onto the main screen. The history's newest row
// carries on to the main screen, so a capture of the alternate screen must not
// join it to the alternate screen's first row. last-command-output reads the
// main screen, so it must not join rows by the alternate screen's flags. The
// emulator has no wrap flags for the main screen while the alternate one is
// up, so those rows come back as the pane shows them.
func TestUnwrapStopsAtTheAlternateScreen(t *testing.T) {
	const alt = "\x1b[?1049h\x1b[H"
	term := vt.NewWithScrollback(10, 3, 100)
	_, _ = fmt.Fprint(term, "l0\r\nl1\r\n0123456789ABCDEFGHIJabcdefghij-END"+alt+strings.Repeat("q", 15))
	p := &PTY{terminal: term}
	got, _ := p.CaptureContentMeta(true, false, true)
	if want := "l0\nl1\n0123456789\n" + strings.Repeat("q", 15) + "\n"; got != want {
		t.Fatalf("unwrapped capture of the alternate screen:\n got %q\nwant %q", got, want)
	}

	term = vt.NewWithScrollback(10, 4, 100)
	_, _ = fmt.Fprint(term, "\x1b]133;A\x07$ \x1b]133;B\x07x\r\n\x1b]133;C\x07"+
		"AAAAAAAAAABBBBB\r\nshort\r\n\x1b]133;D;0\x07\x1b]133;A\x07$ "+alt+strings.Repeat("q", 30))
	p = &PTY{terminal: term}
	if out, _, ok := p.LastCommandOutput(true); !ok || out != "AAAAAAAAAA\nBBBBB\nshort" {
		t.Fatalf("last command output = %q, want %q", out, "AAAAAAAAAA\nBBBBB\nshort")
	}
}
