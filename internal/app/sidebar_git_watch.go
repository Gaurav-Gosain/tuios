package app

import (
	"sync"
	"sync/atomic"
	"time"

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
// run off the update goroutine; the update goroutine only records what it
// wants.
type gitWatcher struct {
	// want is the reading the watch was last asked for, read and written only
	// on the update goroutine, so a sync that changes nothing costs one
	// comparison.
	want gitWatchSpec

	ch chan struct{}

	// polling is set while the section has no working watch for want, and
	// cleared when one is made.
	polling atomic.Bool

	mu      sync.Mutex // guards the fields below and serialises retargeting
	latest  gitWatchSpec
	set     *dirwatch.Set
	stopped bool
	// changed is set by the watch goroutine and taken by the update goroutine.
	changed atomic.Bool
}

// gitWatchSpec is what the watch follows: one reading of the section.
type gitWatchSpec struct {
	dir              string
	found            bool
	branch, upstream string
	live             bool
}

// gitDirChangedMsg says a watched folder of the section's repository changed.
type gitDirChangedMsg struct{ at time.Time }

func (m *OS) gitWatchChan() chan struct{} {
	if m.gitWatch.ch == nil {
		m.gitWatch.ch = make(chan struct{}, 1)
	}
	return m.gitWatch.ch
}

// listenForGitChange waits for a watched folder to change, lets the burst
// settle, and hands one message to the loop.
func listenForGitChange(stop <-chan struct{}, ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case _, ok := <-ch:
			if !ok {
				return nil
			}
		case <-stop:
			return nil
		}
		if !dirwatch.AfterBurst(ch, stop) {
			return nil
		}
		return gitDirChangedMsg{at: time.Now()}
	}
}

// gitSectionPolls reports whether the section reads every interval, because
// it has no watch to tell it when to.
func (m *OS) gitSectionPolls() bool {
	return m.Client == ClientUnknown || m.gitWatch.polling.Load()
}

// gitWatchTarget is what the watch should follow: the reading on screen, or
// nothing when no rail draws the section.
func (m *OS) gitWatchTarget(shown bool) gitWatchSpec {
	v := m.gitView
	if !shown || v.Dir == "" || m.Client == ClientUnknown {
		return gitWatchSpec{}
	}
	return gitWatchSpec{dir: v.Dir, found: v.Found, branch: v.State.Branch, upstream: v.State.Upstream, live: true}
}

// syncGitWatch points the watch at gitWatchTarget. Update calls it after the
// rails' git syncs, with shown set when either rail draws the section.
func (m *OS) syncGitWatch(shown bool) {
	target := m.gitWatchTarget(shown)
	gw := &m.gitWatch
	if target == gw.want {
		return
	}
	gw.want = target
	ch := m.gitWatchChan()
	gw.mu.Lock()
	gw.latest = target
	gw.mu.Unlock()
	go gw.retarget(target, ch)
}

func (gw *gitWatcher) retarget(target gitWatchSpec, ch chan struct{}) {
	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.stopped || target != gw.latest {
		return
	}
	if gw.set != nil {
		gw.set.Close()
		gw.set = nil
	}
	if !target.live {
		gw.polling.Store(false)
		return
	}
	dirs, _ := gitstate.WatchDirs(target.dir)
	set, err := dirwatch.NewSet(func(string) {
		gw.changed.Store(true)
		dirwatch.Nudge(ch)
	})
	if err != nil {
		gw.polling.Store(true)
		return
	}
	for _, d := range dirs {
		if err := set.Add(d); err != nil {
			set.Close()
			gw.polling.Store(true)
			return
		}
	}
	gw.set = set
	gw.polling.Store(false)
}

// stopGitWatch closes the watch for good, when the client exits. Closing the
// channel ends the listener.
func (m *OS) stopGitWatch() {
	gw := &m.gitWatch
	gw.mu.Lock()
	defer gw.mu.Unlock()
	if gw.stopped {
		return
	}
	gw.stopped = true
	if gw.set != nil {
		gw.set.Close()
		gw.set = nil
	}
	if gw.ch != nil {
		close(gw.ch)
	}
}

// noteGitStale marks the section's reading out of date after msg: a watched
// folder changed, or, with changed paths counted, a pane printed output.
func (m *OS) noteGitStale(msg tea.Msg) {
	switch msg.(type) {
	case gitDirChangedMsg:
		if m.gitWatch.changed.Swap(false) {
			m.gitView.stale = true
			if !m.gitView.Found {
				// A ".git" made in a folder that was in no repository.
				m.gitView.fresh = true
			}
		}
	case PTYDataMsg:
		if m.Settings.SidebarGitDirty && m.gitView.Found {
			m.gitView.stale = true
		}
	}
}
