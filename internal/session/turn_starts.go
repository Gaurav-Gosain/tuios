package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Turn starts: the work tree when an agent's turn starts, for who-made-each-
// line (review_origins.go). A checkpoint is the work tree when a turn ends,
// so the lines that changed between one turn's end and the next turn's start
// are the person's, or a program's, and not the agent's. A snapshot when a
// turn starts tells them apart.
//
// Most turns start right after the last ended, with nothing changed between,
// so the snapshot is taken only when something did change. The last
// checkpoint, or turn start, of a work tree keeps the index its tree was
// written from (worktree.Stamp), and a turn start asks git whether any file
// differs from it, from file sizes and times alone. Only then is the work
// tree written as a tree. A turn start writes no ref: it is kept in the
// daemon's memory, and lost with it, which only makes the review credit
// those lines to the next turn again.

// turnStartsKept bounds the turn starts kept for one work tree.
const turnStartsKept = 64

// turnStart is the work tree when a pane's turn started, where it differed
// from the last snapshot.
type turnStart struct {
	pane, session string
	turn          uint64
	tree          string
	// at is when the snapshot started, and snapped when it ended, in Unix
	// nanoseconds.
	at, snapped int64
	// late are the paths of tree that were written after the turn began:
	// the snapshot waits for the daemon's git lock, and the agent may write
	// before it runs.
	late []string
}

// turnMarks holds the stamps and the turn starts, by work tree root.
type turnMarks struct {
	mu     sync.Mutex
	stamps map[string]*worktree.Stamp
	starts map[string][]turnStart
}

// setStamp makes st the work tree's newest stamp.
func (t *turnMarks) setStamp(root string, st *worktree.Stamp) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stamps == nil {
		t.stamps = map[string]*worktree.Stamp{}
	}
	if old := t.stamps[root]; old != nil && old != st {
		old.Close()
	}
	t.stamps[root] = st
}

// stamp is the work tree's newest stamp, nil when it has none.
func (t *turnMarks) stamp(root string) *worktree.Stamp {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stamps[root]
}

// addStart records a turn start.
func (t *turnMarks) addStart(root string, s turnStart) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.starts == nil {
		t.starts = map[string][]turnStart{}
	}
	list := append(t.starts[root], s)
	if len(list) > turnStartsKept {
		list = list[len(list)-turnStartsKept:]
	}
	t.starts[root] = list
}

// startsOf is a copy of the work tree's turn starts, oldest first.
func (t *turnMarks) startsOf(root string) []turnStart {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]turnStart(nil), t.starts[root]...)
}

// close drops every stamp, when the daemon stops.
func (t *turnMarks) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, st := range t.stamps {
		st.Close()
	}
	t.stamps = nil
}

// turnStartWanted reports whether ev starts a turn.
func turnStartWanted(ev SessionEvent) bool {
	return ev.Type == EventAgentState && AgentState(ev.State) == AgentStateWorking && ev.hookPrevState != string(AgentStateWorking)
}

// noteTurnStart marks a turn start for a window whose agent just started
// one. Like noteCheckpointEvent it runs in the event sink, so the git work
// goes to a tracked goroutine.
func (d *Daemon) noteTurnStart(s *Session, ev SessionEvent) {
	if d.checkpoints.off.Load() || !turnStartWanted(ev) {
		return
	}
	window, turn, began := ev.Window, ev.completionSeq+1, time.Now().UnixNano()
	d.goTracked(func() { d.takeTurnStart(s, window, turn, began) })
}

// takeTurnStart snapshots the window's work tree when it changed since its
// last stamp, and records the snapshot as the turn's start. began is when
// the turn started.
func (d *Daemon) takeTurnStart(s *Session, window string, turn uint64, began int64) {
	if d.ctx.Err() != nil {
		return
	}
	var target WindowState
	found := false
	d.forWindowState(s, window, func(w WindowState) { target, found = w, true })
	if !found {
		return
	}
	repo, verr := d.paneRepo(s, target)
	if verr != nil {
		return
	}
	ctx, cancel := context.WithTimeout(d.ctx, checkpointTimeout)
	defer cancel()
	d.checkpoints.gitMu.Lock()
	defer d.checkpoints.gitMu.Unlock()
	start := time.Now()
	last := d.turnMarks.stamp(repo.root)
	changed := true
	if last != nil {
		c, err := last.Changed(ctx, repo.root)
		if err != nil {
			LogBasic("Turn start of window %s in %s: the check failed: %v", shortWindowID(window), repo.root, err)
			return
		}
		changed = c
	}
	checked := time.Since(start)
	if !changed {
		LogBasic("Turn start of window %s in %s: no change since the last snapshot, checked in %s", shortWindowID(window), repo.root, checked.Round(time.Microsecond))
		return
	}
	st, _, err := worktree.SnapshotStamp(ctx, repo.root, d.checkpoints.maxUntracked.Load())
	if err != nil {
		if !errors.Is(err, worktree.ErrNoStamp) {
			LogBasic("Turn start of window %s in %s: the snapshot failed: %v", shortWindowID(window), repo.root, err)
		}
		return
	}
	if last == nil || st.Tree != last.Tree {
		from := "HEAD"
		if last != nil {
			from = last.Tree
		}
		late := writtenSince(ctx, repo.root, from, st.Tree, began)
		d.turnMarks.addStart(repo.root, turnStart{pane: window, session: s.Name(), turn: turn, tree: st.Tree, at: start.UnixNano(), snapped: time.Now().UnixNano(), late: late})
	}
	d.turnMarks.setStamp(repo.root, st)
	LogBasic("Turn start of window %s in %s: changed, checked in %s, snapshot in %s", shortWindowID(window), repo.root, checked.Round(time.Microsecond), (time.Since(start) - checked).Round(time.Millisecond))
}

// writtenSince are the paths that differ between from and tree whose file was
// last modified at or after began, in Unix nanoseconds. A file written before
// the turn began never reads as later than began.
func writtenSince(ctx context.Context, root, from, tree string, began int64) []string {
	paths, err := worktree.ChangedPaths(ctx, root, from, tree)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range paths {
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if err == nil && fi.ModTime().UnixNano() >= began {
			out = append(out, p)
		}
	}
	return out
}
