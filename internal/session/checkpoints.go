package session

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Checkpoints: the state of a pane's git work tree, saved each time its agent
// finishes a turn, so the turn can be read again and undone.
//
// When a window goes from working to done, idle or needs_input, or to unknown
// on a finished turn (agent_turns.go), and the pane's directory is in a git
// work tree, the daemon writes the work tree as a commit under
// refs/tuios/checkpoints/<window id>/<n> (worktree.SaveCheckpoint). The index,
// HEAD, the branch and the stash are not touched. A turn that changed nothing
// since the pane's last checkpoint takes none.
//
// The git work never runs on the event path. The session's event sink only
// notes that a checkpoint is due, under the session's state lock, and a
// tracked goroutine does the rest. One checkpoint runs at a time on the whole
// daemon (checkpointer.gitMu), each bounded by checkpointTimeout, and a pane
// that finishes another turn while its checkpoint runs gets one more after
// it, not one per turn. The verbs that read and restore checkpoints are in
// verb_checkpoint.go.

// checkpointTimeout bounds one checkpoint, or one checkpoint verb, together.
// On a work tree of 50,000 files a checkpoint takes about 100 ms.
const checkpointTimeout = 30 * time.Second

// checkpointer holds the daemon's checkpoint switch and the work in flight.
// Its zero value is on, with the default keep.
type checkpointer struct {
	// off is [agents.checkpoints] enabled = false.
	off atomic.Bool
	// keep is [agents.checkpoints] keep; zero reads as the default.
	keep atomic.Int64

	// gitMu runs one checkpoint or restore at a time, so a restore never
	// reads a work tree a checkpoint is writing a tree of, and the daemon
	// never runs more than one of these git commands at once.
	gitMu sync.Mutex

	mu sync.Mutex
	// due holds the windows with a checkpoint queued or running. again marks
	// one that finished another turn while its checkpoint ran.
	due map[string]*checkpointDue
}

// checkpointDue is a checkpoint a window is waiting for.
type checkpointDue struct {
	again bool
	turn  uint64
	state string
	text  string
}

// SetCheckpoints applies [agents.checkpoints].
func (d *Daemon) SetCheckpoints(c config.CheckpointsConfig) {
	d.checkpoints.off.Store(!c.On())
	d.checkpoints.keep.Store(int64(c.KeepCount()))
}

// checkpointKeep is how many checkpoints a pane keeps.
func (d *Daemon) checkpointKeep() int {
	if n := d.checkpoints.keep.Load(); n > 0 {
		return int(n)
	}
	return config.DefaultCheckpointKeep
}

// checkpointWanted reports whether ev ends a turn a checkpoint is taken for.
// unknown counts only when the turn counted as finished: the detector sends a
// pane to working on any output, and back on the silence timer.
func checkpointWanted(ev SessionEvent) bool {
	if ev.Type != EventAgentState || ev.hookPrevState != string(AgentStateWorking) {
		return false
	}
	switch AgentState(ev.State) {
	case AgentStateDone, AgentStateIdle, AgentStateNeedsInput:
		return true
	case AgentStateUnknown:
		return ev.completionSeq > ev.prevCompletionSeq
	}
	return false
}

// noteCheckpointEvent starts a checkpoint for a window whose agent just
// finished a turn. It runs in the session event sink with the session's state
// lock held, so it reads nothing from the session and runs no git.
func (d *Daemon) noteCheckpointEvent(s *Session, ev SessionEvent) {
	if d.checkpoints.off.Load() || !checkpointWanted(ev) {
		return
	}
	c := &d.checkpoints
	c.mu.Lock()
	if job, ok := c.due[ev.Window]; ok {
		job.again, job.turn, job.state, job.text = true, ev.completionSeq, ev.State, ev.hookMessage
		c.mu.Unlock()
		return
	}
	if c.due == nil {
		c.due = make(map[string]*checkpointDue)
	}
	c.due[ev.Window] = &checkpointDue{turn: ev.completionSeq, state: ev.State, text: ev.hookMessage}
	c.mu.Unlock()
	window := ev.Window
	if !d.goTracked(func() { d.runCheckpoints(s, window) }) {
		c.mu.Lock()
		delete(c.due, window)
		c.mu.Unlock()
	}
}

// runCheckpoints takes the window's checkpoint, and one more for each time
// the window finished a turn while the last one ran.
func (d *Daemon) runCheckpoints(s *Session, window string) {
	c := &d.checkpoints
	for {
		c.mu.Lock()
		job := c.due[window]
		if job == nil {
			c.mu.Unlock()
			return
		}
		meta := *job
		job.again = false
		c.mu.Unlock()

		d.takeTurnCheckpoint(s, window, meta)

		c.mu.Lock()
		if !job.again || d.ctx.Err() != nil {
			delete(c.due, window)
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
	}
}

// takeTurnCheckpoint saves one checkpoint of the window's work tree. A pane
// with no work tree under it, a pane on another machine and a failed git
// call take none; a failure is logged, since nobody is waiting on it.
func (d *Daemon) takeTurnCheckpoint(s *Session, window string, job checkpointDue) {
	if d.ctx.Err() != nil || d.checkpoints.off.Load() {
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
	label := d.checkpointLabel(s, window, job.text)
	ctx, cancel := context.WithTimeout(d.ctx, checkpointTimeout)
	defer cancel()
	d.checkpoints.gitMu.Lock()
	defer d.checkpoints.gitMu.Unlock()
	start := time.Now()
	cp, saved, err := worktree.SaveCheckpoint(ctx, repo.root, worktree.CheckpointMeta{
		Kind:    worktree.CheckpointTurn,
		Pane:    window,
		Session: s.Name(),
		Turn:    job.turn,
		State:   job.state,
		Label:   label,
	}, d.checkpointKeep())
	if err != nil {
		LogBasic("Checkpoint of window %s in %s failed: %v", shortWindowID(window), repo.root, err)
		return
	}
	if saved {
		LogBasic("Checkpoint %d of window %s in %s took %s", cp.N, shortWindowID(window), repo.root, time.Since(start).Round(time.Millisecond))
	}
}

// forWindowState runs fn with a copy of one window's state, if the session
// has the window. It must not be called with the session's state lock held.
func (d *Daemon) forWindowState(s *Session, window string, fn func(WindowState)) {
	state := s.GetState()
	if idx, err := findWindowStateIndex(state.Windows, window); err == nil {
		fn(state.Windows[idx])
	}
}

// checkpointLabel is a short line for what the turn was: the newest prompt
// the pane's activity holds, else the line the turn ended with, else the
// agent's own message.
func (d *Daemon) checkpointLabel(s *Session, window, fallback string) string {
	entries, _, _ := d.activity.read(s.ID, window)
	var ended string
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		switch e.Kind {
		case ActivityPrompt:
			if e.Text != "" {
				return e.Text
			}
		case ActivityTurnEnd:
			if ended == "" {
				ended = e.Text
			}
		}
	}
	if ended != "" {
		return ended
	}
	return fallback
}

// dropCheckpointsOf removes every checkpoint taken in the work tree at root,
// whichever pane took it, running git in repoRoot. It is what removing a
// worktree does with them. A failure is logged: the refs then stay until the
// person removes them with git.
func (d *Daemon) dropCheckpointsOf(repoRoot, root string) {
	ctx, cancel := context.WithTimeout(d.ctx, checkpointTimeout)
	defer cancel()
	d.checkpoints.gitMu.Lock()
	defer d.checkpoints.gitMu.Unlock()
	n, err := worktree.DeleteCheckpointsOf(ctx, repoRoot, root)
	switch {
	case err != nil:
		LogBasic("The checkpoints of %s were not removed: %v", root, err)
	case n > 0:
		LogBasic("Removed %d checkpoints of %s", n, root)
	}
}
