package app

import (
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// # The live review
//
// While the review is open its diff is read again when a file in it changes
// on disk. Nothing polls: the kernel is asked to say when something the diff
// shows can have moved, and the client sleeps until it does. Nothing is
// watched while the review is closed.
//
// What is watched, through one dirwatch content set:
//
//   - the work tree's root, and the folder of each changed file: a file made,
//     removed or renamed there, or a file there written and closed;
//   - each changed file itself where the platform's folder watch does not
//     see writes (kqueue);
//   - the git folders gitstate.WatchDirs names: a commit, a stage or a
//     checkout changes what the diff is against.
//
// A change is let settle (dirwatch.Settle), and then the diff is read again,
// the whole of it, as r does: review-diff writes the work tree as one tree,
// so a read of one path costs the same. The reads are paced by their own
// cost: the next starts no sooner than reviewLiveGap after the last ended, or
// four times as long as the last took, whichever is longer. A work tree an
// agent writes to without a pause is read a few times a second at most, and a
// large one less often.
//
// A read never moves the cursor or the scroll: the row under the cursor is
// found again in the new rows and kept at the same place on screen
// (reviewLiveAnchor). A read is held while a note is being typed, and runs
// once the note is saved or dropped.
//
// The client is on the machine of the repository: the review refuses a pane
// on another machine, and a client attached to another machine's session.

// reviewLiveGap is the least time between the end of one read and the start
// of the next.
const reviewLiveGap = 150 * time.Millisecond

// reviewLiveCostShare is how many times the last read's own time the next
// read waits, at least.
const reviewLiveCostShare = 4

// reviewLiveHold is how often a change held for an open note editor looks
// again whether the editor closed. It runs only while both are so.
const reviewLiveHold = 300 * time.Millisecond

// reviewLiveMax bounds the folders and the files one review watches.
const reviewLiveMax = 256

// reviewLive is the live review's watch. Setting it up and tearing it down are
// syscalls on paths that can be hung mounts, so both run off the update
// goroutine, in commands.
type reviewLive struct {
	mu   sync.Mutex
	set  *dirwatch.Set
	gen  uint64
	key  string
	ch   chan struct{}
	stop chan struct{}
}

// reviewLiveState is what the review knows of its watch and its reads.
type reviewLiveState struct {
	// on is set while a watch is up for this review, and off when none could
	// be made. Neither is set until the first setup answers.
	on, off bool
	// key is the watch's targets, joined, so a read that changes no file
	// does not set the watch up again.
	key string
	// reading is set while a read runs, again when a change came in during
	// it, and held when a change waits for the note editor to close.
	reading, again, held bool
	// started and lastEnd time the reads, and lastTook is the last one's
	// cost, which paces the next.
	started, lastEnd time.Time
	lastTook         time.Duration
}

// ReviewLiveReadyMsg says whether a review's watch is up.
type ReviewLiveReadyMsg struct {
	Gen uint64
	Key string
	OK  bool
}

// ReviewLiveChangedMsg says something the review shows changed on disk, or,
// with Held, that a change held for the note editor should look again.
type ReviewLiveChangedMsg struct {
	Gen  uint64
	Held bool
}

// reviewLiveTargets are the folders and files the review watches, and their
// key. The review of two attempts against each other is not watched.
func (m *OS) reviewLiveTargets() (dirs, files []string, key string) {
	r := &m.review
	d := r.diff
	if d == nil || d.Worktree == "" || d.Against != "" {
		return nil, nil, ""
	}
	root := d.Worktree
	seen := map[string]bool{}
	add := func(dir string) {
		if dir != "" && !seen[dir] && len(dirs) < reviewLiveMax {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	add(root)
	if gd, ok := gitstate.WatchDirs(root); ok {
		for _, dir := range gd {
			add(dir)
		}
	}
	for _, f := range d.Files {
		p := filepath.Join(root, filepath.FromSlash(f.Path))
		add(filepath.Dir(p))
		if len(files) < reviewLiveMax {
			files = append(files, p)
		}
	}
	slices.Sort(files)
	return dirs, files, strings.Join(dirs, "\x00") + "\x01" + strings.Join(files, "\x00")
}

// reviewLiveSync points the watch at what the diff shows, after a read. It
// returns the command that sets the watch up, nil when it is up already.
func (m *OS) reviewLiveSync() tea.Cmd {
	r := &m.review
	if !r.open || m.Client == ClientUnknown {
		return nil
	}
	dirs, files, key := m.reviewLiveTargets()
	if key == r.live.key && (r.live.on || r.live.off) {
		return nil
	}
	r.live.key = key
	gen := r.gen
	lv := &m.reviewLive
	return func() tea.Msg {
		ok := lv.retarget(gen, key, dirs, files)
		return ReviewLiveReadyMsg{Gen: gen, Key: key, OK: ok}
	}
}

// retarget replaces the watch with one on dirs and files, for review gen. It
// reports whether the work tree's root is watched. A review with nothing to
// watch has no watch.
func (lv *reviewLive) retarget(gen uint64, key string, dirs, files []string) bool {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.set != nil {
		lv.set.Close()
		lv.set = nil
	}
	if lv.gen != gen || lv.ch == nil {
		lv.closeStop()
		lv.gen, lv.ch, lv.stop = gen, make(chan struct{}, 1), make(chan struct{})
	}
	lv.key = key
	if len(dirs) == 0 {
		return false
	}
	ch := lv.ch
	set, err := dirwatch.NewContentSet(func(string) { dirwatch.Nudge(ch) })
	if err != nil {
		return false
	}
	if set.Add(dirs[0]) != nil {
		set.Close()
		return false
	}
	for _, dir := range dirs[1:] {
		// A folder of a deleted file is gone, and its parent is watched.
		_ = set.Add(dir)
	}
	for _, f := range files {
		_ = set.AddFile(f)
	}
	lv.set = set
	return true
}

// closeStop ends the listener of the last review. Callers hold mu.
func (lv *reviewLive) closeStop() {
	if lv.stop != nil {
		close(lv.stop)
		lv.stop = nil
	}
}

// reviewLiveStop ends the watch, when the review closes or another opens.
// The watch's own goroutine is ended off the update goroutine.
func (m *OS) reviewLiveStop() {
	lv := &m.reviewLive
	go func() {
		lv.mu.Lock()
		defer lv.mu.Unlock()
		if lv.set != nil {
			lv.set.Close()
			lv.set = nil
		}
		lv.closeStop()
		lv.ch, lv.key = nil, ""
	}()
}

// reviewLiveListen waits for a change for review gen, lets the burst settle,
// and reports it.
func (m *OS) reviewLiveListen(gen uint64) tea.Cmd {
	lv := &m.reviewLive
	lv.mu.Lock()
	ch, stop := lv.ch, lv.stop
	lv.mu.Unlock()
	if ch == nil || stop == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case <-ch:
		case <-stop:
			return nil
		}
		if !dirwatch.AfterBurst(ch, stop) {
			return nil
		}
		return ReviewLiveChangedMsg{Gen: gen}
	}
}

// applyReviewLiveReady records whether the watch is up, and starts the
// listener the first time it is.
func (m *OS) applyReviewLiveReady(msg ReviewLiveReadyMsg) tea.Cmd {
	r := &m.review
	if msg.Gen != r.gen || !r.open || msg.Key != r.live.key {
		return nil
	}
	was := r.live.on
	r.live.on, r.live.off = msg.OK, !msg.OK
	if msg.OK && !was {
		return m.reviewLiveListen(msg.Gen)
	}
	return nil
}

// applyReviewLiveChanged reads the diff again for a change, as soon as the
// pace allows, and listens for the next.
func (m *OS) applyReviewLiveChanged(msg ReviewLiveChangedMsg) tea.Cmd {
	r := &m.review
	if msg.Gen != r.gen || !r.open {
		return nil
	}
	// A change from the watch took the listener, so the next is armed. A
	// held change's tick is not the listener's.
	var listen tea.Cmd
	if !msg.Held {
		listen = m.reviewLiveListen(msg.Gen)
	}
	return tea.Batch(listen, m.reviewLiveRead())
}

// reviewLiveRead starts a read, or holds it: until the note editor closes,
// until the read in flight ends, or until the pace allows.
func (m *OS) reviewLiveRead() tea.Cmd {
	r := &m.review
	gen := r.gen
	switch {
	case r.editor != nil:
		r.live.held = true
		return tea.Tick(reviewLiveHold, func(time.Time) tea.Msg { return ReviewLiveChangedMsg{Gen: gen, Held: true} })
	case r.live.reading:
		r.live.again = true
		return nil
	}
	r.live.held = false
	wait := max(reviewLiveGap, reviewLiveCostShare*r.live.lastTook)
	if until := time.Until(r.live.lastEnd.Add(wait)); until > 0 {
		r.live.reading = true
		return tea.Tick(until, func(time.Time) tea.Msg { return reviewLiveGoMsg{gen: gen} })
	}
	return m.reviewLiveStart()
}

// reviewLiveGoMsg starts a read the pace held back.
type reviewLiveGoMsg struct{ gen uint64 }

// reviewLiveStart reads the diff again, quietly: the header does not say
// "reading", and the answer keeps the place (applyReviewDiff with Live).
func (m *OS) reviewLiveStart() tea.Cmd {
	r := &m.review
	r.live.reading, r.live.again = true, false
	r.live.started = time.Now()
	cmd := m.reviewLoadCmd(r.query, false)
	return func() tea.Msg {
		msg := cmd()
		if d, ok := msg.(ReviewDiffMsg); ok {
			d.Live = true
			return d
		}
		return msg
	}
}

// applyReviewLiveGo starts the read the pace held back.
func (m *OS) applyReviewLiveGo(msg reviewLiveGoMsg) tea.Cmd {
	r := &m.review
	if msg.gen != r.gen || !r.open {
		return nil
	}
	r.live.reading = false
	return m.reviewLiveRead()
}

// reviewLiveDone ends a read: it times it, and starts the next when a change
// came in during it.
func (m *OS) reviewLiveDone() tea.Cmd {
	r := &m.review
	r.live.reading = false
	r.live.lastEnd = time.Now()
	r.live.lastTook = r.live.lastEnd.Sub(r.live.started)
	if r.live.again {
		r.live.again = false
		return m.reviewLiveRead()
	}
	return nil
}

// reviewLiveAnchor is the row under the cursor, as a later read can find it
// again, and where on screen it is.
type reviewLiveAnchor struct {
	path   string
	kind   reviewRowKind
	op     string
	text   string
	note   string
	at     int
	screen int
	ok     bool
}

// reviewLiveMark takes the anchor of the row under the cursor.
func (m *OS) reviewLiveMark() reviewLiveAnchor {
	r := &m.review
	e := r.currentFile()
	rows := m.reviewRows(m.reviewRowsWidth())
	if e == nil || r.cursor < 0 || r.cursor >= len(rows) {
		return reviewLiveAnchor{}
	}
	row := rows[r.cursor]
	a := reviewLiveAnchor{path: e.path, kind: row.kind, at: r.cursor, screen: r.cursor - r.scroll, ok: true}
	switch {
	case row.kind == reviewRowNote && row.note >= 0 && row.note < len(r.notes):
		a.note = r.notes[row.note].ID
	case e.file != nil && row.hunk >= 0 && row.hunk < len(e.file.Hunks):
		h := e.file.Hunks[row.hunk]
		l := row.line
		if row.kind == reviewRowHunk && len(h.Lines) > 0 {
			l = 0
		}
		if l >= 0 && l < len(h.Lines) {
			a.op, a.text = h.Lines[l].Op, h.Lines[l].Text
		}
	}
	return a
}

// reviewLiveKeep puts the cursor back on the anchor's row, at the same place
// on screen. The row nearest the old one wins when the text is on several. A
// row that is gone leaves the cursor where it was, on the rows there are.
func (m *OS) reviewLiveKeep(a reviewLiveAnchor) {
	r := &m.review
	if !a.ok {
		return
	}
	e := r.currentFile()
	rows := m.reviewRows(m.reviewRowsWidth())
	if e == nil || e.path != a.path || len(rows) == 0 {
		return
	}
	best := -1
	for i, row := range rows {
		match := false
		switch {
		case a.note != "":
			match = row.kind == reviewRowNote && row.first && row.note >= 0 && row.note < len(r.notes) && r.notes[row.note].ID == a.note
		case a.text != "" || a.op != "":
			if row.kind != a.kind || e.file == nil || row.hunk < 0 || row.hunk >= len(e.file.Hunks) {
				break
			}
			h := e.file.Hunks[row.hunk]
			l := row.line
			if row.kind == reviewRowHunk && len(h.Lines) > 0 {
				l = 0
			}
			match = l >= 0 && l < len(h.Lines) && h.Lines[l].Op == a.op && h.Lines[l].Text == a.text
		}
		if match && (best < 0 || absInt(i-a.at) < absInt(best-a.at)) {
			best = i
		}
	}
	if best < 0 {
		best = min(a.at, len(rows)-1)
	}
	r.cursor = best
	r.scroll = max(best-a.screen, 0)
}

// reviewLiveDot is the mark before live, filled when the review is live.
func reviewLiveDot(on bool) string {
	switch {
	case overlay.UseASCII() && on:
		return "*"
	case overlay.UseASCII():
		return "o"
	case on:
		return "●"
	}
	return "○"
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
