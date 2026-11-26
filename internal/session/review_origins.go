package session

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Who made each line of a review: review-diff with origins. The chain is the
// review's base, then every turn checkpoint taken in the work tree, in the
// order they were taken, then the work tree now (review.Origins). A line made
// after the last checkpoint is credited to the agent working in the tree now
// when there is exactly one, and to the person otherwise.
//
// A checkpoint is the whole work tree at the end of a turn, and a turn start
// the whole work tree when it began, so a line the person writes between two
// turns is the person's. A line the person writes while a turn runs is
// credited to that turn. Telling those apart needs the agents' own edits,
// from their transcripts.
//
// The answer adds "authors": the first is always the person between turns,
// each other is a pane's turn, or the person while that turn ran. Every
// line's origin is an index into it, plus one.

// reviewOriginSteps bounds the checkpoints the chain walks, newest kept.
const reviewOriginSteps = 64

// reviewOrigin is one entry of review-diff's authors.
type reviewOrigin struct {
	// Kind is "person" or "agent". A person with a turn is the person while
	// that turn of the pane ran.
	Kind string `json:"kind"`
	// Agent names the agent: its harness, or the pane's title.
	Agent   string `json:"agent,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Session string `json:"session,omitempty"`
	// Turn is the pane's turn the lines came from. Working marks the turn
	// running now, which has no checkpoint yet.
	Turn    uint64 `json:"turn,omitempty"`
	Working bool   `json:"working,omitempty"`
	// At is when the turn's checkpoint was taken, in Unix nanoseconds.
	At int64 `json:"at,omitempty"`
}

// reviewOriginCache keeps the diffs of steps between two checkpoints, which
// never change, so a review read again redoes only the step to the work tree.
type reviewOriginCache struct {
	mu   sync.Mutex
	diff map[string]string
}

// reviewOriginCacheMax bounds the cached step diffs.
const reviewOriginCacheMax = 512

func (c *reviewOriginCache) read(dir string) review.OriginDiffs {
	return func(ctx context.Context, from, to string, paths []string) (string, error) {
		key := from + " " + to + " " + strings.Join(paths, "\x00")
		c.mu.Lock()
		out, ok := c.diff[key]
		c.mu.Unlock()
		if ok {
			return out, nil
		}
		out, err := review.StepDiff(ctx, dir, from, to, paths)
		if err != nil {
			return "", err
		}
		c.mu.Lock()
		if c.diff == nil || len(c.diff) >= reviewOriginCacheMax {
			c.diff = map[string]string{}
		}
		c.diff[key] = out
		c.mu.Unlock()
		return out, nil
	}
}

// reviewOrigins marks who made each line of diff, in place, and returns the
// authors the marks index.
func (d *Daemon) reviewOrigins(ctx context.Context, repo reviewRepo, diff *review.Diff) ([]reviewOrigin, error) {
	authors := []reviewOrigin{{Kind: "person"}}
	all, err := worktree.ListCheckpoints(ctx, repo.root, "")
	if err != nil {
		return nil, err
	}
	var turns []worktree.Checkpoint
	for _, cp := range all {
		if cp.Kind == worktree.CheckpointTurn && canonRoot(cp.Worktree) == repo.root {
			turns = append(turns, cp)
		}
	}
	slices.SortStableFunc(turns, func(a, b worktree.Checkpoint) int {
		switch {
		case a.At < b.At:
			return -1
		case a.At > b.At:
			return 1
		}
		return 0
	})
	if len(turns) > reviewOriginSteps {
		turns = turns[len(turns)-reviewOriginSteps:]
	}
	panes := d.reviewPanes(repo.root)
	// The chain, in time order: each checkpoint ends a turn and credits
	// it, and each turn start credits the person with what changed before
	// it (turn_starts.go).
	type mark struct {
		tree string
		at   int64
		cp   *worktree.Checkpoint
	}
	var marks []mark
	for i := range turns {
		marks = append(marks, mark{tree: turns[i].Tree, at: turns[i].At, cp: &turns[i]})
	}
	for _, s := range d.turnMarks.startsOf(repo.root) {
		marks = append(marks, mark{tree: s.tree, at: s.at})
	}
	slices.SortStableFunc(marks, func(a, b mark) int {
		switch {
		case a.at < b.at:
			return -1
		case a.at > b.at:
			return 1
		}
		return 0
	})
	var steps []review.OriginStep
	from := diff.BaseSHA
	var prevAt int64
	for _, mk := range marks {
		step := review.OriginStep{From: from, To: mk.tree}
		if cp := mk.cp; cp != nil {
			a := reviewOrigin{Kind: "agent", Pane: cp.Pane, Session: cp.Session, Turn: cp.Turn, At: cp.At, Agent: "agent"}
			if p, ok := panes[cp.Pane]; ok {
				a.Agent = reviewAgentName(p.WindowState)
				// The agent's own edits judge the lines of its turn
				// (review_edits.go).
				step.Judge = d.reviewJudge(p.sess, cp.Pane, repo.root, prevAt, cp.At)
			}
			authors = append(authors, a)
			step.Author = len(authors) - 1
			if step.Judge != nil {
				authors = append(authors, reviewOrigin{Kind: "person", Pane: cp.Pane, Session: cp.Session, Turn: cp.Turn})
				step.Person = len(authors) - 1
			}
		}
		steps = append(steps, step)
		from, prevAt = mk.tree, mk.at
	}
	// Lines made since the last checkpoint: the agent working in the tree
	// now when there is one, the person otherwise.
	last := 0
	var working []agentPane
	for _, p := range panes {
		if p.AgentState == AgentStateWorking {
			working = append(working, p)
		}
	}
	final := review.OriginStep{From: from, To: diff.TreeSHA}
	if len(working) == 1 {
		w := working[0]
		authors = append(authors, reviewOrigin{Kind: "agent", Agent: reviewAgentName(w.WindowState), Pane: w.ID, Turn: w.CompletionSeq + 1, Working: true})
		last = len(authors) - 1
		final.Judge = d.reviewJudge(w.sess, w.ID, repo.root, prevAt, time.Now().UnixNano())
		if final.Judge != nil {
			authors = append(authors, reviewOrigin{Kind: "person", Pane: w.ID, Session: w.sess.Name(), Turn: w.CompletionSeq + 1})
			final.Person = len(authors) - 1
		}
	}
	final.Author = last
	steps = append(steps, final)
	if err := review.Origins(ctx, diff.Files, steps, d.reviewOriginCache.read(repo.root), last); err != nil {
		return nil, err
	}
	return authors, nil
}

// agentPane is a pane with an agent, and its session.
type agentPane struct {
	WindowState
	sess *Session
}

// reviewPanes are the panes with an agent whose directory is in the work tree
// at root, by window id, across every session.
func (d *Daemon) reviewPanes(root string) map[string]agentPane {
	out := map[string]agentPane{}
	for _, sess := range d.manager.AllSessions() {
		for _, w := range sess.GetState().Windows {
			if w.AgentHarness == "" && w.AgentKind == "" {
				continue
			}
			if r, verr := d.paneRepo(sess, w); verr == nil && r.root == root {
				out[w.ID] = agentPane{WindowState: w, sess: sess}
			}
		}
	}
	return out
}

// reviewAgentName is the name an author shows: the agent's harness, else its
// kind, else the pane's name.
func reviewAgentName(w WindowState) string {
	for _, s := range []string{w.AgentHarness, w.AgentKind, w.CustomName, w.Title} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return "agent"
}
