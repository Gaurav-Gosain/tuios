package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestResizeSeamStaysClosed holds the seam a resize opens inside the compared
// window, where the matrix's resized-while-producing shape cannot keep it: that
// shape floods tens of thousands of rows to be sure the guest is still talking
// when the resizes land, so the rows laid out around the seam are evicted long
// before the comparison reads either side, and only a width the two copies
// still disagree about at the end can fail it.
//
// Here the guest produces little enough that every row it ever made is still
// held on both sides, and slowly enough that the resizes land among the bytes
// rather than after them. A line laid out at a width the other side never had
// is then a row the comparison reads, not one it lost to eviction. There is no
// route: the two copies are compared live, because a route that rebuilds the
// client's emulator from the daemon's snapshot would hand it the daemon's own
// layout and hide exactly the disagreement this exists to see.
func TestResizeSeamStaysClosed(t *testing.T) {
	r := newRig(t, 1)
	ptyID := r.win(0).PTYID
	r.feedPTY(ptyID, `printf 'SW''-READY\n'`, "SW-READY")
	w := r.winByPTY(ptyID)

	r.producing = filepath.Join(t.TempDir(), "producing")
	if err := os.WriteFile(r.producing, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r.startPTY(ptyID, `A=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA; `+
		`i=1; while [ -e '`+r.producing+`' ] && [ $i -le 100 ]; do j=1; while [ $j -le 25 ]; do `+
		`echo "SW-$i-$j-$A"; j=$((j+1)); done; sleep 0.05; i=$((i+1)); done; `+
		`echo SW-""DONE`)
	r.waitDaemonShows(ptyID, "SW-1-1-")

	full := w.Width
	for range 60 {
		w.Resize(max(full/3, 6), w.Height)
		time.Sleep(3 * time.Millisecond)
		w.Resize(full, w.Height)
		time.Sleep(3 * time.Millisecond)
	}
	if r.daemonShows(ptyID, "SW-DONE") {
		t.Fatal("the guest finished before the resizes landed, so this run never reached the seam")
	}
	if err := os.Remove(r.producing); err != nil {
		t.Fatal(err)
	}

	r.waitDaemonShows(ptyID, "SW-DONE")
	r.settle()
	r.converge(ptyID)
	compareSides(t, r, ptyID)
}
