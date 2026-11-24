package app

import (
	"slices"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// # When the git section reads again
//
// The section used to read its directory again every git_refresh_ms, change
// or no change. At idle that was a tick and a reading every 2 s, each a
// handful of file reads and a frame equal to the last one. Now it reads again
// only when something it shows can have moved:
//
//   - the repository's git folders changed (gitstate.WatchDirs): a commit, a
//     checkout, a fetch, a staged path, a jj operation;
//   - a pane printed output, when the section counts changed paths
//     (appearance.git_dirty): an edit leaves no trace in the git folders,
//     and an edit made in a pane comes with output in it;
//   - the focused pane moved to another folder, which GitSyncCmd already
//     compares on every message.
//
// git_refresh_ms is now the least time between two readings, so a build that
// keeps printing, or a fetch that writes many refs, costs at most one reading
// per interval. A reading that is held back for it is the only deadline the
// section arms.
//
// Where no watch can be made (no inotify, kqueue or Windows watch on this
// platform, or a folder that refuses one), the section reads every interval as
// it did before. A model nobody named (ClientUnknown: the tests and the
// benchmarks) watches nothing and reads every interval too, for the reason
// fileWatchTarget gives.

// gitWatcher holds the watch on the git section's repository. Setting it up
// and tearing it down are syscalls on a path that can be a hung mount, so both
// run off the update goroutine. A change nudges the files section's channel,
// so one listener serves both sections; changed says the nudge was this
// watch's.
type gitWatcher struct {
	// gen and shown are the reading and the rail state the watch was last
	// asked for, read and written only on the update goroutine, so a sync that
	// changes nothing costs two comparisons.
	gen   uint64
	shown bool

	// polling is set while the section has no working watch, and changed by
	// the watch goroutine when a folder changed.
	polling, changed atomic.Bool

	mu      sync.Mutex // guards the fields below and serialises retargeting
	seq     uint64
	dirs    []string
	set     *dirwatch.Set
	stopped bool
}

// gitSectionPolls reports whether the section reads every interval, because
// it has no watch to tell it when to.
func (m *OS) gitSectionPolls() bool {
	return m.Client == ClientUnknown || m.gitWatch.polling.Load()
}

// syncGitWatch points the watch at the folders of the reading on screen, or
// at none when no rail draws the section. Update calls it after the rails'
// git syncs, with shown set when either rail draws the section. A model
// nobody named watches nothing, for the reason fileWatchTarget gives.
func (m *OS) syncGitWatch(shown bool) {
	gw := &m.gitWatch
	if m.Client == ClientUnknown || (gw.gen == m.gitView.gen && gw.shown == shown) {
		return
	}
	gw.gen, gw.shown = m.gitView.gen, shown
	dir := ""
	if shown {
		dir = m.gitView.Dir
	}
	ch := m.fileWatchChan()
	gw.mu.Lock()
	gw.seq++
	seq := gw.seq
	gw.mu.Unlock()
	go gw.retarget(dir, seq, ch)
}

func (gw *gitWatcher) retarget(dir string, seq uint64, ch chan struct{}) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.stopped || seq != gw.seq {
		return
	}
	var dirs []string
	if dir != "" {
		dirs, _ = gitstate.WatchDirs(dir)
	}
	if gw.set != nil && slices.Equal(dirs, gw.dirs) {
		return
	}
	if gw.set != nil {
		gw.set.Close()
		gw.set, gw.dirs = nil, nil
	}
	gw.polling.Store(false)
	if len(dirs) == 0 {
		return
	}
	set, err := dirwatch.NewSet(func(string) {
		gw.changed.Store(true)
		dirwatch.Nudge(ch)
	})
	if err == nil {
		for _, d := range dirs {
			if err = set.Add(d); err != nil {
				set.Close()
				break
			}
		}
	}
	if err != nil {
		gw.polling.Store(true)
		return
	}
	gw.set, gw.dirs = set, dirs
}

// stopGitWatch closes the watch for good, when the client exits.
func (m *OS) stopGitWatch() {
	gw := &m.gitWatch
	gw.mu.Lock()
	gw.stopped = true
	if gw.set != nil {
		gw.set.Close()
		gw.set = nil
	}
	gw.mu.Unlock()
}

// noteGitStale marks the section's reading out of date after msg: a watched
// folder changed, or, with changed paths counted, a pane printed output.
func (m *OS) noteGitStale(msg tea.Msg) {
	switch msg.(type) {
	case fileDirChangedMsg:
		if m.gitWatch.changed.Swap(false) {
			m.gitView.stale = true
			// A ".git" made in a folder that was in no repository.
			m.gitView.fresh = !m.gitView.Found
		}
	case PTYDataMsg:
		if m.Settings.SidebarGitDirty && m.gitView.Found {
			m.gitView.stale = true
		}
	}
}
