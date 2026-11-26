package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestReviewIsLive opens the review with ctrl+b v and then changes the work
// tree from outside the client, as an agent or an editor would, with no key
// after the open: a new line shows up, lines added above the cursor leave the
// cursor's line where it is on screen, a new file joins the list, and a change
// made while a note is typed waits until the note is dropped.
//
// How it could pass wrongly, written down first:
//   - The diff could be read again on a timer. The header must say "live",
//     and an idle client with the review open is held to the idle budget in
//     TestIdleCostStaysLow ("live review").
//   - The cursor could be kept by its index while the rows above it grew, so
//     it lands on another line. The line under it is found by its text, on
//     the same screen row as before. The diff is taller than the pane, so
//     the view can scroll to keep it there.
//   - A read could close the note editor or move its row. The editor's line
//     must still be there, and the new line must not show until it is gone.
//
// Artifact: a frame of each step under artifactDir, as text, styled text
// and PNG.
func TestReviewIsLive(t *testing.T) {
	base, repo := fanFixture(t)
	body := func(changed map[int]string, top []string) string {
		var b strings.Builder
		for _, s := range top {
			b.WriteString(s)
			b.WriteByte('\n')
		}
		for i := 1; i <= 60; i++ {
			if s, ok := changed[i]; ok {
				b.WriteString(s)
				b.WriteByte('\n')
				continue
			}
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	write := func(dir, name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, "main.go", body(nil, nil))
	testutil.Git(t, repo, "add", "main.go")
	testutil.Git(t, repo, "commit", "-q", "-m", "main")
	session, path := reviewFanAt(t, base, repo, "live")
	// Four hunks, and a fifth below, so the diff is taller than the pane and
	// the view can scroll to keep a line where it is.
	early := map[int]string{10: "line 10 CHANGED-10", 20: "line 20 CHANGED-20", 30: "line 30 CHANGED-30", 40: "line 40 CHANGED-40"}
	with := func(extra map[int]string) map[int]string {
		out := map[int]string{}
		for k, v := range early {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	write(path, "main.go", body(early, nil))

	const cols, rows = 140, 36
	term := attachIn(t, base, session, startOpts{cols: cols, rows: rows})
	sendKeys(t, term, tuitest.Ctrl('b'), "v")
	// No key from here on.
	waitScreen(t, term, "the review never opened live", "Review", "CHANGED-30", "● live")
	art := artifactDir(t)
	frame := func(name string) {
		t.Helper()
		saveArtifact(t, term, art, name)
		savePNG(t, term.Screen(), shot.XTermPalette(), art, name)
	}
	frame("1-open")
	until := func(what string, ok func(tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(ok, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: %v\n%s", what, err, term.Snapshot())
		}
	}

	// A line written from outside shows up.
	write(path, "main.go", body(with(map[int]string{50: "line 50 LIVE-50"}), nil))
	// LIVE-50 is below the screen, so the counts say it arrived.
	until("a line written outside the client never reached the counts", func(s tuitest.Screen) bool { return screenHas(s, "+5 -5") })
	frame("2-line-arrives")

	// The cursor on CHANGED-30 stays on it, on the same screen row, when
	// lines are added above it.
	row, col := findText(t, term, "CHANGED-30")
	leftClick(t, term, col, row)
	cursorOn := func(s tuitest.Screen, y int) bool {
		line := s.Line(y)
		return strings.Contains(line, "CHANGED-30") && strings.Contains(line, "›")
	}
	until("a click never put the cursor on CHANGED-30", func(s tuitest.Screen) bool { return cursorOn(s, row) })
	write(path, "main.go", body(with(map[int]string{50: "line 50 LIVE-50"}), []string{"TOP-1", "TOP-2", "TOP-3"}))
	until("the lines added at the top never reached the counts", func(s tuitest.Screen) bool { return screenHas(s, "+8 -5") })
	if s := term.Screen(); !cursorOn(s, row) {
		t.Errorf("ASSERTION: lines added above the cursor moved it off CHANGED-30 on row %d\n%s", row, term.Snapshot())
	}
	frame("3-cursor-kept")

	// A new file joins the list.
	write(path, "NEWFILE.txt", "made outside\n")
	until("a new file never joined the list", func(s tuitest.Screen) bool { return screenHas(s, "NEWFILE.txt", "+9 -5") })
	frame("4-new-file")

	// A change made while a note is typed waits for the note.
	hint := func(text string) {
		t.Helper()
		var x int
		footer := rows - 2
		until("the footer never showed "+text, func(s tuitest.Screen) bool {
			i := strings.Index(s.Line(footer), text)
			if i < 0 {
				return false
			}
			x = len([]rune(s.Line(footer)[:i]))
			return true
		})
		leftClick(t, term, x, footer)
	}
	hint("c note")
	until("c note never opened the editor", func(s tuitest.Screen) bool { return screenHas(s, "esc drop") })
	write(path, "main.go", body(with(map[int]string{50: "line 50 LIVE-50", 55: "line 55 HELD-55"}), []string{"TOP-1", "TOP-2", "TOP-3"}))
	held := time.Now().Add(time.Second)
	for time.Now().Before(held) {
		// HELD-55 is below the screen, so the header's counts say whether
		// it landed.
		if s := term.Screen(); !screenHas(s, "+9 -5") || !screenHas(s, "esc drop") {
			t.Fatalf("ASSERTION: a change landed while a note was typed\n%s", term.Snapshot())
		}
		pause(t, "a change held for the note editor stays held", 100*time.Millisecond)
	}
	frame("5-held-for-note")
	hint("esc drop")
	until("the held change never landed once the note was dropped", func(s tuitest.Screen) bool { return screenHas(s, "+10 -6") })
	frame("6-held-lands")
}
