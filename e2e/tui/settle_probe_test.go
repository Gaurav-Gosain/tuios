package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestSettleProbeCountsEveryByte is the check on settled. A client attached to
// a daemon writes to its terminal before its writer exists: the line that
// starts the daemon, the nest probe and the capability probe. Each must be in
// the count a settle probe answers with, or settled could return with the end
// of a frame still unread. So must a host sequence, which goes out apart from
// the frames: the pointer shape a press on a title bar sets. On an idle client
// the terminal has read exactly the bytes the client counted, no more.
func TestSettleProbeCountsEveryByte(t *testing.T) {
	requireReal(t, "counts the bytes a client process writes to its terminal")
	base := t.TempDir()
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{args: []string{"new", "settle-count"}, out: out})
	waitBoot(t, term)
	marks := settleMarksOf(term)
	if marks == "" {
		t.Fatal("the client was started without the settle_marks knob")
	}
	// Right after the attach the daemon's pushes still draw frames, so the
	// terminal can be ahead of an answer by the frames that came after it.
	// Once the client is quiet the two agree exactly. A write the count
	// misses keeps the terminal ahead for good, and the wait runs out.
	agree := func(when string) {
		t.Helper()
		waitUntil(t, "the terminal has read exactly the bytes the client counted, "+when, bootTimeout, func() (bool, string) {
			a := settleProbe(t, term, marks)
			if !a.screen {
				t.Fatal("ASSERTION: a client with its own screen answered screen=false")
			}
			n, _ := term.Progress()
			return n == a.sent, fmt.Sprintf("the terminal read %d bytes and the client counted %d", n, a.sent)
		})
	}
	agree("after the boot")

	// A host sequence goes out apart from the frames: a press on a title
	// bar sets the pointer to grabbing (OSC 22), and the release sets it
	// back. Those bytes must be in the count too.
	newWindow(t, term)
	box := settledScreenBoxes(t, term, 1)[0]
	mouseClick(t, term, box.X+box.Width/2, box.Y, tuitest.MouseLeft, 0)
	waitUntil(t, "the client set the pointer shape on the host", uiTimeout, func() (bool, string) {
		return strings.Contains(out.String(), "\x1b]22;grabbing"), "no OSC 22 grabbing in the client's output"
	})
	agree("after a pointer shape")
}
