package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func ckKeyBar(s tuitest.Screen) string {
	_, rows := s.Size()
	return strings.TrimSpace(s.Line(rows - 1))
}

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

func waitRegion(t *testing.T, term *tuitest.Terminal, region string, lo, hi int, when string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		x0, _, ok := ckMarkSpan(s)
		return ckBarNames(ckKeyBar(s), region) && ok && x0 >= lo && x0 < hi
	}, uiTimeout); err != nil {
		x0, x1, ok := ckMarkSpan(term.Screen())
		t.Fatalf("ASSERTION: %s: want region %s marked from a column in [%d,%d); key bar %q, mark %d..%d (%v): %v\n%s",
			when, region, lo, hi, ckKeyBar(term.Screen()), x0, x1, ok, err, term.Snapshot())
	}
}

func ckBarNames(bar, region string) bool {
	if strings.HasPrefix(bar, region+" ") {
		return true
	}
	for _, other := range []string{"List ", "Focus ", "Reply ", "Side "} {
		if strings.HasPrefix(bar, other) {
			return false
		}
	}
	switch region {
	case "Focus":
		return strings.Contains(bar, "scroll")
	case "Reply":
		return strings.Contains(bar, "undo")
	case "Side":
		return false
	}
	return !strings.Contains(bar, "scroll") && !strings.Contains(bar, "undo")
}

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

	row, col := findText(t, term, "Says:")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitRegion(t, term, "Focus", 48, 60, "a click in the focus")
	ckPress(t, term, tuitest.Esc)
	waitRegion(t, term, "List", 0, 4, "esc in the focus")

	ckPress(t, term, "i")
	if err := term.Type("keep this draft"); err != nil {
		t.Fatalf("type a draft: %v", err)
	}
	waitText(t, term, "the draft", "keep this draft")
	ckPress(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return ckBarNames(ckKeyBar(s), "List") && strings.Contains(s.Text(), "keep this draft")
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

const ckShiftTab = "\x1b[Z"
