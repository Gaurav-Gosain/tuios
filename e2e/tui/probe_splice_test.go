package tuie2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestProbeSplice is a scratch diagnostic, not an assertion.
func TestProbeSplice(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "probe", "--detach"); err != nil {
		t.Fatalf("new: %v %s", err, out)
	}
	wl, err := daemonWindows(base, "probe")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	w := wl.Windows[0]
	t.Logf("window %s tag %s %dx%d", w.ID, w.tag(), w.Width, w.Height)
	control := t.TempDir() + "/control.txt"
	cmd := "printf 'MK" + w.tag() + "-%d\\n' $(seq 1 1200) | tee " + control + "\n"
	if err := paneSend(base, "probe", w.ID, cmd); err != nil {
		t.Fatalf("send: %v", err)
	}
	time.Sleep(5 * time.Second)
	if b, err := os.ReadFile(control); err == nil {
		t.Logf("control file has %d lines", strings.Count(string(b), "\n"))
	} else {
		t.Logf("control file unreadable: %v", err)
	}

	grid, err := daemonPane(base, "probe", w.ID)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	t.Logf("grid rows=%d", len(grid))
	for i, l := range grid {
		t.Logf("grid[%02d] %q", i, l)
		if i > 12 {
			break
		}
	}
	if a, b, found := spliceIn(grid); found {
		t.Logf("GRID SPLICE %d -> %d at rows %d,%d", a.seq, b.seq, a.row, b.row)
		for i := max(a.row-2, 0); i < min(b.row+3, len(grid)); i++ {
			t.Logf("  row %d %q", i, grid[i])
		}
	} else {
		t.Logf("grid clean")
	}

	hist, err := daemonScrollback(base, "probe", w.ID, 400)
	if err != nil {
		t.Fatalf("scrollback: %v", err)
	}
	t.Logf("hist rows=%d", len(hist))
	if a, b, found := spliceIn(hist); found {
		t.Logf("HIST SPLICE %d -> %d at rows %d,%d", a.seq, b.seq, a.row, b.row)
		for i := max(a.row-2, 0); i < min(b.row+3, len(hist)); i++ {
			t.Logf("  row %d %q", i, hist[i])
		}
	} else {
		t.Logf("hist clean")
	}
	t.Logf("witness count grid=%d hist=%d", len(witnessesIn(grid)), len(witnessesIn(hist)))
	t.Logf("joined tail %q", strings.Join(grid[max(len(grid)-3, 0):], " | "))
}
