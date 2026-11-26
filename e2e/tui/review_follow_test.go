package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewFollowsTheAgent opens the review while an agent works, and the
// agent writes a hunk far down the file: the cursor goes to it. A wheel turn
// stops following, and the status line says so. The next hunk is counted in
// the header instead, a click on the count goes to it, and a click on the
// footer's f follows again. No key is pressed after the open.
//
// How it could pass wrongly, written down first:
//   - The hunk could be on screen anyway. It is 140 lines below the first,
//     past the bottom of the pane, and the cursor must be on its header.
//   - A read could still move the view after the wheel. The hunk written
//     then must not take the cursor: the line under it stays the same.
func TestReviewFollowsTheAgent(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	file := filepath.Join(repo, "main.go")
	lines := map[int]string{}
	write := func() {
		t.Helper()
		var b strings.Builder
		for i := 1; i <= 200; i++ {
			if s, ok := lines[i]; ok {
				b.WriteString(s)
			} else {
				fmt.Fprintf(&b, "line %d", i)
			}
			b.WriteByte('\n')
		}
		if err := os.WriteFile(file, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	testutil.Git(t, repo, "add", "main.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "main")
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	// The person's change, before the agent's turn starts.
	lines[10] = "line 10 FIRST"
	write()
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "agent", "--harness", "claude-code"); err != nil {
		t.Fatalf("set-agent-state: %v: %s", err, out)
	}

	const rows = 40
	term := attachIn(t, base, "agent", startOpts{cols: 140, rows: rows})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	// No key from here on.
	waitScreen(t, term, "the review never opened following the agent", "FIRST", "following claude-code")
	art := artifactDir(t)
	frame := func(name string) {
		t.Helper()
		saveArtifact(t, term, art, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), art, name)
	}
	until := func(what string, ok func(tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(ok, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: %v\n%s", what, err, term.Snapshot())
		}
	}
	cursorHeaderAbove := func(s tuitest.Screen, text string) bool {
		row, _, ok := textAt(s, text, 0)
		for y := row - 1; ok && y >= 0 && y > row-6; y-- {
			if l := s.Line(y); strings.Contains(l, "@@") {
				return strings.Contains(l, "›") && strings.Contains(l, "new")
			}
		}
		return false
	}

	// The agent writes far down the file: the cursor follows.
	lines[150] = "line 150 FOLLOWED"
	write()
	until("the cursor never followed to the new hunk", func(s tuitest.Screen) bool { return cursorHeaderAbove(s, "FOLLOWED") })
	frame("1-followed")

	// A wheel turn stops following.
	diffX := 60
	wheelAt(t, term, diffX, 10, tuitest.MouseWheelUp, 1)
	until("the wheel never turned follow off", func(s tuitest.Screen) bool {
		return screenHas(s, "Follow off: you moved. f follows again.") && !screenHas(s, "following claude-code")
	})
	before := term.Screen().Line(10)

	// The next hunk is counted, and takes nothing.
	lines[190] = "line 190 COUNTED"
	write()
	until("the header never counted the new hunk", func(s tuitest.Screen) bool { return screenHas(s, "1 new hunk ]") })
	if got := term.Screen().Line(10); got != before {
		t.Errorf("ASSERTION: a hunk written with follow off moved the view:\nwas %q\nnow %q", before, got)
	}
	frame("2-counted")

	// A click on the count goes to it.
	row, col := findText(t, term, "1 new hunk ]")
	leftClick(t, term, col, row)
	until("a click on the count never went to the new hunk", func(s tuitest.Screen) bool {
		return screenHas(s, "COUNTED") && !screenHas(s, "1 new hunk ]")
	})
	frame("3-went-to-new")

	// The footer's f follows again.
	footer := rows - 2
	var x int
	until("the footer never offered f follow", func(s tuitest.Screen) bool {
		i := strings.Index(s.Line(footer), "f follow")
		if i < 0 {
			return false
		}
		x = len([]rune(s.Line(footer)[:i]))
		return true
	})
	leftClick(t, term, x, footer)
	until("a click on f follow never followed again", func(s tuitest.Screen) bool { return screenHas(s, "following claude-code") })
}
