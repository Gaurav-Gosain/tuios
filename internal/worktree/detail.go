package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// FileStat is one file a worktree changed against a base.
type FileStat struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// DiffLine is one changed line of a diff: its sign, its number in the new
// file for an added line or in the old file for a removed one, and its text.
type DiffLine struct {
	File string `json:"file"`
	Op   string `json:"op"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// diffLineMax bounds the text of one DiffLine.
const diffLineMax = 200

// WorkingDetail is what a worktree changed against base, file by file, the
// first maxFiles files by lines changed, and the first maxLines changed lines
// of its diff. It reads the working state the way WorkingNumstat does,
// untracked files larger than maxUntracked bytes left out.
func WorkingDetail(ctx context.Context, path, base string, maxFiles, maxLines int, maxUntracked int64) ([]FileStat, []DiffLine, error) {
	if strings.HasPrefix(base, "-") {
		return nil, nil, fmt.Errorf("base %q is not a ref", base)
	}
	tree, _, err := SnapshotTree(ctx, path, maxUntracked)
	if err != nil {
		return nil, nil, err
	}
	out, err := runCtx(ctx, path, nil, "diff", "--numstat", "--no-color", "--no-ext-diff", "--no-renames", base, tree, "--")
	if err != nil {
		return nil, nil, err
	}
	var files []FileStat
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(f) < 3 {
			continue
		}
		a, _ := strconv.Atoi(f[0])
		r, _ := strconv.Atoi(f[1])
		files = append(files, FileStat{Path: f[2], Added: a, Removed: r})
	}
	// The files that changed most first, so a short list says the most.
	for i := 1; i < len(files); i++ {
		for j := i; j > 0 && files[j].Added+files[j].Removed > files[j-1].Added+files[j-1].Removed; j-- {
			files[j], files[j-1] = files[j-1], files[j]
		}
	}
	if len(files) > maxFiles {
		files = files[:maxFiles]
	}
	if maxLines <= 0 {
		return files, nil, nil
	}
	diff, err := runCtx(ctx, path, nil, "diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", base, tree, "--")
	if err != nil {
		return files, nil, err
	}
	return files, firstChanges(diff, maxLines), nil
}

// firstChanges reads the first n added or removed lines of a unified diff
// with no context, numbered from their hunk headers.
func firstChanges(diff string, n int) []DiffLine {
	var out []DiffLine
	oldLine, newLine := 0, 0
	file := ""
	for line := range strings.SplitSeq(diff, "\n") {
		if len(out) == n {
			break
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			// diff --git a/PATH b/PATH: the new path, after " b/".
			if i := strings.LastIndex(line, " b/"); i > 0 {
				file = clipLine(line[i+3:])
			}
		case strings.HasPrefix(line, "@@"):
			// @@ -a,b +c,d @@
			f := strings.Fields(line)
			if len(f) >= 3 {
				oldLine = hunkStart(f[1])
				newLine = hunkStart(f[2])
			}
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			out = append(out, DiffLine{File: file, Op: "+", Line: newLine, Text: clipLine(line[1:])})
			newLine++
		case strings.HasPrefix(line, "-"):
			out = append(out, DiffLine{File: file, Op: "-", Line: oldLine, Text: clipLine(line[1:])})
			oldLine++
		}
	}
	return out
}

// hunkStart is the first line number of "-12,3" or "+12".
func hunkStart(s string) int {
	s = strings.TrimLeft(s, "+-")
	s, _, _ = strings.Cut(s, ",")
	n, _ := strconv.Atoi(s)
	return n
}

// clipLine bounds a line's text.
func clipLine(s string) string {
	s = strings.TrimRight(s, "\r")
	if len(s) > diffLineMax {
		s = strings.ToValidUTF8(s[:diffLineMax], "")
	}
	return s
}

// IgnoredFiles lists what git ignores in the worktree at path: the first max
// entries, and how many there are. A folder git ignores whole is one entry
// ending in /. Removing the worktree deletes them, and git stash does not
// keep them.
func IgnoredFiles(ctx context.Context, path string, max int) ([]string, int, error) {
	out, err := runCtx(ctx, path, nil, "status", "--porcelain", "--ignored", "--untracked-files=normal")
	if err != nil {
		return nil, 0, err
	}
	var names []string
	n := 0
	for line := range strings.SplitSeq(out, "\n") {
		name, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "!! ")
		if !ok {
			continue
		}
		n++
		if len(names) < max {
			names = append(names, clipLine(strings.Trim(name, `"`)))
		}
	}
	return names, n, nil
}

// IgnoredHash names a list of ignored files and their count as one value, so
// a caller can say what it showed and a later reader can tell whether it
// still holds. The order of names does not count.
func IgnoredHash(names []string, count int) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	h := sha256.Sum256([]byte(strconv.Itoa(count) + "\n" + strings.Join(sorted, "\n")))
	return hex.EncodeToString(h[:])
}
