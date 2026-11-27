package session

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// review-revert: taking one hunk of a pane's diff against its base out of the
// working file, and putting it back.
//
// It writes the working tree of the worktree under the pane, and nothing
// else: the index is not touched. Like review-stage it is the person's tool:
// scopeDeny, write over a link, and a human_nonce, when given, must be live.
//
// A revert names the hunk by its header and its digest (review.Hunk.Sum), as
// the caller read it with review-diff, with the same base and context. The
// file is read again here, and a hunk that is not there now, or not the same,
// is refused: the person reverts only what they saw. The patch is kept in the
// daemon under an id, which undo takes. git apply refuses a patch whose file
// changed around the hunk, so neither ever writes over an edit made after.
// The kept patches live as long as the daemon, the newest
// reviewRevertKept of them.

// ErrVerbAgentWriting reports a revert of a hunk whose agent works now. A
// revert under a live writer races its next edit. Nothing was changed.
const ErrVerbAgentWriting = "agent_writing"

// ErrVerbRevertChanged reports an undo of a revert whose file changed since,
// or whose patch the daemon no longer has. Nothing was changed.
const ErrVerbRevertChanged = "revert_changed"

// reviewRevertActions are what review-revert does.
var reviewRevertActions = []string{"revert", "undo"}

// reviewRevertKept bounds the patches kept for undo.
const reviewRevertKept = 64

// reviewRevert is one revert's patch, kept for its undo.
type reviewRevert struct {
	id, root, path, patch string
}

// reviewRevertStore keeps the newest reverts for undo. Its zero value is
// ready.
type reviewRevertStore struct {
	mu   sync.Mutex
	kept []reviewRevert
}

func (s *reviewRevertStore) add(r reviewRevert) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept = append(s.kept, r)
	if len(s.kept) > reviewRevertKept {
		s.kept = slices.Delete(s.kept, 0, len(s.kept)-reviewRevertKept)
	}
}

func (s *reviewRevertStore) get(id, root string) (reviewRevert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.kept, func(r reviewRevert) bool { return r.id == id && r.root == root })
	if i < 0 {
		return reviewRevert{}, false
	}
	return s.kept[i], true
}

func (s *reviewRevertStore) drop(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kept = slices.DeleteFunc(s.kept, func(r reviewRevert) bool { return r.id == id })
}

// reviewRevertParams are what review-revert takes.
type reviewRevertParams struct {
	Session     string `json:"session"`
	Window      string `json:"window"`
	Action      string `json:"action"`
	Path        string `json:"path"`
	Hunk        string `json:"hunk"`
	Sum         string `json:"sum"`
	Base        string `json:"base"`
	Uncommitted bool   `json:"uncommitted"`
	Context     *int   `json:"context"`
	ID          string `json:"id"`
	HumanNonce  string `json:"human_nonce"`
}

// verbReviewRevert answers review-revert.
func (d *Daemon) verbReviewRevert(cs *connState, params json.RawMessage) (any, *verbError) {
	var p reviewRevertParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(reviewRevertActions, p.Action) {
		return nil, invalidParam("action", "action is revert or undo", reviewRevertActions...)
	}
	if verr := refuseForwardedPane(cs, "review-revert"); verr != nil {
		return nil, verr
	}
	ctxLines := 3
	if p.Context != nil {
		if *p.Context < 0 || *p.Context > 20 {
			return nil, invalidParam("context", "context is 0 to 20 lines")
		}
		ctxLines = *p.Context
	}
	if p.Action == "undo" {
		if p.ID == "" {
			return nil, invalidParam("id", "id is required: the id of the revert to undo")
		}
	} else {
		switch {
		case p.Path == "":
			return nil, invalidParam("path", "path is required: the file, relative to the repository root")
		case p.Hunk == "" || len(p.Hunk) > 512:
			return nil, invalidParam("hunk", "hunk is required: a hunk header, at most 512 bytes")
		case p.Sum == "":
			return nil, invalidParam("sum", "sum is required: the hunk's digest, as the caller computed it from the lines it read")
		}
		if err := review.ValidPath(p.Path); err != nil {
			return nil, invalidParam("path", err.Error())
		}
		p.Path = filepath.ToSlash(filepath.Clean(p.Path))
		if p.Base != "" {
			if err := review.ValidRef(p.Base); err != nil {
				return nil, invalidParam("base", err.Error())
			}
		}
	}
	// As review-stage: a nonce no client holds is refused before the pane
	// is looked up.
	if p.HumanNonce != "" && !d.humanNonceHeld(p.HumanNonce, cs) {
		if _, verr := d.reviewAuthorOf(cs, p.HumanNonce, "", "review-revert"); verr != nil {
			return nil, verr
		}
	}
	sess, target, repo, verr := d.reviewTarget(cs, p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	if _, verr := d.reviewAuthorOf(cs, p.HumanNonce, sess.ID, "review-revert"); verr != nil {
		return nil, verr
	}

	ctx, cancel := context.WithTimeout(d.ctx, reviewTimeout)
	defer cancel()
	unlock := reviewStageLock(repo.root)
	defer unlock()

	out := map[string]any{
		"type":     "review_revert",
		"session":  sess.Name(),
		"window":   target.ID,
		"worktree": repo.root,
		"action":   p.Action,
	}
	if p.Action == "undo" {
		kept, ok := d.reviewReverts.get(p.ID, repo.root)
		if !ok {
			return nil, hintedVerbError(ErrVerbRevertChanged, "no revert "+echoName(p.ID)+" is kept for this worktree", &VerbHint{
				Detail: "Nothing was changed. A revert is kept only while the daemon runs, and only the newest are kept.",
			})
		}
		if err := review.ApplyToWork(ctx, repo.root, kept.patch, true); err != nil {
			return nil, hintedVerbError(ErrVerbRevertChanged, kept.path+" changed since the revert, so the hunk cannot be put back: "+err.Error(), &VerbHint{
				Verb:   "review-diff",
				Detail: "Nothing was changed. Read the diff again with review-diff.",
			})
		}
		d.reviewReverts.drop(p.ID)
		out["path"], out["id"] = kept.path, kept.id
		return out, nil
	}

	// The file itself may be gone (a deletion), so its folder is what has
	// to resolve inside the worktree, symbolic links followed.
	if dir := path.Dir(p.Path); dir != "." {
		if _, err := review.InsideRoot(repo.root, dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, invalidParam("path", err.Error())
		}
	}
	base, err := review.ResolveBase(ctx, repo.root, p.Base, repo.recorded, p.Uncommitted)
	if err != nil {
		return nil, reviewStageGitFailed(err)
	}
	diff, err := review.Build(ctx, review.Options{Dir: repo.root, Base: base, Paths: []string{p.Path}, Context: ctxLines, MaxUntracked: d.checkpoints.maxUntracked.Load()})
	if err != nil {
		return nil, reviewStageGitFailed(err)
	}
	changed := func() *verbError {
		return hintedVerbError(ErrVerbHunkChanged, p.Path+" changed since you read it, so the hunk "+echoName(p.Hunk)+" is not there as you saw it", &VerbHint{
			Verb:   "review-diff",
			Param:  "hunk",
			Detail: "Nothing was changed. Read the diff again with review-diff, the same base and context.",
		})
	}
	f := diff.FileByPath(p.Path)
	if f == nil {
		return nil, changed()
	}
	if f.Binary || f.Truncated || f.Status == review.StatusRenamed {
		return nil, hintedVerbError(ErrVerbWholeFileOnly, p.Path+" is a rename, a binary file or past the diff caps, so it has no hunk to revert", &VerbHint{
			Detail: "Nothing was changed. Change the file yourself.",
		})
	}
	h := f.HunkByHeader(p.Hunk)
	if h == nil || h.Sum() != p.Sum {
		return nil, changed()
	}
	if who := d.reviewHunkWriter(ctx, repo, diff, h); who != "" {
		return nil, hintedVerbError(ErrVerbAgentWriting, who+" writes this hunk now, so it is not reverted while it works", &VerbHint{
			Detail: "Nothing was changed. Revert it once the agent is at rest, or send it a note with review-note and send-review.",
		})
	}
	patch, err := review.RevertPatch(f, h)
	if err != nil {
		return nil, invalidParam("hunk", err.Error())
	}
	if err := review.ApplyToWork(ctx, repo.root, patch, false); err != nil {
		return nil, reviewStageGitFailed(err)
	}
	id := rand.Text()[:12]
	d.reviewReverts.add(reviewRevert{id: id, root: repo.root, path: p.Path, patch: patch})
	out["path"], out["id"], out["lines"] = p.Path, id, len(h.Changes())
	return out, nil
}

// reviewHunkWriter is the agent that made a line of hunk h of diff and works
// now, empty for none. Who made each line is read as review-diff's origins
// read it. A chain that cannot be read names no one.
func (d *Daemon) reviewHunkWriter(ctx context.Context, repo reviewRepo, diff *review.Diff, h *review.Hunk) string {
	authors, err := d.reviewOrigins(ctx, repo, diff)
	if err != nil {
		return ""
	}
	panes := d.reviewPanes(repo.root)
	for _, ln := range h.Lines {
		if ln.Op == review.OpContext || ln.Origin < 1 || ln.Origin > len(authors) {
			continue
		}
		a := authors[ln.Origin-1]
		if p, ok := panes[a.Pane]; ok && a.Kind == "agent" && p.AgentState == AgentStateWorking {
			return a.Agent
		}
	}
	return ""
}
