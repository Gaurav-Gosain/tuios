package tuie2e

import (
	"testing"
	"time"
)

// The report this file exists for: a local client attached to a daemon session
// and a second client (tuios-web, in the report) attached beside it, a split
// open, and a plain pane switch - alt+n - resizes the panes. Nobody touched a
// window boundary.
//
// The chrome round settled the outer box (multiclient_chrome_test.go). What
// was left was the arithmetic inside it: shared borders and the pane gap were
// process-global config that nothing synced, so two clients whose configs
// disagreed partitioned the same box into different rectangles - or the same
// rectangles into different guest grids - and every state push dragged the
// shared PTYs between the two answers. The web client is exactly the second
// process with its own configuration in force.
//
// This test is the report's own sequence across two real tuios processes with
// genuinely different config files. The web frontend itself cannot be driven
// by this harness; a second tuios process with its own XDG_CONFIG_HOME is the
// faithful stand-in, because tuios-web runs this same client code against the
// same daemon socket.
//
// NEGATIVE CONTROL: measured against a binary built from the unfixed tree
// (20f17bbd, the build the report was filed against): the second client's
// attach alone moved the daemon's windows - the pane at 61,0 59x38 was dragged
// to 60,0 60x38, the config-less client reclaiming the divider column the
// shared-borders client had reserved - and the assertion on `joined` failed.
// Every later push re-fights the same argument, which is the report's
// "switching terminals triggers updates". On the fixed tree the geometry
// inputs are session state, the second client adopts them on attach, and
// nothing below moves a rectangle.
func TestGeometryConfigDisagreementDoesNotMovePanes(t *testing.T) {
	base := t.TempDir()
	// The first client's config file turns shared borders on. The second
	// client's config directory is separate and empty, so its process runs the
	// default: shared borders off. This is the disagreement in the report, and
	// nothing propagates the file between the two.
	writeConfig(t, base, "[appearance]\nshared_borders = true\n")
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "geometry", "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}

	a := attachIn(t, base, "geometry", startOpts{cols: bigCols, rows: bigRows})
	newWindow(t, a)
	waitWindowCount(t, a, 2, "the split on the first client")
	enableTiling(t, a)
	before := waitForSettledGeometryIn(t, base, "geometry", 2)

	b := attachIn(t, base, "geometry", startOpts{
		cols: bigCols, rows: bigRows,
		env: []string{"XDG_CONFIG_HOME=" + t.TempDir()},
	})

	// The join is allowed one settling round (the box is negotiated once), but
	// it must settle back on the same rectangles: both clients are the same
	// size with the same chrome, so nothing about the box changed.
	joined := waitForSettledGeometryIn(t, base, "geometry", 2)
	if !sameGeometry(before, joined) {
		t.Errorf("the second client's attach moved the panes:\n before %v\n after  %v", before, joined)
	}

	// The report's gesture: pane switches, pressed on the client whose config
	// disagrees. Each one pushes state; none of them may move a rectangle.
	for range 6 {
		if err := b.SendKeys("\t"); err != nil {
			t.Fatalf("focus cycle: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Second)

	after := waitForSettledGeometryIn(t, base, "geometry", 2)
	if !sameGeometry(joined, after) {
		t.Errorf("switching panes moved the daemon's windows:\n before %v\n after  %v", joined, after)
	}

	// And from the first client, for the direction that used to yield rather
	// than fight: it must be equally still.
	for range 6 {
		if err := a.SendKeys("\t"); err != nil {
			t.Fatalf("focus cycle on the first client: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Second)

	final := waitForSettledGeometryIn(t, base, "geometry", 2)
	if !sameGeometry(after, final) {
		t.Errorf("switching panes on the first client moved the daemon's windows:\n before %v\n after  %v", after, final)
	}
}
