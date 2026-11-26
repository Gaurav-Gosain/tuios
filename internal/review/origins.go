package review

import (
	"context"
	"strconv"
	"strings"
)

// Who made each line of a diff.
//
// A review's diff is the base against the work tree now. Between the two lie
// the turns of the agents that worked in the tree, each saved as a checkpoint
// (worktree.SaveCheckpoint). Origins walks that chain: from the base to the
// first checkpoint, from each checkpoint to the next, and from the last to the
// work tree now. Each step's diff says which lines it added, and the step is
// credited with them. A line keeps its step through the later steps, as blame
// keeps a commit, until a later step removes or rewrites it.
//
// What this cannot tell: a checkpoint holds the whole work tree, so a line the
// person wrote while an agent's turn ran is credited to that turn, and two
// agents whose turns overlap share the lines of the one that ended first.

// OriginStep is one step of the chain: the tree before it, the tree after it,
// and the author it credits, an index the caller gives meaning to. Judge,
// when set, is asked about each line the step added, by its path and text:
// whether the author made it, may not have, or did not.
type OriginStep struct {
	From, To string
	Author   int
	Judge    func(path, text string) Verdict
}

// Verdict is what a step's judge says of a line it added.
type Verdict int

const (
	// Made: the line is the step's author's.
	Made Verdict = iota
	// Unsure: the line is credited to the step's author, marked as one the
	// author may not have made.
	Unsure
	// NotMade: the line is the person's, not the step's author's.
	NotMade
)

// owner is who a line is credited to, and whether that is unsure.
type owner struct {
	author int
	unsure bool
}

// OriginDiffs reads the diff of each step, for the paths given. read caches
// a step's diff: the steps between checkpoints never change, so a read of the
// work tree again redoes only the last.
type OriginDiffs func(ctx context.Context, from, to string, paths []string) (string, error)

// StepDiff is the diff of one step, as Origins reads it: no context, no
// rename detection, so a moved file is one removed and one added.
func StepDiff(ctx context.Context, dir, from, to string, paths []string) (string, error) {
	args := []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative", "--no-renames", "-U0", from, to}
	return git(ctx, dir, nil, append(args, pathspec(paths)...)...)
}

// Origins sets Line.Origin on the added lines of files: the Author of the
// step that last added the line, plus one, so 0 stays "not known". A line no
// step added gets last+1: it was made after the last step, by whoever the
// caller says that is. A removed line gets the origin most of its hunk's
// added lines have, or last+1 in a hunk that only removes.
func Origins(ctx context.Context, files []File, steps []OriginStep, read OriginDiffs, last int) error {
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	if len(paths) == 0 {
		return nil
	}
	owners := map[string]map[int]owner{}
	for _, s := range steps {
		out, err := read(ctx, s.From, s.To, paths)
		if err != nil {
			return err
		}
		applyStep(owners, out, s)
	}
	for i := range files {
		f := &files[i]
		own := owners[f.Path]
		for h := range f.Hunks {
			lines := f.Hunks[h].Lines
			count := map[int]int{}
			for l := range lines {
				ln := &lines[l]
				if ln.Op != "add" {
					continue
				}
				if a, ok := own[ln.New]; ok {
					ln.Origin, ln.Unsure = a.author+1, a.unsure
				} else {
					ln.Origin = last + 1
				}
				count[ln.Origin]++
			}
			main := last + 1
			for o, n := range count {
				if n > count[main] || n == count[main] && o < main {
					main = o
				}
			}
			for l := range lines {
				if lines[l].Op == "delete" {
					lines[l].Origin = main
				}
			}
		}
	}
	return nil
}

// stepHunk is one hunk of a -U0 diff: old lines a to a+b-1 became new lines
// c to c+d-1.
type stepHunk struct{ a, b, c, d int }

// applyStep carries owners through one step's diff and credits the step's
// author with the lines it added, as its judge says.
func applyStep(owners map[string]map[int]owner, diff string, s OriginStep) {
	path, old := "", ""
	var hunks []stepHunk
	var added []owner
	flush := func() {
		if path == "" {
			return
		}
		owners[path] = moveOwners(owners[path], hunks, added)
		path, hunks, added = "", nil, nil
	}
	judge := func(text string) owner {
		if s.Judge == nil {
			return owner{author: s.Author}
		}
		switch s.Judge(path, text) {
		case Unsure:
			return owner{author: s.Author, unsure: true}
		case NotMade:
			return owner{}
		}
		return owner{author: s.Author}
	}
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
		case strings.HasPrefix(line, "--- "):
			old = strings.TrimPrefix(strings.TrimPrefix(line, "--- "), "a/")
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				// A file the step removed keeps no owners.
				delete(owners, old)
				path = ""
				continue
			}
			path = strings.TrimPrefix(p, "b/")
		case strings.HasPrefix(line, "@@ "):
			if h, ok := parseStepHunk(line); ok {
				hunks = append(hunks, h)
			}
		case path != "" && len(hunks) > 0 && strings.HasPrefix(line, "+"):
			added = append(added, judge(strings.TrimPrefix(line, "+")))
		}
	}
	flush()
}

// moveOwners is own after hunks: an owned line the step left alone moves by
// the lines added and removed above it, a line it removed is dropped, and the
// lines it added get added, in order.
func moveOwners(own map[int]owner, hunks []stepHunk, added []owner) map[int]owner {
	out := make(map[int]owner, len(own))
	for l, a := range own {
		shift, gone := 0, false
		for _, h := range hunks {
			switch {
			case h.b > 0 && l >= h.a && l < h.a+h.b:
				gone = true
			case h.b > 0 && h.a+h.b-1 < l, h.b == 0 && h.a < l:
				shift += h.d - h.b
			}
		}
		if !gone {
			out[l+shift] = a
		}
	}
	i := 0
	for _, h := range hunks {
		for n := h.c; n < h.c+h.d; n++ {
			if i < len(added) {
				out[n] = added[i]
			}
			i++
		}
	}
	return out
}

// parseStepHunk reads "@@ -a[,b] +c[,d] @@".
func parseStepHunk(line string) (stepHunk, bool) {
	f := strings.Fields(line)
	if len(f) < 3 {
		return stepHunk{}, false
	}
	a, b, ok1 := stepRange(strings.TrimPrefix(f[1], "-"))
	c, d, ok2 := stepRange(strings.TrimPrefix(f[2], "+"))
	return stepHunk{a, b, c, d}, ok1 && ok2
}

func stepRange(s string) (start, n int, ok bool) {
	head, tail, found := strings.Cut(s, ",")
	start, err := strconv.Atoi(head)
	if err != nil {
		return 0, 0, false
	}
	n = 1
	if found {
		if n, err = strconv.Atoi(tail); err != nil {
			return 0, 0, false
		}
	}
	return start, n, true
}
