package worktree

import (
	"fmt"
	"strings"
	"sync"
)

// Whether a worktree's branch has landed in its base.
//
// A branch is merged when it has commits of its own and merging it into the
// base would change nothing. The second half is asked of git directly: git
// merge-tree --write-tree computes the merge without touching any checkout or
// ref, and a merge whose tree is the base's own tree adds nothing. That one
// question covers every way a branch lands: a merge commit, a fast-forward, a
// rebase and a squash all leave the base holding the branch's changes.
//
// The first half keeps a fresh branch from reading as merged: a branch with no
// commits merges into its base as nothing, which is the same answer a landed
// branch gives. Commits of its own are counted from the commit the branch
// started at, which tuios records when it makes the worktree. A worktree made
// before that record falls back to the merge base, which is the start point
// until the branch lands and the branch tip after a fast-forward. So an old
// worktree can read as not merged when it is, never the other way round.

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

// mergedInto reports whether head, a commit with commits of its own past
// start, merges into target as nothing. Any git failure, a conflict among
// them, reads as not merged.
func mergedInto(path, start, head, target string) bool {
	if start == "" || !HasCommit(path, start) {
		mb, err := MergeBase(path, head, target)
		if err != nil {
			return false
		}
		start = mb
	}
	out, err := run(path, "rev-list", "--count", start+".."+head)
	if err != nil || strings.TrimSpace(out) == "0" {
		return false
	}
	// merge-tree prints the merged tree on its first line, and exits non-zero
	// on a conflict, which run reports as an error.
	merged, err := run(path, "merge-tree", "--write-tree", target, head)
	if err != nil {
		return false
	}
	tree, _, _ := strings.Cut(merged, "\n")
	baseTree, err := revParse(path, target+"^{tree}")
	if err != nil {
		return false
	}
	return strings.TrimSpace(tree) == baseTree[0]
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
