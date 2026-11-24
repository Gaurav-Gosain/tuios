package tuie2e

import (
	"fmt"
	"testing"
)

// TestSettleProbeCountsEveryByte is the check on settled. A client attached to
// a daemon writes to its terminal before its writer exists: the line that
// starts the daemon, the nest probe and the capability probe. Each must be in
// the count a settle probe answers with, or settled could return with the end
// of a frame still unread. On an idle client the terminal has read exactly
// the bytes the client counted, no more.
func TestSettleProbeCountsEveryByte(t *testing.T) {
	requireReal(t, "counts the bytes a client process writes to its terminal")
	base := t.TempDir()
	term := startIn(t, base, startOpts{args: []string{"new", "settle-count"}})
	waitBoot(t, term)
	marks := settleMarksOf(term)
	if marks == "" {
		t.Fatal("the client was started without the settle_marks knob")
	}
	// Right after the attach the daemon's pushes still draw frames, so the
	// terminal can be ahead of an answer by the frames that came after it.
	// Once the client is quiet the two agree exactly. A write the count
	// misses keeps the terminal ahead for good, and the wait runs out.
	waitUntil(t, "the terminal has read exactly the bytes the client counted", bootTimeout, func() (bool, string) {
		a := settleProbe(t, term, marks)
		if !a.screen {
			t.Fatal("ASSERTION: a client with its own screen answered screen=false")
		}
		n, _ := term.Progress()
		return n == a.sent, fmt.Sprintf("the terminal read %d bytes and the client counted %d", n, a.sent)
	})
}
