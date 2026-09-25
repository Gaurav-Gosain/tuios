package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Review: the diff of what an agent changed, the notes the person leaves on
// it, and sending those notes to the agent.
//
// review-diff reads, and is held like every read: a pane without the admin
// grant reaches its own session and its fan group (scopeRead), and a link needs
// write, because the answer carries file contents that list must not reach
// (the reasoning bundle-worktree follows). review-note writes the daemon's note
// store and send-review types into the pane through the delivery queue, so
// both are scopeWrite, and send-review is a typing verb (typingVerbs), which
// holds the target to panes that hold nothing the caller does not. A note or a
// message is the person's only with a live human nonce.
//
// Where each piece is enforced:
//
//   - The session reach of all three is checkScope and checkGrants on the
//     named session, before the handler runs. review-diff's against names a
//     second session, which is held to the same reach here
//     (callerReachesSession) and must be a sibling of the same fan.
//   - send-review's target is held by holdTypingTarget at the call, and the
//     delivery queue checks the caller's grants against it again when it
//     types (queueOriginRefusal), and refuses a pane on needs_input then.
//   - Who wrote a note, and who sent a message, is the daemon's reading of the
//     connection (reviewAuthor), never a parameter: human only with a live
//     human_nonce (verifyAnyHumanNonce, which refuses any process in a pane).
//   - A note is typed into its pane when it is sent, whoever sends it, so a
//     pane without admin may add or edit a note only on a pane it could type
//     into itself (reviewNoteTargetRefusal, the typingRefusal rule). When the
//     notes are sent, each one's author is held to that rule again as its
//     grants are now (reviewNoteAuthorRefusal), and a note whose author may
//     no longer type there, or whose pane is gone, is withheld: not typed and
//     not marked sent. The person's nonce never carries a pane's words.
//   - The message labels every note written by someone other than its sender
//     with its author (review.Compose), so a pane's note sent by the person
//     never reads as the person's.
//   - A note is changed or removed only by whoever may speak for its author:
//     the person any note, a pane its own, a linked machine its own, and a
//     caller outside every pane every note but the person's.
//   - The git calls read. The working state is written as a tree through a
//     temporary index (review.Build), so the worktree's index and files are
//     never changed. Every call is bounded by reviewTimeout.

// Error codes the review verbs raise, on top of the shared ones.
const (
	// ErrVerbNotRepo reports a pane or session with no git repository under
	// it, so there is nothing to review. Nothing was read.
	ErrVerbNotRepo = "not_repo"
	// ErrVerbNoNotes reports a send-review that found no unsent notes to
	// send. Nothing was typed.
	ErrVerbNoNotes = "no_notes"
)

// reviewTimeout bounds every git call one review verb makes, together.
const reviewTimeout = 10 * time.Second

// reviewMaxPaths bounds the paths filter of review-diff.
const reviewMaxPaths = 256

// reviewRepo is the repository under a pane.
type reviewRepo struct {
	// root is the worktree's own root, symbolic links resolved: the key of
	// its notes and where git runs.
	root string
	// repoRoot is the main checkout of the repository.
	repoRoot string
	// recorded is the base the worktree was made from, when tuios made it.
	recorded string
	// info is the session's worktree record, nil for a plain repository.
	info *WorktreeInfo
}

// reviewTarget resolves the pane a review verb is about, the focused one when
// window is empty, and the repository under it: the session's worktree when
// the pane is in it, else the repository holding the pane's directory.
func (d *Daemon) reviewTarget(sessionName, window string) (*Session, WindowState, reviewRepo, *verbError) {
	sess, verr := d.resolveVerbSession(sessionName)
	if verr != nil {
		return nil, WindowState{}, reviewRepo{}, verr
	}
	state := sess.GetState()
	if window == "" {
		id, err := focusedWindowID(state)
		if err != nil {
			return nil, WindowState{}, reviewRepo{}, mapResolveErr(err, sess)
		}
		window = id
	}
	idx, err := findWindowStateIndex(state.Windows, window)
	if err != nil {
		return nil, WindowState{}, reviewRepo{}, mapResolveErr(err, sess)
	}
	target := state.Windows[idx]
	if target.Host != "" {
		return nil, WindowState{}, reviewRepo{}, hintedVerbError(ErrVerbNotRepo, "window "+shortWindowID(target.ID)+" runs on "+echoName(target.Host)+", and its repository is there, not here", &VerbHint{
			Command: "tuios worktree pull " + target.Host + ":<session>",
			Detail:  "Nothing was read. Reviewing a pane on another machine is not supported yet: attach to that machine and review it there, or bring its work here with tuios worktree pull and review that.",
		})
	}
	cwd := target.Cwd
	if pty := sess.GetPTY(target.PTYID); pty != nil {
		if live, ok := pty.ProcessCwd(); ok && live != "" {
			cwd = live
		}
	}
	if wt := sess.worktreeListing(); wt != nil && !wt.Gone && (cwd == "" || within(canonRoot(wt.Path), canonRoot(cwd))) {
		recorded := wt.Base
		if recorded == "" && wt.Managed {
			// A worktree tuios made from HEAD left the main checkout's
			// branch, which is its base, as compare-fan counts it.
			if b, err := worktree.CurrentBranch(wt.RepoRoot); err == nil && b != "" && b != wt.Branch {
				recorded = b
			}
		}
		return sess, target, reviewRepo{root: canonRoot(wt.Path), repoRoot: wt.RepoRoot, recorded: recorded, info: wt}, nil
	}
	if cwd != "" {
		if _, common, root, ok := gitstate.Locate(cwd); ok {
			repoRoot := common
			if filepath.Base(common) == ".git" {
				repoRoot = filepath.Dir(common)
			}
			return sess, target, reviewRepo{root: canonRoot(root), repoRoot: repoRoot}, nil
		}
	}
	return nil, WindowState{}, reviewRepo{}, hintedVerbError(ErrVerbNotRepo, "no git repository is under window "+shortWindowID(target.ID), &VerbHint{
		Param:  "window",
		Detail: "Nothing was read. Review a pane whose directory is inside a git repository, or a worktree session.",
	})
}

// within reports whether dir is root or under it.
func within(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// reviewParams are what review-diff takes.
type reviewParams struct {
	Session     string   `json:"session"`
	Window      string   `json:"window"`
	Base        string   `json:"base"`
	Against     string   `json:"against"`
	Uncommitted bool     `json:"uncommitted"`
	Paths       []string `json:"paths"`
	Context     *int     `json:"context"`
}

// verbReviewDiff answers review-diff.
func (d *Daemon) verbReviewDiff(cs *connState, params json.RawMessage) (any, *verbError) {
	var p reviewParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
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
	if p.Base != "" {
		if err := review.ValidRef(p.Base); err != nil {
			return nil, invalidParam("base", err.Error())
		}
	}
	if p.Against != "" && (p.Base != "" || p.Uncommitted) {
		return nil, invalidParam("against", "against compares two attempts with each other, so it takes no base and no uncommitted")
	}
	sess, target, repo, verr := d.reviewTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}

	ctx, cancel := context.WithTimeout(d.ctx, reviewTimeout)
	defer cancel()
	opt := review.Options{Dir: repo.root, Paths: p.Paths, Context: lines}
	againstName := ""
	if p.Against != "" {
		sib, verr := d.reviewSibling(cs, sess, repo, p.Against)
		if verr != nil {
			return nil, verr
		}
		againstName = sib.Name
		opt.AgainstDir = sib.Worktree().Path
	} else {
		base, err := review.ResolveBase(ctx, repo.root, p.Base, repo.recorded, p.Uncommitted)
		if err != nil {
			if _, ok := errors.AsType[*review.RefError](err); ok {
				return nil, hintedVerbError(ErrVerbInvalidParams, "base "+echoName(p.Base)+" is not a commit in "+repo.root, &VerbHint{
					Param:  "base",
					Detail: "Nothing was read. Name a branch, a tag or a commit of the repository, or leave base out for the worktree's own base.",
				})
			}
			return nil, reviewGitFailed(err)
		}
		opt.Base = base
	}
	diff, err := review.Build(ctx, opt)
	if err != nil {
		return nil, reviewGitFailed(err)
	}

	if againstName == "" {
		d.reanchorReviewNotes(ctx, repo, target.ID, diff, p.Paths)
		d.reviewNotes.setBase(repo.root, target.ID, diff.Base)
	}
	notes, _ := d.reviewNotes.list(repo.root, target.ID)
	out := map[string]any{
		"type":        "review_diff",
		"session":     sess.Name,
		"window":      target.ID,
		"repo_root":   repo.repoRoot,
		"worktree":    repo.root,
		"base":        diff.Base,
		"base_sha":    diff.BaseSHA,
		"uncommitted": diff.Uncommitted,
		"tree_sha":    diff.TreeSHA,
		"files":       diff.Files,
		"totals":      diff.Totals,
		"truncated":   diff.Truncated,
		"notes":       notes,
		"untrusted":   true,
	}
	if againstName != "" {
		out["against"] = againstName
		out["against_tree"] = diff.AgainstTree
	}
	return out, nil
}

// reviewGitFailed is the refusal for a git call that failed or ran out of
// time.
func reviewGitFailed(err error) *verbError {
	detail := "Nothing in the repository was changed."
	if errors.Is(err, context.DeadlineExceeded) {
		detail = "git took longer than " + reviewTimeout.String() + ", so the diff was given up. Nothing in the repository was changed. Narrow it with paths."
	}
	return hintedVerbError(ErrVerbGitFailed, err.Error(), &VerbHint{Detail: detail})
}

// reviewSibling resolves against: a session of the same fan as the reviewed
// one, that the caller could name itself.
func (d *Daemon) reviewSibling(cs *connState, sess *Session, repo reviewRepo, name string) (*Session, *verbError) {
	if repo.info == nil || repo.info.Group == "" {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+sess.Name+" is not part of a fan, so it has no attempt to compare against", &VerbHint{
			Param:  "against",
			Detail: "Nothing was read. against names another attempt of the same fan. Leave it out to review against the base.",
		})
	}
	sib, info, verr := d.worktreeTarget(name)
	if verr != nil {
		return nil, verr
	}
	if sib == sess || info.Group != repo.info.Group || info.RepoRoot != repo.info.RepoRoot {
		return nil, hintedVerbError(ErrVerbInvalidParams, "session "+sib.Name+" is not another attempt of fan "+repo.info.Group, &VerbHint{
			Param:   "against",
			Command: "tuios fan compare " + sess.Name,
			Detail:  "Nothing was read. against names another session of the same fan.",
		})
	}
	if !d.callerReachesSession(cs, sib.Name) {
		return nil, hintedVerbError(ErrVerbForbidden, "session "+echoName(name)+" is outside what this caller reaches", &VerbHint{
			Param:  "against",
			Detail: "Nothing was read. A pane without the admin grant reaches its own session and its fan group.",
		})
	}
	if wt := sib.worktreeListing(); wt == nil || wt.Gone {
		return nil, hintedVerbError(ErrVerbNotRepo, "the worktree of session "+sib.Name+" is gone", &VerbHint{Param: "against"})
	}
	return sib, nil
}

// reanchorReviewNotes finds each note of the pane again in the diff just
// read, and records where it sits now or that it is outdated. Only notes on
// the paths the diff covered are looked at. A note on the old side is looked
// for in the diff's base; a note on a whole hunk among the file's hunks.
func (d *Daemon) reanchorReviewNotes(ctx context.Context, repo reviewRepo, window string, diff *review.Diff, paths []string) {
	notes, _ := d.reviewNotes.list(repo.root, window)
	if len(notes) == 0 {
		return
	}
	newLines := map[string][]string{}
	newErr := map[string]error{}
	oldLines := map[string][]string{}
	oldErr := map[string]error{}
	var moved []review.Note
	for _, n := range notes {
		if len(paths) > 0 && !slices.Contains(paths, n.Path) {
			continue
		}
		f := diff.FileByPath(n.Path)
		was := n
		switch {
		case n.IsHunk():
			if f == nil {
				n.Outdated = true
				break
			}
			if f.Truncated || f.Binary {
				continue
			}
			h, ok := review.AnchorHunk(f.Hunks, n.Side, n.HunkHeader, n.Line)
			n.Outdated = !ok
			if ok {
				n.HunkHeader = h.Header
				n.Line = h.NewStart
				if n.Side == review.SideOld {
					n.Line = h.OldStart
				}
			}
		case n.Side == review.SideOld:
			path := n.Path
			if f != nil && f.OldPath != "" {
				path = f.OldPath
			}
			lines, seen := oldLines[path]
			if !seen && oldErr[path] == nil {
				var err error
				lines, err = review.BlobLines(ctx, repo.root, diff.BaseSHA, path)
				oldLines[path], oldErr[path] = lines, err
			}
			if oldErr[path] != nil {
				if ctx.Err() != nil {
					return
				}
				n.Outdated = true
				break
			}
			line, ok := review.Anchor(lines, n.Line, n.Quote)
			n.Outdated = !ok
			if ok {
				n.Line = line
			}
		default:
			lines, seen := newLines[n.Path]
			if !seen && newErr[n.Path] == nil {
				var err error
				lines, err = review.FileLines(repo.root, n.Path)
				newLines[n.Path], newErr[n.Path] = lines, err
			}
			if err := newErr[n.Path]; err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					// Too big, or not a file: where the note sits is not
					// known, so it is left as it was.
					continue
				}
				n.Outdated = true
				break
			}
			line, ok := review.Anchor(lines, n.Line, n.Quote)
			n.Outdated = !ok
			if ok {
				n.Line = line
			}
		}
		if n.Line != was.Line || n.HunkHeader != was.HunkHeader || n.Outdated != was.Outdated {
			moved = append(moved, n)
		}
	}
	d.reviewNotes.applyAnchors(repo.root, window, moved)
}

// reviewAuthor is who the caller on cs is, as a note or a message records
// it: human with a live nonce, link:HOST over a link, the pane's window id
// for a process in a pane, shell otherwise. A nonce that does not verify is
// refused rather than dropped, so the caller learns it did not count.
type reviewAuthor struct {
	by    string
	human bool
	// pane is the caller's window, for a process in a pane.
	pane string
}

func (d *Daemon) reviewAuthorOf(cs *connState, nonce, verb string) (reviewAuthor, *verbError) {
	human := nonce != "" && d.verifyAnyHumanNonce(nonce, cs)
	if nonce != "" && !human {
		return reviewAuthor{}, hintedVerbError(ErrVerbNotHuman, "human_nonce does not belong to a client attached right now", &VerbHint{
			Param:  "human_nonce",
			Detail: "Nothing was changed. Only the person's attached client holds a nonce, and a process inside a pane cannot use one. Leave human_nonce out to call " + verb + " as yourself.",
		})
	}
	if human {
		return reviewAuthor{by: queueByHuman, human: true}, nil
	}
	if cs != nil && cs.viaLink {
		cs.mu.Lock()
		peer := cs.linkPeer
		cs.mu.Unlock()
		return reviewAuthor{by: queueByLinkPrefix + firstNonEmpty(peer, "*")}, nil
	}
	if pa := d.paneAuthority(cs); pa != nil {
		return reviewAuthor{by: pa.window, pane: pa.window}, nil
	}
	return reviewAuthor{by: queueByShell}, nil
}

// mayTouch reports whether the author may change or remove note n: the person
// any note, a caller outside every pane every note but the person's, and a
// pane or a linked machine only the notes it wrote.
func (a reviewAuthor) mayTouch(n review.Note) bool {
	switch {
	case a.human:
		return true
	case a.by == queueByShell:
		return n.By != queueByHuman
	default:
		return n.By == a.by
	}
}

// reviewNoteParams are what review-note takes.
type reviewNoteParams struct {
	Action     string `json:"action"`
	Session    string `json:"session"`
	Window     string `json:"window"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Side       string `json:"side"`
	Quote      string `json:"quote"`
	Hunk       string `json:"hunk"`
	Text       string `json:"text"`
	ID         string `json:"id"`
	HumanNonce string `json:"human_nonce"`
}

// verbReviewNote answers review-note.
func (d *Daemon) verbReviewNote(cs *connState, params json.RawMessage) (any, *verbError) {
	var p reviewNoteParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	if !slices.Contains(reviewNoteActions, p.Action) {
		return nil, invalidParam("action", "action is one of the note actions", reviewNoteActions...)
	}
	if p.Side != "" && !slices.Contains(reviewNoteSides, p.Side) {
		return nil, invalidParam("side", "side is new or old", reviewNoteSides...)
	}
	if p.Action != "list" {
		if verr := refuseForwardedPane(cs, "review-note"); verr != nil {
			return nil, verr
		}
	}
	text := review.CleanText(p.Text)
	switch p.Action {
	case "add", "edit":
		if text == "" {
			return nil, invalidParam("text", "text is required: what the note says")
		}
		if len(p.Text) > review.TextMax {
			return nil, invalidParam("text", "text is at most "+strconv.Itoa(review.TextMax)+" bytes")
		}
	}
	if (p.Action == "edit" || p.Action == "remove") && p.ID == "" {
		return nil, invalidParam("id", "id is required: the note to "+p.Action+" (see review-note list)")
	}
	author, verr := d.reviewAuthorOf(cs, p.HumanNonce, "review-note")
	if verr != nil {
		return nil, verr
	}
	sess, target, repo, verr := d.reviewTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	if p.Action == "add" || p.Action == "edit" {
		if verr := d.reviewNoteTargetRefusal(cs, target); verr != nil {
			return nil, verr
		}
	}
	out := map[string]any{"type": "review_notes", "session": sess.Name, "window": target.ID, "worktree": repo.root}

	switch p.Action {
	case "add":
		n, verr := d.newReviewNote(repo, p, text, author)
		if verr != nil {
			return nil, verr
		}
		added, ok := d.reviewNotes.add(repo.root, target.ID, n)
		if !ok {
			return nil, hintedVerbError(ErrVerbInvalidParams, "the worktree holds "+strconv.Itoa(reviewNotesMax)+" review notes, as many as it may", &VerbHint{
				Verb:   "review-note",
				Detail: "Nothing was added. Remove the notes that are dealt with (action remove or clear) first.",
			})
		}
		out["id"] = added.ID
	case "edit":
		switch d.reviewNotes.edit(repo.root, target.ID, p.ID, text, author.by, author.mayTouch) {
		case reviewNoteMissing:
			return nil, reviewNoteMissingError(p.ID)
		case reviewNoteRefused:
			return nil, reviewNoteRefusedError("edit", p.ID)
		}
		out["id"] = p.ID
	case "remove":
		_, found, refused := d.reviewNotes.remove(repo.root, target.ID, p.ID, author.mayTouch)
		switch {
		case !found:
			return nil, reviewNoteMissingError(p.ID)
		case refused > 0:
			return nil, reviewNoteRefusedError("remove", p.ID)
		}
		out["removed"] = 1
	case "clear":
		removed, _, refused := d.reviewNotes.remove(repo.root, target.ID, "", author.mayTouch)
		out["removed"] = removed
		// The notes clear matched and left alone, because this caller may
		// not speak for their author. Always present on clear, 0 for none.
		out["kept"] = refused
	}
	notes, _ := d.reviewNotes.list(repo.root, target.ID)
	out["notes"] = notes
	return out, nil
}

// newReviewNote builds the note review-note add asked for.
func (d *Daemon) newReviewNote(repo reviewRepo, p reviewNoteParams, text string, author reviewAuthor) (review.Note, *verbError) {
	if err := review.ValidPath(p.Path); err != nil {
		return review.Note{}, invalidParam("path", "path is required, relative to the repository root: "+err.Error())
	}
	side := p.Side
	if side == "" {
		side = review.SideNew
	}
	n := review.Note{Path: filepath.ToSlash(filepath.Clean(p.Path)), Side: side, Text: text, By: author.by, At: time.Now().UnixNano()}
	if p.Hunk != "" {
		if len(p.Hunk) > 512 {
			return review.Note{}, invalidParam("hunk", "hunk is a hunk header, at most 512 bytes")
		}
		oldStart, _, newStart, _, err := review.ParseHunkHeader(p.Hunk)
		if err != nil {
			return review.Note{}, invalidParam("hunk", "hunk is a header such as \"@@ -88,4 +100,6 @@\": "+err.Error())
		}
		n.HunkHeader = review.CleanQuote(p.Hunk)
		n.Line = newStart
		if side == review.SideOld {
			n.Line = oldStart
		}
		return n, nil
	}
	if p.Line < 1 {
		return review.Note{}, invalidParam("line", "line is required, from 1, for a note on a line; pass hunk for a note on a whole hunk")
	}
	n.Line = p.Line
	n.Quote = review.CleanQuote(p.Quote)
	if n.Quote == "" && side == review.SideNew {
		// The line as it is now, so the note can be found again when the
		// diff moves.
		if lines, err := review.FileLines(repo.root, n.Path); err == nil && p.Line <= len(lines) {
			n.Quote = review.CleanQuote(lines[p.Line-1])
		}
	}
	return n, nil
}

func reviewNoteMissingError(id string) *verbError {
	return hintedVerbError(ErrVerbInvalidParams, "no review note "+echoName(id)+" on this pane", &VerbHint{
		Param:   "id",
		Command: "tuios review notes",
		Detail:  "Nothing was changed. List the pane's notes to see their ids.",
	})
}

func reviewNoteRefusedError(action, id string) *verbError {
	return hintedVerbError(ErrVerbForbidden, action+" of review note "+echoName(id)+" is refused: it was written by someone this caller may not speak for", &VerbHint{
		Detail: "Nothing was changed. A pane or a linked machine changes only the notes it wrote, and the person's notes need the attached client's nonce.",
	})
}

// sendReviewParams are what send-review takes.
type sendReviewParams struct {
	Session    string   `json:"session"`
	Window     string   `json:"window"`
	IDs        []string `json:"ids"`
	Now        bool     `json:"now"`
	HumanNonce string   `json:"human_nonce"`
}

// verbSendReview answers send-review: the pane's unsent notes, or the ones
// named, composed into one message and handed to the delivery queue.
func (d *Daemon) verbSendReview(cs *connState, params json.RawMessage) (any, *verbError) {
	var p sendReviewParams
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	sess, target, repo, verr := d.reviewTarget(p.Session, p.Window)
	if verr != nil {
		return nil, verr
	}
	state := sess.GetState()
	target, verr = queueTarget(sess, state, target.ID)
	if verr != nil {
		return nil, verr
	}

	all, base := d.reviewNotes.list(repo.root, target.ID)
	var picked []review.Note
	if len(p.IDs) > 0 {
		for _, id := range p.IDs {
			i := slices.IndexFunc(all, func(n review.Note) bool { return n.ID == id })
			if i < 0 {
				return nil, reviewNoteMissingError(id)
			}
			if !slices.ContainsFunc(picked, func(n review.Note) bool { return n.ID == id }) {
				picked = append(picked, all[i])
			}
		}
	} else {
		for _, n := range all {
			if n.SentAt == 0 {
				picked = append(picked, n)
			}
		}
	}
	// A note is typed with the sender's authority, so each author is held to
	// what it may type into this pane now. The rest are withheld.
	var withheld []string
	withheldWhy := ""
	allowed := picked[:0:0]
	for _, n := range picked {
		if why := d.reviewNoteAuthorRefusal(n.By, sess, target); why != "" {
			withheld = append(withheld, n.ID)
			if withheldWhy == "" {
				withheldWhy = why
			}
			continue
		}
		allowed = append(allowed, n)
	}
	picked = allowed
	if len(picked) == 0 && len(withheld) > 0 {
		return nil, hintedVerbError(ErrVerbForbidden, "every note picked was withheld: "+withheldWhy, &VerbHint{
			Verb:   "review-note",
			Detail: "Nothing was sent. A note is typed into the pane with the authority of whoever wrote it, and its author may not type into window " + shortWindowID(target.ID) + " now. Edit the note to make it yours, or remove it.",
		})
	}
	if len(picked) == 0 {
		return nil, hintedVerbError(ErrVerbNoNotes, "window "+shortWindowID(target.ID)+" has no unsent review notes", &VerbHint{
			Verb:    "review-note",
			Command: "tuios review note -w " + shortWindowID(target.ID) + " FILE:LINE 'text'",
			Detail:  "Nothing was sent. Add a note first, or name notes already sent with ids to send them again.",
		})
	}

	// The entry decides who the message is from, the way queue-prompt
	// decides it: from the connection, the nonce checked.
	e, verr := d.newQueueEntry(cs, sess, state, "", "", p.HumanNonce)
	if verr != nil {
		return nil, verr
	}
	e.text = review.Compose(review.Message{
		From:       d.reviewSender(e),
		Base:       base,
		Notes:      picked,
		CleanQuote: func(s string) string { return attentionText(s, 4*review.QuoteMax) },
		SenderBy:   e.by,
		Author:     d.reviewAuthorName,
	})
	if len(e.text) > queueMaxText {
		return nil, invalidParam("ids", "the "+strconv.Itoa(len(picked))+" notes make a message longer than "+strconv.Itoa(queueMaxText)+" bytes; send fewer at a time with ids")
	}
	if p.Now {
		if target.AgentState == AgentStateNeedsInput {
			return nil, agentBlockedError(target)
		}
		if !d.agentReady(target, fanReadyStates) || d.queue.count(target.ID) > 0 {
			return nil, hintedVerbError(ErrVerbNotReady, "the agent in window "+shortWindowID(target.ID)+" is not at rest with nothing queued, so the notes were not sent", &VerbHint{
				Command: "tuios review send -w " + shortWindowID(target.ID),
				Detail:  "Nothing was sent. Leave now out to queue the notes: they are typed when the agent next comes to rest.",
			})
		}
	}
	res, verr := d.queuePrompt(sess, target, e)
	if verr != nil {
		return nil, verr
	}
	ids := make([]string, len(picked))
	for i, n := range picked {
		ids[i] = n.ID
	}
	d.reviewNotes.markSent(repo.root, target.ID, ids, time.Now().UnixNano())
	out := map[string]any{
		"type":       "review_sent",
		"session":    sess.Name,
		"window":     target.ID,
		"notes":      len(picked),
		"ids":        ids,
		"queued_id":  res.ID,
		"position":   res.Position,
		"queued":     res.Queued,
		"delivering": res.Delivering,
	}
	if len(withheld) > 0 {
		out["withheld"] = withheld
		out["withheld_reason"] = withheldWhy
	}
	return out, nil
}

// reviewNoteTargetRefusal holds a pane without admin that adds or edits a note
// to the panes it could type into itself. A note is typed into its pane when
// it is sent, possibly by the person, so without this a pane with write could
// have its words typed into a pane holding more than it does. The pane is not
// held to the needs_input rule here: nothing is typed now, and the queue never
// types over a prompt.
func (d *Daemon) reviewNoteTargetRefusal(cs *connState, target WindowState) *verbError {
	pa := d.paneAuthority(cs)
	if pa == nil || pa.hosted || pa.grants.Has(GrantAdmin) {
		return nil
	}
	if why := d.typingRefusal(pa, target, true); why != "" {
		return grantForbidden("review-note", pa, why+". A note is typed into its pane when it is sent")
	}
	return nil
}

// reviewNoteAuthorRefusal says why a note written by by may not be typed into
// target now, or "" when it may. The person and a caller outside every pane
// are held to nothing. A linked machine is held to its link policy as it is
// now. A pane is held to its grants as they are now, the way the delivery
// queue holds the pane that queued an entry: its write reach and the
// typingRefusal rule. A pane that is gone can no longer be held to anything,
// so its notes are refused.
func (d *Daemon) reviewNoteAuthorRefusal(by string, sess *Session, target WindowState) string {
	switch {
	case by == queueByHuman || by == queueByShell:
		return ""
	case strings.HasPrefix(by, queueByLinkPrefix):
		peer := strings.TrimPrefix(by, queueByLinkPrefix)
		if peer == "*" {
			peer = ""
		}
		var hosts linkPolicyTable
		if t := d.linkPolicies.Load(); t != nil {
			hosts = *t
		}
		if !config.LinkPolicyFor(hosts, peer).Allows(config.LinkAllowWrite) {
			return "the link from " + firstNonEmpty(printableClaim(peer, 40), "a machine that gave no name") + " that wrote a note may no longer write here"
		}
		return ""
	case strings.HasPrefix(by, "hosted:"):
		if !d.manager.grants.defaults().Has(GrantAdmin) {
			return "a note was written by a pane that runs here for another machine, and the default grants no longer reach any session"
		}
		return ""
	}
	session := d.sessionOfWindow(by)
	if session == "" {
		return "window " + shortWindowID(by) + " that wrote a note is gone, so what it may type cannot be checked"
	}
	g, explicit := d.manager.grants.effective(by)
	if g.Has(GrantAdmin) {
		return ""
	}
	pa := &paneAuth{window: by, session: session, grants: g, explicit: explicit}
	if why := d.paneWriteReach(pa, sess.Name); why != "" {
		return "window " + shortWindowID(by) + " that wrote a note holds " + g.String() + " now: " + why
	}
	if why := d.typingRefusal(pa, target, true); why != "" {
		return "window " + shortWindowID(by) + " that wrote a note holds " + g.String() + " now: " + why
	}
	return ""
}

// reviewAuthorName names the author of a note in a message: a pane by its
// window's label, else review.AuthorName.
func (d *Daemon) reviewAuthorName(by string) string {
	switch {
	case by == queueByHuman || by == queueByShell || strings.HasPrefix(by, queueByLinkPrefix):
		return review.AuthorName(by)
	case strings.HasPrefix(by, "hosted:"):
		return "a pane on another machine"
	}
	if sn := d.sessionOfWindow(by); sn != "" {
		if s := d.manager.GetSession(sn); s != nil {
			if w, ok := findWindowState(s.GetState(), by); ok {
				if name := printableClaim(windowLabelOf(w), 40); name != "" {
					return "pane " + name
				}
			}
		}
	}
	return "pane " + shortWindowID(by)
}

// reviewSender names who a message of notes is from, as its header says it.
// "the person" needs the verified nonce the entry was built on.
func (d *Daemon) reviewSender(e *queueEntry) string {
	switch e.origin.kind {
	case queueByHuman:
		return "the person"
	case "link":
		if e.origin.peer != "" {
			return "a caller on " + printableClaim(e.origin.peer, 40)
		}
		return "a caller on a linked machine"
	case "pane":
		name := ""
		if sn := d.sessionOfWindow(e.origin.window); sn != "" {
			if s := d.manager.GetSession(sn); s != nil {
				if w, ok := findWindowState(s.GetState(), e.origin.window); ok {
					name = printableClaim(windowLabelOf(w), 40)
				}
			}
		}
		if name == "" {
			name = shortWindowID(e.origin.window)
		}
		return "pane " + name
	}
	return "a script"
}
