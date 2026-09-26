package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railRowMarked reports whether the rail's sessions section shows session
// with the mark of an agent that needs the person. The row is read inside the
// rail's band, right of its divider, so the dock's own "need you in <name>"
// cannot satisfy it.
func railRowMarked(s tuitest.Screen, session string) bool {
	_, rows := s.Size()
	for y := range rows {
		line := s.Line(y)
		at := strings.LastIndex(line, "│")
		if at < 0 {
			continue
		}
		if strings.Contains(line[at:], "▲ "+session) {
			return true
		}
	}
	return false
}

// TestRailMarksAForeignAgentWhenTheDockDoes: an agent in another session asks
// for the person. The dock says so the moment the attention event lands, and
// the rail's row for that session has to say so too, not on the listing poll
// three seconds later. Three sessions are asked one after another, each timed
// on its own, so a poll that happens to land inside one window cannot pass
// all three. The last frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The dock text names the session, so the mark is read only right of the
//     rail's divider.
//   - A session with no agent row at all could be matched by name, so the
//     row has to carry the needs-you mark in front of the name.
//   - One lucky poll could land inside a single window, so there are three
//     windows, a second apart, and every one has to pass.
//
// Negative control: with foreignAgentRefreshCmd returning nil, the marks
// arrive with the 3 second poll and at least two of the three waits fail.
func TestRailMarksAForeignAgentWhenTheDockDoes(t *testing.T) {
	const within = 1200 * time.Millisecond
	names := []string{"e2e-ask-a", "e2e-ask-b", "e2e-ask-c"}
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, name := range append([]string{"e2e-home"}, names...) {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", name, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-home"}, shippedLooks: true})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1 && strings.Contains(s.Text(), "e2e-ask-c")
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached with the rail listing every session: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)

	for _, name := range names {
		time.Sleep(time.Second)
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", name, "needs_input",
			"--kind", "question", "--harness", "claude-code", "-m", "which branch?"); err != nil {
			t.Fatalf("set-agent-state in %s: %v\n%s", name, err, out)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return railRowMarked(s, name) }, within); err != nil {
			t.Fatalf("the rail did not mark %s within %v of its agent asking: %v\n%s", name, within, err, term.Snapshot())
		}
	}
	saveArtifact(t, term, artifactDir(t), "rail-foreign-marks")
	alive(t, term, "after three foreign agents asked")
}
