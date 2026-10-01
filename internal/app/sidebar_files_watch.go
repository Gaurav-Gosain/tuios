package app

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/session"
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
// A folder on this machine is watched here. A folder on another machine, the
// listing of a pane on another host, is on a disk this process cannot see, so
// the daemon is asked to watch it and push a change (session/daemon_dirwatch.go).
// The push lands on the same channel the local watch uses, so the two are read
// again the same way. A daemon too old to offer the watch leaves such a listing
// as it was before: read again when the pane changes directory.

// fileWatchSettle is how long a change is left to settle before the folder is
// read again. A `git checkout` or an `rm -r` is thousands of events, and the
// listing only needs the state they leave behind. It is spent only after a
// change, never on an idle client.
const fileWatchSettle = 100 * time.Millisecond

// fileWatchGap is the least time between two reads of a changed folder. A
// folder that keeps changing, under a build or an editor's swap files, is
// read at most twice a second, and once more after the last change.
const fileWatchGap = 500 * time.Millisecond

// fileDirChangedMsg says the watched folder's entries changed.
type fileDirChangedMsg struct {
	// at is when the listener let the change through. The next listener
	// waits fileWatchGap from it.
	at time.Time
}

// fileWatcher holds the one directory watch a client keeps. Setting it up and
// tearing it down are syscalls on a path that can be a hung network mount, so
// both happen off the update goroutine; the update goroutine only records what
// it wants.
type fileWatcher struct {
	// want is what the update goroutine last asked for. Read and written only
	// there, so a sync that changes nothing costs one comparison.
	want fileWatchSpec

	ch chan struct{}

	mu  sync.Mutex // serialises retargeting, and guards the fields below
	dir string
	w   *dirwatch.Watcher
	// remote is the daemon watching dir for this client, nil when the watch is
	// local or there is none.
	remote *session.TUIClient
	// latest is the most recent target asked for, so a slow setup for a folder
	// the user has already left does not install itself over a newer one.
	latest fileWatchSpec
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
// settle, and hands one message to the loop. It waits at least fileWatchGap
// after last, the previous message. A change made while it waits is covered
// by the read that follows, and a change after that wakes the next listener,
// so a burst always ends with one read of the final state.
func listenForFileChange(ch chan struct{}, last time.Time) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		time.Sleep(max(fileWatchSettle, time.Until(last.Add(fileWatchGap))))
		select {
		case <-ch:
		default:
		}
		return fileDirChangedMsg{at: time.Now()}
	}
}

// fileWatchSpec is where the watch should be: a folder, and the daemon to ask
// when the folder is not on this machine.
type fileWatchSpec struct {
	dir string
	// remote is the daemon to ask, nil for a folder on this machine.
	remote *session.TUIClient
	// origin is the pane the folder was listed for, which tells the daemon
	// which machine the folder is on.
	origin string
}

// fileWatchTarget is where the watch should be: the folder the section is
// showing, when it was read cleanly. A folder on another machine is watched by
// the daemon, when there is one to ask.
func (m *OS) fileWatchTarget() fileWatchSpec {
	v := m.filesView
	if v.Dir == "" || v.Err != "" {
		return fileWatchSpec{}
	}
	if v.Host == "" && m.AttachedHost == "" {
		return fileWatchSpec{dir: v.Dir}
	}
	if m.DaemonClient == nil {
		return fileWatchSpec{}
	}
	return fileWatchSpec{dir: v.Dir, remote: m.DaemonClient, origin: v.Origin}
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

func (fw *fileWatcher) retarget(target fileWatchSpec, ch chan struct{}) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped || target != fw.latest {
		return
	}
	if fw.w != nil {
		fw.w.Close()
		fw.w = nil
	}
	if fw.remote != nil && fw.remote != target.remote {
		// The daemon keeps one watch per connection, so a new remote target
		// replaces the old one by itself. Only a move off that daemon has to
		// end it.
		_, _ = fw.remote.WatchDir("", "")
	}
	fw.dir, fw.remote = "", nil
	dir := target.dir
	if dir == "" {
		return
	}
	if target.remote != nil {
		if offered, err := target.remote.WatchDir(target.origin, dir); offered && err == nil {
			fw.dir, fw.remote = dir, target.remote
		}
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
	if fw.remote != nil {
		_, _ = fw.remote.WatchDir("", "")
		fw.remote, fw.dir = nil, ""
	}
	if fw.ch != nil {
		close(fw.ch)
	}
}

// remoteDirChanged takes the daemon's push that dir changed. It runs on the
// daemon client's read goroutine, so it only signals, and only for the folder
// the daemon is watching for this client now.
func (fw *fileWatcher) remoteDirChanged(dir string) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.stopped || fw.remote == nil || fw.dir != dir || fw.ch == nil {
		return
	}
	select {
	case fw.ch <- struct{}{}:
	default:
	}
}

// refreshChangedFolder reads the listed folder again because it changed on
// disk. It is a quiet read: the names stay up, no "loading" row is drawn, the
// scroll position is kept, and no state the rail draws changes until a
// different listing comes back, because nothing the user did asked for it.
func (m *OS) refreshChangedFolder() tea.Cmd {
	v := m.filesView
	if v.Dir == "" || v.Want != v.Dir || m.fileWatchTarget().dir == "" {
		// Nothing listed, a walk to another folder in flight, or a listing
		// this client did not read from its own disk.
		return nil
	}
	if v.Loading {
		// A read the user asked for is in flight, and may have read the
		// folder before this change. It is asked again, and stays the read
		// the user sees.
		return m.readFileList(v.Dir, v.Origin, v.Pinned)
	}
	m.filesView.QuietReq++
	return m.fileListCmd(v.Dir, v.Origin, v.Pinned, m.filesView.QuietReq)
}
