package tuie2e

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

func numberedSessionRow(screen, name string) bool {
	for line := range strings.SplitSeq(screen, "\n") {
		rail := line
		if before, _, ok := strings.Cut(line, "│"); ok {
			rail = before
		}
		if !strings.Contains(rail, name) {
			continue
		}
		for d := 1; d <= 9; d++ {
			if strings.Contains(rail, strconv.Itoa(d)+" "+name) {
				return true
			}
		}
	}
	return false
}

// With show_numbers on, the rail leads each session row with its switch
// number, on every client that draws the rail. This walks the rail through a
// detach and a reattach: the numbers must come back with the rail, not only
// on the client that created the sessions.
func TestRailSwitchNumbersSurviveReattach(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nshow_numbers = true\nwidth = 30\n")

	if out, err := tuiosCLI(t, base, "new", "e2e-alpha", "--detach"); err != nil {
		t.Fatalf("create first session: %v: %s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("first client never attached: %v\n%s", err, term.Snapshot())
	}
	if out, err := tuiosCLI(t, base, "new", "e2e-beta", "--detach"); err != nil {
		t.Fatalf("create second session: %v: %s", err, out)
	}

	expectNumber := func(term *tuitest.Terminal, name, what string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			return numberedSessionRow(s.Text(), name)
		}, uiTimeout); err != nil {
			t.Fatalf("%s: the rail never led %s with a switch number: %v\n%s", what, name, err, term.Snapshot())
		}
	}

	expectNumber(term, "e2e-alpha", "on the first client")
	expectNumber(term, "e2e-beta", "on the first client")

	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach: %v", err)
	}
	waitExit(t, term, "after the detach")
	if !sessionListed(t, base, "e2e-alpha") {
		out, _ := tuiosCLI(t, base, "ls")
		t.Fatalf("the session did not survive the detach\nls:\n%s", out)
	}

	second := startIn(t, base, startOpts{args: []string{"attach", "e2e-alpha"}})
	if err := second.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the reattached client never came up: %v\n%s", err, second.Snapshot())
	}
	expectNumber(second, "e2e-alpha", "after the reattach")
	expectNumber(second, "e2e-beta", "after the reattach")
}
