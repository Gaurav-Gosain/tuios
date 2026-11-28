package worktree

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

// Comparing the attempts of a fan: how much each one changed against its
// base, and what two of them did differently.
//
// Both read a worktree's working state, committed or not, untracked files
// included and ignored files left out, as one git tree. The tree is written
// through a temporary index, so the worktree's own index and files are as they
// were: what the agent staged stays staged and nothing is added. The objects
// the tree needs are written to the repository's object store as loose
// objects, which git gc collects like any other unreferenced object.
//
// An untracked file larger than the caller's limit is left out and never
// read. Staging a file copies it into the object store, and a clean filter
// such as Git LFS's copies it again into its own store, so a multi-gigabyte
// data file the repository does not track would otherwise be copied on every
// read, and a read cut off by its timeout would leave a partial copy behind.

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
		// jj writes a conflicted commit's tree with a .jjconflict-* folder for
		// each side, while the files on disk hold the conflict markers. The
		// files are the working state, and jj reads markers written back as
		// the same conflict.
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
	tree, late, err := snapshotTreeExcluding(ctx, path, skipped, maxUntracked)
	if err != nil {
		return "", nil, err
	}
	return tree, mergeSkipped(skipped, late), nil
}

// jjMaterialize returns tree, jj's tree of the working-copy commit in the
// workspace at root, with each conflicted file as it is on disk. Other paths
// keep jj's entries, so a path a sparse workspace leaves off disk stays.
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
			// A side that deletes the file leaves nothing on disk to write.
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

// mergeSkipped is the large files a caller listed first and the ones stageAll
// found after it, sorted.
func mergeSkipped(skipped, late []string) []string {
	if len(late) == 0 {
		return skipped
	}
	all := append(slices.Clone(skipped), late...)
	slices.Sort(all)
	return slices.Compact(all)
}

// snapshotTreeExcluding is SnapshotTree with the paths in exclude, relative
// to path, left out. They are never read, so a large file costs nothing. An
// untracked file larger than limit bytes that was not in exclude, because it
// was made or grew after the caller listed them, is left out too and
// returned.
func snapshotTreeExcluding(ctx context.Context, path string, exclude []string, limit int64) (string, []string, error) {
	tmp, ctx, err := newTemp(ctx, "index")
	if err != nil {
		return "", nil, err
	}
	defer tmp.remove()
	return snapshotInto(ctx, path, exclude, limit, filepath.Join(tmp.path, "index"))
}

// snapshotInto is snapshotTreeExcluding with the index it writes at index, a
// file in a folder the caller owns.
func snapshotInto(ctx context.Context, path string, exclude []string, limit int64, index string) (string, []string, error) {
	env := []string{"GIT_INDEX_FILE=" + index}
	if copyIndex(ctx, path, index) {
		if tree, late, err := addAndWriteTree(ctx, path, env, exclude, limit); err == nil {
			return tree, late, nil
		} else if ctx.Err() != nil {
			return "", nil, err
		}
	}
	// git refuses an empty file as an index, so it starts from nothing.
	_ = os.Remove(index)
	// A repository with no commit yet has no HEAD to start from, and every
	// file is new, so the index starts empty.
	if _, err := runCtx(ctx, path, env, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		if _, err := runCtx(ctx, path, env, "read-tree", "HEAD"); err != nil {
			return "", nil, err
		}
	}
	return addAndWriteTree(ctx, path, env, exclude, limit)
}

// copyIndex copies the index of the worktree at path over dest, and reports
// whether it did. The worktree's index is only read.
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
	// git trusts an entry's stat data only when the file is older than the
	// index. The copy keeps the original's time, so a file changed just after
	// the agent's last git call is still hashed again.
	_ = os.Chtimes(dest, info.ModTime(), info.ModTime())
	return true
}

// addAndWriteTree stages every file of the worktree at path into the index env
// names and writes it as a tree.
func addAndWriteTree(ctx context.Context, path string, env []string, exclude []string, limit int64) (string, []string, error) {
	late, err := stageAll(ctx, path, env, nil, exclude, limit)
	if err != nil {
		return "", nil, err
	}
	out, err := runCtx(ctx, path, env, "write-tree")
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(out), late, nil
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
	_, err := stageAll(ctx, dir, env, specs, exclude, 0)
	return err
}

// stageAll is StageAll that also leaves out each untracked file larger than
// limit bytes (none when limit is zero or less), and returns those paths.
//
// The size is read from the list stageAll stages, just before it stages it.
// A caller's own list of large files is older: an agent that writes while a
// snapshot runs can make a file after it, which git would then read whole.
// A file can still grow in the moment between the size check and git's read
// of it. git then stores it whole, so the staged sizes are read back and an
// entry past the limit is taken out of the index before any tree is written
// (dropLargeStaged).
func stageAll(ctx context.Context, dir string, env, specs, exclude []string, limit int64) ([]string, error) {
	if len(specs) == 0 {
		specs = []string{"."}
	}
	args := append([]string{"ls-files", "-z", "--modified", "--deleted", "--others", "--exclude-standard", "--"}, specs...)
	out, err := runCtx(ctx, dir, env, args...)
	if err != nil {
		return nil, err
	}
	var untracked map[string]bool
	if limit > 0 {
		others, err := runCtx(ctx, dir, env, append([]string{"ls-files", "-z", "--others", "--exclude-standard", "--"}, specs...)...)
		if err != nil {
			return nil, err
		}
		untracked = map[string]bool{}
		for p := range strings.SplitSeq(others, "\x00") {
			untracked[p] = p != ""
		}
	}
	skip := make(map[string]bool, len(exclude))
	for _, p := range exclude {
		skip[filepath.ToSlash(p)] = true
	}
	var in strings.Builder
	var late, added []string
	seen := map[string]bool{}
	for p := range strings.SplitSeq(out, "\x00") {
		// A path that ends in a slash is a repository inside the work tree
		// that is not a submodule; git add records only a warning for it.
		if p == "" || skip[p] || seen[p] || strings.HasSuffix(p, "/") {
			continue
		}
		seen[p] = true
		if untracked[p] {
			info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
			if err == nil && info.Mode().IsRegular() && info.Size() > limit {
				late = append(late, p)
				continue
			}
			added = append(added, p)
		}
		in.WriteString(p)
		in.WriteByte(0)
	}
	if in.Len() == 0 {
		return late, nil
	}
	if stageAllBeforeAdd != nil {
		stageAllBeforeAdd()
	}
	if _, err := runStdin(ctx, dir, env, in.String(), "update-index", "--add", "--remove", "-z", "--stdin"); err != nil {
		return late, err
	}
	grew, err := dropLargeStaged(ctx, dir, env, specs, added, limit)
	return append(late, grew...), err
}

// stageAllBeforeAdd, when set, runs between stageAll's size check and its
// update-index. Tests grow a file there.
var stageAllBeforeAdd func()

// dropLargeStaged takes each of paths, untracked files stageAll staged into
// the index env names, out of that index when its staged blob is larger than
// limit bytes, and returns those paths. Such a file grew after the size
// check. No tree holds it, so its blob is unreachable and git gc prunes it.
// It reads the index once (ls-files) and the sizes in one cat-file.
func dropLargeStaged(ctx context.Context, dir string, env, specs, paths []string, limit int64) ([]string, error) {
	if len(paths) == 0 || limit <= 0 {
		return nil, nil
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	out, err := runCtx(ctx, dir, env, append([]string{"ls-files", "-z", "--stage", "--"}, specs...)...)
	if err != nil {
		return nil, err
	}
	var oids, names []string
	for e := range strings.SplitSeq(out, "\x00") {
		// MODE OID STAGE, a tab, then the path.
		meta, p, ok := strings.Cut(e, "\t")
		if !ok || !want[p] {
			continue
		}
		if f := strings.Fields(meta); len(f) == 3 && f[0] != "160000" {
			oids = append(oids, f[1])
			names = append(names, p)
		}
	}
	if len(oids) == 0 {
		return nil, nil
	}
	sizes, err := runStdin(ctx, dir, env, strings.Join(oids, "\n")+"\n", "cat-file", "--batch-check=%(objectsize)")
	if err != nil {
		return nil, err
	}
	var drop []string
	for i, line := range strings.Split(strings.TrimSuffix(sizes, "\n"), "\n") {
		if n, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64); err == nil && i < len(names) && n > limit {
			drop = append(drop, names[i])
		}
	}
	if len(drop) == 0 {
		return nil, nil
	}
	if _, err := runStdin(ctx, dir, env, strings.Join(drop, "\x00")+"\x00", "update-index", "--force-remove", "-z", "--stdin"); err != nil {
		return nil, err
	}
	return drop, nil
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
	// A jj workspace's HEAD is @-; without a ".git" git has none for it.
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

// ChangedPaths are the paths that differ between the trees, or commits, from
// and to, relative to root.
func ChangedPaths(ctx context.Context, root, from, to string) ([]string, error) {
	out, err := runCtx(ctx, root, nil, "diff-tree", "-r", "-z", "--name-only", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	var paths []string
	for p := range strings.SplitSeq(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// runCtx is runEnv bounded by ctx: a git that takes longer than the caller
// can wait is killed.
//
//go:noinline
func runCtx(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	return runGit(ctx, dir, env, nil, args...)
}
