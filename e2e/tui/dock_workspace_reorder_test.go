package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// dockPillRow finds the dock row carrying the workspace strip and returns the
// row plus its text, by looking for the named pills rather than by computing
// where the dock is: the strip's own geometry is the thing under test.
func dockPillRow(t *testing.T, term *tuitest.Terminal, names ...string) (row int, line string) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		text := s.Line(r)
		ok := true
		for _, n := range names {
			if !strings.Contains(text, n) {
				ok = false
				break
			}
		}
		if ok {
			return r, text
		}
	}
	t.Fatalf("no dock row carries every pill %v\n%s", names, term.Snapshot())
	return 0, ""
}

// pillColumn is the screen column a pill's label starts at.
//
// It counts runes rather than bytes. The dock strip is drawn out of powerline
// caps and Nerd Font glyphs, every one of them three bytes and one cell, so a
// byte offset used as a column lands most of a pill to the left of where it
// meant to and a drag aimed with one picks up a different pill than the one
// being read on screen. That mistake made this test report a working reorder as
// a broken one.
func pillColumn(line, needle string) int {
	b := strings.Index(line, needle)
	if b < 0 {
		return -1
	}
	return len([]rune(line[:b]))
}

// pillOrder is the left-to-right sequence the named pills appear in on one row.
func pillOrder(line string, names ...string) []string {
	type at struct {
		name string
		col  int
	}
	found := make([]at, 0, len(names))
	for _, n := range names {
		if c := pillColumn(line, n); c >= 0 {
			found = append(found, at{n, c})
		}
	}
	for i := 1; i < len(found); i++ {
		for j := i; j > 0 && found[j].col < found[j-1].col; j-- {
			found[j], found[j-1] = found[j-1], found[j]
		}
	}
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.name)
	}
	return out
}

// TestDockWorkspacePillsDragIntoANewOrder drives the reorder the way a user
// makes it: press a pill, drag it across its neighbour, release, and read the
// strip back off the screen.
//
// It is an on-screen test because the hit rectangles it depends on only exist as
// a side effect of the renderer drawing the strip, so nothing below the real
// frame can tell whether a pill was picked up at all. The second half is the
// half that matters: after the drag the workspace numbers are unchanged, so the
// keys that address a workspace by number still land where they always did.
func TestDockWorkspacePillsDragIntoANewOrder(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create work: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "work"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	// Three occupied workspaces, named so the pills can be told apart on screen.
	//
	// switchWorkspace rather than the keys on their own: it waits for the dock
	// to report the workspace it landed on. newWindow takes its baseline count
	// off that same dock, so reading it while the previous workspace's count was
	// still on screen left it waiting for a number the new workspace never
	// reaches.
	for _, ws := range []string{"2", "3"} {
		switchWorkspace(t, term, ws, 0)
		newWindow(t, term)
	}
	for _, wsName := range [][2]string{{"1", "EDIT"}, {"2", "REVW"}, {"3", "DPLY"}} {
		if out, err := tuiosCLI(t, base, "set-workspace-name", wsName[0], wsName[1], "--session", "work"); err != nil {
			t.Fatalf("name workspace %s: %v: %s", wsName[0], err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return screenHas(s, "EDIT", "REVW", "DPLY")
	}, uiTimeout); err != nil {
		t.Fatalf("the dock never showed all three named pills: %v\n%s", err, term.Snapshot())
	}

	row, line := dockPillRow(t, term, "EDIT", "REVW", "DPLY")
	if got := pillOrder(line, "EDIT", "REVW", "DPLY"); !equalStrings(got, []string{"EDIT", "REVW", "DPLY"}) {
		t.Fatalf("the strip started in order %v, want EDIT REVW DPLY\n%s", got, term.Snapshot())
	}

	// Drag the first pill onto the third. Every cell between is reported, which
	// is what turns the press into a drag rather than a click.
	from := pillColumn(line, "EDIT") + 1
	to := pillColumn(line, "DPLY") + 1
	mouseDrag(t, term, from, row, to, row, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for r := 0; r < len(s.Text()); r++ {
			if !strings.Contains(s.Line(r), "EDIT") {
				continue
			}
			got := pillOrder(s.Line(r), "EDIT", "REVW", "DPLY")
			if len(got) == 3 && got[2] == "EDIT" {
				return true
			}
		}
		return false
	}, uiTimeout); err != nil {
		_, after := dockPillRow(t, term, "EDIT", "REVW", "DPLY")
		t.Fatalf("the dragged pill did not land last, strip reads %v: %v\n%s",
			pillOrder(after, "EDIT", "REVW", "DPLY"), err, term.Snapshot())
	}

	// The arrangement is presentation. Every workspace keeps its number, so the
	// key that addresses workspace 1 still reaches the workspace called EDIT,
	// wherever the drag put its pill.
	out, err := tuiosCLI(t, base, "session-info", "--session", "work")
	if err != nil {
		t.Fatalf("session-info: %v: %s", err, out)
	}
	if !strings.Contains(out, "1=EDIT") {
		t.Errorf("workspace 1 stopped being the one named EDIT:\n%s", out)
	}
	if !strings.Contains(out, "order") {
		t.Errorf("the daemon never took the arrangement:\n%s", out)
	}

	// And the daemon still answers to the number, which is the assertion that
	// fails if a reorder had renumbered anything.
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "1"); err != nil {
		t.Fatalf("switch to workspace 1: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 1 did not come back with its one pane: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after dragging a workspace pill")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
