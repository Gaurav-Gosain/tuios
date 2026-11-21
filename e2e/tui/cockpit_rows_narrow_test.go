package tuie2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

func screenLineWith(s tuitest.Screen, markers ...string) string {
	for line := range strings.SplitSeq(s.Text(), "\n") {
		ok := true
		for _, m := range markers {
			ok = ok && strings.Contains(line, m)
		}
		if ok {
			return line
		}
	}
	return ""
}

// TestCockpitRowsKeepWhatTheyDoAt80Columns opens the agents view at 80x24 on
// a fleet too big for two lines a row, with more Working agents than the list
// shows. The row that needs the person keeps its second line: what it asks.
// A Working row, on one line, says what it does after its name, in place of
// its harness and session. j past the last Working row opens the rows "+N
// more working" stands for.
//
// Negative control: on main the Needs you row is one line, "api-auth claude
// api", with no "Allow Bash"; a Working row says "cache-2 claude cache" and
// not what it edits; and j stops at the last row shown.
func TestCockpitRowsKeepWhatTheyDoAt80Columns(t *testing.T) {
	term, base, _, _ := startCockpitFleetWith(t, 120, 40, nil, "")
	if err := term.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("extra-%d", i)
		if out, err := tuiosCLI(t, base, "new-window", name, "-s", "misc", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
			t.Fatalf("open %s: %v\n%s", name, err, out)
		}
		if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "misc", "-w", name, "--harness", "codex", "-m", fmt.Sprintf("Edit internal/extra/step%d.go", i)); err != nil {
			t.Fatalf("report %s: %v\n%s", name, err, out)
		}
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Working  12")
	}, uiTimeout); err != nil {
		t.Fatalf("the list never showed the twelve Working agents: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(uiTimeout)
	saveFrame(t, term, "rows-80x24")
	dir := artifactDir(t)
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "rows-80x24")

	s := term.Screen()
	if screenLineWith(s, "Allow Bash", "go test") == "" {
		t.Errorf("ASSERTION: the Needs you row lost the line that says what it asks:\n%s", term.Snapshot())
	}
	if screenLineWith(s, "cache-2", "Edit") == "" || screenLineWith(s, "cache-1", "Bash") == "" {
		t.Errorf("ASSERTION: a Working row does not say what it does on its line:\n%s", term.Snapshot())
	}

	opened := false
	for range 30 {
		sendKeys(t, term, "j")
		_ = term.WaitStable(uiTimeout)
		if strings.Contains(term.Screen().Text(), "extra-6") {
			opened = true
			break
		}
	}
	if !opened {
		t.Fatalf("ASSERTION: j never opened the rows +N more working stands for:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "rows-80x24-opened")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "rows-80x24-opened")
	alive(t, term, "after walking the list at 80x24")
}
