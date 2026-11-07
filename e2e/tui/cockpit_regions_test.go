package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The agents view has regions: the list, the focus, the reply and the side
// column. tab steps them and skips one the frame does not draw, the key bar
// names the one that has the keys and shows its keys, and an accent rule
// marks its top edge. esc goes back one step at a time. N goes to the next
// agent that needs the person.

// ckKeyBar is the key bar, the screen's last row.
func ckKeyBar(s tuitest.Screen) string {
	_, rows := s.Size()
	return strings.TrimSpace(s.Line(rows - 1))
}

// ckMarkSpan is the columns of the region mark: the run of the rule glyph on
// the row above the columns. ok is false when no row has one.
func ckMarkSpan(s tuitest.Screen) (x0, x1 int, ok bool) {
	cols, rows := s.Size()
	for y := 1; y < min(rows, 6); y++ {
		x0, x1 = -1, -1
		for x := range cols {
			if s.Cell(x, y).Content == "▁" {
				if x0 < 0 {
					x0 = x
				}
				x1 = x + 1
			}
		}
		if x0 >= 0 && x1-x0 > 10 {
			return x0, x1, true
		}
	}
	return 0, 0, false
}

// waitRegion waits for the key bar to name region and for the mark to start
// in the columns [lo, hi).
func waitRegion(t *testing.T, term *tuitest.Terminal, region string, lo, hi int, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x0, _, ok := ckMarkSpan(s)
		return strings.HasPrefix(ckKeyBar(s), region+" ") && ok && x0 >= lo && x0 < hi
	}, uiTimeout); err != nil {
		x0, x1, ok := ckMarkSpan(term.Screen())
		t.Fatalf("ASSERTION: %s: want region %s marked from a column in [%d,%d); key bar %q, mark %d..%d (%v): %v\n%s",
			when, region, lo, hi, ckKeyBar(term.Screen()), x0, x1, ok, err, term.Snapshot())
	}
}

// ckPress sends keys and gives the frame a beat.
func ckPress(t *testing.T, term *tuitest.Terminal, keys ...any) {
	t.Helper()
	if err := term.SendKeys(keys...); err != nil {
		t.Fatalf("send %v: %v", keys, err)
	}
	time.Sleep(250 * time.Millisecond)
}

// TestCockpitRegions walks the regions at 200x50, where the side column
// shows, and at 120x36, where it does not. tab goes List, Focus, Reply, Side
// and back to List, shift+tab goes the other way, and each step names the
// region in the key bar and moves the mark over it. A click in the focus
// gives it the keys. esc steps back: out of the reply with the draft kept,
// out of the focus to the list, then clears the filter, then closes the
// view; the draft is there when the view opens again. N goes to each agent
// that needs the person in turn and wraps.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: with tab opening the
// reply as before, the Focus step fails; with the side column taking a step
// it does not draw, the 120 column walk fails; with esc closing the view at
// once, the ladder fails; with N doing nothing, the N check fails.
func TestCockpitRegions(t *testing.T) {
	term, base, _, _ := startCockpitFleet(t, 200, 50, nil)
	// A second agent that needs the person, for N.
	if out, err := tuiosCLI(t, base, "set-agent-state", "needs_input", "-s", "misc", "-w", "scratch",
		"--harness", "claude-code", "--kind", "question", "-m", "Which branch?"); err != nil {
		t.Fatalf("scratch asks: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "Needs you  2")
	}, uiTimeout); err != nil {
		t.Fatalf("scratch never joined Needs you: %v\n%s", err, term.Snapshot())
	}
	cursorTo(t, term, func(line string) bool { return strings.Contains(line, "bench-idle") })

	// 200 columns: list 0..48, focus from 51, side from 154.
	waitRegion(t, term, "List", 0, 4, "the view opens on the list")
	saveCockpitFrames(t, term, "regions-200-list")
	ckPress(t, term, tuitest.Tab)
	waitRegion(t, term, "Focus", 48, 60, "tab from the list")
	if !strings.Contains(ckKeyBar(term.Screen()), "scroll") {
		t.Fatalf("ASSERTION: the focus's key bar does not offer its scroll keys: %q", ckKeyBar(term.Screen()))
	}
	saveCockpitFrames(t, term, "regions-200-focus")
	ckPress(t, term, tuitest.Tab)
	waitRegion(t, term, "Reply", 48, 60, "tab from the focus")
	saveCockpitFrames(t, term, "regions-200-reply")
	ckPress(t, term, tuitest.Tab)
	waitRegion(t, term, "Side", 140, 200, "tab from the reply")
	if !strings.Contains(ckKeyBar(term.Screen()), "open pane") {
		t.Fatalf("ASSERTION: the side column's key bar does not offer enter: %q", ckKeyBar(term.Screen()))
	}
	saveCockpitFrames(t, term, "regions-200-side")
	ckPress(t, term, tuitest.Tab)
	waitRegion(t, term, "List", 0, 4, "tab from the side column")
	ckPress(t, term, ckShiftTab)
	waitRegion(t, term, "Side", 140, 200, "shift+tab from the list")
	ckPress(t, term, tuitest.Esc)
	waitRegion(t, term, "List", 0, 4, "esc in the side column")

	// A click in the focus gives it the keys.
	row, col := findText(t, term, "Says:")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitRegion(t, term, "Focus", 48, 60, "a click in the focus")
	ckPress(t, term, tuitest.Esc)
	waitRegion(t, term, "List", 0, 4, "esc in the focus")

	// The esc ladder, from the reply with text in it.
	ckPress(t, term, "i")
	if err := term.Type("keep this draft"); err != nil {
		t.Fatalf("type a draft: %v", err)
	}
	waitText(t, term, "the draft", "keep this draft")
	ckPress(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.HasPrefix(ckKeyBar(s), "List ") && strings.Contains(s.Text(), "keep this draft")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc in the reply did not give the list the keys and keep the draft: %v\n%s", err, term.Snapshot())
	}
	ckPress(t, term, "f")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "release-not") && strings.Contains(ckKeyBar(s), "esc clear search")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: f did not narrow the list, or esc does not say it clears it: %v\n%s", err, term.Snapshot())
	}
	ckPress(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "release-not") && strings.Contains(ckKeyBar(s), "esc close")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not clear the filter: %v\n%s", err, term.Snapshot())
	}
	ckPress(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !ckViewUp(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the last esc did not close the view: %v\n%s", err, term.Snapshot())
	}
	ckPress(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitFor(ckViewUp, uiTimeout); err != nil {
		t.Fatalf("the view did not open again: %v\n%s", err, term.Snapshot())
	}
	cursorTo(t, term, func(line string) bool { return strings.Contains(line, "bench-idle") })
	waitText(t, term, "the draft after the view closed", "keep this draft")

	// N: each agent that needs the person, then the first again.
	var seen []string
	for range 3 {
		ckPress(t, term, "N")
		got := ckCursorRow(term.Screen())
		seen = append(seen, got)
	}
	needs := func(s string) bool { return strings.Contains(s, "api-auth") || strings.Contains(s, "scratch") }
	if !needs(seen[0]) || !needs(seen[1]) || seen[0] == seen[1] || seen[2] != seen[0] {
		t.Fatalf("ASSERTION: N did not walk the two agents that need you and wrap: %q\n%s", seen, term.Snapshot())
	}

	// 120 columns: no side column, so tab goes List, Focus, Reply, List.
	ckPress(t, term, "q")
	narrow := openCockpitClient(t, base, 120, 36, nil)
	cursorTo(t, narrow, func(line string) bool { return strings.Contains(line, "bench-idle") })
	waitRegion(t, narrow, "List", 0, 4, "the narrow view opens on the list")
	ckPress(t, narrow, tuitest.Tab)
	waitRegion(t, narrow, "Focus", 48, 60, "tab at 120 columns")
	ckPress(t, narrow, tuitest.Tab)
	waitRegion(t, narrow, "Reply", 48, 60, "tab from the focus at 120 columns")
	ckPress(t, narrow, tuitest.Tab)
	waitRegion(t, narrow, "List", 0, 4, "tab from the reply at 120 columns, past the side column it does not draw")
	saveCockpitFrames(t, narrow, "regions-120-list")
	alive(t, narrow, "after the regions")
}

// ckShiftTab is shift+tab as terminals send it, CSI Z.
const ckShiftTab = "\x1b[Z"
