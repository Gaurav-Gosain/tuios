package tuie2e

import (
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
// section moves while the pointer sweeps the session rows, in either the plain
// or the split space layout.
//
// The layout is the one the bug was reported against: terminals, agents,
// sessions, git and files, with a spacer between each.
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
//   - the split layout's spaces section moving while the sessions rows hold:
//     its header and rows are compared as well.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Rail peek holds the
// terminals height").
func TestRailPeekHoldsTheTerminalsHeight(t *testing.T) {
	for _, mode := range []string{"off", "split"} {
		t.Run(mode, func(t *testing.T) { railPeekHold(t, mode) })
	}
}

// railPeekSections is the reporter's section order: terminals above sessions.
const railPeekSections = "sections = 'terminals,spacer:5,agents,spacer:5,sessions,spacer:5,git,files'\n"

// railPeekSessions are the fixture's sessions and their pane counts. The
// attached one sits between the other two, so one peek is shorter than the
// held section and one is longer.
var railPeekSessions = []struct {
	name  string
	panes int
}{
	{"pk-main", 3}, {"pk-few", 1}, {"pk-many", 6},
}

func railPeekHold(t *testing.T, mode string) {
	base := t.TempDir()
	dir := artifactDir(t)
	writeConfig(t, base, spaceLayoutRail(mode)+railPeekSections)
	killDaemon(t, base)
	for _, s := range railPeekSessions {
		args := []string{"new", s.name, "--detach"}
		if mode == "split" {
			args = append(args, "--space", "work")
		}
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("create %s: %v: %s", s.name, err, out)
		}
		for range s.panes - 1 {
			if out, err := tuiosCLI(t, base, "new-window", "-s", s.name, "--no-focus"); err != nil {
				t.Fatalf("open a window in %s: %v: %s", s.name, err, out)
			}
		}
	}
	// A loose session in split mode gives the spaces section a second row,
	// so a moved section shows as a moved row rather than a moved header only.
	if mode == "split" {
		if out, err := tuiosCLI(t, base, "new", "pk-loose", "--detach"); err != nil {
			t.Fatalf("create pk-loose: %v: %s", err, out)
		}
	}

	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach", "pk-main"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 3 }, bootTimeout); err != nil {
		t.Fatalf("the client never attached to pk-main with its three panes: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "pk-main")

	needles := []string{"terminals", "sessions", "pk-main", "pk-few", "pk-many"}
	if mode == "split" {
		needles = append(needles, "spaces")
	}
	rest := waitSpaceRail(t, term, "the resting rail", needles...)
	saveArtifact(t, term, dir, "rest")

	// What must hold still: every session row, every header below the
	// terminals section, and in split mode the spaces section's rows.
	type mark struct {
		what string
		row  int
	}
	snapshot := func(s tuitest.Screen) []mark {
		var out []mark
		// Below the sessions header: a peek names its session on the
		// terminals header too, which is above it.
		for _, s2 := range railPeekSessions {
			out = append(out, mark{s2.name, railRowAfter(s, s2.name, railHeaderRow(s, "sessions"))})
		}
		for _, h := range []string{"terminals", "sessions", "spaces", "git", "files"} {
			out = append(out, mark{h + " header", railHeaderRow(s, h)})
		}
		if mode == "split" {
			for i, text := range spaceRowsListed(s) {
				out = append(out, mark{"spaces row " + strconv.Itoa(i) + " " + text, railHeaderRow(s, "spaces") + 1 + i})
			}
		}
		return out
	}
	want := snapshot(rest)
	if railHeaderRow(rest, "terminals") > railHeaderRow(rest, "sessions") {
		t.Fatalf("the fixture does not have terminals above sessions\n%s", term.Snapshot())
	}
	for _, m := range want {
		if strings.HasPrefix(m.what, "pk-") && m.row < 0 {
			t.Fatalf("the resting rail does not list %s\n%s", m.what, term.Snapshot())
		}
	}
	termHdr := railHeaderRow(rest, "terminals")
	// The held height: the lines between the terminals header and the next
	// thing drawn, which at rest are the attached session's three panes.
	held := 3
	for r := termHdr + 1; r <= termHdr+held; r++ {
		if railRowText(rest, r) == "" {
			t.Fatalf("the resting terminals section is not three rows tall: row %d is blank\n%s", r, term.Snapshot())
		}
	}

	// The sweep: down the session rows and back up, peeking each one.
	order := []string{"pk-main", "pk-few", "pk-many", "pk-few", "pk-main", "pk-many"}
	for step, name := range order {
		row := railRowAfter(rest, name, railHeaderRow(rest, "sessions"))
		mouseHover(t, term, strings.Index(railLine(rest, row), name)+1, row)
		peeked := name != "pk-main"
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			names := strings.Contains(railLine(s, railHeaderRow(s, "terminals")), name)
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
				t.Fatalf("ASSERTION: hovering %s moved %s from row %d to row %d (%s)\n%s",
					name, want[i].what, want[i].row, got[i].row, got[i].what, term.Snapshot())
			}
		}
		switch name {
		case "pk-few":
			// One pane, then blank lines to the held height.
			if railRowText(s, termHdr+1) == "" {
				t.Fatalf("ASSERTION: peeking pk-few drew no pane row\n%s", term.Snapshot())
			}
			for r := termHdr + 2; r <= termHdr+held; r++ {
				if text := railRowText(s, r); text != "" {
					t.Fatalf("ASSERTION: peeking pk-few drew %q on row %d, want the held line blank\n%s", text, r, term.Snapshot())
				}
			}
		case "pk-many":
			// Six panes in three lines: two rows and "+4".
			if last := railRowText(s, termHdr+held); !strings.HasSuffix(last, "+4") {
				t.Fatalf("ASSERTION: peeking pk-many ends the section in %q, want the +4 overflow row\n%s", last, term.Snapshot())
			}
		}
	}

	// The pointer leaves the rail and the attached session's panes come back.
	mouseHover(t, term, 80, 20)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(railLine(s, railHeaderRow(s, "terminals")), "pk-")
	}, uiTimeout); err != nil {
		t.Fatalf("leaving the rail did not end the peek: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "after")
	alive(t, term, "after sweeping the session rows")
}
