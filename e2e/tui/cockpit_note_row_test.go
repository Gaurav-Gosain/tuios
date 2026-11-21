package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAgentsViewSnoozedNoteWithNoAgentListed snoozes one of two items in the
// agents view whose sessions have no agent in the list: plain shells set
// errored from the command line. The view draws "1 snoozed. S shows it."
// under Needs you beside the other item, a row that names no agent, and the
// client keeps running.
//
// How this could pass wrongly, written down first:
//   - The snooze could not happen at all, and a view with nothing new to draw
//     does not crash either. The note on screen is the snooze's mark, so the
//     wait asks for it beside the other item.
//   - The client could crash after the last check. The rows are rebuilt on
//     every key and tick; the crash this guards came in under a second, and
//     the two second hold covers the rebuilds that follow.
//   - A crash could be drawn as the crash overlay rather than an exit. Every
//     look asks for no "tuios hit a bug" as well as a running client.
//
// Negative controls (NEGATIVE_CONTROLS.md): on main, and with the note row's
// agent written out as 0, the client exits after z, 2 and the wait fails at
// once with the last frame.
func TestAgentsViewSnoozedNoteWithNoAgentListed(t *testing.T) {
	term, base := attachClientBase(t)
	for _, s := range []struct{ name, msg string }{
		{"e2e-note-a", "build failed on main"},
		{"e2e-note-b", "lint failed on main"},
	} {
		if out, err := tuiosCLI(t, base, "new", s.name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", s.name, err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "-s", s.name, "errored",
			"--harness", "claude-code", "-m", s.msg); err != nil {
			t.Fatalf("set-agent-state errored in %s: %v\n%s", s.name, err, out)
		}
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the agents view: %v", err)
	}
	waitText(t, term, "both items under Needs you", "2 needs you", "build failed on main", "lint failed on main")

	if err := term.SendKeys("z"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the snooze picker", "2 1h")
	if err := term.SendKeys("2"); err != nil {
		t.Fatal(err)
	}

	noteBesideItem := func() (bool, string) {
		if code, exited := term.ExitCode(); exited {
			t.Fatalf("tuios exited with code %d after the snooze\n%s", code, term.Snapshot())
		}
		text := term.Screen().Text()
		ok := strings.Contains(text, "1 snoozed. S shows it.") &&
			strings.Contains(text, "lint failed on main") &&
			!strings.Contains(text, "tuios hit a bug")
		return ok, term.Snapshot()
	}
	waitUntil(t, "the snoozed note beside the other item, the client running", uiTimeout, noteBesideItem)
	stayTrue(t, "the note and the other item stay, the client running", 2*time.Second, noteBesideItem)
	saveFrame(t, term, "cockpit-note-row")
	alive(t, term, "after snoozing an item beside another with no agent listed")
}
