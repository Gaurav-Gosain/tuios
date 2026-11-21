package session

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

const checkpointTimeout = 30 * time.Second

const tempRemoveWait = time.Second

// RemoveTempFolders ends the git work in the temporary folders of this
// process and removes the folders. The daemon's process calls it after the
// daemon has stopped: a call that outlived the shutdown's wait would
// otherwise leave its folder in the temp dir.
func RemoveTempFolders() { worktree.RemoveTemp(tempRemoveWait) }

type checkpointer struct {
	off          atomic.Bool
	keep         atomic.Int64
	maxUntracked atomic.Int64

	gitMu sync.Mutex

	mu  sync.Mutex
	due map[string]*checkpointDue
}

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
	d.checkpoints.maxUntracked.Store(c.MaxUntrackedBytes())
}

func (d *Daemon) checkpointKeep() int {
	if n := d.checkpoints.keep.Load(); n > 0 {
		return int(n)
	}
	return config.DefaultCheckpointKeep
}

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
		Kind:         worktree.CheckpointTurn,
		Pane:         window,
		Session:      s.Name(),
		Turn:         job.turn,
		State:        job.state,
		Label:        label,
		MaxUntracked: d.checkpoints.maxUntracked.Load(),
	}, d.checkpointKeep())
	if err != nil {
		LogBasic("Checkpoint of window %s in %s failed: %v", shortWindowID(window), repo.root, err)
		return
	}
	if saved {
		LogBasic("Checkpoint %d of window %s in %s took %s", cp.N, shortWindowID(window), repo.root, time.Since(start).Round(time.Millisecond))
	}
}

func (d *Daemon) forWindowState(s *Session, window string, fn func(WindowState)) {
	state := s.GetState()
	if idx, err := findWindowStateIndex(state.Windows, window); err == nil {
		fn(state.Windows[idx])
	}
}

func (d *Daemon) checkpointLabel(s *Session, window, fallback string) string {
	entries, _, _ := d.activity.read(s.ID, window)
	var ended string
	for _, e := range slices.Backward(entries) {
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
