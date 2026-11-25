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
// (vt.Terminal.ScrollbackBase), a page at a time, when a reader asks for one.
// A line not read yet reads as blank, and the page that brings it wakes the
// renderer.
//
// The daemon's oldest number is what the last snapshot or page said. The
// daemon drops lines and renumbers on its own, so it can be stale: a page
// carries the daemon's current oldest number and epoch, and pages of an older
// epoch are dropped.

// HistoryFetch reads up to max history lines before line number before from
// the daemon. A nil page says the daemon does not number the pane's history.
type HistoryFetch func(before int64, max int) (*snapshot.HistoryPage, error)

const (
	// historyPageLines is the lines one fetch asks for, and the unit the
	// cache keeps.
	historyPageLines = 256
	// maxHistoryPages bounds the pages kept: 16,384 lines.
	maxHistoryPages = 64
	// historyRetry is how long a page that failed to come is not asked again.
	historyRetry = 2 * time.Second
)

// remoteHistory is the lines of a pane only its daemon holds.
type remoteHistory struct {
	mu    sync.Mutex
	fetch HistoryFetch

	// known says daemonBase and epoch came from the daemon.
	known      bool
	daemonBase int64
	epoch      uint64
	// localEpoch is the emulator's own epoch when daemonBase was set. A
	// resize of the emulator renumbers its history, and the daemon's numbers
	// then are not known until the next page.
	localEpoch uint64

	pages    map[int64]*historyPage
	used     []int64
	fetching map[int64]bool
	failed   map[int64]time.Time
	gen      uint64
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
// daemon does not number its history.
func (w *Window) NoteDaemonHistory(base int64, epoch uint64, numbered bool) {
	r := w.remoteState()
	var local uint64
	if w.Terminal != nil {
		local = w.Terminal.ScrollbackEpoch()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !numbered {
		r.known = false
		r.dropPages()
		return
	}
	if epoch != r.epoch {
		r.dropPages()
	}
	r.known, r.daemonBase, r.epoch, r.localEpoch = true, base, epoch, local
	r.gen++
}

// remoteState is the window's remoteHistory, made on first use.
func (w *Window) remoteState() *remoteHistory {
	if r := w.remote.Load(); r != nil {
		return r
	}
	w.remote.CompareAndSwap(nil, &remoteHistory{})
	return w.remote.Load()
}

// remoteLines is how many lines the daemon holds above the oldest the
// emulator holds, and the number of the first of them.
func (w *Window) remoteLines() (n int, first int64) {
	r := w.remote.Load()
	if r == nil || w.Terminal == nil {
		return 0, 0
	}
	base, ok := w.Terminal.ScrollbackBase()
	if !ok {
		return 0, 0
	}
	local := w.Terminal.ScrollbackLen()
	epoch := w.Terminal.ScrollbackEpoch()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.known || r.fetch == nil {
		return 0, 0
	}
	if epoch != r.localEpoch {
		// The emulator renumbered its history. The daemon renumbers too, but
		// by how much is not known here until a page says.
		r.known = false
		r.dropPages()
		r.gen++
		r.requestLocked(w, base)
		return 0, 0
	}
	if local == 0 {
		// The emulator holds no history: none was sent, or a clear dropped
		// it on both sides.
		r.daemonBase = max(r.daemonBase, base)
	}
	if base <= r.daemonBase {
		return 0, 0
	}
	return int(base - r.daemonBase), r.daemonBase
}

// line is line number n, and its flags, if a page holds it. Otherwise it asks
// for the page.
func (r *remoteHistory) line(w *Window, n int64) (line uv.Line, wrapped, padded, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := pageIndex(n)
	p := r.pages[idx]
	if p == nil {
		r.requestLocked(w, (idx+1)*historyPageLines)
		return nil, false, false, false
	}
	r.touchLocked(idx)
	i := int(n - p.first)
	if i < 0 || i >= len(p.lines) {
		return nil, false, false, false
	}
	return p.lines[i], p.wraps[i], p.pads[i], true
}

// requestLocked asks the daemon for the page of lines before number before,
// once, off the caller's goroutine.
func (r *remoteHistory) requestLocked(w *Window, before int64) {
	idx := pageIndex(before - 1)
	if r.fetch == nil || r.fetching[idx] || time.Since(r.failed[idx]) < historyRetry {
		return
	}
	if r.fetching == nil {
		r.fetching = map[int64]bool{}
	}
	r.fetching[idx] = true
	fetch, epoch := r.fetch, r.epoch
	go w.readHistoryPage(r, fetch, idx, epoch)
}

// readHistoryPage reads page idx from the daemon and keeps it.
func (w *Window) readHistoryPage(r *remoteHistory, fetch HistoryFetch, idx int64, epoch uint64) {
	page, err := fetch((idx+1)*historyPageLines, historyPageLines)
	var p *historyPage
	if err == nil && page != nil {
		p = &historyPage{first: page.First}
		// The colours resolve against the emulator's palette, which the
		// output writer changes under the lock.
		w.RLockIO()
		if w.Terminal != nil {
			_ = page.Lines(w.Terminal, func(_ int64, line uv.Line, wrapped, padded bool) {
				p.lines = append(p.lines, line)
				p.wraps = append(p.wraps, wrapped)
				p.pads = append(p.pads, padded)
			})
		}
		w.RUnlockIO()
	}
	var local uint64
	if w.Terminal != nil {
		local = w.Terminal.ScrollbackEpoch()
	}

	r.mu.Lock()
	delete(r.fetching, idx)
	switch {
	case err != nil:
		if r.failed == nil {
			r.failed = map[int64]time.Time{}
		}
		r.failed[idx] = time.Now()
	case page == nil:
		r.known = false
		r.dropPages()
	default:
		if page.Epoch != r.epoch {
			r.dropPages()
			r.epoch = page.Epoch
		}
		r.daemonBase, r.known, r.localEpoch = page.Base, true, local
		if page.Epoch == epoch {
			r.keepLocked(idx, p)
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

// wakeRenderer asks for a frame, as new output does.
func (w *Window) wakeRenderer() {
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

// dropPages forgets every page.
func (r *remoteHistory) dropPages() {
	r.pages, r.used, r.failed = nil, nil, nil
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
