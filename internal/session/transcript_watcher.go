package session

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// TranscriptWatcher turns filesystem notification into a callback per joined
// transcript file.
//
// # Why a directory and not a file
//
// The unit of watching is the directory the file sits in, not the file. On
// Linux, inotify on a directory reports modifications to the files inside it and
// names the file in the event, so one watch covers every transcript in a
// project. Forty panes spread across twelve projects is twelve watches rather
// than forty, and twelve is free against a default fs.inotify.max_user_watches
// of 8192 at the very lowest and 524288 on most machines. Directories are
// refcounted so the twelfth pane in a project adds nothing.
//
// It also survives the file being replaced. A harness that rotates its session
// file writes a new inode at a path a file watch would no longer be following;
// the directory watch reports it either way.
//
// # When the directory goes away
//
// The kernel drops a directory's watch when the directory is deleted or moved,
// but the refcount here still counts the joins in it. Without more, a
// directory made again at the same path is never watched again: the next join
// there is not the first, so it adds nothing, and the joins already there wait
// for events that never come. So a directory the kernel dropped is marked
// lost, and its parent is watched until the directory comes back. The parent's
// Create event re-adds the directory and wakes every join in it, because their
// files may have been written before the watch was back. A join in a lost
// directory re-adds it too. The parent watch exists only while a directory is
// lost, and it is event-driven like the rest, with no timer.
//
// # What it costs when nothing is happening
//
// One file descriptor and one goroutine for the whole daemon, and the goroutine
// is blocked in a channel receive. There is no ticker here and none is reachable
// from here: a session whose agents are all idle produces no events, so this
// costs no wakeups and no work. That is the whole reason notification was chosen
// over a poll.
//
// # When it is not available
//
// Every failure is degradation rather than an error. If the watcher cannot be
// created at all, or a directory cannot be added because the kernel is out of
// watches (ENOSPC) or the daemon is out of descriptors (EMFILE), Watch returns
// an error and the caller puts that join on the pane's own output instead. That
// fallback costs nothing at idle either, because a silent pane emits no output,
// and it happens to be well aimed: the moment worth catching is a turn ending,
// and a turn ends immediately after the pane paints its last chunk.
type TranscriptWatcher struct {
	mu sync.Mutex
	w  *fsnotify.Watcher
	// dirs refcounts the directories being watched, so the last file in a
	// project takes its watch with it and no earlier one does.
	dirs map[string]int
	// onChange maps an absolute file path to the joins waiting on it, by the
	// key each join watched with. Two windows may be joined to the same file,
	// which happens when a session is attached from two panes, and each one
	// counts once in dirs and is taken out alone by Unwatch.
	onChange map[string]map[string]func()
	// lost holds the watched directories whose watch the kernel dropped
	// because the directory was deleted or moved.
	lost map[string]bool
	// parents refcounts the parent directories watched for a lost directory
	// to come back.
	parents map[string]int
	closed  bool
}

// NewTranscriptWatcher starts a watcher, or reports why it could not.
//
// A daemon that gets an error here keeps running with no watcher at all, and
// every join falls back to the output-driven read. Nothing about the daemon
// requires this to succeed.
func NewTranscriptWatcher() (*TranscriptWatcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	tw := &TranscriptWatcher{
		w:        w,
		dirs:     make(map[string]int),
		onChange: make(map[string]map[string]func()),
		lost:     make(map[string]bool),
		parents:  make(map[string]int),
	}
	go tw.run()
	return tw, nil
}

// Watch registers a callback for a file under key, adding a watch on its
// directory if this is the first file there. A second Watch with the same path
// and key replaces the callback and counts nothing more.
func (t *TranscriptWatcher) Watch(path, key string, onChange func()) error {
	if t == nil {
		return errNoTranscriptWatcher
	}
	dir := filepath.Dir(path)
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errNoTranscriptWatcher
	}
	first := t.dirs[dir] == 0
	lost := t.lost[dir]
	t.mu.Unlock()

	if lost {
		// The directory was deleted and made again while joins still
		// counted it. Watch it again before counting this join in it.
		if err := t.rearm(dir); err != nil {
			return err
		}
	} else if first {
		// Added outside the lock: fsnotify's Add touches the kernel, and holding
		// the lock across it would block every other join behind one syscall.
		if err := t.w.Add(dir); err != nil {
			return err
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errNoTranscriptWatcher
	}
	fns := t.onChange[path]
	if fns == nil {
		fns = make(map[string]func())
		t.onChange[path] = fns
	}
	if _, again := fns[key]; !again {
		t.dirs[dir]++
	}
	fns[key] = onChange
	return nil
}

// Unwatch removes the callback a join registered under key, and drops the
// directory watch when it was the last one there. Another join on the same
// file keeps its callback.
func (t *TranscriptWatcher) Unwatch(path, key string) {
	if t == nil {
		return
	}
	dir := filepath.Dir(path)
	t.mu.Lock()
	fns, ok := t.onChange[path]
	if _, has := fns[key]; !ok || !has {
		t.mu.Unlock()
		return
	}
	delete(fns, key)
	if len(fns) == 0 {
		delete(t.onChange, path)
	}
	t.dirs[dir]--
	last := t.dirs[dir] <= 0
	var dropParent string
	if last {
		delete(t.dirs, dir)
		if t.lost[dir] {
			delete(t.lost, dir)
			dropParent = t.releaseParentLocked(filepath.Dir(dir))
		}
	}
	closed := t.closed
	t.mu.Unlock()
	if last && !closed {
		_ = t.w.Remove(dir)
		if dropParent != "" {
			_ = t.w.Remove(dropParent)
		}
	}
}

// markLost records that the kernel dropped the watch on dir, and watches its
// parent so the directory coming back is seen.
func (t *TranscriptWatcher) markLost(dir string) {
	parent := filepath.Dir(dir)
	t.mu.Lock()
	if t.closed || t.dirs[dir] == 0 || t.lost[dir] {
		t.mu.Unlock()
		return
	}
	t.lost[dir] = true
	t.parents[parent]++
	addParent := t.parents[parent] == 1 && t.dirs[parent] == 0
	t.mu.Unlock()
	// A moved directory may still be watched at its new place. That watch
	// reports names this watcher does not know, so it is removed.
	_ = t.w.Remove(dir)
	if addParent {
		// When the parent cannot be watched either, the directory stays
		// lost until a new join in it watches it again.
		_ = t.w.Add(parent)
	}
	// The directory may be back already, before the parent was watched.
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		t.comeBack(dir)
	}
}

// comeBack watches a lost directory again and wakes every join in it.
func (t *TranscriptWatcher) comeBack(dir string) {
	if t.rearm(dir) != nil {
		return
	}
	t.mu.Lock()
	var cbs []func()
	for path, fns := range t.onChange {
		if filepath.Dir(path) == dir {
			for _, fn := range fns {
				cbs = append(cbs, fn)
			}
		}
	}
	t.mu.Unlock()
	for _, cb := range cbs {
		cb()
	}
}

// rearm adds the watch on a lost directory again and stops watching its
// parent for it. It returns the error of the add, and nil for a directory
// that is not lost.
func (t *TranscriptWatcher) rearm(dir string) error {
	if err := t.w.Add(dir); err != nil {
		return err
	}
	t.mu.Lock()
	var dropParent string
	if t.lost[dir] {
		delete(t.lost, dir)
		dropParent = t.releaseParentLocked(filepath.Dir(dir))
	}
	t.mu.Unlock()
	if dropParent != "" {
		_ = t.w.Remove(dropParent)
	}
	return nil
}

// releaseParentLocked drops one count on a parent watch and returns the
// parent when its watch is to be removed. Called with the mutex held.
func (t *TranscriptWatcher) releaseParentLocked(parent string) string {
	t.parents[parent]--
	if t.parents[parent] > 0 {
		return ""
	}
	delete(t.parents, parent)
	if t.dirs[parent] > 0 {
		return ""
	}
	return parent
}

// Close stops the watcher.
func (t *TranscriptWatcher) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	t.onChange = nil
	t.dirs = nil
	t.lost = nil
	t.parents = nil
	t.mu.Unlock()
	return t.w.Close()
}

// run drains the event channel. It blocks here for the daemon's whole life on a
// machine whose agents are idle, which is what "no polling" means in practice.
func (t *TranscriptWatcher) run() {
	for {
		select {
		case ev, ok := <-t.w.Events:
			if !ok {
				return
			}
			// Chmod alone is not a content change, and reacting to it would wake
			// a read for every backup tool that touches the directory.
			if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			t.mu.Lock()
			var cbs []func()
			for _, fn := range t.onChange[ev.Name] {
				cbs = append(cbs, fn)
			}
			watched := t.dirs[ev.Name] > 0
			lost := t.lost[ev.Name]
			t.mu.Unlock()
			switch {
			case watched && !lost && ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0:
				// The watched directory itself went away.
				t.markLost(ev.Name)
			case lost && ev.Op&(fsnotify.Create|fsnotify.Rename) != 0:
				// A parent reports the lost directory made again, or moved
				// back.
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					t.comeBack(ev.Name)
				}
			}
			for _, cb := range cbs {
				cb()
			}
		case _, ok := <-t.w.Errors:
			// Errors are drained and dropped. An fsnotify error names the path it
			// happened on, and that path is the user's project and session, so it
			// is not written anywhere. Draining matters more than reporting: the
			// channel is unbuffered, and leaving it full stops events.
			if !ok {
				return
			}
		}
	}
}

// errNoTranscriptWatcher says notification is unavailable, which puts a join on
// the output-driven fallback rather than failing it.
var errNoTranscriptWatcher = watcherUnavailable{}

type watcherUnavailable struct{}

func (watcherUnavailable) Error() string { return "no transcript watcher" }
