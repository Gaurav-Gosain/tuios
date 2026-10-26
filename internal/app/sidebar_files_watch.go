package app

import (
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// # Keeping the listing true to the disk
//
// Reading a folder only when the focused pane's directory changes would leave
// a file deleted in the pane on the rail until the user moves away and back.
// The answer is not a poll: a poll is filesystem work on a client that is
// doing nothing. The kernel is asked to say when the listed folder's entries
// change, and the client sleeps until it does. An idle client with the rail
// open does no work at all.
//
// A folder on this machine is watched here. A folder on another machine, the
// listing of a pane on another host, is on a disk this process cannot see, so
// the daemon is asked to watch it and push a change (session/daemon_dirwatch.go).
// The push lands on the same channel the local watch uses, so the two are read
// again the same way. A daemon too old to offer the watch leaves such a listing
// read again only when the pane changes directory.

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

	// The tree's open folders, watched through one dirwatch.Set. treeWant is
	// what the update goroutine last asked for, joined, and is read and
	// written only there. treeSeq orders the retargets, like latest does for
	// the root. set and setDirs are guarded by mu.
	treeWant string
	treeSeq  uint64
	set      *dirwatch.Set
	setDirs  map[string]bool
	// treeMu serialises the goroutines that add and remove the set's
	// watches. Those are syscalls on paths that can be hung mounts, so they
	// run under treeMu and never under mu, which the daemon's push and the
	// client's exit also take.
	treeMu sync.Mutex

	// dmu guards what changed since the loop last asked: the root, and the
	// open folders by path. The watch goroutines write it, so it has a lock
	// of its own and is never held while mu is taken.
	dmu       sync.Mutex
	dirtyRoot bool
	dirty     map[string]struct{}
}

// markRootDirty records that the root's entries changed.
func (fw *fileWatcher) markRootDirty() {
	fw.dmu.Lock()
	fw.dirtyRoot = true
	fw.dmu.Unlock()
}

// markDirty records that an open folder's entries changed.
func (fw *fileWatcher) markDirty(dir string) {
	fw.dmu.Lock()
	if fw.dirty == nil {
		fw.dirty = map[string]struct{}{}
	}
	fw.dirty[dir] = struct{}{}
	fw.dmu.Unlock()
}

// takeDirty answers what changed since the last call, and forgets it.
func (fw *fileWatcher) takeDirty() (root bool, dirs []string) {
	fw.dmu.Lock()
	defer fw.dmu.Unlock()
	root = fw.dirtyRoot
	fw.dirtyRoot = false
	for d := range fw.dirty {
		dirs = append(dirs, d)
	}
	clear(fw.dirty)
	return root, dirs
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
func listenForFileChange(stop <-chan struct{}, ch chan struct{}, last time.Time) tea.Cmd {
	return func() tea.Msg {
		select {
		case _, ok := <-ch:
			if !ok {
				return nil
			}
		case <-stop:
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
		fw.markRootDirty()
		select {
		case ch <- struct{}{}:
		default: // a change is already pending, and one read covers both
		}
	})
	if err != nil {
		// Without a watch the listing is still read again when the pane
		// changes directory.
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
	if fw.set != nil {
		fw.set.Close()
		fw.set, fw.setDirs = nil, nil
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
	fw.markRootDirty()
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

// syncTreeWatch points the tree's watch at the open folders on screen. Only a
// listing read from this machine's disk is watched: the daemon keeps one watch
// per connection and it is the root's, so a folder open under a root on
// another machine is read again only when it opens again or the root changes.
func (m *OS) syncTreeWatch(dirs []string) {
	if t := m.fileWatchTarget(); t.dir == "" || t.remote != nil {
		dirs = nil
	}
	key := strings.Join(dirs, "\x00")
	fw := &m.fileWatch
	if key == fw.treeWant {
		return
	}
	fw.treeWant = key
	ch := m.fileWatchChan()
	fw.mu.Lock()
	fw.treeSeq++
	seq := fw.treeSeq
	fw.mu.Unlock()
	go fw.retargetTree(slices.Clone(dirs), seq, ch)
}

// retargetTree makes the set watch exactly dirs. It runs off the update
// goroutine, because adding a watch is a syscall on a path that can be a hung
// mount, and it holds mu only to read and write the bookkeeping.
func (fw *fileWatcher) retargetTree(dirs []string, seq uint64, ch chan struct{}) {
	fw.treeMu.Lock()
	defer fw.treeMu.Unlock()
	fw.mu.Lock()
	if fw.stopped || seq != fw.treeSeq {
		fw.mu.Unlock()
		return
	}
	if len(dirs) == 0 {
		set := fw.set
		fw.set, fw.setDirs = nil, nil
		fw.mu.Unlock()
		if set != nil {
			set.Close()
		}
		return
	}
	if fw.set == nil {
		set, err := dirwatch.NewSet(func(dir string) {
			fw.markDirty(dir)
			dirwatch.Nudge(ch)
		})
		if err != nil {
			// Without a watch an open folder is read again when it opens.
			fw.mu.Unlock()
			return
		}
		fw.set, fw.setDirs = set, map[string]bool{}
	}
	set := fw.set
	want := make(map[string]bool, len(dirs))
	for _, d := range dirs {
		want[d] = true
	}
	var drop []string
	for d := range fw.setDirs {
		if !want[d] {
			drop = append(drop, d)
			delete(fw.setDirs, d)
		}
	}
	for _, d := range dirs {
		fw.setDirs[d] = true
	}
	fw.mu.Unlock()
	// A set the client closed meanwhile refuses these, which is the answer.
	for _, d := range drop {
		set.Remove(d)
	}
	for _, d := range dirs {
		_ = set.Add(d)
	}
}

// rewatchTree watches an open folder again after it was read because it
// changed. A folder deleted and made again under the same path lost its watch
// with the old directory; this watches the new one. A folder still watched is
// left alone, at the cost of one map lookup on Linux.
func (m *OS) rewatchTree(dir string) {
	fw := &m.fileWatch
	go func() {
		fw.treeMu.Lock()
		defer fw.treeMu.Unlock()
		fw.mu.Lock()
		set, ok := fw.set, fw.setDirs[dir]
		fw.mu.Unlock()
		if set != nil && ok {
			_ = set.Refresh(dir)
		}
	}()
}
