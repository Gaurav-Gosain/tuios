package client

import (
	"encoding/binary"
	"math"
	"sync"

	"github.com/Gaurav-Gosain/mygo/plugins/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

// historyFetch is how many rows one pane-history call asks for.
const historyFetch = 1000

// history follows where the history a pane's terminal holds starts in the
// pane's history on the daemon. A snapshot brings the last 500 rows, and the
// H frame before it the id of the first. As the view nears the top,
// FetchHistory asks pane-history for the rows above it and adds them to the
// terminal.
type history struct {
	mu sync.Mutex
	// next is the id an H frame gave, for the snapshot after it.
	next   int64
	nextOK bool
	// first is the id of the oldest history row the terminal holds; ok
	// says it is known. A daemon without ids leaves it unknown. A resize
	// does not: the daemon's emulator keeps its history at the width it
	// was written at, and the rows fetched wrap at this terminal's width
	// as they are written.
	first int64
	ok    bool
	// gen counts snapshots, and unwritten says the terminal has not yet
	// written the one read last: rows added before it would go.
	gen       uint64
	unwritten bool
	// busy says a fetch runs, and done that the pane holds nothing older.
	busy, done bool
}

// idFrame takes an H frame.
func (h *history) idFrame(p []byte) {
	if len(p) < 8 {
		return
	}
	h.mu.Lock()
	h.next, h.nextOK = int64(binary.BigEndian.Uint64(p)), true
	h.mu.Unlock()
}

// snapshot takes an S frame: the terminal starts over with its rows. It
// reports whether the daemon gave the rows ids.
func (h *history) snapshot() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.first, h.ok = h.next, h.nextOK
	h.nextOK, h.done, h.unwritten = false, false, true
	h.gen++
	return h.ok
}

// written says the terminal wrote what it read before.
func (h *history) written() {
	h.mu.Lock()
	h.unwritten = false
	h.mu.Unlock()
}

// FetchHistory adds the pane's history rows above those t holds, when the
// daemon gives row ids and has older rows. The terminal calls for it as
// its view nears the top (terminal.Options.OnNearTop). It blocks on the
// daemon: call it on a goroutine of its own.
func (s *Stream) FetchHistory(t *terminal.Terminal) {
	h := &s.hist
	h.mu.Lock()
	if t == nil || !h.ok || h.done || h.busy || h.unwritten {
		h.mu.Unlock()
		return
	}
	h.busy = true
	before, gen := h.first, h.gen
	h.mu.Unlock()
	if held := s.heldOldest(t); held > before {
		// The terminal's scrollback limit let go of rows that came after
		// the snapshot: the rows to fetch are above those it still holds,
		// not above the snapshot's first.
		before = held
	}
	raw, err := s.c.verb("pane-history", map[string]any{"session": s.session, "window": s.window, "before": before, "count": historyFetch})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.busy = false
	var r struct {
		Rows  string `json:"rows"`
		First int64  `json:"first"`
		Count int    `json:"count"`
	}
	if err == nil {
		err = jsonx.Unmarshal(raw, &r)
	}
	switch {
	case err != nil:
		// An older daemon, a pane without ids, or a lost link: until the
		// next snapshot, the terminal keeps what it holds.
		debugf("pane-history %s: %v", s.window, err)
		h.ok = false
		return
	case r.Count == 0:
		h.done = true
		return
	case h.gen != gen || h.unwritten || !h.ok:
		return // the terminal started over meanwhile
	}
	// A full-screen program's screen shows: the next call adds them.
	if _, err := t.PrependHistory([]byte(r.Rows)); err == nil {
		h.first = r.First
	}
}

// heldOldest is the id of the oldest history row t holds, from the id of
// the pane's newest history row and how many rows t's scrollback holds; 0
// when the daemon does not say. The pane's size is this view's lease, so t
// holds the newest rows as the daemon numbers them.
func (s *Stream) heldOldest(t *terminal.Terminal) int64 {
	n := t.HistoryRows()
	if n == 0 {
		return 0
	}
	raw, err := s.c.verb("pane-history", map[string]any{"session": s.session, "window": s.window, "before": int64(math.MaxInt64), "count": 1})
	if err != nil {
		return 0
	}
	var r struct {
		First int64 `json:"first"`
	}
	if jsonx.Unmarshal(raw, &r) != nil {
		return 0
	}
	return r.First - int64(n) + 1
}
