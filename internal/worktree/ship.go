package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Shipping: what takes an agent's work from its worktree to the base branch
// and to a remote. CommitAll commits the work tree on its own branch,
// MergeBranch merges a branch into the branch the main checkout has checked
// out, and Push sends a branch to a remote.
//
// Every git call here runs with the person's own configuration. Unlike a
// checkpoint, no identity is set and no signing flag is passed: the commit is
// the person's, made the way their git makes one, hooks included, and its
// message is what the caller gave with nothing added. Nothing is ever forced:
// a merge that conflicts is aborted and a push that is not a fast-forward is
// refused by git and reported.

// ErrNothingToCommit reports a work tree with no change to commit.
var ErrNothingToCommit = errors.New("nothing to commit")

// MergeMode is how MergeBranch merges.
type MergeMode string

// The merge modes.
const (
	// MergeDefault fast-forwards when it can and makes a merge commit when
	// it cannot, as git merge does.
	MergeDefault MergeMode = ""
	// MergeSquash makes one commit on the target with the branch's change.
	MergeSquash MergeMode = "squash"
	// MergeFFOnly only fast-forwards, and refuses a branch that diverged.
	MergeFFOnly MergeMode = "ff-only"
)

// ConflictError is a merge that stopped on conflicts and was aborted. The
// main checkout is as it was before.
type ConflictError struct {
	// Files are the paths that conflicted, relative to the repository root.
	Files []string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("the merge conflicts in %d %s: %s", len(e.Files), plural(len(e.Files), "file", "files"), strings.Join(e.Files, ", "))
}

// DirtyError is a main checkout with tracked changes, which a merge refuses.
type DirtyError struct {
	// Files are the changed paths git status reports, at most a few.
	Files []string
	// Count is how many there are.
	Count int
}

func (e *DirtyError) Error() string {
	return fmt.Sprintf("the main checkout has %d uncommitted %s", e.Count, plural(e.Count, "change", "changes"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// CommitAll stages every change in the work tree at dir, untracked files
// included and ignored ones not, and commits it with message. It returns the
// new commit. When the commit fails, a hook refused it or signing failed, the
// index is put back as it was, so the attempt leaves nothing staged that was
// not staged before.
func CommitAll(ctx context.Context, dir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("the commit message is empty")
	}
	n, err := ChangesCtx(ctx, dir)
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", ErrNothingToCommit
	}
	// The index as it is now, so a failed commit can put it back. write-tree
	// refuses an index with conflicts, which a commit would refuse as well.
	saved, err := runCtx(ctx, dir, nil, "write-tree")
	if err != nil {
		return "", err
	}
	if _, err := runCtx(ctx, dir, nil, "add", "-A"); err != nil {
		_, _ = runCtx(context.WithoutCancel(ctx), dir, nil, "read-tree", strings.TrimSpace(saved))
		return "", err
	}
	if _, err := runCtx(ctx, dir, nil, "commit", "-q", "-m", message); err != nil {
		_, _ = runCtx(context.WithoutCancel(ctx), dir, nil, "read-tree", strings.TrimSpace(saved))
		return "", err
	}
	return HeadCommitCtx(ctx, dir)
}

// MergeResult is what MergeBranch did.
type MergeResult struct {
	// Into is the branch merged into, Branch the one merged.
	Into   string `json:"into"`
	Branch string `json:"branch"`
	// Before and After are the target's commit before and after.
	Before string `json:"before"`
	After  string `json:"after"`
	// Mode is the mode used: merge, squash or ff-only. FastForward is set
	// when the target only moved forward.
	Mode        string `json:"mode"`
	FastForward bool   `json:"fast_forward"`
	// UpToDate is set when the target already held every commit of the
	// branch, and nothing changed.
	UpToDate bool `json:"up_to_date"`
	// Commits is how many commits of the branch the target did not have.
	Commits int `json:"commits"`
}

// dirtyShown bounds the paths a DirtyError carries.
const dirtyShown = 5

// MergeBranch merges branch into into, in the main checkout at repoRoot,
// which must have into checked out and no tracked change. message is the
// message of the commit a merge or a squash makes; empty takes git's own.
//
// A merge that stops on conflicts is aborted with git reset --merge, which
// puts the checkout and the index back as they were, and returns a
// ConflictError naming the files.
func MergeBranch(ctx context.Context, repoRoot, branch, into string, mode MergeMode, message string) (MergeResult, error) {
	res := MergeResult{Into: into, Branch: branch, Mode: string(mode)}
	if res.Mode == "" {
		res.Mode = "merge"
	}
	if branch == "" || into == "" || strings.HasPrefix(branch, "-") || strings.HasPrefix(into, "-") {
		return res, fmt.Errorf("merge of %q into %q: not a branch", branch, into)
	}
	if branch == into {
		return res, fmt.Errorf("%s cannot be merged into itself", branch)
	}
	head, err := CurrentBranch(repoRoot)
	if err != nil {
		return res, err
	}
	if head != into {
		on := head
		if on == "" {
			on = "a detached HEAD"
		}
		return res, fmt.Errorf("the main checkout %s is on %s, not %s", repoRoot, on, into)
	}
	if err := checkClean(ctx, repoRoot); err != nil {
		return res, err
	}
	before, err := HeadCommitCtx(ctx, repoRoot)
	if err != nil {
		return res, err
	}
	res.Before, res.After = before, before
	tip, err := runCtx(ctx, repoRoot, nil, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return res, err
	}
	tip = strings.TrimSpace(tip)
	count, err := runCtx(ctx, repoRoot, nil, "rev-list", "--count", before+".."+tip)
	if err != nil {
		return res, err
	}
	_, _ = fmt.Sscan(strings.TrimSpace(count), &res.Commits)
	if res.Commits == 0 {
		res.UpToDate = true
		return res, nil
	}
	_, ancestorErr := runCtx(ctx, repoRoot, nil, "merge-base", "--is-ancestor", before, tip)
	canFF := ancestorErr == nil

	args := []string{"merge", "--no-edit"}
	switch mode {
	case MergeSquash:
		args = []string{"merge", "--squash"}
	case MergeFFOnly:
		if !canFF {
			return res, fmt.Errorf("%s has commits %s does not, so it cannot be fast-forwarded", into, branch)
		}
		args = append(args, "--ff-only")
	default:
		if message != "" && !canFF {
			args = append(args, "-m", message)
		}
	}
	args = append(args, "refs/heads/"+branch)
	if _, err := runCtx(ctx, repoRoot, nil, args...); err != nil {
		return res, abortMerge(ctx, repoRoot, before, err)
	}
	if mode == MergeSquash {
		commit := []string{"commit", "-q"}
		if message != "" {
			commit = append(commit, "-m", message)
		} else {
			// git merge --squash leaves its summary in SQUASH_MSG, which
			// commit uses as the message when none is given.
			commit = append(commit, "--no-edit")
		}
		if _, err := runCtx(ctx, repoRoot, nil, commit...); err != nil {
			return res, abortMerge(ctx, repoRoot, before, err)
		}
	}
	after, err := HeadCommitCtx(ctx, repoRoot)
	if err != nil {
		return res, err
	}
	res.After = after
	res.FastForward = after == tip && canFF && mode != MergeSquash
	return res, nil
}

// checkClean refuses a checkout with a tracked change or a merge in
// progress. Untracked files do not count: git merge itself refuses one the
// merge would write over, before it changes anything.
func checkClean(ctx context.Context, dir string) error {
	out, err := runCtx(ctx, dir, nil, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	var files []string
	count := 0
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 4 {
			continue
		}
		count++
		if len(files) < dirtyShown {
			files = append(files, line[3:])
		}
	}
	if count > 0 {
		return &DirtyError{Files: files, Count: count}
	}
	gitdir, err := runCtx(ctx, dir, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(gitdir), name)); err == nil {
			return fmt.Errorf("the main checkout %s has a merge, rebase or cherry-pick in progress", dir)
		}
	}
	return nil
}

// abortMerge undoes a merge that failed part of the way and returns the error
// to report: a ConflictError when files conflicted, else cause. The checkout
// is put back to before with git reset --merge, which keeps nothing of the
// merge and leaves alone what the merge did not touch.
func abortMerge(ctx context.Context, dir, before string, cause error) error {
	bg := context.WithoutCancel(ctx)
	out, _ := runCtx(bg, dir, nil, "diff", "--name-only", "--diff-filter=U")
	var files []string
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	if _, err := runCtx(bg, dir, nil, "reset", "-q", "--merge", before); err != nil {
		return fmt.Errorf("%w, and undoing it failed: %v", cause, err)
	}
	if len(files) > 0 {
		slices.Sort(files)
		return &ConflictError{Files: files}
	}
	return cause
}

// PushTarget is the remote a branch is pushed to and the branch's name there.
type PushTarget struct {
	Remote string `json:"remote"`
	// URL is the remote's push URL, as git config holds it.
	URL string `json:"url"`
	// Branch is the local branch, Upstream the remote-tracking branch it
	// follows, empty when it follows none yet.
	Branch   string `json:"branch"`
	Upstream string `json:"upstream,omitempty"`
}

// ErrNoRemote reports a repository with no remote to push to.
var ErrNoRemote = errors.New("the repository has no remote")

// ResolvePush finds where branch is pushed from the work tree at dir: the
// remote the branch follows, else the only remote, else origin. remote, when
// set, names it instead.
func ResolvePush(ctx context.Context, dir, branch, remote string) (PushTarget, error) {
	t := PushTarget{Branch: branch}
	out, err := runCtx(ctx, dir, nil, "remote")
	if err != nil {
		return t, err
	}
	remotes := strings.Fields(out)
	if len(remotes) == 0 {
		return t, ErrNoRemote
	}
	configured, _ := runCtx(ctx, dir, nil, "config", "--get", "branch."+branch+".remote")
	configured = strings.TrimSpace(configured)
	switch {
	case remote != "":
		t.Remote = remote
	case configured != "" && slices.Contains(remotes, configured):
		t.Remote = configured
	case len(remotes) == 1:
		t.Remote = remotes[0]
	case slices.Contains(remotes, "origin"):
		t.Remote = "origin"
	default:
		return t, fmt.Errorf("the repository has %d remotes (%s) and branch %s follows none of them", len(remotes), strings.Join(remotes, ", "), branch)
	}
	if !slices.Contains(remotes, t.Remote) {
		return t, fmt.Errorf("the repository has no remote %q", t.Remote)
	}
	url, err := runCtx(ctx, dir, nil, "remote", "get-url", "--push", t.Remote)
	if err != nil {
		return t, err
	}
	t.URL = strings.TrimSpace(url)
	if up, err := runCtx(ctx, dir, nil, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}"); err == nil {
		t.Upstream = strings.TrimSpace(up)
	}
	return t, nil
}

// Push sends branch to the target's remote under the same name and sets it as
// the branch's upstream. It never forces: a remote branch that moved on is
// refused by git, and the error says so.
func Push(ctx context.Context, dir string, t PushTarget) error {
	if strings.HasPrefix(t.Remote, "-") || strings.HasPrefix(t.Branch, "-") {
		return fmt.Errorf("push of %q to %q: not a branch", t.Branch, t.Remote)
	}
	_, err := runCtx(ctx, dir, nil, "push", "--porcelain", "--set-upstream", t.Remote, "refs/heads/"+t.Branch+":refs/heads/"+t.Branch)
	return err
}

// RemoteBranchCommit is the commit the remote-tracking branch of t holds now,
// as this repository last fetched or pushed it, "" when there is none.
func RemoteBranchCommit(ctx context.Context, dir string, t PushTarget) string {
	out, err := runCtx(ctx, dir, nil, "rev-parse", "--verify", "-q", "refs/remotes/"+t.Remote+"/"+t.Branch+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// BranchCommitCtx is the commit branch points at in the repository at dir.
func BranchCommitCtx(ctx context.Context, dir, branch string) (string, error) {
	out, err := runCtx(ctx, dir, nil, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Subjects lists the subjects of the commits in base..tip, newest first, at
// most limit.
func Subjects(ctx context.Context, dir, base, tip string, limit int) ([]string, error) {
	rng := tip
	if base != "" {
		rng = base + ".." + tip
	}
	out, err := runCtx(ctx, dir, nil, "log", "--format=%s", fmt.Sprintf("--max-count=%d", limit), rng)
	if err != nil {
		return nil, err
	}
	var subjects []string
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if line != "" {
			subjects = append(subjects, line)
		}
	}
	return subjects, nil
}
