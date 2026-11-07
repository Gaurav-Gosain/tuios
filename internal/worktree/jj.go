package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// Shipping from a jj workspace. jj checks out no branch and has no index, so
// what git does by branch and by staging is done here by bookmark and by the
// working-copy commit:
//
//   - the branch is the bookmark on @, else on @- when @ is empty, which is
//     where "jj commit" leaves the work;
//   - a commit is "jj commit", and the bookmarks on the parent it was made on
//     move to it, as a git branch moves with a commit made on it;
//   - a merge is a fast-forward of the target's bookmark, and only that: a
//     branch the target has moved past is to be rebased first, in jj;
//   - a push is "jj git push" of the bookmark, refused when the bookmark has
//     moved off the commit the person allowed.
//
// The jj commands that change something snapshot the working copy first, as
// every jj command does.

// jjDir is jj's own directory as git status lists it: jj ignores it for git,
// and it is not the work's.
const jjDir = ".jj/"

// jjRun runs jj in the workspace at root and returns its stdout. A failure
// carries jj's own stderr.
func jjRun(ctx context.Context, root string, args ...string) (string, error) {
	cmd := childproc.CommandContext(ctx, "jj", append([]string{"--no-pager", "--color=never"}, args...)...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errfmt.Errorf("jj %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

// jjBookmark is the revset of the local bookmark name.
func jjBookmark(name string) string {
	return "bookmarks(exact:" + strconv.Quote(name) + ")"
}

// jjBranch is the bookmark the work in the workspace at root sits on, or ""
// when there is none.
func jjBranch(ctx context.Context, root string) (string, error) {
	out, err := jjRun(ctx, root, "--ignore-working-copy", "log", "--no-graph", "-r", "@ | @-",
		"-T", `if(self.contained_in("@"), "@", "-") ++ "\t" ++ empty ++ "\t" ++ local_bookmarks.map(|b| b.name()).join(" ") ++ "\n"`)
	if err != nil {
		return "", err
	}
	atEmpty, at, parent := false, "", ""
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) < 3 {
			continue
		}
		name, _, _ := strings.Cut(f[2], " ")
		if f[0] == "@" {
			atEmpty, at = f[1] == "true", name
		} else if parent == "" {
			parent = name
		}
	}
	// A bookmark on @ is the branch even on an empty @: a fresh workspace's.
	if at == "" && atEmpty {
		return parent, nil
	}
	return at, nil
}

// jjCommitAll commits the working-copy commit of the workspace at root.
func jjCommitAll(ctx context.Context, root, message string) (string, error) {
	empty, err := jjRun(ctx, root, "log", "--no-graph", "-r", "@", "-T", "empty")
	if err != nil {
		return "", err
	}
	if empty == "true" {
		return "", ErrNothingToCommit
	}
	if _, err := jjRun(ctx, root, "commit", "-m", message); err != nil {
		return "", err
	}
	// The parent's bookmarks follow the commit, trunk's excepted: committing
	// on a trunk checkout must not move trunk.
	names, err := jjRun(ctx, root, "--ignore-working-copy", "log", "--no-graph", "-r", "@-- ~ ::trunk()",
		"-T", `local_bookmarks.map(|b| b.name()).join("\n") ++ "\n"`)
	if err != nil {
		return "", err
	}
	if fields := strings.Fields(names); len(fields) > 0 {
		if _, err := jjRun(ctx, root, append(append([]string{"--ignore-working-copy", "bookmark", "move"}, fields...), "--to", "@-")...); err != nil {
			return "", err
		}
	}
	return gitstate.JJCommit(ctx, root, "@-", false)
}

// jjMerge fast-forwards the bookmark into to the bookmark branch.
func jjMerge(ctx context.Context, root, branch, into string, mode MergeMode) (MergeResult, error) {
	res := MergeResult{Into: into, Branch: branch, Mode: string(MergeFFOnly)}
	before, err := gitstate.JJCommit(ctx, root, jjBookmark(into), false)
	if err != nil {
		return res, err
	}
	tip, err := gitstate.JJCommit(ctx, root, jjBookmark(branch), false)
	if err != nil {
		return res, err
	}
	res.Before, res.After = before, before
	count, err := runCtx(ctx, root, nil, "rev-list", "--count", before+".."+tip)
	if err != nil {
		return res, err
	}
	_, _ = fmt.Sscan(strings.TrimSpace(count), &res.Commits)
	if res.Commits == 0 {
		res.UpToDate = true
		return res, nil
	}
	if mode == MergeSquash {
		return res, errfmt.Errorf("a squash merge is not done in a jj workspace: squash %s's commits with jj squash, then merge again", branch)
	}
	if _, err := runCtx(ctx, root, nil, "merge-base", "--is-ancestor", before, tip); err != nil {
		return res, errfmt.Errorf("%s has commits %s does not, so it cannot be fast-forwarded: rebase it with jj rebase -b %s -d %s, then merge again", into, branch, branch, into)
	}
	if _, err := jjRun(ctx, root, "--ignore-working-copy", "bookmark", "move", into, "--to", tip); err != nil {
		return res, err
	}
	res.After, res.FastForward = tip, true
	return res, nil
}

// jjPush pushes the bookmark t.Branch to t.Remote, when it still points at
// commit.
func jjPush(ctx context.Context, root string, t PushTarget, commit string) error {
	now, err := gitstate.JJCommit(ctx, root, jjBookmark(t.Branch), false)
	if err != nil {
		return err
	}
	if now != commit {
		return errfmt.Errorf("bookmark %s moved to %s after %s was allowed, and nothing was pushed: ask again", t.Branch, short(now), short(commit))
	}
	_, err = jjRun(ctx, root, "git", "push", "--remote", t.Remote, "-b", t.Branch)
	return err
}

// JJTrunkBookmark is the local bookmark on trunk() in the jj workspace dir is
// in: what a merge goes into when nothing else names a target. A feature
// bookmark fast-forwarded onto trunk shares its commit, so main, master or
// trunk wins, as in jj's own trunk(); any other name only when it is alone.
func JJTrunkBookmark(dir string) (string, bool) {
	r := gitstate.Find(dir)
	if !r.JJ {
		return "", false
	}
	out, err := jjRun(context.Background(), r.Root, "--ignore-working-copy", "log", "--no-graph", "-r", "trunk()",
		"-T", `local_bookmarks.map(|b| b.name()).join(" ")`)
	if err != nil {
		return "", false
	}
	names := strings.Fields(out)
	for _, n := range []string{"main", "master", "trunk"} {
		if slices.Contains(names, n) {
			return n, true
		}
	}
	if len(names) == 1 {
		return names[0], true
	}
	return "", false
}

// Worktree sessions in a jj repository are jj workspaces. A workspace has no
// branch of its own, so the session's branch is a bookmark made on the new
// workspace's working-copy commit, which follows the work as jj rewrites it.
// Forgetting a workspace loses nothing: its commits stay in the repository.

// jjDetect is DetectEither for a jj workspace, the main one included.
func jjDetect(root, mainRoot string) (Info, bool) {
	// The rail's reading, cached on jj's operation log, so a detection that
	// finds nothing new runs no jj.
	branch := filepath.Base(root)
	if st, ok := gitstate.Read(root); ok && st.Branch != "" {
		branch = st.Branch
	}
	return Info{Repo: filepath.Base(mainRoot), RepoRoot: mainRoot, Branch: branch, Path: root}, true
}

// jjAdd is Add for a jj repository: a workspace at path named after branch,
// on the bookmark branch when it exists, else on base (trunk() when empty)
// with the bookmark made on the new workspace's working copy.
func jjAdd(root, path, branch, base string) (bool, error) {
	ctx := context.Background()
	name := Slug(branch)
	rev, created := jjBookmark(branch), false
	if !jjBookmarkExists(ctx, root, branch) {
		rev, created = base, true
		if rev == "" {
			rev = "trunk()"
		}
	}
	if _, err := jjRun(ctx, root, "workspace", "add", "--name", name, "-r", rev, path); err != nil {
		return false, err
	}
	if created {
		if _, err := jjRun(ctx, root, "--ignore-working-copy", "bookmark", "create", branch, "-r", name+"@"); err != nil {
			return true, err
		}
	}
	return created, nil
}

func jjBookmarkExists(ctx context.Context, root, branch string) bool {
	_, err := gitstate.JJCommit(ctx, root, jjBookmark(branch), false)
	return err == nil
}

// jjWorkHead is the commit holding the work in the workspace at root: @ when
// it changes something, else @-, where jj commit leaves the work. git's HEAD
// there is @-, or nothing at all without a ".git".
func jjWorkHead(ctx context.Context, root string) (string, error) {
	return gitstate.JJCommit(ctx, root, "coalesce(@ ~ empty(), @-)", false)
}

// jjMergedTips is what Merged compares in the workspace at root: the base's
// name, and the commit holding the work then the base's commit. jj resolves
// the base, since a store git does not read has no refs for git to. With no
// base it is trunk() itself: the bookmarks on it include any branch already
// fast-forwarded there.
func jjMergedTips(ctx context.Context, root, base string) (refs, tips []string, err error) {
	head, err := jjWorkHead(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	ref, rev := base, jjBookmark(base)
	if base == "" {
		ref, rev = "trunk()", "trunk()"
	}
	tip, err := gitstate.JJCommit(ctx, root, rev, false)
	if err != nil {
		return nil, nil, err
	}
	return []string{ref}, []string{head, tip}, nil
}

// jjMadeCommits reports whether the bookmark branch, the session's own, is on
// a commit that changes something and that start does not hold. jj keeps no
// reflog of a bookmark, so the bookmark tuios made the workspace with is what
// tells the workspace's commits from the ones its base brought.
func jjMadeCommits(ctx context.Context, root, branch, start string) bool {
	if branch == "" {
		return false
	}
	revset := jjBookmark(branch) + " ~ empty()"
	if start != "" {
		revset = "(" + revset + ") ~ ::" + start
	}
	out, err := jjRun(ctx, root, "--ignore-working-copy", "log", "--no-graph", "-r", revset, "-T", `commit_id ++ "\n"`)
	return err == nil && strings.TrimSpace(out) != ""
}

// jjChanges counts the paths the working-copy commit changes.
func jjChanges(ctx context.Context, path string) (int, error) {
	tree, _, err := SnapshotTree(ctx, path, 0)
	if err != nil {
		return 0, err
	}
	parent, err := HeadCommitCtx(ctx, path)
	if err != nil {
		return 0, err
	}
	out, err := runCtx(ctx, path, nil, "diff-tree", "-r", "--name-only", parent, tree)
	if err != nil {
		return 0, err
	}
	return len(strings.Fields(out)), nil
}

// jjStatusEnv is an index of the working-copy commit for git status to read
// the workspace against: git's own index there is jj's export, or there is
// none. done removes it.
func jjStatusEnv(ctx context.Context, path string) (env []string, done func(), err error) {
	tree, _, err := SnapshotTree(ctx, path, 0)
	if err != nil {
		return nil, nil, err
	}
	tmp, err := os.CreateTemp("", "tuios-jj-index-*")
	if err != nil {
		return nil, nil, err
	}
	index := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(index)
	done = func() { _ = os.Remove(index) }
	env = []string{"GIT_INDEX_FILE=" + index}
	if _, err := runCtx(ctx, path, env, "read-tree", tree); err != nil {
		done()
		return nil, nil, err
	}
	return env, done, nil
}

// jjRemove forgets the workspace at path and deletes its directory. The
// forget snapshots the working copy first, so the work is in its commit.
func jjRemove(path string) error {
	if _, err := jjRun(context.Background(), path, "workspace", "forget"); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
