package tuie2e

import (
	"regexp"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// foldedHeads are the group headings the test folds, with the count each
// must keep while folded.
var foldedHeads = []struct {
	title string
	re    *regexp.Regexp
}{
	{"Working", regexp.MustCompile(`Working\s+2\b`)},
	{"Idle", regexp.MustCompile(`Idle\s+2\b`)},
}

// TestCockpitFoldedGroupsKeepTheirHeadings folds every group of the agents
// view, then closes and opens it again. Each folded group keeps its heading
// and its count, and the view never says "No agents yet." while it has
// agents.
//
// How it could fail, written down first:
//   - With every group folded the list holds headings and no agent row, and
//     a check for an empty list that counts agent rows only draws the empty
//     state over the headings. The person saw "No agents yet." under a header
//     that said 2 working, with "F open group" in the key bar.
//   - A folded group could drop its heading, or its count, and leave nothing
//     to open it again with.
//   - The folds are kept in the client's state file and read back when the
//     view opens again, so the second opening reads them from the file.
//
// How it could pass wrongly: a heading could still be on screen from the
// rail. The rail has no group headings, and the check reads the count beside
// the title, which only the view draws.
//
// Negative control: on main, after the last fold the view shows
// "No agents yet." and the heading checks fail.
func TestCockpitFoldedGroupsKeepTheirHeadings(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	type agent struct{ session, name, state string }
	agents := []agent{
		{"w1", "build-a", "working"}, {"w2", "build-b", "working"},
		{"i1", "docs-a", "idle"}, {"i2", "docs-b", "idle"},
	}
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create session home: %v\n%s", err, out)
	}
	for _, a := range agents {
		if out, err := tuiosCLI(t, base, "new", a.session, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", a.session, err, out)
		}
		for _, args := range [][]string{
			{"set-window", "-s", a.session, "-w", "0", "--name", a.name},
			{"set-agent-state", a.state, "-s", a.session, "-w", a.name, "--harness", "claude-code"},
		} {
			if out, err := tuiosCLI(t, base, args...); err != nil {
				t.Fatalf("%v: %v\n%s", args, err, out)
			}
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 44, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	open := func(why string) {
		t.Helper()
		sendKeys(t, term, tuitest.Ctrl('b'), "i")
		waitText(t, term, why, "Working", "Idle")
	}
	open("the agents view")

	// Fold each group from its first row: F on a row folds its group and
	// leaves the cursor on the heading, and j goes on to the next one.
	for _, h := range foldedHeads {
		row, col := findText(t, term, h.title)
		mouseClick(t, term, col, row+1, tuitest.MouseLeft, 0)
		time.Sleep(insertGuard)
		sendKeys(t, term, "F")
		time.Sleep(insertGuard)
	}
	check := func(when string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			text := s.Text()
			for _, h := range foldedHeads {
				if !h.re.MatchString(text) {
					return false
				}
			}
			// The focus column lists the folded group's agents under "The
			// group is folded.", which says the fold took.
			return regexp.MustCompile(`The group is folded\.`).MatchString(text)
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s, every group is not folded to its heading and count (want %q): %v\n%s", when, []string{"Working 2", "Idle 2"}, err, term.Snapshot())
		}
		if waitAbsent(term, "No agents yet.") != nil {
			t.Fatalf("ASSERTION: %s, the view says No agents yet. with four agents\n%s", when, term.Snapshot())
		}
	}
	check("with every group folded")
	railShot(t, term, "folded")

	sendKeys(t, term, tuitest.Esc)
	time.Sleep(insertGuard)
	open("the agents view again")
	check("opened again with the folds from the state file")
	railShot(t, term, "folded-again")
}

// waitAbsent is nil once text is not on screen for a moment, an error when it
// stays.
func waitAbsent(term *tuitest.Terminal, text string) error {
	return term.WaitFor(func(s tuitest.Screen) bool {
		return !regexp.MustCompile(regexp.QuoteMeta(text)).MatchString(s.Text())
	}, 2*time.Second)
}
