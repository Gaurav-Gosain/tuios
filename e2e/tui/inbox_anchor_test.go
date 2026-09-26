package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestInboxThatFillsAfterOpeningIsCentred opens the Inbox with nothing in it,
// then a risky approval arrives and fills it with the list, the detail and a
// two-line footer. The panel keeps its first top while it fits, so it does not
// jump at every small change. It used to keep that top however much it grew,
// which left the filled Inbox on the bottom edge with half the screen empty
// above it. A panel whose centre moves more than a few rows is centred again.
// The frame is saved under artifactDir.
//
// How this could pass wrongly, written down first:
//   - The item could already be in the Inbox when it opens, and then the
//     first frame is the tall one and nothing is tested. The empty state is
//     waited for before the approval is made.
//   - The panel's edges are read from its title and its last footer line,
//     so the check fails if either is missing rather than measuring nothing.
//   - A panel that changes by a row or two keeps its top by design, so the
//     change has to be the whole list arriving: the empty panel is about ten
//     rows tall and the filled one about twenty-seven.
//
// Negative control: with overlayAnchorY returning the held top whenever the
// screen height is the same, the check fails with 13 rows above the panel and
// 1 below.
func TestInboxThatFillsAfterOpeningIsCentred(t *testing.T) {
	base, term := approvalsClient(t)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	waitText(t, term, "the empty Inbox", "Nothing is waiting for you.")

	startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-risk","tool_name":"Bash","tool_input":{"command":"rm -rf build/"}}`)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "Approvals 1", "Risky: recursive delete", "esc close")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never filled with the risky approval: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	s := term.Screen()
	saveArtifact(t, term, artifactDir(t), "inbox-filled")

	_, rows := s.Size()
	title, footer := -1, -1
	for r := range rows {
		line := s.Line(r)
		if title < 0 && strings.HasPrefix(strings.TrimSpace(line), "Inbox") {
			title = r
		}
		if strings.Contains(line, "esc close") {
			footer = r
		}
	}
	if title < 1 || footer < 0 {
		t.Fatalf("could not find the panel's title (%d) and footer (%d)\n%s", title, footer, term.Snapshot())
	}
	// One padding row above the title and one below the footer.
	above := title - 1
	below := rows - 1 - (footer + 1)
	if below < 1 || above-below > 2 || below-above > 2 {
		t.Fatalf("the filled Inbox is not centred: %d rows above it and %d below\n%s", above, below, term.Snapshot())
	}
}
