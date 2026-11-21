package worktree

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
)

const mergedCacheCap = 512

var (
	mergedMu    sync.Mutex
	mergedCache = map[string]string{}
)

// Merged reports the ref the worktree's branch is merged into, or "" when it
// is not merged into any.
//
// branch is the worktree's branch as the session records it; only a jj
// workspace reads it. base is the ref the branch was made from. Empty means
// the branch the main checkout at repoRoot has checked out, or in jj the
// bookmark on trunk(). start is the commit the branch began
// at, empty when it was not recorded. The base is checked, then its upstream
// when it has one, as git last fetched it: Merged never fetches.
//
// The answer is cached per branch tip, base tips and start, so a listing
// polled every few seconds runs only rev-parse (two calls when the base has
// an upstream) per worktree until something moves. In jj it is cached per
// operation log state, which is a directory read.
func Merged(path, repoRoot, branch, start, base string) (string, error) {
	var refs, tips []string
	var key string
	r := gitstate.Find(path)
	if r.JJ {
		if r.Root == r.Main {
			return "", nil
		}
		ops, err := r.JJOps()
		if err != nil {
			return "", err
		}
		key = path + "\x00" + branch + "\x00" + start + "\x00" + base + "\x00" + ops
	} else {
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
		refs = []string{base}
		var err error
		tips, err = revParse(path, "HEAD", base+"^{commit}", base+"@{upstream}^{commit}")
		if err == nil {
			up, uerr := run(path, "rev-parse", "--abbrev-ref", base+"@{upstream}")
			if uerr == nil && strings.TrimSpace(up) != "" {
				refs = append(refs, strings.TrimSpace(up))
			} else {
				tips = tips[:2]
			}
		} else {
			if tips, err = revParse(path, "HEAD", base+"^{commit}"); err != nil {
				return "", err
			}
		}
		key = path + "\x00" + start + "\x00" + base + "\x00" + strings.Join(tips, "\x00")
	}
	mergedMu.Lock()
	into, hit := mergedCache[key]
	mergedMu.Unlock()
	if hit {
		return into, nil
	}
	if r.JJ {
		var err error
		if refs, tips, err = jjMergedTips(context.Background(), r.Root, base); err != nil {
			return "", err
		}
	}

	into = ""
	for i, ref := range refs {
		if mergedInto(path, branch, start, tips[0], tips[i+1]) {
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

func mergedInto(path, branch, start, head, target string) bool {
	mb, err := MergeBase(path, head, target)
	if err != nil {
		return false
	}
	if mb == head {
		if r := gitstate.Find(path); r.JJ {
			return jjMadeCommits(context.Background(), r.Root, branch, start)
		}
		return madeCommits(path, start, head)
	}
	trees, err := revParse(path, head+"^{tree}", mb+"^{tree}", target+"^{tree}")
	if err != nil || trees[0] == trees[1] {
		return false
	}
	merged, err := mergeTree(path, target, head)
	return err == nil && merged == trees[2]
}

func mergeTree(path, target, head string) (string, error) {
	objects, err := run(path, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return "", err
	}
	tmp, ctx, err := newTemp(context.Background(), "merge-tree")
	if err != nil {
		return "", err
	}
	defer tmp.remove()
	alternates := strings.TrimSpace(objects)
	if prev := os.Getenv("GIT_ALTERNATE_OBJECT_DIRECTORIES"); prev != "" {
		alternates += string(os.PathListSeparator) + prev
	}
	env := []string{"GIT_OBJECT_DIRECTORY=" + tmp.path, "GIT_ALTERNATE_OBJECT_DIRECTORIES=" + alternates}
	out, err := runGit(ctx, path, env, nil, "merge-tree", "--write-tree", target, head)
	if err != nil {
		return "", err
	}
	tree, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(tree), nil
}

const reflogScan = 64

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

func ownEntry(subject, sha string) bool {
	for _, p := range []string{"commit:", "commit (amend):", "commit (initial):", "revert:", "cherry-pick:"} {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	if _, onto, ok := strings.Cut(subject, "(finish): "); ok {
		if _, onto, ok = strings.Cut(onto, " onto "); ok {
			return strings.TrimSpace(onto) != sha
		}
	}
	return false
}

func isAncestor(dir, a, b string) bool {
	if strings.HasPrefix(a, "-") || strings.HasPrefix(b, "-") {
		return false
	}
	_, err := run(dir, "merge-base", "--is-ancestor", a, b)
	return err == nil
}

func revParse(dir string, revs ...string) ([]string, error) {
	for _, r := range revs {
		if strings.HasPrefix(r, "-") {
			return nil, errfmt.Errorf("%q is not a revision", r)
		}
	}
	out, err := run(dir, append([]string{"rev-parse"}, revs...)...)
	if err != nil {
		return nil, err
	}
	hashes := strings.Fields(out)
	if len(hashes) != len(revs) {
		return nil, errfmt.Errorf("git rev-parse: %d hashes for %d revisions", len(hashes), len(revs))
	}
	return hashes, nil
}
