package worktree

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// Numstat is how much a worktree changed against a base: the files that
// differ and the lines added and removed in them. A binary file counts as a
// file with no lines.
type Numstat struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// SnapshotTree writes the working state of the worktree at path as a git tree
// and returns its hash, with the untracked files larger than maxUntracked
// bytes left out and never read. It returns their paths, relative to path and
// sorted. Zero or less is no limit.
//
// The temporary index starts as a copy of the worktree's own, so git's stat
// cache holds and only the files that changed since the agent last touched
// the index are hashed again. A worktree with no index, or one git cannot use
// from a copy (a split index keeps part of itself beside the original), starts
// from HEAD instead, which hashes every tracked file.
//
// In a jj workspace the working state is jj's working-copy commit, recorded
// the way jj records it: git's index there is jj's export, and a sparse
// workspace leaves every path outside its patterns in it as if deleted. jj
// applies its own limit to new files, so nothing is reported skipped. A
// conflicted file is read from disk, with its markers.
func SnapshotTree(ctx context.Context, path string, maxUntracked int64) (string, []string, error) {
	if r := gitstate.Find(path); r.JJ {
		id, err := gitstate.JJCommit(ctx, r.Root, "@", true)
		if err != nil {
			return "", nil, err
		}
		tree, err := runCtx(ctx, path, nil, "rev-parse", "--verify", id+"^{tree}")
		if err != nil {
			return "", nil, err
		}
		tree, err = jjMaterialize(ctx, r.Root, strings.TrimSpace(tree))
		return tree, nil, err
	}
	skipped, err := largeUntracked(ctx, path, maxUntracked)
	if err != nil {
		return "", nil, err
	}
	tree, err := snapshotTreeExcluding(ctx, path, skipped)
	if err != nil {
		return "", nil, err
	}
	return tree, skipped, nil
}

func jjMaterialize(ctx context.Context, root, tree string) (string, error) {
	out, err := runCtx(ctx, root, nil, "ls-tree", "-r", "-z", "--name-only", tree)
	if err != nil {
		return "", err
	}
	var drop, write []string
	seen := map[string]bool{}
	for p := range strings.SplitSeq(out, "\x00") {
		dir, rest, _ := strings.Cut(p, "/")
		switch {
		case p == "JJ-CONFLICT-README":
			drop = append(drop, p)
		case strings.HasPrefix(dir, ".jjconflict-") && rest != "":
			drop = append(drop, p)
			if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rest))); err == nil && !seen[rest] {
				seen[rest] = true
				write = append(write, rest)
			}
		}
	}
	if len(drop) == 0 {
		return tree, nil
	}
	tmp, ctx, err := newTemp(ctx, "index")
	if err != nil {
		return "", err
	}
	defer tmp.remove()
	index := filepath.Join(tmp.path, "index")
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := runCtx(ctx, root, env, "read-tree", tree); err != nil {
		return "", err
	}
	if _, err := runStdin(ctx, root, env, strings.Join(drop, "\x00")+"\x00", "update-index", "--force-remove", "-z", "--stdin"); err != nil {
		return "", err
	}
	if len(write) > 0 {
		if _, err := runStdin(ctx, root, env, strings.Join(write, "\x00")+"\x00", "update-index", "--add", "-z", "--stdin"); err != nil {
			return "", err
		}
	}
	out, err = runCtx(ctx, root, env, "write-tree")
	return strings.TrimSpace(out), err
}

func snapshotTreeExcluding(ctx context.Context, path string, exclude []string) (string, error) {
	tmp, ctx, err := newTemp(ctx, "index")
	if err != nil {
		return "", err
	}
	defer tmp.remove()
	index := filepath.Join(tmp.path, "index")
	env := []string{"GIT_INDEX_FILE=" + index}
	if copyIndex(ctx, path, index) {
		if tree, err := addAndWriteTree(ctx, path, env, exclude); err == nil {
			return tree, nil
		} else if ctx.Err() != nil {
			return "", err
		}
	}
	_ = os.Remove(index)
	if _, err := runCtx(ctx, path, env, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		if _, err := runCtx(ctx, path, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	return addAndWriteTree(ctx, path, env, exclude)
}

func copyIndex(ctx context.Context, path, dest string) bool {
	out, err := runCtx(ctx, path, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return false
	}
	src := strings.TrimSpace(out)
	if src == "" {
		return false
	}
	if !filepath.IsAbs(src) {
		src = filepath.Join(path, src)
	}
	info, err := os.Stat(src)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 {
		return false
	}
	if os.WriteFile(dest, data, 0o600) != nil {
		return false
	}
	_ = os.Chtimes(dest, info.ModTime(), info.ModTime())
	return true
}

func addAndWriteTree(ctx context.Context, path string, env []string, exclude []string) (string, error) {
	if err := StageAll(ctx, path, env, nil, exclude); err != nil {
		return "", err
	}
	out, err := runCtx(ctx, path, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// StageAll stages in the worktree at dir what git add --all would: each
// changed, removed and untracked file that is not ignored, under the
// pathspecs in specs ("." when there are none). The paths in exclude,
// relative to dir, are left out and never read. env is added to git's
// environment, so GIT_INDEX_FILE there names the index it stages into.
//
// It lists the paths with ls-files and stages them with update-index. git add
// looks inside each submodule's work tree with a git of its own, which reads
// that repository's own config, so its filters would run outside the settings
// childproc.Git gives. ls-files and update-index read only a submodule's
// commit.
func StageAll(ctx context.Context, dir string, env, specs, exclude []string) error {
	if len(specs) == 0 {
		specs = []string{"."}
	}
	args := append([]string{"ls-files", "-z", "--modified", "--deleted", "--others", "--exclude-standard", "--"}, specs...)
	out, err := runCtx(ctx, dir, env, args...)
	if err != nil {
		return err
	}
	skip := make(map[string]bool, len(exclude))
	for _, p := range exclude {
		skip[filepath.ToSlash(p)] = true
	}
	var in strings.Builder
	seen := map[string]bool{}
	for p := range strings.SplitSeq(out, "\x00") {
		if p == "" || skip[p] || seen[p] || strings.HasSuffix(p, "/") {
			continue
		}
		seen[p] = true
		in.WriteString(p)
		in.WriteByte(0)
	}
	if in.Len() == 0 {
		return nil
	}
	_, err = runStdin(ctx, dir, env, in.String(), "update-index", "--add", "--remove", "-z", "--stdin")
	return err
}

// WorkingNumstat counts what the worktree at path changed against base, a
// commit or ref: its commits and its uncommitted work together. Untracked
// files larger than maxUntracked bytes are not counted, as in SnapshotTree.
func WorkingNumstat(ctx context.Context, path, base string, maxUntracked int64) (Numstat, error) {
	if strings.HasPrefix(base, "-") {
		return Numstat{}, errfmt.Errorf("base %q is not a ref", base)
	}
	tree, _, err := SnapshotTree(ctx, path, maxUntracked)
	if err != nil {
		return Numstat{}, err
	}
	out, err := runCtx(ctx, path, nil, "diff", "--numstat", "--no-color", "--no-ext-diff", base, tree, "--")
	if err != nil {
		return Numstat{}, err
	}
	return ParseNumstat(out), nil
}

// ParseNumstat reads the output of git diff --numstat: one line per file,
// added and removed counts first, "-" for both on a binary file.
func ParseNumstat(out string) Numstat {
	var n Numstat
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(fields) < 3 {
			continue
		}
		n.Files++
		if a, err := strconv.Atoi(fields[0]); err == nil {
			n.Added += a
		}
		if r, err := strconv.Atoi(fields[1]); err == nil {
			n.Removed += r
		}
	}
	return n
}

// DiffWorking is what the worktree at b has that the worktree at a does not:
// a unified diff of their working states, from a to b. Both must be worktrees
// of one repository, which share its object store. With stat it is the
// summary form. Untracked files larger than maxUntracked bytes are left out
// on both sides, as in SnapshotTree.
func DiffWorking(ctx context.Context, a, b string, stat bool, maxUntracked int64) (string, error) {
	ta, _, err := SnapshotTree(ctx, a, maxUntracked)
	if err != nil {
		return "", err
	}
	tb, _, err := SnapshotTree(ctx, b, maxUntracked)
	if err != nil {
		return "", err
	}
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, ta, tb, "--")
	return runCtx(ctx, a, nil, args...)
}

// HeadCommitCtx is HeadCommit bounded by ctx.
func HeadCommitCtx(ctx context.Context, path string) (string, error) {
	if r := gitstate.Find(path); r.JJ {
		return gitstate.JJCommit(ctx, r.Root, "@-", false)
	}
	out, err := runCtx(ctx, path, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// WorkRevs names, for git, the work at path and base: HEAD and base as they
// are, or in a jj workspace the commit holding the work and base's commit,
// which jj resolves, since git there may have no refs to.
func WorkRevs(ctx context.Context, path, base string) (head, from string, err error) {
	r := gitstate.Find(path)
	if !r.JJ {
		return "HEAD", base, nil
	}
	if head, err = jjWorkHead(ctx, r.Root); err != nil {
		return "", "", err
	}
	if from, err = gitstate.JJCommit(ctx, r.Root, base, false); err != nil {
		return "", "", err
	}
	return head, from, nil
}

// MergeBaseCtx is MergeBase bounded by ctx.
func MergeBaseCtx(ctx context.Context, path, a, b string) (string, error) {
	if strings.HasPrefix(a, "-") || strings.HasPrefix(b, "-") {
		return "", errfmt.Errorf("merge-base of %q and %q: not a ref", a, b)
	}
	out, err := runCtx(ctx, path, nil, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AheadCtx is Ahead bounded by ctx.
func AheadCtx(ctx context.Context, path, base string) (int, error) {
	head, from, err := WorkRevs(ctx, path, base)
	if err != nil {
		return 0, err
	}
	out, err := runCtx(ctx, path, nil, "rev-list", "--count", from+".."+head)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// ChangesCtx is Changes bounded by ctx.
func ChangesCtx(ctx context.Context, path string) (int, error) {
	if gitstate.Find(path).JJ {
		return jjChanges(ctx, path)
	}
	out, err := runCtx(ctx, path, nil, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return 0, err
	}
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
}

//go:noinline
func runCtx(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	return runGit(ctx, dir, env, nil, args...)
}
