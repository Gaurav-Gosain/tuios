package tuie2e

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestDaemonCreatedWindowsTile drives the exact scenario the owner reported:
// with a client attached and tiling on, windows are created through the daemon
// path (tuios run-command NewWindow, the same path 'tuios new'/CLI callers use)
// rather than interactively. Every such window must join the tiled layout, so
// the six windows partition the screen with no overlap.
func TestDaemonCreatedWindowsTile(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "e2e"}})
	killDaemon(t, base)

	waitBoot(t, term)

	// One window interactively, then turn tiling on. From here the attached
	// client is in tiling mode and has synced that to the daemon.
	newWindow(t, term)
	waitWindowCount(t, term, 1, "first window")
	enableTiling(t, term)

	// Create five more windows through the daemon verb path.
	for i := 2; i <= 6; i++ {
		out, err := tuiosCLI(t, base, "run-command", "NewWindow")
		if err != nil {
			t.Fatalf("run-command NewWindow #%d failed: %v\n%s", i, err, out)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after daemon NewWindow #%d", i))
	}

	// Give the client a beat to place and sync the final window back.
	rects := waitForSettledGeometry(t, base, 6)

	// The signature of the bug: a window still carrying the daemon's full-size
	// Unplaced box, or two windows on top of each other.
	for _, r := range rects {
		if r.Width >= 120 {
			t.Errorf("window %s spans the full width (%d): it was never tiled", r.ID, r.Width)
		}
	}
	for a := 0; a < len(rects); a++ {
		for b := a + 1; b < len(rects); b++ {
			if geomOverlap(rects[a], rects[b]) {
				t.Errorf("windows overlap: %s (%d,%d %dx%d) and %s (%d,%d %dx%d)",
					rects[a].ID, rects[a].X, rects[a].Y, rects[a].Width, rects[a].Height,
					rects[b].ID, rects[b].X, rects[b].Y, rects[b].Width, rects[b].Height)
			}
		}
	}

	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}
}

type winRect struct {
	ID                  string
	X, Y, Width, Height int
}

func geomOverlap(a, b winRect) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

// waitForSettledGeometry polls list-windows --json until the daemon reports n
// windows whose geometry has stopped changing, so the assertion runs against the
// placed layout rather than a mid-flight frame.
func waitForSettledGeometry(t *testing.T, base string, n int) []winRect {
	t.Helper()
	var prev []winRect
	deadline := time.Now().Add(15 * time.Second)
	stableSince := 0
	for time.Now().Before(deadline) {
		out, err := tuiosCLI(t, base, "list-windows", "--json")
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		rects, ok := parseWindows(out)
		if !ok || len(rects) != n {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if sameGeometry(prev, rects) {
			stableSince++
			if stableSince >= 3 {
				return rects
			}
		} else {
			stableSince = 0
		}
		prev = rects
		time.Sleep(200 * time.Millisecond)
	}
	if prev != nil {
		return prev
	}
	t.Fatalf("daemon never reported %d windows with settled geometry", n)
	return nil
}

func sameGeometry(a, b []winRect) bool {
	if len(a) != len(b) {
		return false
	}
	byID := make(map[string]winRect, len(a))
	for _, r := range a {
		byID[r.ID] = r
	}
	for _, r := range b {
		p, ok := byID[r.ID]
		if !ok || p != r {
			return false
		}
	}
	return true
}

func parseWindows(jsonOut string) ([]winRect, bool) {
	var payload struct {
		Windows []struct {
			ID     string `json:"window_id"`
			X      int    `json:"x"`
			Y      int    `json:"y"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		return nil, false
	}
	rects := make([]winRect, 0, len(payload.Windows))
	for _, w := range payload.Windows {
		rects = append(rects, winRect{ID: w.ID, X: w.X, Y: w.Y, Width: w.Width, Height: w.Height})
	}
	return rects, true
}
