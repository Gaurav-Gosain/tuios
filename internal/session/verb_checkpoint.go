package session

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// The checkpoint verbs: list a pane's checkpoints, read what one turn
// changed, and put the work tree back to a checkpoint. checkpoints.go takes
// them.
//
// list-checkpoints and checkpoint-diff read, and are held like review-diff:
// scopeRead, and over a link list for the listing and write for the diff,
// which carries file contents. restore-checkpoint writes files in the pane's
// work tree, which the pane's agent then reads and runs, so it is scopeWrite
// and a pane without admin may restore only a pane it could type into
// (typingRefusal, as review-note holds its target).
//
// A restore loses nothing that git can see: it first saves the work tree as a
// safety checkpoint, so the restore is itself undone by restoring that one.
// Ignored files are in no checkpoint and are never touched. It refuses a pane
// whose agent is mid-turn unless force is given, since the agent would go on
// writing over the restored files.

// ErrVerbNoCheckpoint reports a checkpoint number the pane does not have.
// Nothing was read or changed.
const ErrVerbNoCheckpoint = "no_checkpoint"

// checkpointVerbs are the registry entries of the checkpoint verbs.
func checkpointVerbs() map[string]verbEntry {
	checkpointFields := "n, ref, commit, tree, head, kind (turn or safety), pane, session, turn, state, label, worktree, at (Unix nanoseconds), and skipped and skipped_count (the untracked files left out as larger than max_untracked_mb, omitted when none)"
	return map[string]verbEntry{
		"list-checkpoints": {
			description: "List the checkpoints of a pane: the state of its git work tree, saved each time its agent finished a turn that changed a file, and before each restore. Oldest first. The labels are the agent's prompts, so the result is marked untrusted.",
			params: []verbParam{
				sessionParam,
				windowParam,
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree the pane is in, symbolic links resolved."},
				{Name: "enabled", Type: "bool", Description: "Whether checkpoints are taken: [agents.checkpoints] enabled."},
				{Name: "keep", Type: "int", Description: "How many checkpoints one pane keeps: [agents.checkpoints] keep."},
				{Name: "checkpoints", Type: "[]object", Description: "One entry per checkpoint: " + checkpointFields + "."},
				{Name: "untrusted", Type: "bool", Description: "Always true: the labels are the agent's text."},
			},
			examples: []string{
				`{"id":1,"verb":"list-checkpoints","params":{"session":"work","window":"build"}}`,
			},
			handler: (*Daemon).verbListCheckpoints,
		},
		"checkpoint-diff": {
			description: "Read what one turn changed: the diff of a checkpoint against the pane's checkpoint before it, or against HEAD when it was taken for the first one. Nothing in the repository, its index or its working tree is changed. The text is the repository's, so it is marked untrusted.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "n", Type: "int", Description: "The checkpoint. Omit for the newest."},
				{Name: "paths", Type: "[]string", Description: "Only these paths, relative to the repository root. Omit for every changed file."},
				{Name: "context", Type: "int", Description: "Lines of context around each change, 0 to 20.", Default: "3"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree."},
				{Name: "checkpoint", Type: "object", Description: "The checkpoint read: " + checkpointFields + "."},
				{Name: "base", Type: "string", Description: "What the diff runs from: checkpoint N, HEAD <hash>, or an empty tree."},
				{Name: "base_tree", Type: "string", Description: "The tree the diff runs from."},
				{Name: "files", Type: "[]object", Description: "One entry per changed file, as review-diff returns them."},
				{Name: "totals", Type: "object", Description: "files, added and removed over the whole diff."},
				{Name: "truncated", Type: "bool", Description: "The diff passed a cap (400 files, 2 MiB, 5000 lines in a file) and the files past it carry counts only."},
				{Name: "untrusted", Type: "bool", Description: "Always true: the text is the repository's, not the daemon's."},
			},
			examples: []string{
				`{"id":1,"verb":"checkpoint-diff","params":{"session":"work","window":"build","n":2}}`,
			},
			handler: (*Daemon).verbCheckpointDiff,
		},
		"restore-checkpoint": {
			description: "Put a pane's git work tree back to a checkpoint. The work tree is first saved as a safety checkpoint, so the restore can be undone by restoring that one. Only files that differ are written or removed. The index, HEAD, the branch and ignored files are not changed. Refused while the pane's agent is working or waiting on a prompt, unless force is given.",
			params: []verbParam{
				sessionParam,
				windowParam,
				{Name: "n", Type: "int", Required: true, Description: "The checkpoint to restore."},
				{Name: "force", Type: "bool", Description: "Restore while the agent is mid-turn.", Default: "false"},
			},
			returns: []verbParam{
				{Name: "session", Type: "string", Description: "The session of the pane."},
				{Name: "window", Type: "string", Description: "The pane, by id."},
				{Name: "worktree", Type: "string", Description: "The root of the work tree."},
				{Name: "restored", Type: "object", Description: "The checkpoint restored."},
				{Name: "safety", Type: "object", Description: "The checkpoint that holds the work tree as it was before the restore. Restore it to undo."},
				{Name: "written", Type: "[]string", Description: "The files written from the checkpoint."},
				{Name: "removed", Type: "[]string", Description: "The files removed, which the checkpoint does not have."},
				{Name: "skipped", Type: "[]string", Description: "The submodules that differ from the checkpoint. A restore does not change a submodule."},
			},
			examples: []string{
				`{"id":1,"verb":"restore-checkpoint","params":{"session":"work","window":"build","n":1}}`,
			},
			handler: (*Daemon).verbRestoreCheckpoint,
		},
	}
}

// checkpointTarget resolves the pane a checkpoint verb is about and the work
// tree under it.
func (d *Daemon) checkpointTarget(sessionName, window string) (*Session, WindowState, reviewRepo, *verbError) {
	sess, target, verr := d.resolveVerbPane(sessionName, window)
	if verr != nil {
		return nil, WindowState{}, reviewRepo{}, verr
	}
	if target.Host != "" {
		return nil, WindowState{}, reviewRepo{}, hintedVerbError(ErrVerbNotRepo, "window "+shortWindowID(target.ID)+" runs on "+echoName(target.Host)+", and its checkpoints are there, not here", &VerbHint{
			Detail: "Nothing was read or changed. Checkpoints of a pane on another machine are not supported yet. Attach to that machine and use them there.",
		})
	}
	repo, verr := d.paneRepo(sess, target)
	if verr != nil {
		return nil, WindowState{}, reviewRepo{}, verr
	}
	return sess, target, repo, nil
}

// checkpointGitFailed is the refusal for a git call of a checkpoint verb that
// failed or ran out of time.
func checkpointGitFailed(err error, changed bool) *verbError {
	detail := "Nothing in the work tree was changed."
	if changed {
		detail = "The restore stopped part of the way. The safety checkpoint holds the work tree as it was before. Restore it to go back."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		detail = "git took longer than " + checkpointTimeout.String() + ". " + detail
	}
	return hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{Detail: detail})
}

// noCheckpoint is the refusal for a checkpoint the pane does not have.
func noCheckpoint(sess *Session, target WindowState, n int, list []worktree.Checkpoint) *verbError {
	avail := make([]string, 0, len(list))
	for _, cp := range list {
		avail = append(avail, strconv.Itoa(cp.N))
	}
	msg := "window " + shortWindowID(target.ID) + " has no checkpoint " + strconv.Itoa(n)
	if n == 0 {
		msg = "window " + shortWindowID(target.ID) + " has no checkpoints"
	}
	return hintedVerbError(ErrVerbNoCheckpoint, msg, &VerbHint{
		Param:     "n",
		Available: avail,
		Command:   "tuios checkpoint list -s " + sess.Name() + " -w " + shortWindowID(target.ID),
		Detail:    "Nothing was read or changed. A checkpoint is taken when the pane's agent finishes a turn that changed a file in its git work tree.",
	})
}

// checkpointsOfPane lists the pane's checkpoints taken in this work tree.
func checkpointsOfPane(ctx context.Context, repo reviewRepo, window string) ([]worktree.Checkpoint, error) {
	list, err := worktree.ListCheckpoints(ctx, repo.root, window)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []worktree.Checkpoint{}
	}
	return list, nil
}

// verbListCheckpoints answers list-checkpoints.
func (d *Daemon) verbListCheckpoints(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, target, repo, verr := d.checkpointTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	ctx, cancel := context.WithTimeout(d.ctx, checkpointTimeout)
	defer cancel()
	list, err := checkpointsOfPane(ctx, repo, target.ID)
	if err != nil {
		return nil, checkpointGitFailed(err, false)
	}
	return map[string]any{
		"type":        "checkpoint_list",
		"session":     sess.Name(),
		"window":      target.ID,
		"worktree":    repo.root,
		"enabled":     !d.checkpoints.off.Load(),
		"keep":        d.checkpointKeep(),
		"checkpoints": list,
		"untrusted":   true,
	}, nil
}

// verbCheckpointDiff answers checkpoint-diff.
func (d *Daemon) verbCheckpointDiff(_ *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string   `json:"session"`
		Window  string   `json:"window"`
		N       int      `json:"n"`
		Paths   []string `json:"paths"`
		Context *int     `json:"context"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.N < 0 {
		return nil, invalidParam("n", "n is a checkpoint number from 1")
	}
	if p.Context != nil && (*p.Context < 0 || *p.Context > 20) {
		return nil, invalidParam("context", "context is 0 to 20 lines")
	}
	lines := 3
	if p.Context != nil {
		lines = *p.Context
	}
	if len(p.Paths) > reviewMaxPaths {
		return nil, invalidParam("paths", "paths holds at most "+strconv.Itoa(reviewMaxPaths)+" paths")
	}
	for _, path := range p.Paths {
		if err := review.ValidPath(path); err != nil {
			return nil, invalidParam("paths", err.Error())
		}
	}
	sess, target, repo, verr := d.checkpointTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	ctx, cancel := context.WithTimeout(d.ctx, checkpointTimeout)
	defer cancel()
	list, err := checkpointsOfPane(ctx, repo, target.ID)
	if err != nil {
		return nil, checkpointGitFailed(err, false)
	}
	var cp worktree.Checkpoint
	var ok bool
	if p.N == 0 && len(list) > 0 {
		cp, ok = list[len(list)-1], true
	} else {
		cp, ok = worktree.FindCheckpoint(list, p.N)
	}
	if !ok {
		return nil, noCheckpoint(sess, target, p.N, list)
	}
	baseTree, baseName, err := worktree.CheckpointBase(ctx, repo.root, list, cp)
	if err != nil {
		return nil, checkpointGitFailed(err, false)
	}
	diff, err := review.Build(ctx, review.Options{
		Dir:     repo.root,
		Base:    review.Base{Name: baseName, SHA: baseTree},
		Tree:    cp.Tree,
		Paths:   p.Paths,
		Context: lines,
	})
	if err != nil {
		return nil, checkpointGitFailed(err, false)
	}
	return map[string]any{
		"type":       "checkpoint_diff",
		"session":    sess.Name(),
		"window":     target.ID,
		"worktree":   repo.root,
		"checkpoint": cp,
		"base":       baseName,
		"base_tree":  baseTree,
		"files":      diff.Files,
		"totals":     diff.Totals,
		"truncated":  diff.Truncated,
		"untrusted":  true,
	}, nil
}

// verbRestoreCheckpoint answers restore-checkpoint.
func (d *Daemon) verbRestoreCheckpoint(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Session string `json:"session"`
		Window  string `json:"window"`
		N       int    `json:"n"`
		Force   bool   `json:"force"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if p.N < 1 {
		return nil, invalidParam("n", "n is required: the checkpoint to restore, from 1")
	}
	sess, target, repo, verr := d.checkpointTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	if pa := d.paneAuthority(cs); pa != nil && !pa.hosted && !pa.grants.Has(GrantAdmin) {
		if why := d.typingRefusal(pa, target, true); why != "" {
			return nil, grantForbidden("restore-checkpoint", pa, why+". A restore writes the files that pane's agent works on")
		}
	}
	if !p.Force && (target.AgentState == AgentStateWorking || target.AgentState == AgentStateNeedsInput) {
		return nil, hintedVerbError(ErrVerbNotReady, "the agent in window "+shortWindowID(target.ID)+" is "+target.AgentState.Name()+", and would go on writing over the restored files", &VerbHint{
			Param:   "force",
			Command: "tuios checkpoint restore -s " + sess.Name() + " -w " + shortWindowID(target.ID) + " " + strconv.Itoa(p.N) + " --force",
			Detail:  "Nothing was changed. Wait for the agent to finish its turn, or pass force to restore now.",
		})
	}

	ctx, cancel := context.WithTimeout(d.ctx, checkpointTimeout)
	defer cancel()
	d.checkpoints.gitMu.Lock()
	defer d.checkpoints.gitMu.Unlock()
	list, err := checkpointsOfPane(ctx, repo, target.ID)
	if err != nil {
		return nil, checkpointGitFailed(err, false)
	}
	cp, ok := worktree.FindCheckpoint(list, p.N)
	if !ok {
		return nil, noCheckpoint(sess, target, p.N, list)
	}
	if cp.Worktree != "" && cp.Worktree != repo.root {
		return nil, hintedVerbError(ErrVerbInvalidParams, "checkpoint "+strconv.Itoa(cp.N)+" was taken in "+cp.Worktree+", and the pane is now in "+repo.root, &VerbHint{
			Param:  "n",
			Detail: "Nothing was changed. Restore a checkpoint taken in the work tree the pane is in now.",
		})
	}
	safety, _, err := worktree.SaveCheckpoint(ctx, repo.root, worktree.CheckpointMeta{
		Kind:         worktree.CheckpointSafety,
		Pane:         target.ID,
		Session:      sess.Name(),
		Turn:         target.CompletionSeq,
		State:        target.AgentState.Name(),
		Label:        "before restoring checkpoint " + strconv.Itoa(cp.N),
		MaxUntracked: d.checkpoints.maxUntracked.Load(),
	}, 0)
	if err != nil {
		return nil, checkpointGitFailed(err, false)
	}
	res, err := worktree.RestoreTree(ctx, repo.root, safety.Tree, cp.Tree)
	var blocked *worktree.OverwriteError
	switch {
	case errors.As(err, &blocked):
		// RestoreTree refused before it touched a file. The safety checkpoint
		// it was compared against holds nothing new, and a refusal that says
		// nothing was changed must not leave one behind.
		if derr := worktree.DeleteCheckpoints(ctx, repo.root, []worktree.Checkpoint{safety}); derr != nil {
			LogBasic("Could not remove safety checkpoint %d of window %s after a refused restore: %v", safety.N, shortWindowID(target.ID), derr)
		}
		return nil, hintedVerbError(ErrVerbInvalidParams, err.Error(), &VerbHint{
			Param:     "n",
			Available: blocked.Paths,
			Detail:    "Nothing in the work tree was changed. These files are ignored or new, so no checkpoint holds them, and the restore would lose them. Move them out of the way, then restore again.",
		})
	case err != nil:
		return nil, checkpointGitFailed(err, true)
	}
	// The keep bound is applied after the restore, so the safety checkpoint
	// is never the one pruned, however small keep is.
	if keep := d.checkpointKeep(); len(list)+1 > keep {
		if all, err := worktree.ListCheckpoints(ctx, repo.root, target.ID); err == nil && len(all) > keep {
			var drop []worktree.Checkpoint
			for _, old := range all[:len(all)-keep] {
				if old.N != cp.N && old.N != safety.N {
					drop = append(drop, old)
				}
			}
			_ = worktree.DeleteCheckpoints(ctx, repo.root, drop)
		}
	}
	if res.Written == nil {
		res.Written = []string{}
	}
	if res.Removed == nil {
		res.Removed = []string{}
	}
	if res.Skipped == nil {
		res.Skipped = []string{}
	}
	LogBasic("Restored checkpoint %d of window %s in %s (safety checkpoint %d)", cp.N, shortWindowID(target.ID), repo.root, safety.N)
	return map[string]any{
		"type":     "checkpoint_restored",
		"session":  sess.Name(),
		"window":   target.ID,
		"worktree": repo.root,
		"restored": cp,
		"safety":   safety,
		"written":  res.Written,
		"removed":  res.Removed,
		"skipped":  res.Skipped,
	}, nil
}
