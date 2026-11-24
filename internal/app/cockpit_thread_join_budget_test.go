package app

import (
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The most bytes one frame may allocate after the long thread of
// benchBigThread grew by one answer. A rebuild joined every card's lines into
// four slices that grew from nothing by doubling and copying, the thread's
// lines among them, about 1,050,000 bytes a frame. With the slices sized
// before the join about 580,000 are left, most of it the cards themselves.
// The figure sits far above that, so small changes to the cards do not trip
// it, and below both the first and the figure with the thread's lines left
// to grow (about 970,000), so that growth cannot come back unseen.
const threadAppendFrameBytes = 800_000

// TestAThreadRebuildSizesItsLinesBeforeTheJoin holds the bytes of a frame
// after the long thread grew by one answer, read through runtime.MemStats:
// five runs, each on a fresh thread drawn once, of 100 appends with a frame
// each, and the lowest run is compared, since MemStats counts the whole
// process and only extra work raises every run. The entries slice is grown
// first, so a run is not charged for the append that doubles it.
//
// The ways it could pass wrongly, and the check for each: the frame was not
// rebuilt (the line count and the appended answers); a slice lost or gained
// a line (the line count); a drawn line is not at its own line's place (each
// line drawn again); a hit is not at its line's place (each hit); an id is
// not on its item's lines (one run for each id, the entries' order, the
// appended answers). The race detector adds allocations, so the test skips
// under it.
func TestAThreadRebuildSizesItsLinesBeforeTheJoin(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector adds allocations")
	}
	const runs, appends = 5, 100
	var m *OS
	var th *agentThread
	var best uint64
	for r := range runs {
		m, th = benchBigThread(t)
		m.renderInbox()
		th.entries = slices.Grow(th.entries, appends)
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for i := range appends {
			en := threadEntry{ID: "n" + strconv.Itoa(i), Role: "assistant", Kind: "text", Text: "Next step."}
			en.sum = threadSum(&en)
			th.entries = append(th.entries, en)
			th.gen++
			m.renderInbox()
		}
		runtime.ReadMemStats(&after)
		size := (after.TotalAlloc - before.TotalAlloc) / appends
		t.Logf("run %d: %d bytes a frame", r+1, size)
		if r == 0 || size < best {
			best = size
		}
	}

	if best > threadAppendFrameBytes {
		t.Errorf("ASSERTION: the budget: a frame after the thread grew by one answer allocated %d bytes (lowest of %d runs of %d appends), want at most %d: the thread's line slices grow by doubling in the join",
			best, runs, appends, threadAppendFrameBytes)
	}

	n := len(th.lines)
	if len(th.body) != n || len(th.lineIDs) != n || len(th.hits) != n || n <= 1000 {
		t.Fatalf("ASSERTION: one length: the thread holds %d lines, %d drawn lines, %d ids and %d hits, want one length above 1000",
			n, len(th.body), len(th.lineIDs), len(th.hits))
	}

	w := th.bodyAt.width
	var c *ckCanvas
	for i := range n {
		c = c.reset(w, 1, m.cockpitLook().canvas)
		if got := ckRender(c, w, th.lines[i]); got != th.body[i] {
			t.Errorf("ASSERTION: each drawn line is its line: drawn line %d of %d is %q, its line drawn again at width %d is %q", i, n, th.body[i], w, got)
			break
		}
	}

	set, matched := 0, true
	for i := range n {
		if th.hits[i] != th.lines[i].hit {
			t.Errorf("ASSERTION: each hit is its line's: hit %d of %d is %q, its line's hit is %q", i, n, th.hits[i], th.lines[i].hit)
			matched = false
			break
		}
		if th.hits[i] != "" {
			set++
		}
	}
	if matched && set == 0 {
		t.Errorf("ASSERTION: each hit is its line's: none of the %d lines has a hit", n)
	}

	seen := map[string]bool{}
	for i := range n {
		id := th.lineIDs[i]
		if i > 0 && th.lineIDs[i-1] == id {
			continue
		}
		if seen[id] {
			t.Errorf("ASSERTION: one run for each id: id %q starts a run again at line %d", id, i)
			break
		}
		seen[id] = true
	}

	pos := make(map[string]int, len(th.entries))
	for k, en := range th.entries {
		pos[en.ID] = k
	}
	last, lastID := -1, ""
	for i := range n {
		id := th.lineIDs[i]
		if i > 0 && th.lineIDs[i-1] == id {
			continue
		}
		k, ok := pos[id]
		if !ok {
			continue
		}
		if k < last {
			t.Errorf("ASSERTION: the entries' order: the run of %q (entry %d) at line %d comes after the run of %q (entry %d)", id, k, i, lastID, last)
			break
		}
		last, lastID = k, id
	}

	for k := 1; k < appends; k++ {
		id := "n" + strconv.Itoa(k)
		i := slices.Index(th.lineIDs, id)
		switch {
		case i < 0:
			t.Errorf("ASSERTION: the appended answers: no line carries the id of answer %q", id)
		case i+1 >= n || th.lineIDs[i+1] != id:
			t.Errorf("ASSERTION: the appended answers: answer %q starts at line %d and holds one line, want two", id, i)
		case i+2 < n && th.lineIDs[i+2] == id:
			t.Errorf("ASSERTION: the appended answers: answer %q starts at line %d and holds more than two lines", id, i)
		case !strings.Contains(th.body[i], "Next step."):
			t.Errorf("ASSERTION: the appended answers: the first drawn line of answer %q (line %d) is %q, want its text", id, i, th.body[i])
		case th.lines[i].blank() || !th.lines[i+1].blank():
			t.Errorf("ASSERTION: the appended answers: answer %q at line %d: first line blank %v, second line blank %v, want its text then a blank line",
				id, i, th.lines[i].blank(), th.lines[i+1].blank())
		default:
			continue
		}
		break
	}
}
