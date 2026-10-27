package webshell

import (
	"fmt"
	"sort"

	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Review is the fake repository's answer to tuios's review-diff: what the
// checkout a folder is in changed since its base, committed or not, with
// untracked files.
type Review struct {
	// Root is the checkout's folder, and RepoRoot the main checkout's.
	Root, RepoRoot string
	Branch         string
	// Session is the session the checkout's panes are in on a real machine:
	// the repository's name for the main checkout, and the worktree session
	// tuios makes for a linked one.
	Session string
	// Base is the base as named, and BaseHash the commit the diff runs from.
	Base, BaseHash string
	// Uncommitted says the diff runs from the checkout's last commit.
	Uncommitted bool
	Files       []review.File
}

// ReviewDiff reads what the checkout dir is in changed. The base is chosen
// the way review-diff chooses it: uncommitted asks for the last commit; then
// base, which has to be a branch of the repository or HEAD; then the branch a
// worktree was made from; then the last commit.
func ReviewDiff(dir, base string, uncommitted bool) (Review, error) {
	fsMu.RLock()
	defer fsMu.RUnlock()
	c := checkoutAt(dir)
	if c == nil {
		return Review{}, fmt.Errorf("no git repository under %s", prettyPath(dir))
	}
	r := Review{Root: c.root, RepoRoot: repo.root, Branch: c.branch, Session: baseName(repo.root)}
	if c != repo {
		r.Session = worktree.SessionName(baseName(repo.root), c.branch)
	}
	var from map[string]string
	switch {
	case uncommitted || (base == "" && c.base == ""):
		from, r.Base, r.BaseHash, r.Uncommitted = c.head, "HEAD", c.headHash(), true
	case base == "" || base == c.base:
		from, r.Base, r.BaseHash = c.fork, c.base, c.forkHash
	case base == "HEAD" || base == c.branch:
		from, r.Base, r.BaseHash = c.head, base, c.headHash()
	default:
		other := (*checkout)(nil)
		for _, o := range checkouts() {
			if o.branch == base {
				other = o
			}
		}
		if other == nil {
			return Review{}, fmt.Errorf("%q is not a commit here", base)
		}
		// The merge base of two branches of this repository: where the
		// worktree left the main branch.
		switch {
		case other.base == c.branch:
			from, r.BaseHash = other.fork, other.forkHash
		case c.base == other.branch:
			from, r.BaseHash = c.fork, c.forkHash
		default:
			from, r.BaseHash = other.head, other.headHash()
		}
		r.Base = base
	}
	work := c.files()
	paths := map[string]bool{}
	for p := range from {
		paths[p] = true
	}
	for p := range work {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		old, inOld := from[p]
		cur, inWork := work[p]
		if inOld && inWork && old == cur {
			continue
		}
		f := review.File{Path: p, Status: review.StatusModified}
		_, tracked := c.head[p]
		switch {
		case !inWork:
			f.Status = review.StatusDeleted
		case !inOld && !tracked:
			f.Status = review.StatusUntracked
		case !inOld:
			f.Status = review.StatusAdded
		}
		f.Hunks, f.Added, f.Removed = reviewHunks(lineDiff(old, cur))
		r.Files = append(r.Files, f)
	}
	return r, nil
}

// reviewHunks turns a line diff into review hunks with three lines of
// context, and counts the lines added and removed.
func reviewHunks(ops []diffOp) (hunks []review.Hunk, added, removed int) {
	for _, h := range groupHunks(ops, 3) {
		rh := review.Hunk{Header: h.header(), OldStart: h.aStart, OldLines: h.aCount, NewStart: h.bStart, NewLines: h.bCount}
		if h.aCount == 0 {
			rh.OldStart--
		}
		if h.bCount == 0 {
			rh.NewStart--
		}
		a, b := h.aStart, h.bStart
		for _, op := range h.ops {
			switch op.Op {
			case '-':
				rh.Lines = append(rh.Lines, review.Line{Op: review.OpDelete, Old: a, Text: op.Line})
				a++
				removed++
			case '+':
				rh.Lines = append(rh.Lines, review.Line{Op: review.OpAdd, New: b, Text: op.Line})
				b++
				added++
			default:
				rh.Lines = append(rh.Lines, review.Line{Op: review.OpContext, Old: a, New: b, Text: op.Line})
				a++
				b++
			}
		}
		hunks = append(hunks, rh)
	}
	return hunks, added, removed
}
