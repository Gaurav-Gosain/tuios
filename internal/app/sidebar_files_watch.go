package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
)

// # Keeping the listing true to the disk
//
// The section used to read a folder when the focused pane's directory changed
// and at no other time, so a file deleted in the pane stayed on the rail until
// the user moved away and back (issue #313). The answer is not a poll: a poll
// is filesystem work on a client that is doing nothing. The kernel is asked to
// say when the listed folder's entries change, and the client sleeps until it
// does. An idle client with the rail open does no work at all, as before.
//
// Only a folder on this machine is watched. The client and its daemon share a
// disk, so a local session's listing is one this process can watch. A pane on
// another host lists a disk this process cannot see, and keeps the old
// behaviour: it is read again when the pane changes directory.

// fileWatchSettle is how long a change is left to settle before the folder is
// read again. A `git checkout` or an `rm -r` is thousands of events, and the
// listing only needs the state they leave behind. It is spent only after a
// change, never on an idle client.
const fileWatchSettle = 100 * time.Millisecond

// fileDirChangedMsg says the watched folder's entries changed.
type fileDirChangedMsg struct{}

// fileWatcher holds the one directory watch a client keeps. Setting it up and
// tearing it down are syscalls on a path that can be a hung network mount, so
// both happen off the update goroutine; the update goroutine only records what
// it wants.
type fileWatcher struct {
	// want is what the update goroutine last asked for. Read and written only
	// there, so a sync that changes nothing costs one string comparison.
	want string

	ch chan struct{}

	mu  sync.Mutex // serialises retargeting, and guards the fields below
	dir string
	w   *dirwatch.Watcher
	// latest is the most recent target asked for, so a slow setup for a folder
	// the user has already left does not install itself over a newer one.
	latest string
	// stopped is set when the client exits. The channel is closed then, so no
	// watch may be made after it.
	stopped bool
}

func (m *OS) fileWatchChan() chan struct{} {
	if m.fileWatch.ch == nil {
		m.fileWatch.ch = make(chan struct{}, 1)
	}
	return m.fileWatch.ch
}

// listenForFileChange waits for the watched folder to change, lets the burst
// settle, and hands one message to the loop.
func listenForFileChange(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		time.Sleep(fileWatchSettle)
		select {
		case <-ch:
		default:
		}
		return fileDirChangedMsg{}
	}
}

// fileWatchTarget is the folder the watch should be on: the one the section is
// showing, when it was read from this machine's disk and read cleanly.
func (m *OS) fileWatchTarget() string {
	v := m.filesView
	if v.Dir == "" || v.Err != "" || v.Host != "" || m.AttachedHost != "" {
		return ""
	}
	return v.Dir
}

// syncFileWatch points the watch at fileWatchTarget. It is called wherever the
// listing on screen changes, and does nothing when the target is unchanged.
func (m *OS) syncFileWatch() {
	target := m.fileWatchTarget()
	fw := &m.fileWatch
	if target == fw.want {
		return
	}
	fw.want = target
	ch := m.fileWatchChan()
	fw.mu.Lock()
	fw.latest = target
	fw.mu.Unlock()
	go fw.retarget(target, ch)
}

func (fw *fileWatcher) retarget(dir string, ch chan struct{}) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped || dir != fw.latest || dir == fw.dir {
		return
	}
	if fw.w != nil {
		fw.w.Close()
		fw.w, fw.dir = nil, ""
	}
	if dir == "" {
		return
	}
	w, err := dirwatch.Watch(dir, func() {
		select {
		case ch <- struct{}{}:
		default: // a change is already pending, and one read covers both
		}
	})
	if err != nil {
		// No watch is no worse than before this existed: the listing is read
		// again when the pane changes directory.
		return
	}
	fw.w, fw.dir = w, dir
}

// stopFileWatch closes the watch for good, when the client exits. Closing the
// channel ends the listener, which would otherwise outlive an SSH or web
// session as a parked goroutine.
func (m *OS) stopFileWatch() {
	fw := &m.fileWatch
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped {
		return
	}
	fw.stopped = true
	if fw.w != nil {
		fw.w.Close()
		fw.w, fw.dir = nil, ""
	}
	if fw.ch != nil {
		close(fw.ch)
	}
}

// refreshChangedFolder reads the listed folder again because it changed on
// disk. It is a quiet read: the names stay up, no "loading" row is drawn, and
// the scroll position is kept, because nothing the user did asked for it.
func (m *OS) refreshChangedFolder() tea.Cmd {
	v := m.filesView
	if v.Dir == "" || v.Want != v.Dir || m.fileWatchTarget() == "" {
		// Nothing listed, a walk to another folder in flight, or a listing
		// this client did not read from its own disk.
		return nil
	}
	quiet := !v.Loading || v.Quiet
	cmd := m.readFileList(v.Dir, v.Origin, v.Pinned)
	m.filesView.Quiet = quiet
	return cmd
}
