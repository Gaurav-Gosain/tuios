package tuie2e

import (
	"fmt"
	"testing"
)

// The splits a user makes in the master-stack layout, kept when the layout is
// computed again. A terminal resize computes it again every time, so it is
// the way the report reached the user: with shared borders on, every pane but
// the master and the first stack split went back to an equal share.
//
// Each case resizes the panes with the keys, then resizes the client, then
// attaches a second, smaller client, which lays the panes out again from the
// session's state alone. After each step every pane must keep its share of
// the box the panes fill, in the daemon's rectangles.

// paneBoxOf is the box the rectangles fill.
func paneBoxOf(rects []winRect) (x, y, w, h int) {
	x, y = 1<<30, 1<<30
	right, bottom := 0, 0
	for _, r := range rects {
		x, y = min(x, r.X), min(y, r.Y)
		right, bottom = max(right, r.X+r.Width), max(bottom, r.Y+r.Height)
	}
	return x, y, right - x, bottom - y
}

// sameShares reports the first pane whose share of the box differs from its
// share in want by more than slack cells.
func sameShares(want, got []winRect, slack int) error {
	_, _, ww, wh := paneBoxOf(want)
	_, _, gw, gh := paneBoxOf(got)
	byID := map[string]winRect{}
	for _, r := range got {
		byID[r.ID] = r
	}
	for i, w := range want {
		g, ok := byID[w.ID]
		if !ok {
			return fmt.Errorf("pane %d is gone", i)
		}
		ew, eh := float64(w.Width)*float64(gw)/float64(ww), float64(w.Height)*float64(gh)/float64(wh)
		if d := float64(g.Width) - ew; d > float64(slack) || d < -float64(slack) {
			return fmt.Errorf("pane %d is %d columns wide, want about %.1f", i, g.Width, ew)
		}
		if d := float64(g.Height) - eh; d > float64(slack) || d < -float64(slack) {
			return fmt.Errorf("pane %d is %d rows tall, want about %.1f", i, g.Height, eh)
		}
	}
	return nil
}

// TestMasterStackKeepsSplitsAcrossAResize is the report: with shared borders
// on, a resize of the client put the master-stack panes back to equal splits.
//
// NEGATIVE CONTROL: fails on main (524e2f46) at "the daemon after the client
// grew" in the grid and stack-of-three cases. The tiler kept only the master
// ratio and the ratio of a stack of exactly two, so the grid's rows and
// columns and a third stack pane went back to equal shares. With
// MasterSplitsFrom's result not recorded in SyncMasterStackFromGeometry, it
// fails at the same step.
func TestMasterStackKeepsSplitsAcrossAResize(t *testing.T) {
	for _, tc := range []struct {
		name, appearance string
		panes            int
	}{
		{"grid", "shared_borders = true\n", 4},
		{"stack-of-three", "shared_borders = true\nmaster_grid = false\n", 4},
		{"stack-of-two", "shared_borders = true\n", 3},
		{"no-shared-borders", "master_grid = false\n", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			const name = "splits"
			term := masterSession(t, base, name, tc.appearance, tc.panes)
			equal := waitForSettledGeometryIn(t, base, name, tc.panes)
			// The focus is on the last pane: narrow the masters' side and
			// make the last pane taller.
			sendKeys(t, term, ">", ">", ">", "}", "}", "}")
			resized := waitForShape(t, base, name, tc.panes, "the daemon after the keys", func(rects []winRect) error {
				if sameShares(equal, rects, 1) == nil {
					return fmt.Errorf("the keys resized nothing")
				}
				return nil
			})
			dir := artifactDir(t)
			saveArtifact(t, term, dir, "resized")

			if err := term.Resize(150, 45); err != nil {
				t.Fatalf("resize the client: %v", err)
			}
			waitForShape(t, base, name, tc.panes, "the daemon after the client grew", func(rects []winRect) error {
				if _, _, w, _ := paneBoxOf(rects); w != 150 {
					return fmt.Errorf("the panes are %d columns wide in all, want 150", w)
				}
				return sameShares(resized, rects, 2)
			})
			saveArtifact(t, term, dir, "grown")

			// A second client lays the panes out again from the session's
			// state: smallest policy, so the session shrinks to it.
			small := attachIn(t, base, name, startOpts{cols: 100, rows: 32})
			waitForShape(t, base, name, tc.panes, "the daemon with a smaller client attached", func(rects []winRect) error {
				if _, _, w, _ := paneBoxOf(rects); w != 100 {
					return fmt.Errorf("the panes are %d columns wide in all, want 100", w)
				}
				return sameShares(resized, rects, 2)
			})
			saveArtifact(t, small, dir, "second-client")
			saveArtifact(t, term, dir, "first-client-shrunk")
		})
	}
}
