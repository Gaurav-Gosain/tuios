package terminal

import (
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
	"tuios.dev/vt/snapshot"
)

// A client can hold less of a pane's history than its daemon: a snapshot on
// attach carries the newest thousand lines, and the daemon can keep many
// more. The window adds the lines only the daemon holds above the ones the
// emulator holds, and reads them from the daemon by number
// (vt.Terminal.ScrollbackBase), a page at a time.
//
// Only a reader of one line asks for its page: the renderer and copy mode's
// cursor. A line not read yet reads as blank, and the page that brings it
// wakes the renderer. A reader of a whole range, such as search, gets the
// pages already read and blank lines for the rest, and asks for nothing:
// walking a long history a page at a time would hold up every other request
// of the client. UnreadHistory says how many lines such a reader missed.
//
// One page is read at a time. The pages asked for while one is on its way
// wait, newest first, up to maxWantedPages: the screen moved past the older
// ones.
//
// The daemon's oldest number is what the last snapshot or page said. A page
// carries the daemon's current oldest number and numbering epoch, and pages of
// an older epoch are dropped. A request carries the emulator's own epoch: a
// reply that comes back after the emulator renumbered its history is dropped,
// and the page is asked for again in the new numbering.
//
// Locks: the emulator's numbers are read under the window's I/O lock, which
// the readers hold, and then remoteHistory.mu is taken. Nothing takes the I/O
// lock while it holds mu.

// HistoryFetch reads up to max history lines before line number before from
// the daemon. A nil page says the daemon does not number the pane's history.
type HistoryFetch func(before int64, max int) (*snapshot.HistoryPage, error)

const (
	// historyPageLines is the lines one fetch asks for, and the unit the
	// cache keeps.
	historyPageLines = 256
	// maxHistoryPages bounds the pages kept: 16,384 lines.
	maxHistoryPages = 64
	// maxWantedPages bounds the pages waiting to be read.
	maxWantedPages = 4
	// historyRetry is how long a page that failed to come is not asked again.
	historyRetry = 2 * time.Second
)

// remoteHistory is the lines of a pane only its daemon holds.
type remoteHistory struct {
	mu    sync.Mutex
	fetch HistoryFetch

	// known says daemonBase and epoch came from the daemon, for the
	// emulator's numbering localEpoch. lost says they were known and a
	// renumbering of the emulator made them unknown: the window asks for a
	// page to learn them again.
	known, lost bool
	daemonBase  int64
	epoch       uint64
	localEpoch  uint64

	pages  map[int64]*historyPage
	used   []int64
	failed map[int64]time.Time
	gen    uint64

	// wanted is the pages to read, the newest last, each with the emulator's
	// epoch it was asked in. reading says a read is on its way, and inFlight
	// is the page it reads.
	wanted   []wantedPage
	reading  bool
	inFlight wantedPage
}

// wantedPage is a page to read, and the emulator's epoch it was asked in.
type wantedPage struct {
	idx   int64
	local uint64
}

// historyPage is one page of lines read from the daemon.
type historyPage struct {
	first       int64
	lines       []uv.Line
	wraps, pads []bool
}

// pageIndex is the page that holds line number n.
func pageIndex(n int64) int64 {
	if n < 0 {
		return (n - historyPageLines + 1) / historyPageLines
	}
	return n / historyPageLines
}

// SetHistorySource sets how the window reads lines only its daemon holds,
// or with nil, that it reads none.
func (w *Window) SetHistorySource(fetch HistoryFetch) {
	r := w.remoteState()
	r.mu.Lock()
	r.fetch = fetch
	r.mu.Unlock()
}

// NoteDaemonHistory records the daemon's oldest line number and epoch from a
// snapshot the window's emulator was built from. It is not numbered when the
// daemon does not number its history. The caller holds the I/O lock it
// applied the snapshot under.
func (w *Window) NoteDaemonHistory(base int64, epoch uint64, numbered bool) {
	if w.Terminal == nil {
		return
	}
	local := w.Terminal.ScrollbackEpoch()
	r := w.remoteState()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	if !numbered {
		r.known, r.lost = false, false
		r.dropPages()
		return
	}
	if epoch != r.epoch {
		r.dropPages()
	}
	r.known, r.lost, r.daemonBase, r.epoch, r.localEpoch = true, false, base, epoch, local
}

// remoteState is the window's remoteHistory, made on first use.
func (w *Window) remoteState() *remoteHistory {
	if r := w.remote.Load(); r != nil {
		return r
	}
	w.remote.CompareAndSwap(nil, &remoteHistory{})
	return w.remote.Load()
}

// remoteSpan is the lines the daemon holds above the oldest the emulator
// holds: how many, the number of the first, and the emulator's epoch.
type remoteSpan struct {
	n     int
	first int64
	local uint64
}

// remoteLines is the window's remoteSpan. The caller holds the I/O lock, or
// reads the emulator without one as the other history readers do.
func (w *Window) remoteLines() remoteSpan {
	r := w.remote.Load()
	if r == nil || w.Terminal == nil {
		return remoteSpan{}
	}
	base, ok := w.Terminal.ScrollbackBase()
	if !ok {
		return remoteSpan{}
	}
	local := w.Terminal.ScrollbackLen()
	epoch := w.Terminal.ScrollbackEpoch()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fetch == nil {
		return remoteSpan{}
	}
	if r.known && epoch != r.localEpoch {
		// The emulator renumbered its history. The daemon renumbers too, but
		// by how much is not known here until a page says.
		r.known, r.lost = false, true
		r.dropPages()
		r.gen++
	}
	if !r.known {
		if r.lost {
			r.requestLocked(w, pageIndex(base-1), epoch)
		}
		return remoteSpan{}
	}
	if local == 0 {
		// The emulator holds no history: none was sent, or a clear dropped
		// it on both sides.
		r.daemonBase = max(r.daemonBase, base)
	}
	if base <= r.daemonBase {
		return remoteSpan{}
	}
	return remoteSpan{n: int(base - r.daemonBase), first: r.daemonBase, local: epoch}
}

// line is line number n, and its flags, if a page holds it. Otherwise, with
// ask, it asks for the page.
func (r *remoteHistory) line(w *Window, n int64, local uint64, ask bool) (line uv.Line, wrapped, padded, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := pageIndex(n)
	p := r.pages[idx]
	if p == nil {
		if ask {
			r.requestLocked(w, idx, local)
		}
		return nil, false, false, false
	}
	r.touchLocked(idx)
	i := int(n - p.first)
	if i < 0 || i >= len(p.lines) {
		return nil, false, false, false
	}
	return p.lines[i], p.wraps[i], p.pads[i], true
}

// held is how many of the lines numbered first to first+n-1 a page holds.
func (r *remoteHistory) held(first int64, n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	held := 0
	for _, p := range r.pages {
		lo, hi := max(p.first, first), min(p.first+int64(len(p.lines)), first+int64(n))
		held += int(max(hi-lo, 0))
	}
	return held
}

// requestLocked asks for page idx, read off the caller's goroutine. A page
// already asked for moves to the front of the wait.
func (r *remoteHistory) requestLocked(w *Window, idx int64, local uint64) {
	if r.fetch == nil || time.Since(r.failed[idx]) < historyRetry {
		return
	}
	if r.reading && r.inFlight == (wantedPage{idx: idx, local: local}) {
		return
	}
	for i, want := range r.wanted {
		if want.idx == idx {
			r.wanted = append(r.wanted[:i], r.wanted[i+1:]...)
			break
		}
	}
	r.wanted = append(r.wanted, wantedPage{idx: idx, local: local})
	if len(r.wanted) > maxWantedPages {
		r.wanted = r.wanted[len(r.wanted)-maxWantedPages:]
	}
	if !r.reading {
		r.reading = true
		go w.readHistoryPages(r)
	}
}

// readHistoryPages reads the wanted pages one at a time, the newest first,
// until none is left.
func (w *Window) readHistoryPages(r *remoteHistory) {
	for {
		r.mu.Lock()
		if len(r.wanted) == 0 || r.fetch == nil {
			r.wanted, r.reading = nil, false
			r.mu.Unlock()
			return
		}
		want := r.wanted[len(r.wanted)-1]
		r.wanted = r.wanted[:len(r.wanted)-1]
		r.inFlight = want
		fetch := r.fetch
		r.mu.Unlock()
		w.readHistoryPage(r, fetch, want)
	}
}

// readHistoryPage reads one page and keeps it, if the emulator's numbering
// is still the one it was asked in.
func (w *Window) readHistoryPage(r *remoteHistory, fetch HistoryFetch, want wantedPage) {
	page, err := fetch((want.idx+1)*historyPageLines, historyPageLines)
	var p *historyPage
	var local uint64
	stale := true
	// The colours resolve against the emulator's palette, and its epoch says
	// whether it renumbered since the ask. Both change under the lock.
	w.RLockIO()
	if w.Terminal != nil {
		local = w.Terminal.ScrollbackEpoch()
		stale = local != want.local
		if err == nil && page != nil && !stale {
			p = &historyPage{first: page.First}
			_ = page.Lines(w.Terminal, func(_ int64, line uv.Line, wrapped, padded bool) {
				p.lines = append(p.lines, line)
				p.wraps = append(p.wraps, wrapped)
				p.pads = append(p.pads, padded)
			})
		}
	}
	w.RUnlockIO()

	r.mu.Lock()
	switch {
	case err != nil:
		if r.failed == nil {
			r.failed = map[int64]time.Time{}
		}
		r.failed[want.idx] = time.Now()
	case stale:
		// Asked in another numbering. The next reader asks again.
	case page == nil:
		r.known, r.lost = false, false
		r.dropPages()
	default:
		if page.Epoch != r.epoch {
			r.dropPages()
			r.epoch = page.Epoch
		}
		r.daemonBase, r.known, r.lost, r.localEpoch = page.Base, true, false, local
		if len(p.lines) > 0 {
			r.keepLocked(want.idx, p)
		}
	}
	r.gen++
	r.mu.Unlock()

	w.wakeRenderer()
	if err != nil {
		// Asked again once the wait is over, by the frame this wakes.
		time.AfterFunc(historyRetry+100*time.Millisecond, w.wakeRenderer)
	}
}

// wakeRenderer asks for a frame that draws the pane again, as new output
// does. The signal alone redraws the screen from the pane's cached content:
// HasNewOutput is what makes the frame read the history again.
func (w *Window) wakeRenderer() {
	w.HasNewOutput.Store(true)
	select {
	case w.PTYDataChan <- struct{}{}:
	default:
	}
}

// keepLocked keeps page p as page idx, dropping the least used page past
// the bound.
func (r *remoteHistory) keepLocked(idx int64, p *historyPage) {
	if r.pages == nil {
		r.pages = map[int64]*historyPage{}
	}
	r.pages[idx] = p
	r.touchLocked(idx)
	for len(r.used) > maxHistoryPages {
		delete(r.pages, r.used[0])
		r.used = r.used[1:]
	}
}

// touchLocked marks page idx as the one used last.
func (r *remoteHistory) touchLocked(idx int64) {
	for i, u := range r.used {
		if u == idx {
			r.used = append(r.used[:i], r.used[i+1:]...)
			break
		}
	}
	r.used = append(r.used, idx)
}

// dropPages forgets every page and every page asked for.
func (r *remoteHistory) dropPages() {
	r.pages, r.used, r.failed, r.wanted = nil, nil, nil, nil
}

// remoteGeneration changes whenever the lines only the daemon holds do.
func (w *Window) remoteGeneration() uint64 {
	r := w.remote.Load()
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// UnreadHistory is how many lines of the history only the daemon holds and
// no page has brought yet: lines a search of the whole history did not see.
func (w *Window) UnreadHistory() int {
	span := w.remoteLines()
	if span.n == 0 {
		return 0
	}
	return span.n - w.remote.Load().held(span.first, span.n)
}

// remoteText is line as text cells, for ScrollbackText.
func remoteText(line uv.Line, buf []vt.TextCell) []vt.TextCell {
	buf = buf[:0]
	for i := range line {
		c := &line[i]
		if c.Width == 0 && c.Content == "" {
			continue
		}
		buf = append(buf, vt.TextCell{Content: []byte(c.Content), Width: c.Width})
	}
	return buf
}
