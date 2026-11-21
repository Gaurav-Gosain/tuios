package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

const jjDir = ".jj/"

func jjRun(ctx context.Context, root string, args ...string) (string, error) {
	out, err := gitstate.RunJJ(ctx, root, args...)
	if err != nil {
		return "", errfmt.Errorf("jj %s: %w", args[0], err)
	}
	return out, nil
}

func jjBookmark(name string) string {
	return "bookmarks(exact:" + strconv.Quote(name) + ")"
}

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
	if at == "" && atEmpty {
		return parent, nil
	}
	return at, nil
}

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

func jjDetect(root, mainRoot string) (Info, bool) {
	branch := filepath.Base(root)
	if st, ok := gitstate.Read(root); ok && st.Branch != "" {
		branch = st.Branch
	}
	return Info{Repo: filepath.Base(mainRoot), RepoRoot: mainRoot, Branch: branch, Path: root}, true
}

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

func jjWorkHead(ctx context.Context, root string) (string, error) {
	return gitstate.JJCommit(ctx, root, "coalesce(@ ~ empty(), @-)", false)
}

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

func jjStatusEnv(ctx context.Context, path string) (env []string, done func(), err error) {
	tree, _, err := SnapshotTree(ctx, path, 0)
	if err != nil {
		return nil, nil, err
	}
	tmp, ctx, err := newTemp(ctx, "index")
	if err != nil {
		return nil, nil, err
	}
	done = tmp.remove
	env = []string{"GIT_INDEX_FILE=" + filepath.Join(tmp.path, "index")}
	if _, err := runCtx(ctx, path, env, "read-tree", tree); err != nil {
		done()
		return nil, nil, err
	}
	return env, done, nil
}

// JJLeftOut lists the files in the jj workspace at path that its
// working-copy commit does not hold after a snapshot: a new file over
// snapshot.max-new-file-size, or one snapshot.auto-track leaves out. Ignored
// files are not listed. Forgetting the workspace keeps the commit and loses
// these.
func JJLeftOut(path string) ([]string, error) {
	ctx := context.Background()
	env, done, err := jjStatusEnv(ctx, path)
	if err != nil {
		return nil, err
	}
	defer done()
	out, err := runCtx(ctx, path, env, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var left []string
	for record := range strings.SplitSeq(out, "\x00") {
		if len(record) > 3 {
			left = append(left, record[3:])
		}
	}
	return left, nil
}

func jjRemove(path string, force bool) error {
	if !force {
		left, err := JJLeftOut(path)
		if err != nil {
			return err
		}
		if len(left) > 0 {
			return errfmt.Errorf("%s holds files jj did not record: %s; use force to delete them", path, strings.Join(left, ", "))
		}
	}
	if _, err := jjRun(context.Background(), path, "workspace", "forget"); err != nil {
		return err
	}
	return os.RemoveAll(path)
}
