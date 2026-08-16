package session

import (
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
	// onChange maps an absolute file path to the joins waiting on it. It is a
	// slice because two windows may be joined to the same file, which happens
	// when a session is attached from two panes.
	onChange map[string][]func()
	closed   bool
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
		onChange: make(map[string][]func()),
	}
	go tw.run()
	return tw, nil
}

// Watch registers a callback for a file, adding a watch on its directory if this
// is the first file there.
func (t *TranscriptWatcher) Watch(path string, onChange func()) error {
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
	t.mu.Unlock()

	if first {
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
	t.dirs[dir]++
	t.onChange[path] = append(t.onChange[path], onChange)
	return nil
}

// Unwatch removes every callback for a file and drops the directory watch when
// it was the last file there.
func (t *TranscriptWatcher) Unwatch(path string) {
	if t == nil {
		return
	}
	dir := filepath.Dir(path)
	t.mu.Lock()
	if _, ok := t.onChange[path]; !ok {
		t.mu.Unlock()
		return
	}
	delete(t.onChange, path)
	t.dirs[dir]--
	last := t.dirs[dir] <= 0
	if last {
		delete(t.dirs, dir)
	}
	closed := t.closed
	t.mu.Unlock()
	if last && !closed {
		_ = t.w.Remove(dir)
	}
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
			cbs := append([]func(){}, t.onChange[ev.Name]...)
			t.mu.Unlock()
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
