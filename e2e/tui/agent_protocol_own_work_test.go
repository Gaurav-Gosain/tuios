package tuie2e

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestProtocolPaneReportsOwnWorkAfterATurn runs an ACP version 2 agent that
// starts work of its own after a turn, takes a prompt while that work runs,
// and starts work of its own again after the second turn. The turn takes the
// first stretch of own work in, so its end leaves nothing of it behind: the
// next stretch is reported again, the pane says "the agent is working on its
// own" a second time and list-agents reads working. The pane never says the
// agent stopped working on its own, during that turn or after it. The stand-in
// agent says running once more 900 ms into each stretch, and that repeat adds
// no line: whether the first stretch's repeat comes before the prompt, during
// the turn or after it, the pane ends with the line exactly twice.
//
// How this could pass wrongly, written down first:
//   - A count read before the second report could see the first line twice
//     through wrapping: the capture is unwrapped and the count is exact.
//   - The prompt could arrive after the agent's own work had stopped, which
//     is no prompt during own work at all: the first own-work line and the
//     working state are waited for before the prompt is sent, and the stand-in
//     agent never stops that work by itself.
//   - A slow machine could read the pane before the second turn ended: the
//     test waits for that turn's reply and for "turn finished" twice, and
//     every later read is a wait or a hold.
//
// Negative controls: NEGATIVE_CONTROLS.md, "An agent's own work after a turn".
func TestProtocolPaneReportsOwnWorkAfterATurn(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeACPVersion(t, base, 2)
	fake := buildFakeACP(t)
	const session = "e2e-own-after-turn"
	const ownLine = "the agent is working on its own"
	const stoppedLine = "the agent stopped working on its own"

	out, err := tuiosCLI(t, base, "start-agent", fake+" -versions=1,2", "--protocol", "acp", "-s", session, "--name", "helper", "--prompt", "later one")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	pane := func() string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", session, "-w", "helper", "--unwrap")
		return out
	}
	waitProtoPane(t, base, session, "helper", "ECHO: later one", ownLine)
	waitProtoState(t, base, session, "working")

	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "helper", "later two\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitUntil(t, "the second turn ended after its reply", uiTimeout, func() (bool, string) {
		p := pane()
		return strings.Contains(p, "ECHO: later two") && strings.Count(p, "turn finished") == 2, p
	})
	waitUntil(t, "the agent's own work after the second turn is reported", uiTimeout, func() (bool, string) {
		p, st := pane(), protoState(t, base, session)
		n := strings.Count(p, ownLine)
		return n == 2 && st == "working", fmt.Sprintf("%q %d times, list-agents reads %q\n%s", ownLine, n, st, p)
	})
	stayTrue(t, "the own-work line stays at two and no stopped line shows", 2*time.Second, func() (bool, string) {
		p := pane()
		n := strings.Count(p, ownLine)
		return n == 2 && !strings.Contains(p, stoppedLine), fmt.Sprintf("%q %d times\n%s", ownLine, n, p)
	})
}

// TestProtocolPaneReportsOwnWorkTwice runs an ACP version 2 agent that starts
// work of its own after a turn, stops it, and starts it again, with no turn of
// the person's between: the pane says "the agent is working on its own", then
// "the agent stopped working on its own", then "the agent is working on its
// own" again, and list-agents reads working at the end. The stand-in agent
// says running once more 900 ms into the second stretch, and that repeat adds
// no line.
//
// How this could pass wrongly, written down first:
//   - A count read before the second report could see the first line twice
//     through wrapping: the capture is unwrapped and every count is exact.
//   - The three lines could be there in another order: the stopped line's
//     place is compared with the first and the last own-work line.
//   - A slow machine could read the pane before the last report: every read is
//     a wait or a hold.
//
// Negative controls: NEGATIVE_CONTROLS.md, "An agent's own work after a turn".
func TestProtocolPaneReportsOwnWorkTwice(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeACPVersion(t, base, 2)
	fake := buildFakeACP(t)
	const session = "e2e-own-twice"
	const ownLine = "the agent is working on its own"
	const stoppedLine = "the agent stopped working on its own"

	out, err := tuiosCLI(t, base, "start-agent", fake+" -versions=1,2", "--protocol", "acp", "-s", session, "--name", "helper", "--prompt", "later chain")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	pane := func() string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", session, "-w", "helper", "--unwrap")
		return out
	}
	counts := func(p string) (own, stopped int) {
		return strings.Count(p, ownLine), strings.Count(p, stoppedLine)
	}
	waitUntil(t, "own work, its end and own work again are reported in order", uiTimeout, func() (bool, string) {
		p, st := pane(), protoState(t, base, session)
		own, stopped := counts(p)
		mid := strings.Index(p, stoppedLine)
		inOrder := mid > strings.Index(p, ownLine) && mid < strings.LastIndex(p, ownLine)
		return own == 2 && stopped == 1 && inOrder && st == "working",
			fmt.Sprintf("%q %d times, %q %d times, in order %v, list-agents reads %q\n%s", ownLine, own, stoppedLine, stopped, inOrder, st, p)
	})
	stayTrue(t, "the own-work line stays at two and the stopped line at one", 2*time.Second, func() (bool, string) {
		p := pane()
		own, stopped := counts(p)
		return own == 2 && stopped == 1, fmt.Sprintf("%q %d times, %q %d times\n%s", ownLine, own, stoppedLine, stopped, p)
	})
}
