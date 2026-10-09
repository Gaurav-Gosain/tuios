package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Staging: the diffs of a worktree's index, and moving changes between the
// working state and the index by file, by hunk and by line.
//
// Two index diffs exist. The unstaged diff runs from the index (written as a
// tree with git write-tree) to the working state (worktree.SnapshotTree, so
// untracked files are in it). The staged diff runs from HEAD (the empty tree
// before the first commit) to the index. Both are read by Build, so a hunk's
// header and its lines are the same here as review-diff shows them for the
// same mode and context.
//
// A selection of lines becomes a patch against the index, applied with
// git apply --cached. Nothing here writes the working tree: every write is to
// the index, through git add, git reset, git apply --cached or
// git update-index. Each of them runs with the worktree's own index, never
// the caller's GIT_INDEX_FILE (gitEnv drops it).

// Index diff modes, as review-diff's index parameter names them.
const (
	IndexUnstaged = "unstaged"
	IndexStaged   = "staged"
)

// IndexTree writes the index of the worktree at dir as a tree and returns its
// hash. An index with a conflict cannot be written, and is an error.
func IndexTree(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HasHead reports whether the repository at dir has a commit.
func HasHead(ctx context.Context, dir string) bool {
	_, err := revParse(ctx, dir, "HEAD^{commit}")
	return err == nil
}

// headTree is HEAD's tree, or the empty tree before the first commit.
func headTree(ctx context.Context, dir string) (string, error) {
	if sha, err := revParse(ctx, dir, "HEAD^{tree}"); err == nil {
		return sha, nil
	}
	return emptyTree(ctx, dir)
}

// IndexOptions are the Options Build takes for an index diff of dir: the
// unstaged diff (index to working state) or the staged diff (HEAD to index).
// Base.Name is "index" or "HEAD", and Base.SHA is a tree.
func IndexOptions(ctx context.Context, dir, mode string) (Options, error) {
	index, err := IndexTree(ctx, dir)
	if err != nil {
		return Options{}, err
	}
	switch mode {
	case IndexUnstaged:
		return Options{Dir: dir, Base: Base{Name: "index", SHA: index, From: "index"}}, nil
	case IndexStaged:
		head, err := headTree(ctx, dir)
		if err != nil {
			return Options{}, err
		}
		return Options{Dir: dir, Base: Base{Name: "HEAD", SHA: head, From: "index"}, Tree: index}, nil
	}
	return Options{}, fmt.Errorf("index mode %q is not unstaged or staged", mode)
}

// IndexChange reads one file of an index diff of dir again, with its hunks,
// for staging. path is the file's path, or the old path of a rename. It
// returns nil when the file has no change in that diff.
//
// The listing runs over the whole tree, as review-diff's does, so a rename is
// seen as one. The hunks are read for the one file, which gives the same
// hunks as the whole diff for any file that is not a rename.
func IndexChange(ctx context.Context, dir, mode, path string, context int) (*File, error) {
	opt, err := IndexOptions(ctx, dir, mode)
	if err != nil {
		return nil, err
	}
	tree := opt.Tree
	if tree == "" {
		if tree, err = worktree.SnapshotTree(ctx, dir); err != nil {
			return nil, err
		}
	}
	common := []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative", "-M"}
	out, err := git(ctx, dir, nil, append(append([]string{}, common...), "--raw", "--numstat", "-z", opt.Base.SHA, tree, "--")...)
	if err != nil {
		return nil, err
	}
	files, err := ParseRawNumstat(out)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(files, func(f File) bool { return f.Path == path })
	if i < 0 {
		i = slices.IndexFunc(files, func(f File) bool { return f.OldPath == path })
	}
	if i < 0 {
		return nil, nil
	}
	f := files[i]
	f.Hunks = []Hunk{}
	if f.Binary || f.Status == StatusRenamed {
		return &f, nil
	}
	one := []File{f}
	unified := append(append([]string{}, common...), fmt.Sprintf("-U%d", max(context, 0)), "--src-prefix=a/", "--dst-prefix=b/", opt.Base.SHA, tree, "--", ":(literal)"+f.Path)
	if _, err := streamUnified(ctx, dir, unified, one, DefaultLimits); err != nil {
		return nil, err
	}
	return &one[0], nil
}

// HunkByHeader finds the hunk of f whose header is exactly header.
func (f *File) HunkByHeader(header string) *Hunk {
	for i := range f.Hunks {
		if f.Hunks[i].Header == header {
			return &f.Hunks[i]
		}
	}
	return nil
}

// Changes are the indices of h's added and removed lines.
func (h *Hunk) Changes() []int {
	var out []int
	for i, l := range h.Lines {
		if l.Op != OpContext {
			out = append(out, i)
		}
	}
	return out
}

// indexLacks reports whether a patch of f creates the file in the index:
// the index side of the diff has no file. For the unstaged
// diff that is a new file (A or U), for the staged diff a deleted one.
func (f *File) indexLacks(unstage bool) bool {
	if unstage {
		return f.Status == StatusDeleted
	}
	return f.Status == StatusAdded || f.Status == StatusUntracked
}

// SelectionPatch builds the patch that applies the selected lines of hunk h
// of file f to the index, for git apply --cached. sel holds indices into
// h.Lines; context lines in it are ignored.
//
// To stage, f is from the unstaged diff (index to working state): a selected
// add is added, a selected delete removed, an unselected add dropped and an
// unselected delete kept as context. To unstage, f is from the staged diff
// (HEAD to index), and the patch is that diff's selection reversed, built
// forward against the index: a selected add is removed, a selected delete put
// back, an unselected add kept as context and an unselected delete dropped.
//
// Either way every line on the index side stays in the patch, so the hunk's
// start and length on that side are the hunk's own.
func SelectionPatch(f *File, h *Hunk, sel []int, unstage bool) (string, error) {
	picked := map[int]bool{}
	for _, i := range sel {
		if i < 0 || i >= len(h.Lines) {
			return "", fmt.Errorf("line %d is outside the hunk, which has %d lines", i, len(h.Lines))
		}
		if h.Lines[i].Op != OpContext {
			picked[i] = true
		}
	}
	if len(picked) == 0 {
		return "", errors.New("the selection holds no added or removed line")
	}
	var body strings.Builder
	oldCount, newCount := 0, 0
	// ended is the side a line without its newline closed: ' ' for both.
	// After it a side that ended takes no more lines, or git apply would
	// join two lines into one.
	var ended byte
	badEnd := false
	emit := func(prefix byte, l Line) {
		if ended == ' ' || (ended != 0 && prefix != '+' && prefix != '-') || ended == prefix {
			badEnd = true
		}
		if l.NoNewline {
			ended = prefix
		}
		body.WriteByte(prefix)
		body.WriteString(l.raw)
		body.WriteByte('\n')
		if l.NoNewline {
			body.WriteString("\\ No newline at end of file\n")
		}
		if prefix != '+' {
			oldCount++
		}
		if prefix != '-' {
			newCount++
		}
	}
	for i, l := range h.Lines {
		switch {
		case l.Op == OpContext:
			emit(' ', l)
		case !unstage && l.Op == OpAdd:
			if picked[i] {
				emit('+', l)
			}
		case !unstage: // a delete
			if picked[i] {
				emit('-', l)
			} else {
				emit(' ', l)
			}
		case l.Op == OpAdd: // unstage
			if picked[i] {
				emit('-', l)
			} else {
				emit(' ', l)
			}
		default: // unstage a delete
			if picked[i] {
				emit('+', l)
			}
		}
	}
	if badEnd {
		return "", errors.New("the selection keeps a line that has no newline at the end of the file, and puts lines after it. Select the changes to the last line too")
	}
	start := h.OldStart
	if unstage {
		start = h.NewStart
	}
	newStart := start
	switch {
	case oldCount == 0 && newCount > 0:
		newStart = start + 1
	case newCount == 0 && oldCount > 0:
		newStart = start - 1
	}

	var p strings.Builder
	a, b := quotePatchPath("a/"+f.Path), quotePatchPath("b/"+f.Path)
	fmt.Fprintf(&p, "diff --git %s %s\n", a, b)
	if f.indexLacks(unstage) {
		mode := f.newMode
		if unstage {
			mode = f.oldMode
		}
		if mode == "" || strings.Trim(mode, "0") == "" {
			mode = "100644"
		}
		fmt.Fprintf(&p, "new file mode %s\n--- /dev/null\n+++ %s\n", mode, b)
	} else {
		fmt.Fprintf(&p, "--- %s\n+++ %s\n", a, b)
	}
	fmt.Fprintf(&p, "@@ -%d,%d +%d,%d @@\n", start, oldCount, newStart, newCount)
	p.WriteString(body.String())
	return p.String(), nil
}

// quotePatchPath writes a path the way git apply reads one: in double quotes
// with C escapes when it holds a quote, a backslash, a space or a control
// byte, else as it is.
func quotePatchPath(p string) string {
	if !strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == ' ' }) {
		return p
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, "\\%03o", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ApplyToIndex applies patch to the index of the worktree at dir with
// git apply --cached. zeroContext allows hunks with no context, as a diff
// read with no context lines has.
func ApplyToIndex(ctx context.Context, dir, patch string, zeroContext bool) error {
	args := []string{"apply", "--cached", "--recount", "--whitespace=nowarn"}
	if zeroContext {
		args = append(args, "--unidiff-zero")
	}
	_, err := gitInput(ctx, dir, patch, append(args, "-")...)
	return err
}

// StageFile stages the whole of each path: its change, its removal, or the
// untracked file.
func StageFile(ctx context.Context, dir string, paths ...string) error {
	_, err := git(ctx, dir, nil, append([]string{"add", "-A"}, pathspec(paths)...)...)
	return err
}

// UnstageFile puts each path's index entry back as HEAD has it, or removes
// it before the first commit.
func UnstageFile(ctx context.Context, dir string, paths ...string) error {
	if HasHead(ctx, dir) {
		_, err := git(ctx, dir, nil, append([]string{"reset", "-q"}, pathspec(paths)...)...)
		return err
	}
	_, err := git(ctx, dir, nil, append([]string{"update-index", "--force-remove", "--"}, paths...)...)
	return err
}

// IndexEntry is one path's entry in the index: its mode and blob, or Absent
// when the index has no entry for it.
type IndexEntry struct {
	Path   string `json:"path"`
	Mode   string `json:"mode,omitempty"`
	SHA    string `json:"sha,omitempty"`
	Absent bool   `json:"absent,omitempty"`
}

// ConflictError reports a path with a merge conflict in the index.
type ConflictError struct{ Path string }

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s has a merge conflict in the index", e.Path)
}

// IndexEntries reads the index entries of paths, in the order given.
func IndexEntries(ctx context.Context, dir string, paths []string) ([]IndexEntry, error) {
	out, err := git(ctx, dir, nil, append([]string{"-c", "core.quotePath=false", "ls-files", "-s", "-z", "--full-name"}, pathspec(paths)...)...)
	if err != nil {
		return nil, err
	}
	found := map[string]IndexEntry{}
	for rec := range strings.SplitSeq(out, "\x00") {
		if rec == "" {
			continue
		}
		meta, path, ok := strings.Cut(rec, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-files line %q", rec)
		}
		if fields[2] != "0" {
			return nil, &ConflictError{Path: path}
		}
		found[path] = IndexEntry{Path: path, Mode: fields[0], SHA: fields[1]}
	}
	entries := make([]IndexEntry, len(paths))
	for i, p := range paths {
		if e, ok := found[p]; ok {
			entries[i] = e
		} else {
			entries[i] = IndexEntry{Path: p, Absent: true}
		}
	}
	return entries, nil
}

// ValidEntry checks an index entry a caller sent back: a plain path, and a
// mode and an object of this repository, or absent with neither.
func ValidEntry(ctx context.Context, dir string, e IndexEntry) error {
	if err := ValidPath(e.Path); err != nil {
		return err
	}
	if e.Absent {
		if e.Mode != "" || e.SHA != "" {
			return fmt.Errorf("%s is absent and carries a mode or a sha", e.Path)
		}
		return nil
	}
	switch e.Mode {
	case "100644", "100755", "120000", "160000":
	default:
		return fmt.Errorf("%s has mode %q, which is not a file, a link or a submodule", e.Path, e.Mode)
	}
	empty, err := emptyTree(ctx, dir)
	if err != nil {
		return err
	}
	if len(e.SHA) != len(empty) || strings.Trim(e.SHA, "0123456789abcdef") != "" {
		return fmt.Errorf("%s has sha %q, which is not an object name here", e.Path, e.SHA)
	}
	if e.Mode == "160000" {
		// A submodule's commit lives in the submodule, not here.
		return nil
	}
	if _, err := git(ctx, dir, nil, "cat-file", "-e", e.SHA+"^{blob}"); err != nil {
		return fmt.Errorf("%s names blob %s, which this repository does not have", e.Path, e.SHA)
	}
	return nil
}

// SetIndexEntries writes entries into the index with
// git update-index --index-info: an absent one removes the path's entry.
func SetIndexEntries(ctx context.Context, dir string, entries []IndexEntry) error {
	empty, err := emptyTree(ctx, dir)
	if err != nil {
		return err
	}
	zero := strings.Repeat("0", len(empty))
	var in strings.Builder
	for _, e := range entries {
		if e.Absent {
			fmt.Fprintf(&in, "0 %s\t%s\x00", zero, e.Path)
		} else {
			fmt.Fprintf(&in, "%s %s\t%s\x00", e.Mode, e.SHA, e.Path)
		}
	}
	_, err = gitInput(ctx, dir, in.String(), "update-index", "-z", "--index-info")
	return err
}

// gitInput is git with stdin.
func gitInput(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", firstArg(args), msg)
	}
	return stdout.String(), nil
}
