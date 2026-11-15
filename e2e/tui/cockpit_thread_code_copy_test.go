package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitThreadCodeBlockCopy copies code blocks in the agents view's
// thread, at 120 columns: a 300-column Go block by a click on its header,
// then a 2000-line block, which the daemon cuts, with C.
//
// The ways it can go wrong, each an assertion here:
//   - the header has no copy hit, or the click does nothing;
//   - the copy is the drawn rows (the long line cut, the fold's 12 lines)
//     and not the code;
//   - C copies a block other than the lowest one on screen;
//   - the status line gives no count, or does not say the block was cut
//     when the agent's message was longer than the thread keeps (the
//     daemon cuts an entry at 8 KiB).
//
// The artifacts are the frames code-copy-click and code-copy-key.
func TestCockpitThreadCodeBlockCopy(t *testing.T) {
	base, term := openCodeThread(t, codeBlocksAnswer)
	buffer := func(before string) string {
		t.Helper()
		got := before
		waitUntil(t, "the copy to reach the paste buffer", uiTimeout, func() (bool, string) {
			got = mustCLIOr(t, base, "show-buffer")
			return got != before, got
		})
		return got
	}

	// 1. A click on copy in the Go block's header copies the code.
	s := term.Screen()
	head := rowWith(s, "package words") - 1
	col := strings.Index(s.Line(head), "copy  go")
	if col < 0 {
		saveCockpitFrames(t, term, "code-copy-no-hit")
		t.Fatalf("ASSERTION: the Go block's header has no copy: %q\n%s", s.Line(head), term.Snapshot())
	}
	col = len([]rune(s.Line(head)[:col]))
	before := mustCLIOr(t, base, "show-buffer")
	for _, a := range []tuitest.MouseAction{tuitest.MousePress, tuitest.MouseRelease} {
		if err := term.SendMouse(tuitest.MouseEvent{Col: col + 1, Row: head, Button: tuitest.MouseLeft, Action: a}); err != nil {
			t.Fatal(err)
		}
	}
	want := "package words\n\n" + codeWrapLine
	if got := buffer(before); got != want {
		saveCockpitFrames(t, term, "code-copy-click-missing")
		t.Fatalf("ASSERTION: the click copied\n%q\nwant\n%q\n%s", got, want, term.Snapshot())
	}
	waitText(t, term, "the copy count", "Copied 3 lines.")
	saveCockpitFrames(t, term, "code-copy-click")

	// 2. C copies the lowest block, the 2000-line one, as far as the thread
	// holds it, and says that the rest is not in the thread.
	before = mustCLIOr(t, base, "show-buffer")
	if err := term.SendKeys("C"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the cut copy count", "Copied ", "only part of this block")
	shown := term.Screen().Text()
	saveCockpitFrames(t, term, "code-copy-key")
	got := buffer(before)
	n := strings.Count(got, "\n") + 1
	if !strings.HasPrefix(codeLongBlock, got) || !strings.HasPrefix(got, "line 0001\nline 0002\n") || n <= 12 {
		saveCockpitFrames(t, term, "code-copy-key-missing")
		t.Fatalf("ASSERTION: C copied %d lines, not the start of the long block past its fold:\n%.200q\n%s", n, got, term.Snapshot())
	}
	if !strings.Contains(shown, "Copied "+itoa(n)+" lines.") {
		t.Fatalf("ASSERTION: the status line does not say %d lines were copied\n%s", n, term.Snapshot())
	}
	alive(t, term, "after the code copy")
}
