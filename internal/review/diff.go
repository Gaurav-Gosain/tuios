package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Base is the commit a diff runs from, and how it was found.
type Base struct {
	// Name is the base as named: the ref given, the worktree's recorded base,
	// the upstream branch, or HEAD.
	Name string
	// SHA is the commit the diff runs from: the merge base of HEAD and Name,
	// or HEAD itself for uncommitted changes only. In a repository with no
	// commit yet it is the empty tree.
	SHA string
	// Uncommitted says the diff shows only what is not committed: asked for,
	// or no base was found.
	Uncommitted bool
	// From says where the base came from: param, worktree, upstream or head.
	From string
}

// RefError reports a base the caller named that git cannot resolve.
type RefError struct {
	Ref string
	Err error
}

func (e *RefError) Error() string { return fmt.Sprintf("%q is not a commit here: %v", e.Ref, e.Err) }

func (e *RefError) Unwrap() error { return e.Err }

// ValidRef refuses a ref that git could read as an option or that holds a
// control character. Everything else is left to git to resolve.
func ValidRef(ref string) error {
	if ref == "" {
		return errors.New("empty ref")
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("%q starts with a dash", ref)
	}
	if len(ref) > 256 {
		return errors.New("ref is longer than 256 bytes")
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return fmt.Errorf("%q holds a space or a control character", ref)
		}
	}
	return nil
}

// ResolveBase finds the commit to diff the worktree at dir from. In order:
// uncommitted asks for HEAD; then the base the caller named, which must
// resolve; then the base the worktree was made from (recorded), then the
// merge base with the upstream branch, then HEAD. A named or recorded base
// is taken through the merge base with HEAD, so a base that moved on since
// the branch left it does not show its own changes as removed.
func ResolveBase(ctx context.Context, dir, requested, recorded string, uncommitted bool) (Base, error) {
	head, headErr := revParse(ctx, dir, "HEAD^{commit}")
	headBase := func(from string) (Base, error) {
		if headErr != nil {
			empty, err := emptyTree(ctx, dir)
			if err != nil {
				return Base{}, err
			}
			return Base{Name: "HEAD", SHA: empty, Uncommitted: true, From: from}, nil
		}
		return Base{Name: "HEAD", SHA: head, Uncommitted: true, From: from}, nil
	}
	if uncommitted {
		return headBase("head")
	}
	fromRef := func(ref, from string) (Base, error) {
		if err := ValidRef(ref); err != nil {
			return Base{}, &RefError{Ref: ref, Err: err}
		}
		sha, err := revParse(ctx, dir, ref+"^{commit}")
		if err != nil {
			return Base{}, &RefError{Ref: ref, Err: err}
		}
		if headErr == nil {
			if mb, err := git(ctx, dir, nil, "merge-base", head, sha); err == nil && strings.TrimSpace(mb) != "" {
				sha = strings.TrimSpace(mb)
			}
		}
		return Base{Name: ref, SHA: sha, From: from}, nil
	}
	if requested != "" {
		return fromRef(requested, "param")
	}
	if recorded != "" {
		if b, err := fromRef(recorded, "worktree"); err == nil {
			return b, nil
		} else if ctx.Err() != nil {
			return Base{}, err
		}
	}
	if headErr == nil {
		if name, err := git(ctx, dir, nil, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil {
			name = strings.TrimSpace(name)
			if mb, err := git(ctx, dir, nil, "merge-base", "HEAD", "@{upstream}"); err == nil && strings.TrimSpace(mb) != "" {
				return Base{Name: name, SHA: strings.TrimSpace(mb), From: "upstream"}, nil
			}
		}
	}
	if ctx.Err() != nil {
		return Base{}, ctx.Err()
	}
	return headBase("head")
}

// Options say what Build reads.
type Options struct {
	// Dir is the worktree whose working state is the new side.
	Dir string
	// Base is the old side, from ResolveBase. Ignored with AgainstDir.
	Base Base
	// AgainstDir is another worktree of the same repository whose working
	// state is the old side instead of Base.
	AgainstDir string
	// Paths keeps only these paths, relative to the worktree's root. Each is
	// taken literally, never as a pattern.
	Paths []string
	// Context is the lines of context around each change.
	Context int
	// Limits bound the diff; the zero value is DefaultLimits.
	Limits Limits
}

// Build reads the diff. Nothing in the repository, its index or its working
// tree is changed: the working state is written as a tree through a
// temporary index, whose objects git gc collects like any other loose object.
func Build(ctx context.Context, opt Options) (*Diff, error) {
	lim := opt.Limits
	if lim.Files <= 0 {
		lim = DefaultLimits
	}
	tree, err := worktree.SnapshotTree(ctx, opt.Dir)
	if err != nil {
		return nil, err
	}
	d := &Diff{TreeSHA: tree, Files: []File{}}
	from := opt.Base.SHA
	if opt.AgainstDir != "" {
		from, err = worktree.SnapshotTree(ctx, opt.AgainstDir)
		if err != nil {
			return nil, fmt.Errorf("the other worktree: %w", err)
		}
		d.AgainstTree = from
	} else {
		d.Base, d.BaseSHA, d.Uncommitted = opt.Base.Name, opt.Base.SHA, opt.Base.Uncommitted
	}
	if from == "" {
		return nil, errors.New("no base to diff from")
	}
	spec := pathspec(opt.Paths)

	// Settings a repository or the person's config may carry that would
	// change what git prints are pinned here.
	common := []string{"-c", "core.quotePath=false", "diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-relative", "-M"}

	listArgs := append(append([]string{}, common...), "--raw", "--numstat", "-z", from, tree)
	out, err := git(ctx, opt.Dir, nil, append(listArgs, spec...)...)
	if err != nil {
		return nil, err
	}
	files, err := ParseRawNumstat(out)
	if err != nil {
		return nil, err
	}
	if opt.AgainstDir == "" {
		markUntracked(ctx, opt.Dir, files, spec)
	}
	for i := range files {
		files[i].Hunks = []Hunk{}
	}

	if len(files) > 0 {
		unified := append(append([]string{}, common...), fmt.Sprintf("-U%d", max(opt.Context, 0)), "--src-prefix=a/", "--dst-prefix=b/", from, tree)
		truncated, err := streamUnified(ctx, opt.Dir, append(unified, spec...), files, lim)
		if err != nil {
			return nil, err
		}
		d.Truncated = truncated
	}
	d.Files = files
	for _, f := range files {
		d.Totals.Files++
		d.Totals.Added += f.Added
		d.Totals.Removed += f.Removed
	}
	return d, nil
}

// pathspec is the "--" and the paths, each literal.
func pathspec(paths []string) []string {
	out := []string{"--"}
	for _, p := range paths {
		out = append(out, ":(literal)"+p)
	}
	return out
}

// markUntracked marks the added files that git does not track yet as U.
// Ignored files are never in the tree, so they are not in the diff either.
func markUntracked(ctx context.Context, dir string, files []File, spec []string) {
	hasAdded := false
	for _, f := range files {
		if f.Status == StatusAdded {
			hasAdded = true
			break
		}
	}
	if !hasAdded {
		return
	}
	args := append([]string{"-c", "core.quotePath=false", "ls-files", "-z", "--others", "--exclude-standard", "--full-name"}, spec...)
	out, err := git(ctx, dir, nil, args...)
	if err != nil {
		return
	}
	untracked := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			untracked[p] = true
		}
	}
	for i := range files {
		if files[i].Status == StatusAdded && untracked[files[i].Path] {
			files[i].Status = StatusUntracked
		}
	}
}

// streamUnified runs git diff and reads its output as it comes, stopping git
// once a limit is reached, so a huge diff costs no more than the limits.
func streamUnified(ctx context.Context, dir string, args []string, files []File, lim Limits) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, err
	}
	if err := cmd.Start(); err != nil {
		return false, err
	}
	truncated, stopped, rerr := readUnified(stdout, files, lim)
	if stopped || rerr != nil {
		cancel()
	}
	werr := cmd.Wait()
	if rerr != nil {
		return false, rerr
	}
	if stopped {
		return truncated, nil
	}
	if werr != nil {
		if ctx.Err() != nil {
			return false, fmt.Errorf("git diff: %w", ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = werr.Error()
		}
		return false, fmt.Errorf("git diff: %s", msg)
	}
	return truncated, nil
}

// maxAnchorFile bounds the file a note's line is looked for in.
const maxAnchorFile = 8 << 20

// FileLines reads a file of the worktree at root as lines, for finding a
// note's line again. path must stay inside root, symbolic links included. A
// file past 8 MiB is not read.
func FileLines(root, path string) ([]string, error) {
	full, err := InsideRoot(root, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > maxAnchorFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxAnchorFile)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return SplitLines(string(data)), nil
}

// BlobLines reads path as it is in rev, as lines.
func BlobLines(ctx context.Context, dir, rev, path string) ([]string, error) {
	if err := ValidPath(path); err != nil {
		return nil, err
	}
	out, err := git(ctx, dir, nil, "cat-file", "blob", rev+":"+path)
	if err != nil {
		return nil, err
	}
	if len(out) > maxAnchorFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxAnchorFile)
	}
	return SplitLines(out), nil
}

// SplitLines splits text into lines without their line ends.
func SplitLines(text string) []string {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	return lines
}

// ValidPath refuses a note or filter path that is not a plain relative path
// inside the worktree.
func ValidPath(path string) error {
	switch {
	case path == "":
		return errors.New("empty path")
	case len(path) > 4096:
		return errors.New("path is longer than 4096 bytes")
	case strings.ContainsRune(path, 0):
		return errors.New("path holds a NUL byte")
	case filepath.IsAbs(path) || strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\"):
		return fmt.Errorf("%q is absolute; name it relative to the repository root", path)
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("%q leaves the repository", path)
		}
	}
	return nil
}

// InsideRoot joins path to root and makes sure the result, with its symbolic
// links resolved, is still inside root.
func InsideRoot(root, path string) (string, error) {
	if err := ValidPath(path); err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q leaves the repository", path)
	}
	return real, nil
}

// revParse resolves a revision to its full hash.
func revParse(ctx context.Context, dir, rev string) (string, error) {
	out, err := git(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "--end-of-options", rev)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if sha == "" {
		return "", fmt.Errorf("%s does not resolve", rev)
	}
	return sha, nil
}

// emptyTree is the hash of the empty tree in the repository's hash format.
func emptyTree(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "hash-object", "-t", "tree", "--stdin")
	cmd.Dir = dir
	cmd.Env = gitEnv(nil)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitEnv is the environment git runs with: nothing interactive, no optional
// locks, and never the caller's index.
func gitEnv(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_INDEX_FILE=") || strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_OPTIONAL_LOCKS=0")
	return append(env, extra...)
}

// git runs one git command in dir, bounded by ctx, and returns its stdout. A
// failure carries git's own message.
func git(ctx context.Context, dir string, extra []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extra)
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

// firstArg is the git subcommand of args, past any -c settings.
func firstArg(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return "git"
}
