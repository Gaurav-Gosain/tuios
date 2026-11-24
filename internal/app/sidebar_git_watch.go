package app

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/dirwatch"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/testknob"
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
// it did before. The watch fails on its own goroutine, after the message that
// asked for it, so it nudges the update loop once: an idle client gets no
// other message, and nextWake would never see polling to arm the tick. A model nobody named (ClientUnknown: the tests and the
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

	// seq numbers the retargets, so a late one is dropped. It is an atomic
	// and not under mu, because a retarget holds mu across the watch's
	// syscalls, and the update goroutine must not wait on a hung mount.
	seq atomic.Uint64

	mu      sync.Mutex // guards the fields below and serialises retargeting
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
	go gw.retarget(dir, gw.seq.Add(1), m.fileWatchChan())
}

func (gw *gitWatcher) retarget(dir string, seq uint64, ch chan struct{}) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.stopped || seq != gw.seq.Load() {
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
	if len(dirs) == 0 {
		gw.polling.Store(false)
		return
	}
	var set *dirwatch.Set
	err := gitWatchFault()
	if err == nil {
		set, err = dirwatch.NewSet(func(string) {
			gw.changed.Store(true)
			dirwatch.Nudge(ch)
		})
	}
	if err == nil {
		for _, d := range dirs {
			if err = set.Add(d); err != nil {
				set.Close()
				break
			}
		}
	}
	if err != nil {
		// polling is never cleared on the way here, so a client that was
		// polling already has its tick armed and needs no nudge. changed
		// makes the nudge the git section's: the files section does not read
		// again for it, and noteGitStale marks the reading stale.
		if !gw.polling.Swap(true) {
			gw.changed.Store(true)
			dirwatch.Nudge(ch)
		}
		return
	}
	gw.polling.Store(false)
	gw.set, gw.dirs = set, dirs
}

var errGitWatchFault = errors.New("git watch failed by test knob")

// gitWatchFault fails the watch on purpose, as a platform with no watch or a
// full inotify table does. The knob git_watch_fail names a file, and the
// failure lands when the test removes it, so the e2e suite can fail the watch
// on a client that has gone quiet.
func gitWatchFault() error {
	path := testknob.String("git_watch_fail")
	if path == "" {
		return nil
	}
	testknob.WaitWhile(path, 10*time.Millisecond, time.Minute)
	return errGitWatchFault
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
