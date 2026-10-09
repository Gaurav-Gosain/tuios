package session

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/review"
)

// review-stage: staging and unstaging a pane's changes by file, by hunk and by
// line, and undoing either.
//
// It writes the index of the worktree under the pane, and nothing else: the
// working tree is never written. It is the person's tool, held like keep-fan:
// scopeDeny, so a restricted connection is refused and a pane needs the admin
// grant, and write over a link. A human_nonce, when given, must be live.
//
// Every stage and unstage answers with an undo object: the index entries of
// the paths it touched as they were before, and as they are after. undo takes
// that object back, refuses with index_changed unless the entries are still
// as after, and writes the before entries back with git update-index. Its own
// answer carries the reverse object, so sending that back redoes the call.
//
// One call at a time per worktree (reviewStageLock): a stage reads the diff,
// builds a patch from it and applies it, and a second call between the read
// and the apply would make the patch stale.

// Error codes review-stage raises, on top of the shared ones.
const (
	// ErrVerbHunkChanged reports a hunk or a file that is not in the diff
	// any more, or not as the caller saw it. Nothing was changed.
	ErrVerbHunkChanged = "hunk_changed"
	// ErrVerbIndexChanged reports an undo of a call whose result is not in
	// the index any more. Nothing was changed.
	ErrVerbIndexChanged = "index_changed"
	// ErrVerbWholeFileOnly reports a hunk or lines asked of a file that is
	// staged only whole: a rename, a binary file, or one past the diff caps.
	ErrVerbWholeFileOnly = "whole_file_only"
)

// reviewStageMaxEntries bounds the entries an undo object carries: a file and
// the old path of its rename.
const reviewStageMaxEntries = 2

// reviewStageLocks holds one mutex per worktree root.
var reviewStageLocks sync.Map

// reviewStageLock locks the worktree at root for one review-stage call, and
// returns the unlock.
func reviewStageLock(root string) func() {
	mu, _ := reviewStageLocks.LoadOrStore(root, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

// reviewUndo is the undo object of a review-stage result.
type reviewUndo struct {
	Entries []review.IndexEntry `json:"entries"`
	After   []review.IndexEntry `json:"after"`
}

// reviewStageParams are what review-stage takes.
type reviewStageParams struct {
	Session    string      `json:"session"`
	Window     string      `json:"window"`
	Action     string      `json:"action"`
	Path       string      `json:"path"`
	Hunk       string      `json:"hunk"`
	Lines      *[]int      `json:"lines"`
	Context    *int        `json:"context"`
	Undo       *reviewUndo `json:"undo"`
	HumanNonce string      `json:"human_nonce"`
}

// verbReviewStage answers review-stage.
func (d *Daemon) verbReviewStage(cs *connState, params json.RawMessage) (any, *verbError) {
	var p reviewStageParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(reviewStageActions, p.Action) {
		return nil, invalidParam("action", "action is stage, unstage or undo", reviewStageActions...)
	}
	if verr := refuseForwardedPane(cs, "review-stage"); verr != nil {
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
		if p.Undo == nil || len(p.Undo.Entries) == 0 {
			return nil, invalidParam("undo", "undo is required: the undo object of the call to undo")
		}
		if p.Hunk != "" || p.Lines != nil {
			return nil, invalidParam("undo", "undo takes the undo object only, no hunk and no lines")
		}
	} else {
		if p.Undo != nil {
			return nil, invalidParam("undo", "undo is for action undo only")
		}
		if p.Path == "" {
			return nil, invalidParam("path", "path is required: the file, relative to the repository root")
		}
		if p.Lines != nil && p.Hunk == "" {
			return nil, invalidParam("lines", "lines are indices into a hunk, so they need hunk")
		}
		if len(p.Hunk) > 512 {
			return nil, invalidParam("hunk", "hunk is a hunk header, at most 512 bytes")
		}
	}
	if p.Path != "" {
		if err := review.ValidPath(p.Path); err != nil {
			return nil, invalidParam("path", err.Error())
		}
		p.Path = filepath.ToSlash(filepath.Clean(p.Path))
	}
	if _, verr := d.reviewAuthorOf(cs, p.HumanNonce, "review-stage"); verr != nil {
		return nil, verr
	}
	sess, target, repo, verr := d.reviewTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}

	// The file itself may be gone (a deletion), so its folder is what has
	// to resolve inside the worktree, symbolic links followed.
	for _, e := range reviewStagePaths(p) {
		if dir := path.Dir(e); dir != "." {
			if _, err := review.InsideRoot(repo.root, dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, invalidParam("path", err.Error())
			}
		}
	}

	ctx, cancel := context.WithTimeout(d.ctx, reviewTimeout)
	defer cancel()
	unlock := reviewStageLock(repo.root)
	defer unlock()

	var (
		undo  reviewUndo
		named = p.Path
	)
	if p.Action == "undo" {
		undo, verr = reviewStageUndo(ctx, repo.root, *p.Undo)
		if named == "" {
			named = p.Undo.Entries[0].Path
		}
	} else {
		undo, verr = reviewStageChange(ctx, repo.root, p, ctxLines)
	}
	if verr != nil {
		return nil, verr
	}
	return map[string]any{
		"type":     "review_stage",
		"session":  sess.Name(),
		"window":   target.ID,
		"worktree": repo.root,
		"path":     named,
		"action":   p.Action,
		"undo":     undo,
	}, nil
}

// reviewStagePaths are the paths a call names: path, or the undo object's.
func reviewStagePaths(p reviewStageParams) []string {
	if p.Undo == nil {
		return []string{p.Path}
	}
	out := []string{}
	for _, e := range p.Undo.Entries {
		out = append(out, e.Path)
	}
	return out
}

// reviewStageChange stages or unstages p.Path, its hunk p.Hunk, or the lines
// p.Lines of that hunk.
func reviewStageChange(ctx context.Context, root string, p reviewStageParams, contextLines int) (reviewUndo, *verbError) {
	unstage := p.Action == "unstage"
	mode := review.IndexUnstaged
	if unstage {
		mode = review.IndexStaged
	}
	f, err := review.IndexChange(ctx, root, mode, p.Path, contextLines)
	if err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}
	if f == nil {
		what := "unstaged"
		if unstage {
			what = "staged"
		}
		return reviewUndo{}, hintedVerbError(ErrVerbHunkChanged, p.Path+" has no "+what+" change now", &VerbHint{
			Verb:   "review-diff",
			Param:  "path",
			Detail: "Nothing was changed. Read the diff again with review-diff and index " + mode + ".",
		})
	}
	paths := []string{f.Path}
	if f.OldPath != "" {
		paths = append(paths, f.OldPath)
	}
	before, err := review.IndexEntries(ctx, root, paths)
	if err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}

	whole := p.Hunk == ""
	var patch string
	if !whole {
		if f.Status == review.StatusRenamed || f.Binary || f.Truncated {
			why := "is a rename"
			switch {
			case f.Binary:
				why = "is a binary file"
			case f.Truncated:
				why = "is past the diff caps"
			}
			return reviewUndo{}, hintedVerbError(ErrVerbWholeFileOnly, p.Path+" "+why+", so it is "+p.Action+"d only whole", &VerbHint{
				Param:  "hunk",
				Detail: "Nothing was changed. Leave hunk and lines out to " + p.Action + " the whole file.",
			})
		}
		h := f.HunkByHeader(p.Hunk)
		if h == nil {
			return reviewUndo{}, hintedVerbError(ErrVerbHunkChanged, "the hunk "+echoName(p.Hunk)+" of "+p.Path+" is not in the diff now", &VerbHint{
				Verb:   "review-diff",
				Param:  "hunk",
				Detail: "Nothing was changed. The diff changed since it was read. Read it again with review-diff, index " + mode + " and the same context.",
			})
		}
		sel := h.Changes()
		if p.Lines != nil {
			sel = *p.Lines
			for _, i := range sel {
				if i < 0 || i >= len(h.Lines) {
					return reviewUndo{}, invalidParam("lines", "line "+strconv.Itoa(i)+" is outside the hunk, which has "+strconv.Itoa(len(h.Lines))+" lines (0 to "+strconv.Itoa(len(h.Lines)-1)+")")
				}
			}
		}
		// A patch cannot remove a file from the index, only empty it. A
		// selection of every line of a file the index side drops (a
		// deletion to stage, an addition to unstage) is the whole file.
		drops := (!unstage && f.Status == review.StatusDeleted) || (unstage && f.Status == review.StatusAdded)
		if drops && len(f.Hunks) == 1 && reviewSelectsAll(h, sel) {
			whole = true
		} else {
			var err error
			if patch, err = review.SelectionPatch(f, h, sel, unstage); err != nil {
				return reviewUndo{}, invalidParam("lines", err.Error())
			}
		}
	}
	if whole {
		if unstage {
			err = review.UnstageFile(ctx, root, paths...)
		} else {
			err = review.StageFile(ctx, root, paths...)
		}
		if err != nil {
			return reviewUndo{}, reviewStageGitFailed(err)
		}
	} else if err := review.ApplyToIndex(ctx, root, patch, contextLines == 0); err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}
	after, err := review.IndexEntries(ctx, root, paths)
	if err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}
	return reviewUndo{Entries: before, After: after}, nil
}

// reviewSelectsAll reports whether sel holds every added and removed line of
// h.
func reviewSelectsAll(h *review.Hunk, sel []int) bool {
	for _, i := range h.Changes() {
		if !slices.Contains(sel, i) {
			return false
		}
	}
	return true
}

// reviewStageUndo writes u.Entries back into the index, when the index still
// holds u.After for the same paths.
func reviewStageUndo(ctx context.Context, root string, u reviewUndo) (reviewUndo, *verbError) {
	if len(u.Entries) > reviewStageMaxEntries || len(u.After) != len(u.Entries) {
		return reviewUndo{}, invalidParam("undo", "undo is the undo object a review-stage result returned: entries and after, one per path, at most "+strconv.Itoa(reviewStageMaxEntries))
	}
	paths := make([]string, len(u.Entries))
	for i, e := range u.Entries {
		if slices.Contains(paths[:i], e.Path) {
			return reviewUndo{}, invalidParam("undo", "undo names "+echoName(e.Path)+" twice")
		}
		if u.After[i].Path != e.Path {
			return reviewUndo{}, invalidParam("undo", "undo's entries and after name different paths")
		}
		for _, x := range []review.IndexEntry{e, u.After[i]} {
			if err := review.ValidEntry(ctx, root, x); err != nil {
				return reviewUndo{}, invalidParam("undo", err.Error())
			}
		}
		paths[i] = e.Path
	}
	now, err := review.IndexEntries(ctx, root, paths)
	if err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}
	if !slices.Equal(now, u.After) {
		return reviewUndo{}, hintedVerbError(ErrVerbIndexChanged, "the index changed since that call, so it cannot be undone", &VerbHint{
			Verb:   "review-diff",
			Detail: "Nothing was changed. Something else staged or unstaged these paths after the call. Read the diff again and stage or unstage what you need.",
		})
	}
	if err := review.SetIndexEntries(ctx, root, u.Entries); err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}
	restored, err := review.IndexEntries(ctx, root, paths)
	if err != nil {
		return reviewUndo{}, reviewStageGitFailed(err)
	}
	return reviewUndo{Entries: u.After, After: restored}, nil
}

// reviewStageGitFailed is the refusal for a git call of review-stage that
// failed. A conflicted path has its own message.
func reviewStageGitFailed(err error) *verbError {
	if c, ok := errors.AsType[*review.ConflictError](err); ok {
		return hintedVerbError(ErrVerbGitFailed, c.Error(), &VerbHint{
			Detail: "Nothing was changed. Resolve the conflict in the worktree first.",
		})
	}
	detail := "The working tree was not changed. Read the diff again with review-diff to see the index."
	if errors.Is(err, context.DeadlineExceeded) {
		detail = "git took longer than " + reviewTimeout.String() + ", so the call was given up. The working tree was not changed."
	}
	return hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{Detail: detail})
}
