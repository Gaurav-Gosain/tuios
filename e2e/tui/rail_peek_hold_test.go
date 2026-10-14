package tuie2e

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailPeekHoldsTheTerminalsHeight: with the terminals section laid out
// above the sessions section, hovering a session row peeks that session's
// panes into the terminals section without changing its height. A peeked
// session with fewer panes than the attached one is padded with blank lines,
// one with more ends in the rail's "+N" row, and no row below the terminals
// section moves while the pointer sweeps the session rows.
//
// A peek that resized the section moved the hovered row out from under the
// pointer, which peeked another session and resized the section again, so the
// rail jumped for as long as the pointer swept it.
//
// The layout is terminals, agents, sessions, git and files, with a spacer
// between each.
//
// How it could pass wrongly, and what stops it:
//   - no peek at all, so nothing moves: every hover must put the hovered
//     session's name on the terminals header.
//   - the peeked list drawn at its own length, with the pointer's row read
//     before the frame settles: each assertion waits for the header to name
//     the session and then for the screen to stop changing.
//   - a longer peek pushing the rows below it down and a shorter one pulling
//     them up cancelling out across the sweep: every row is compared with the
//     resting frame after every single hover, not once at the end.
//   - the longer peek's extra panes silently dropped: the section must end in
//     the "+N" row naming exactly the panes it could not show.
//
// The rail at rest, after every hover and after the pointer leaves is saved
// under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Rail peek holds the
// terminals height").
func TestRailPeekHoldsTheTerminalsHeight(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nwidth = 28\nposition = 'left'\n"+
		"sections = 'terminals,spacer:5,agents,spacer:5,sessions,spacer:5,git,files'\n")
	killDaemon(t, base)
	// The attached session sits between the other two, so one peek is shorter
	// than the held section and one is longer.
	sessions := []struct {
		name  string
		panes int
	}{
		{"pk-main", 3}, {"pk-few", 1}, {"pk-many", 6},
	}
	for _, s := range sessions {
		if out, err := tuiosCLI(t, base, "new", s.name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", s.name, err, out)
		}
		for range s.panes - 1 {
			if out, err := tuiosCLI(t, base, "new-window", "-s", s.name, "--no-focus"); err != nil {
				t.Fatalf("open a window in %s: %v: %s", s.name, err, out)
			}
		}
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "pk-main"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 3 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached to pk-main with its three panes: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "pk-main")

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, n := range []string{"terminals", "sessions", "pk-main", "pk-few", "pk-many"} {
			if railRowOf(s, n) < 0 {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never drew its sections and sessions: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	rest := term.Screen()
	saveArtifact(t, term, dir, "rest")

	// What must hold still: every session row and every header.
	type mark struct {
		what string
		row  int
	}
	snapshot := func(s tuitest.Screen) []mark {
		var out []mark
		// Below the sessions header: a peek names its session on the
		// terminals header too, which is above it.
		for _, s2 := range sessions {
			out = append(out, mark{s2.name, railRowAfter(s, s2.name, peekHeaderRow(s, "sessions"))})
		}
		for _, h := range []string{"terminals", "sessions", "git", "files"} {
			out = append(out, mark{h + " header", peekHeaderRow(s, h)})
		}
		return out
	}
	want := snapshot(rest)
	if peekHeaderRow(rest, "terminals") > peekHeaderRow(rest, "sessions") {
		t.Fatalf("the fixture does not have terminals above sessions\n%s", term.Snapshot())
	}
	for _, m := range want {
		if strings.HasPrefix(m.what, "pk-") && m.row < 0 {
			t.Fatalf("the resting rail does not list %s\n%s", m.what, term.Snapshot())
		}
	}
	termHdr := peekHeaderRow(rest, "terminals")
	// The held height: the lines under the terminals header, which at rest
	// are the attached session's three panes.
	held := 3
	for r := termHdr + 1; r <= termHdr+held; r++ {
		if peekRowText(rest, r) == "" {
			t.Fatalf("the resting terminals section is not three rows tall: row %d is blank\n%s", r, term.Snapshot())
		}
	}

	// The sweep: down the session rows and back up, peeking each one.
	order := []string{"pk-main", "pk-few", "pk-many", "pk-few", "pk-main", "pk-many"}
	for step, name := range order {
		row := railRowAfter(rest, name, peekHeaderRow(rest, "sessions"))
		mouseHover(t, term, strings.Index(railLine(rest, row), name)+1, row)
		peeked := name != "pk-main"
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			names := strings.Contains(railLine(s, peekHeaderRow(s, "terminals")), name)
			return names == peeked
		}, uiTimeout); err != nil {
			t.Fatalf("hovering %s: the terminals header naming it is %v, want %v: %v\n%s",
				name, !peeked, peeked, err, term.Snapshot())
		}
		_ = term.WaitStable(500 * time.Millisecond)
		s := term.Screen()
		saveArtifact(t, term, dir, "hover-"+strconv.Itoa(step)+"-"+name)
		got := snapshot(s)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ASSERTION: hovering %s moved %s from row %d to row %d\n%s",
					name, want[i].what, want[i].row, got[i].row, term.Snapshot())
			}
		}
		switch name {
		case "pk-few":
			// One pane, then blank lines to the held height.
			if peekRowText(s, termHdr+1) == "" {
				t.Fatalf("ASSERTION: peeking pk-few drew no pane row\n%s", term.Snapshot())
			}
			for r := termHdr + 2; r <= termHdr+held; r++ {
				if text := peekRowText(s, r); text != "" {
					t.Fatalf("ASSERTION: peeking pk-few drew %q on row %d, want the held line blank\n%s", text, r, term.Snapshot())
				}
			}
		case "pk-many":
			// Six panes in three lines: two rows and "+4".
			if last := peekRowText(s, termHdr+held); !strings.HasSuffix(last, "+4") {
				t.Fatalf("ASSERTION: peeking pk-many ends the section in %q, want the +4 overflow row\n%s", last, term.Snapshot())
			}
		}
	}

	// The pointer leaves the rail and the attached session's panes come back.
	mouseHover(t, term, 80, 20)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(railLine(s, peekHeaderRow(s, "terminals")), "pk-")
	}, uiTimeout); err != nil {
		t.Fatalf("leaving the rail did not end the peek: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "after")
	alive(t, term, "after sweeping the session rows")
}

// peekHeaderRow is the screen row of a rail section's header: the row whose
// first word is the label, where a session row starts with its glyph. -1 when
// not drawn.
func peekHeaderRow(s tuitest.Screen, label string) int {
	_, rows := s.Size()
	for r := range rows {
		if f := strings.Fields(peekRowText(s, r)); len(f) > 0 && f[0] == label {
			return r
		}
	}
	return -1
}

// peekRowText is a rail row with the gutter, the edge rule and the padding
// taken off.
func peekRowText(s tuitest.Screen, row int) string {
	line := strings.TrimSuffix(strings.TrimRight(railLine(s, row), " "), "│")
	return strings.TrimSpace(strings.TrimPrefix(line, "▎"))
}

// TestRailPeekOnTheDefaultOrderListsEveryPane: with the default section order
// the terminals section is below the sessions section, so its height cannot
// move the row under the pointer and a peek is not held. Attached to a
// session of one pane, hovering a session of six lists all six panes in the
// terminals section, in order, with no "+N" row.
//
// Held at the attached session's one line, the peek would show only "… +6".
//
// How it could pass wrongly, and what stops it:
//   - no peek at all, so the attached session's pane is all there is: the
//     terminals header must name the hovered session.
//   - the peek cut to a "+N" row that happens to sit beside the pane names:
//     each of the six lines under the header must carry its pane's name, and
//     none may end in a "+N".
//   - one good frame among changing ones: the screen must settle before the
//     rows are read.
//
// The rail at rest and while peeking is saved under artifactDir.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Rail peek holds the
// terminals height").
func TestRailPeekOnTheDefaultOrderListsEveryPane(t *testing.T) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\nwidth = 28\nposition = 'left'\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "pk-one", "--detach"); err != nil {
		t.Fatalf("create pk-one: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "pk-six", "--detach"); err != nil {
		t.Fatalf("create pk-six: %v: %s", err, out)
	}
	panes := []string{"six0", "six1", "six2", "six3", "six4", "six5"}
	if out, err := tuiosCLI(t, base, "set-window", "-s", "pk-six", "--name", panes[0]); err != nil {
		t.Fatalf("name pk-six's first pane: %v: %s", err, out)
	}
	for _, name := range panes[1:] {
		if out, err := tuiosCLI(t, base, "new-window", "-s", "pk-six", name, "--no-focus"); err != nil {
			t.Fatalf("open %s in pk-six: %v: %s", name, err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "pk-one"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached to pk-one with its one pane: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "pk-one")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, n := range []string{"terminals", "sessions", "pk-one", "pk-six"} {
			if railRowOf(s, n) < 0 {
				return false
			}
		}
		return true
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never drew its sections and sessions: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(2 * time.Second)
	rest := term.Screen()
	saveArtifact(t, term, dir, "rest")
	if peekHeaderRow(rest, "sessions") > peekHeaderRow(rest, "terminals") {
		t.Fatalf("the fixture does not have sessions above terminals\n%s", term.Snapshot())
	}

	row := railRowAfter(rest, "pk-six", peekHeaderRow(rest, "sessions"))
	mouseHover(t, term, strings.Index(railLine(rest, row), "pk-six")+1, row)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(railLine(s, peekHeaderRow(s, "terminals")), "pk-six")
	}, uiTimeout); err != nil {
		t.Fatalf("hovering pk-six never named it on the terminals header: %v\n%s", err, term.Snapshot())
	}
	_ = term.WaitStable(500 * time.Millisecond)
	s := term.Screen()
	saveArtifact(t, term, dir, "peek")
	hdr := peekHeaderRow(s, "terminals")
	for i, name := range panes {
		text := peekRowText(s, hdr+1+i)
		if !slices.Contains(strings.Fields(text), name) {
			t.Fatalf("ASSERTION: the peek's line %d is %q, want %s: a peek of pk-six lists all six panes\n%s", i, text, name, term.Snapshot())
		}
		if f := strings.Fields(text); strings.HasPrefix(f[len(f)-1], "+") {
			t.Fatalf("ASSERTION: the peek's line %d is %q, which ends in a +N row\n%s", i, text, term.Snapshot())
		}
	}

	mouseHover(t, term, 80, 20)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(railLine(s, peekHeaderRow(s, "terminals")), "pk-")
	}, uiTimeout); err != nil {
		t.Fatalf("leaving the rail did not end the peek: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "after")
	alive(t, term, "after peeking pk-six")
}
