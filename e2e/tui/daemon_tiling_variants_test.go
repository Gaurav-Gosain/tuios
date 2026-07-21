package tuie2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

func assertTiled(t *testing.T, rects []winRect, cols int) {
	t.Helper()
	for _, r := range rects {
		t.Logf("window %s: (%d,%d) %dx%d", r.ID, r.X, r.Y, r.Width, r.Height)
	}
	for _, r := range rects {
		if r.Width >= cols {
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
}

// Variant B: attach, enable tiling via the daemon verb FIRST (while 0 windows),
// then create windows through the daemon verb. Stresses placeUnplacedWindows +
// adoptSyncedWindows for windows arriving into an already-tiled session.
func TestDaemonVariantB_ToggleFirstThenWindows(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "vb"}})
	killDaemon(t, base)
	waitBoot(t, term)

	if out, err := tuiosCLI(t, base, "run-command", "--session", "vb", "ToggleTiling"); err != nil {
		t.Fatalf("ToggleTiling failed: %v\n%s", err, out)
	}
	for i := 1; i <= 3; i++ {
		if out, err := tuiosCLI(t, base, "run-command", "--session", "vb", "NewWindow"); err != nil {
			t.Fatalf("NewWindow #%d failed: %v\n%s", i, err, out)
		}
		waitWindowCount(t, term, i, fmt.Sprintf("after NewWindow #%d", i))
	}
	rects := waitForSettledGeometry(t, base, 3)
	assertTiled(t, rects, 120)
}

// Variant C: windows created in a DETACHED session (no client), then a client
// attaches, then tiling is enabled via the daemon verb. This is the closest to
// the reported "created windows in a daemon session" flow.
func TestDaemonVariantC_Verb(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "vc", "--detach"); err != nil {
		t.Fatalf("create detached: %v: %s", err, out)
	}
	// Detached session starts with one window; add two more via the daemon verb.
	for i := 2; i <= 3; i++ {
		if out, err := tuiosCLI(t, base, "run-command", "--session", "vc", "NewWindow"); err != nil {
			t.Fatalf("NewWindow #%d failed: %v\n%s", i, err, out)
		}
	}

	c := startIn(t, base, startOpts{args: []string{"attach", "vc"}})
	if err := c.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 3
	}, bootTimeout); err != nil {
		t.Fatalf("client never saw 3 windows: %v\n%s", err, c.Snapshot())
	}

	if out, err := tuiosCLI(t, base, "run-command", "--session", "vc", "ToggleTiling"); err != nil {
		t.Fatalf("ToggleTiling failed: %v\n%s", err, out)
	}
	rects := waitForSettledGeometry(t, base, 3)
	assertTiled(t, rects, 120)
}

// Variant D: same as C but tiling is enabled through the INTERACTIVE 't' key on
// the attached client, not the daemon verb.
func TestDaemonVariantD_Interactive(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "vd", "--detach"); err != nil {
		t.Fatalf("create detached: %v: %s", err, out)
	}
	for i := 2; i <= 3; i++ {
		if out, err := tuiosCLI(t, base, "run-command", "--session", "vd", "NewWindow"); err != nil {
			t.Fatalf("NewWindow #%d failed: %v\n%s", i, err, out)
		}
	}

	c := startIn(t, base, startOpts{args: []string{"attach", "vd"}})
	if err := c.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 3
	}, bootTimeout); err != nil {
		t.Fatalf("client never saw 3 windows: %v\n%s", err, c.Snapshot())
	}

	// Interactive tiling toggle: the client boots into terminal mode when a
	// window exists, so return to window-management mode first.
	if err := c.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("to window mode: %v", err)
	}
	if err := c.WaitForText("Window Management Mode", uiTimeout); err != nil {
		t.Fatalf("never entered window management mode: %v\n%s", err, c.Snapshot())
	}
	time.Sleep(insertGuard)
	if err := c.SendKeys("t"); err != nil {
		t.Fatalf("toggle tiling: %v", err)
	}
	rects := waitForSettledGeometry(t, base, 3)
	assertTiled(t, rects, 120)
}
