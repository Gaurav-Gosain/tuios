package tuie2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A workspace whose panes a user resized keeps them where the user put them.
// It does not keep them in a box the session has left: the session's size
// moves while the workspace is off screen, when a client resizes, attaches
// or leaves. On the way back the panes have to fill the session's box again,
// with the user's splits.

// workspaceRects is the daemon's rectangles of one workspace's panes.
func workspaceRects(t *testing.T, base, name string, ws int) []winRect {
	t.Helper()
	wl, err := daemonWindows(base, name)
	if err != nil {
		t.Fatalf("list-windows: %v", err)
	}
	all := waitForSettledGeometryIn(t, base, name, len(wl.Windows))
	var out []winRect
	for _, w := range wl.Windows {
		if w.Workspace != ws {
			continue
		}
		for _, r := range all {
			if r.ID == w.ID {
				out = append(out, r)
			}
		}
	}
	return out
}

// TestCustomWorkspaceFollowsTheSessionSize resizes the panes of workspace 2,
// leaves it, and changes the session's size twice while it is off screen: a
// smaller client attaches, then leaves. Each time the first client comes back
// to workspace 2, the panes fill the session's width and keep their shares.
//
// NEGATIVE CONTROL: fails on main (524e2f46) at "workspace 2 after the
// session shrank": the panes kept the 120-column rectangles from before, in
// a session of 100 columns. With the tiledLayoutStale term cut from the
// retile in SwitchToWorkspace it fails at the same step.
func TestCustomWorkspaceFollowsTheSessionSize(t *testing.T) {
	for _, lay := range []string{"master-stack", "bsp"} {
		t.Run(lay, func(t *testing.T) {
			base := t.TempDir()
			const name = "wsbox"
			writeConfig(t, base, fmt.Sprintf("[startup]\nopen_default_window = true\ntiled = true\nlayout = %q\n", lay))
			if out, err := tuiosCLI(t, base, "new", "-d", name); err != nil {
				t.Fatalf("create the session: %v\n%s", err, out)
			}
			a := attachIn(t, base, name, startOpts{cols: 120, rows: 40})
			waitForSettledGeometryIn(t, base, name, 1)
			sendKeys(t, a, tuitest.Alt("2"))
			time.Sleep(300 * time.Millisecond)
			newWindow(t, a)
			newWindow(t, a)
			equal := workspaceRects(t, base, name, 2)
			sendKeys(t, a, ">", ">", ">")
			var resized []winRect
			deadline := time.Now().Add(uiTimeout)
			for {
				resized = workspaceRects(t, base, name, 2)
				if sameShares(equal, resized, 1) != nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the keys resized nothing\n%s", describeRects(resized))
				}
				time.Sleep(200 * time.Millisecond)
			}
			dir := artifactDir(t)
			saveArtifact(t, a, dir, "ws2-resized")
			sendKeys(t, a, tuitest.Alt("1"))
			time.Sleep(300 * time.Millisecond)

			back := func(what string, width int) {
				t.Helper()
				sendKeys(t, a, tuitest.Alt("2"))
				deadline := time.Now().Add(uiTimeout)
				for {
					rects := workspaceRects(t, base, name, 2)
					_, _, w, _ := paneBoxOf(rects)
					err := sameShares(resized, rects, 2)
					if w == width && err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("%s: the panes are %d columns wide in all, want %d (%v)\n%s", what, w, width, err, describeRects(rects))
					}
					time.Sleep(200 * time.Millisecond)
				}
				waitSpanRight(t, a, width, what+": the first client")
				saveArtifact(t, a, dir, fmt.Sprintf("ws2-at-%d", width))
				sendKeys(t, a, tuitest.Alt("1"))
				time.Sleep(300 * time.Millisecond)
			}

			b := attachSmall(t, base, name, startOpts{cols: 100, rows: 32})
			waitWSSizeIn(t, base, name, 100, 32, "with the smaller client attached")
			back("workspace 2 after the session shrank", 100)

			if err := b.Close(); err != nil {
				t.Fatalf("close the smaller client: %v", err)
			}
			waitWSSizeIn(t, base, name, 120, 40, "after the smaller client left")
			back("workspace 2 after the session grew back", 120)
		})
	}
}
