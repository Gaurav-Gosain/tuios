package worktree

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Whether a worktree's branch has landed in its base.
//
// A branch is merged when it has changes of its own and the base already holds
// them. Two shapes of history answer that differently.
//
// When the branch tip is not in the base, the branch has commits the base does
// not. It is merged when it changes something against its merge base with the
// base, and merging it into the base would change nothing. git merge-tree
// --write-tree computes that merge without touching a checkout or a ref, and a
// merge whose tree is the base's own tree adds nothing. That covers a squash
// and a rebase. The first half keeps a commit and its revert, a branch whose
// net change is empty, from reading as merged.
//
// When the branch tip is in the base, a merge commit or a fast-forward landed
// it, or the branch has nothing of its own and only fast-forwarded to commits
// the base made. The graph cannot tell those apart, so the branch's reflog
// does: a branch has commits of its own when one of its entries made a commit
// (commit, amend, revert, cherry-pick, or a rebase that left work past its
// onto commit) and that commit is in the branch past the commit it started
// at. With no reflog, the branch reads as not merged. So the check can miss a
// merged branch, never claim one that is not.

// mergedCacheCap bounds the cache. The listing is polled, and a key that no
// longer matches any tip is dead weight; clearing the whole map when it fills
// is cheaper than tracking which keys are live.
const mergedCacheCap = 512

var (
	mergedMu    sync.Mutex
	mergedCache = map[string]string{}
)

// Merged reports the ref the worktree's branch is merged into, or "" when it
// is not merged into any.
//
// base is the ref the branch was made from. Empty means the branch the main
// checkout at repoRoot has checked out. start is the commit the branch began
// at, empty when it was not recorded. The base is checked, then its upstream
// when it has one, as git last fetched it: Merged never fetches.
//
// The answer is cached per branch tip, base tips and start, so a listing
// polled every few seconds runs only rev-parse (two calls when the base has
// an upstream) per worktree until something moves.
func Merged(path, repoRoot, start, base string) (string, error) {
	if base == "" {
		out, err := run(repoRoot, "symbolic-ref", "--quiet", "--short", "HEAD")
		if err != nil {
			return "", err
		}
		base = strings.TrimSpace(out)
	}
	if base == "" || strings.HasPrefix(base, "-") {
		return "", nil
	}
	refs := []string{base}
	tips, err := revParse(path, "HEAD", base+"^{commit}", base+"@{upstream}^{commit}")
	if err == nil {
		up, uerr := run(path, "rev-parse", "--abbrev-ref", base+"@{upstream}")
		if uerr == nil && strings.TrimSpace(up) != "" {
			refs = append(refs, strings.TrimSpace(up))
		} else {
			tips = tips[:2]
		}
	} else {
		// No upstream, or base is not a branch. The plain pair is enough.
		if tips, err = revParse(path, "HEAD", base+"^{commit}"); err != nil {
			return "", err
		}
	}
	key := path + "\x00" + start + "\x00" + base + "\x00" + strings.Join(tips, "\x00")
	mergedMu.Lock()
	into, hit := mergedCache[key]
	mergedMu.Unlock()
	if hit {
		return into, nil
	}

	into = ""
	head := tips[0]
	for i, ref := range refs {
		if mergedInto(path, start, head, tips[i+1]) {
			into = ref
			break
		}
	}
	mergedMu.Lock()
	if len(mergedCache) >= mergedCacheCap {
		clear(mergedCache)
	}
	mergedCache[key] = into
	mergedMu.Unlock()
	return into, nil
}

// mergedInto reports whether head has changes of its own and target already
// holds them. Any git failure, a conflict among them, reads as not merged.
func mergedInto(path, start, head, target string) bool {
	mb, err := MergeBase(path, head, target)
	if err != nil {
		return false
	}
	if mb == head {
		// The base holds the tip. Merged only when the branch made commits.
		return madeCommits(path, start, head)
	}
	// The branch has commits the base does not. Its net change against the
	// merge base must not be empty, so a commit and its revert is not merged.
	trees, err := revParse(path, head+"^{tree}", mb+"^{tree}", target+"^{tree}")
	if err != nil || trees[0] == trees[1] {
		return false
	}
	merged, err := mergeTree(path, target, head)
	return err == nil && merged == trees[2]
}

// mergeTree is the tree git merge-tree --write-tree gives for merging head
// into target. merge-tree writes the merged tree's objects, so it runs with a
// temporary object directory that borrows the repository's objects as an
// alternate. The repository gains nothing, and the directory is removed.
func mergeTree(path, target, head string) (string, error) {
	objects, err := run(path, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp("", "tuios-merge-tree-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	alternates := strings.TrimSpace(objects)
	if prev := os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES"); prev != "" {
		alternates += string(os.PathListSeparator) + prev
	}
	env := []string{"GIT_OBJECT_DIRECTORY=" + tmp, "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + alternates}
	// merge-tree prints the merged tree on its first line, and exits non-zero
	// on a conflict, which runEnv reports as an error.
	out, err := runEnv(path, env, "merge-tree", "--write-tree", target, head)
	if err != nil {
		return "", err
	}
	tree, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(tree), nil
}

// reflogScan bounds how many reflog entries madeCommits reads.
const reflogScan = 64

// madeCommits reports whether the branch checked out at path made a commit
// that head holds and start does not. It reads the branch's reflog, newest
// first. A detached HEAD, or a branch with no reflog, made none.
func madeCommits(path, start, head string) bool {
	branch, err := CurrentBranch(path)
	if err != nil || branch == "" || strings.HasPrefix(branch, "-") {
		return false
	}
	out, err := run(path, "log", "-g", fmt.Sprintf("-n%d", reflogScan), "--format=%H %gs", "refs/heads/"+branch, "--")
	if err != nil {
		return false
	}
	for line := range strings.Lines(out) {
		sha, subject, _ := strings.Cut(strings.TrimSpace(line), " ")
		if !ownEntry(subject, sha) {
			continue
		}
		if !isAncestor(path, sha, head) {
			continue
		}
		if start != "" && isAncestor(path, sha, start) {
			continue
		}
		return true
	}
	return false
}

// ownEntry reports whether a reflog entry with this subject, which set the
// branch to sha, made a commit of the branch's own. A merge, a fast-forward,
// a reset and the branch's creation make none. A rebase made some when it
// left the branch past the commit it rebased onto.
func ownEntry(subject, sha string) bool {
	for _, p := range []string{"commit:", "commit (amend):", "commit (initial):", "revert:", "cherry-pick:"} {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	// "rebase (finish): refs/heads/b onto <sha>", also after "pull --rebase".
	if _, onto, ok := strings.Cut(subject, "(finish): "); ok {
		if _, onto, ok = strings.Cut(onto, " onto "); ok {
			return strings.TrimSpace(onto) != sha
		}
	}
	return false
}

// isAncestor reports whether commit a is b or an ancestor of b.
func isAncestor(dir, a, b string) bool {
	if strings.HasPrefix(a, "-") || strings.HasPrefix(b, "-") {
		return false
	}
	_, err := run(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

// revParse resolves each rev to a full hash, in order.
func revParse(dir string, revs ...string) ([]string, error) {
	for _, r := range revs {
		if strings.HasPrefix(r, "-") {
			return nil, fmt.Errorf("%q is not a revision", r)
		}
	}
	out, err := run(dir, append([]string{"rev-parse"}, revs...)...)
	if err != nil {
		return nil, err
	}
	hashes := strings.Fields(out)
	if len(hashes) != len(revs) {
		return nil, fmt.Errorf("git rev-parse: %d hashes for %d revisions", len(hashes), len(revs))
	}
	return hashes, nil
}
