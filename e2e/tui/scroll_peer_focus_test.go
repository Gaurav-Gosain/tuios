package tuie2e

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The report this file exists for: two clients attached to one daemon session
// in the scrolling (niri) layout, and one of them moves focus. The other draws
// the focus border on the new window and leaves its viewport where it was, so
// the focused window can be entirely off its screen.
//
// Two real tuios processes on one daemon, three named panes, and the assertion
// is what the second client has on screen.
//
// NEGATIVE CONTROL: run against the tree before the fix, the second client's
// frame after the step still read "BRAVO ... CHARLIE" while the client that had
// moved the focus read "ALPHA ... BRAVO", and the assertion below failed. At
// the internal level the peer had the focused pane at x=-65 on a screen 100
// wide.
func TestScrollingPeerFollowsFocusIntoView(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "strip", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}

	a := attachIn(t, base, "strip", startOpts{cols: 100, rows: 30})
	renameWindow(t, a, "ALPHA")
	newWindow(t, a)
	renameWindow(t, a, "BRAVO")
	newWindow(t, a)
	renameWindow(t, a, "CHARLIE")
	waitWindowCount(t, a, 3, "three panes on the first client")
	enableTiling(t, a)

	if err := a.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open the palette: %v", err)
	}
	waitPaletteOpen(t, a, "to pick the scrolling layout")
	if err := a.SendKeys("scrolling", tuitest.Enter); err != nil {
		t.Fatalf("pick the scrolling layout: %v", err)
	}
	waitPaletteClosed(t, a, "after picking the scrolling layout")
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "ALPHA") && strings.Contains(s.Text(), "CHARLIE")
	}, uiTimeout); err != nil {
		t.Fatalf("the first client never took the scrolling layout: %v\n%s", err, a.Snapshot())
	}
	t.Logf("the first client in the scrolling layout:\n%s", a.Snapshot())

	b := attachIn(t, base, "strip", startOpts{cols: 100, rows: 30})
	if err := b.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "CHARLIE")
	}, uiTimeout); err != nil {
		t.Fatalf("the second client never showed the focused column: %v\n%s", err, b.Snapshot())
	}
	time.Sleep(time.Second)

	before := b.Snapshot()
	t.Logf("the second client before the first one moves focus:\n%s", before)
	if !strings.Contains(before, "CHARLIE") {
		t.Fatalf("the second client is not showing the focused column to start with:\n%s", before)
	}

	for range 2 {
		if err := a.SendKeys(tuitest.Alt("p")); err != nil {
			t.Fatalf("step the focus left: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
	if err := a.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "ALPHA")
	}, uiTimeout); err != nil {
		t.Fatalf("the first client never scrolled to the focused column: %v\n%s", err, a.Snapshot())
	}
	time.Sleep(2 * time.Second)

	after := b.Snapshot()
	t.Logf("the first client, which moved the focus:\n%s", a.Snapshot())
	t.Logf("the second client after the first one moved focus:\n%s", after)
	if !strings.Contains(after, "ALPHA") {
		t.Errorf("the second client never brought the focused column on screen:\n%s", after)
	}
	if strings.Contains(after, "CHARLIE") {
		t.Errorf("the second client is still showing the far end of the strip:\n%s", after)
	}
}

func stripOrder(rects []winRect) []string {
	sorted := slices.Clone(rects)
	slices.SortStableFunc(sorted, func(a, b winRect) int { return a.X - b.X })
	ids := make([]string, 0, len(sorted))
	for _, r := range sorted {
		ids = append(ids, r.ID)
	}
	return ids
}

func newPaneIn(t *testing.T, before []string, after []winRect) string {
	t.Helper()
	for _, r := range after {
		if !slices.Contains(before, r.ID) {
			return r.ID
		}
	}
	t.Fatalf("no new pane among %v (was %v)", after, before)
	return ""
}

func waitStripFocus(t *testing.T, base, want, when string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if focusedWindowID(t, base, "strip") == want {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s: the focus is on %s, want %s", when, focusedWindowID(t, base, "strip"), want)
}

// TestScrollingNewColumnOpensBesideFocus is issue 952: in the scrolling layout
// with a daemon, a new pane went to the end of the strip and not into a column
// right after the focused one, which is where ScrollingLayout.AddColumn puts it
// and where a client without a daemon put it. A split is a new pane too, so it
// is checked the same way. Closing the focused column moves the focus one
// column left, as it does without a daemon.
//
// NEGATIVE CONTROL: see NEGATIVE_CONTROLS.md, issue 952.
func TestScrollingNewColumnOpensBesideFocus(t *testing.T) {
	term, base := scrollingStrip(t, 3)
	start := stripOrder(waitForSettledGeometry(t, base, 3))
	for i, id := range start {
		focusAndName(t, base, id, []string{"ONE", "TWO", "THREE"}[i])
	}
	for range 2 {
		if err := term.SendKeys(tuitest.Alt("p")); err != nil {
			t.Fatalf("step the focus left: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
	waitStripFocus(t, base, start[0], "after stepping to the first column")
	waitText(t, term, "the first column on screen", "ONE")

	if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
		t.Fatalf("NewWindow: %v\n%s", err, out)
	}
	after := waitForSettledGeometry(t, base, 4)
	added := newPaneIn(t, start, after)
	waitStripFocus(t, base, added, "after the new pane opened")
	focusAndName(t, base, added, "NEW")
	waitText(t, term, "the new pane's name", "NEW")
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	railShot(t, term, "new-pane")
	want := []string{start[0], added, start[1], start[2]}
	if got := stripOrder(after); !slices.Equal(got, want) {
		t.Fatalf("the new pane is not in the column after the focused one:\n got  %v\n want %v\n%s", got, want, term.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "run-command", "Split", "vertical"); err != nil {
		t.Fatalf("Split vertical: %v\n%s", err, out)
	}
	split := waitForSettledGeometry(t, base, 5)
	splitPane := newPaneIn(t, want, split)
	wantSplit := []string{start[0], added, splitPane, start[1], start[2]}
	if got := stripOrder(split); !slices.Equal(got, wantSplit) {
		t.Fatalf("the split pane is not in the column after the focused one:\n got  %v\n want %v\n%s", got, wantSplit, term.Snapshot())
	}
	waitStripFocus(t, base, splitPane, "after the split")

	if out, err := tuiosCLI(t, base, "run-command", "CloseWindow"); err != nil {
		t.Fatalf("CloseWindow: %v\n%s", err, out)
	}
	closed := waitForSettledGeometry(t, base, 4)
	if got := stripOrder(closed); !slices.Equal(got, want) {
		t.Fatalf("the strip after the close:\n got  %v\n want %v\n%s", got, want, term.Snapshot())
	}
	waitStripFocus(t, base, added, "after the focused column closed")
	railShot(t, term, "after-close")

	if out, err := tuiosCLI(t, base, "focus-window", "-s", "strip", start[0]); err != nil {
		t.Fatalf("focus ONE: %v\n%s", err, out)
	}
	waitStripFocus(t, base, start[0], "after focusing ONE")
	runPaletteRow(t, term, "bsp tiling", "Layout: BSP tiling", "Layout: BSP tiling")
	waitPaletteClosed(t, term, "after switching to BSP")
	tiled := waitForSettledGeometry(t, base, 4)
	if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
		t.Fatalf("NewWindow in BSP: %v\n%s", err, out)
	}
	bsp := waitForSettledGeometry(t, base, 5)
	rectOf := func(rects []winRect, id string) winRect {
		for _, r := range rects {
			if r.ID == id {
				return r
			}
		}
		t.Fatalf("no pane %s in %v", id, rects)
		return winRect{}
	}
	if before, after := rectOf(tiled, added), rectOf(bsp, added); before != after {
		t.Errorf("the pane after the switch to BSP split NEW, the target of the old split, not the focused ONE: NEW went from %+v to %+v", before, after)
	}
	if before, after := rectOf(tiled, start[0]), rectOf(bsp, start[0]); before == after {
		t.Errorf("the pane after the switch to BSP did not split the focused ONE: ONE stayed at %+v", before)
	}
	railShot(t, term, "bsp-new-pane")
}

func focusAndName(t *testing.T, base, id, name string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "strip", id); err != nil {
		t.Fatalf("focus %s: %v\n%s", id, err, out)
	}
	waitStripFocus(t, base, id, "after focusing "+name)
	if out, err := tuiosCLI(t, base, "run-command", "RenameWindow", name); err != nil {
		t.Fatalf("name %s: %v\n%s", name, err, out)
	}
}

func rectByID(t *testing.T, rects []winRect, id string) winRect {
	t.Helper()
	for _, r := range rects {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no pane %s in %v", id, rects)
	return winRect{}
}

// TestScrollingRevealsANewPane is issue 1507: with columns narrower than the
// screen, a pane opened beside a column that was itself revealed with a peek
// of its neighbour lands in that peek. It took the focus, but the strip only
// moved for a column with nothing on screen, so the pane the user had just
// opened was cut off at the right edge.
//
// The same rule left a pane cut off when it was focused by name: the
// focus-window verb and the navigator. Each route starts from the same strip,
// with the target pane in the peek at the right edge.
//
// NEGATIVE CONTROL: see NEGATIVE_CONTROLS.md, issue 1507.
func TestScrollingRevealsANewPane(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nlayout = \"scrolling\"\n\n[appearance]\nscroll_column_width = 50\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "strip"}})
	waitWindowCount(t, term, 1, "first column")
	for n := 2; n <= 3; n++ {
		if out, err := tuiosCLI(t, base, "run-command", "NewWindow"); err != nil {
			t.Fatalf("NewWindow: %v\n%s", err, out)
		}
		waitWindowCount(t, term, n, "another column")
	}
	start := stripOrder(waitForSettledGeometry(t, base, 3))
	for i, id := range start {
		focusAndName(t, base, id, []string{"ONE", "TWO", "THREE"}[i])
	}

	stepToTwo := func(when string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Alt("p")); err != nil {
			t.Fatalf("step the focus left: %v", err)
		}
		waitStripFocus(t, base, start[1], when)
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("the screen never settled: %v", err)
		}
	}
	wholeOnScreen := func(id string, n int, route string) {
		t.Helper()
		waitStripFocus(t, base, id, "after "+route)
		rects := waitForSettledGeometry(t, base, n)
		if err := term.WaitStable(uiTimeout); err != nil {
			t.Fatalf("the screen never settled: %v", err)
		}
		if r := rectByID(t, rects, id); r.X < 0 || r.X+r.Width > 120 {
			t.Errorf("%s: the focused pane is not wholly on screen: %+v\n%s", route, r, term.Snapshot())
		}
	}

	stepToTwo("after stepping to the middle column")
	railShot(t, term, "before-new-pane")
	if err := term.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatalf("open a pane: %v", err)
	}
	waitWindowCount(t, term, 4, "the new pane")
	added := newPaneIn(t, start, waitForSettledGeometry(t, base, 4))
	wholeOnScreen(added, 4, "a new pane")
	railShot(t, term, "after-new-pane")
	if out, err := tuiosCLI(t, base, "run-command", "RenameWindow", "NEW"); err != nil {
		t.Fatalf("name the new pane: %v\n%s", err, out)
	}

	stepToTwo("before focus-window")
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "strip", added); err != nil {
		t.Fatalf("focus-window: %v\n%s", err, out)
	}
	wholeOnScreen(added, 4, "focus-window")

	stepToTwo("before the navigator")
	openNavigator(t, term)
	sendKeys(t, term, "/")
	if err := term.SendKeys("NEW"); err != nil {
		t.Fatalf("type the query: %v", err)
	}
	waitText(t, term, "the pane in the navigator", "› NEW")
	sendKeys(t, term, tuitest.Enter)
	wholeOnScreen(added, 4, "the navigator")
	railShot(t, term, "after-navigator")
}
