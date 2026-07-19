package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestScrolledOutputRendersCorrectly runs a command whose output scrolls the
// pane many screens' worth and asserts the pane is coherent afterwards.
//
// Scrolling is the path where the emulator rewrites every line in the cell
// buffer, which is the same buffer the renderer walks and the resize path
// reallocates. Several of this session's blank-pane bugs surfaced as a pane
// that had scrolled and then rendered as empty cells or as a stale cached
// frame, so the assertion is not merely "tuios survived": it is that the tail
// of the scrolled output is on screen, that the shell prompt came back, and
// that the pane still updates afterwards.
func TestScrolledOutputRendersCorrectly(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	// Enough lines to scroll the pane many times over. Each line is tagged so
	// the tail can be identified exactly rather than by counting.
	const last = 400
	runInShell(t, term, fmt.Sprintf("for i in $(seq 1 %d); do echo \"LINE-$i-END\"; done", last),
		fmt.Sprintf("LINE-%d-END", last), 60*time.Second)

	// The final lines must be the ones on screen: a pane that scrolled but
	// rendered a stale cache would be showing earlier lines instead.
	s := term.Screen()
	for _, n := range []int{last, last - 1, last - 2} {
		want := fmt.Sprintf("LINE-%d-END", n)
		if !strings.Contains(s.Text(), want) {
			t.Fatalf("scrolled pane is missing %q; it is not showing the tail of the output\n%s",
				want, term.Snapshot())
		}
	}
	// And early lines must have scrolled off, proving the viewport really moved
	// rather than the whole output happening to fit.
	if strings.Contains(s.Text(), "LINE-1-END") {
		t.Fatalf("LINE-1-END is still on screen after %d lines: the pane did not scroll, "+
			"so this test is not exercising the scroll path\n%s", last, term.Snapshot())
	}

	// The pane must still be live after the scroll, not frozen on a final frame.
	runInShell(t, term, "echo POST-SCROLL-$((6*7))", "POST-SCROLL-42", 20*time.Second)

	// And it must survive losing and regaining focus, which is when a stale or
	// discarded cached layer would show itself.
	leaveTerminalMode(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "after second window")
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("tab: %v", err)
	}
	if err := term.WaitForText("POST-SCROLL-42", uiTimeout); err != nil {
		t.Fatalf("scrolled pane lost its content across a focus switch: %v\n%s",
			err, term.Snapshot())
	}
	alive(t, term, "after scrolled output")
}

// TestScrollbackModeShowsEarlierOutput drives the scrollback viewer over content
// that has scrolled off, which is the read path that walks the scrollback ring
// rather than the live screen.
func TestScrollbackModeShowsEarlierOutput(t *testing.T) {
	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	const last = 300
	runInShell(t, term, fmt.Sprintf("for i in $(seq 1 %d); do echo \"SB-$i-END\"; done", last),
		fmt.Sprintf("SB-%d-END", last), 60*time.Second)
	leaveTerminalMode(t, term)

	// Ctrl+B [ enters copy mode over the scrollback. Motions are vim's, so the
	// jump to the oldest line is "gg" rather than a single "g"; a single "g"
	// only arms the pending-g state and silently does nothing.
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("enter copy mode: %v", err)
	}
	if err := term.WaitForText("COPY MODE", uiTimeout); err != nil {
		t.Fatalf("copy mode never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("g", "g"); err != nil {
		t.Fatalf("jump to oldest: %v", err)
	}
	if err := term.WaitForText("SB-1-END", uiTimeout); err != nil {
		t.Fatalf("scrollback did not reach the oldest line: %v\n%s", err, term.Snapshot())
	}

	// G returns to the newest line.
	if err := term.SendKeys("G"); err != nil {
		t.Fatalf("jump to newest: %v", err)
	}
	if err := term.WaitForText(fmt.Sprintf("SB-%d-END", last), uiTimeout); err != nil {
		t.Fatalf("scrollback did not return to the newest line: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after scrollback navigation")
}
