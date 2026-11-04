// Package git reads what kero shows of git: a worktree's branch and
// changes, trees of its working tree as it was (snapshots), and its files
// changed since one. It runs the user's git, and writes nothing to a
// repository's index or working tree: snapshots go through an index of
// their own, and every command takes --no-optional-locks, so that an agent
// committing never finds kero holding index.lock.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ErrNoGit: git isn't installed, or on macOS only as the stub that asks to
// install the command line tools.
var ErrNoGit = errors.New("git is not installed")

var (
	gitOnce sync.Once
	gitPath string
)

// command returns the git to run, or "" for none.
func command() string {
	gitOnce.Do(func() {
		p, err := exec.LookPath("git")
		if err != nil {
			return
		}
		// /usr/bin/git without the command line tools is a stub that
		// shows a dialog asking to install them, each time it runs.
		if runtime.GOOS == "darwin" && p == "/usr/bin/git" && exec.Command("/usr/bin/xcode-select", "-p").Run() != nil {
			return
		}
		gitPath = p
	})
	return gitPath
}

// run runs git in dir, with env added, and returns its output.
func run(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	return runInput(ctx, dir, env, nil, args...)
}

// runInput runs git as run does, with stdin as its input.
func runInput(ctx context.Context, dir string, env []string, stdin []byte, args ...string) ([]byte, error) {
	git := command()
	if git == "" {
		return nil, ErrNoGit
	}
	cmd := exec.CommandContext(ctx, git, append([]string{"--no-optional-locks", "-c", "core.quotepath=false", "-c", "color.ui=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	hide(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return out, fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
	}
	return out, nil
}

// Repo is a worktree of a repository: its main checkout or a linked one.
type Repo struct {
	Root   string // the worktree's top level
	GitDir string // its git directory, the repository's for the main checkout
	Common string // the repository's git directory (--git-common-dir)
}

// Open returns the worktree that holds dir.
func Open(ctx context.Context, dir string) (Repo, error) {
	out, err := run(ctx, dir, nil, "rev-parse", "--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return Repo{}, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		return Repo{}, errors.New("git rev-parse: not a worktree")
	}
	// --git-common-dir is relative to dir, unless absolute.
	path := func(p string) string {
		p = filepath.FromSlash(strings.TrimSpace(p))
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		return filepath.Clean(p)
	}
	return Repo{Root: path(lines[0]), GitDir: path(lines[1]), Common: path(lines[2])}, nil
}

// Ignored returns which of names, entries of dir, git ignores there: none
// outside a worktree, or when git can't tell.
func Ignored(ctx context.Context, dir string, names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	in := []byte(strings.Join(names, "\x00") + "\x00")
	// It exits 1 when it ignores none of them.
	out, err := runInput(ctx, dir, nil, in, "check-ignore", "--stdin", "-z")
	if err != nil && len(out) == 0 {
		return nil
	}
	ignored := map[string]bool{}
	for _, n := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if n != "" {
			ignored[n] = true
		}
	}
	return ignored
}

// Status is a worktree's branch and how its working tree differs from its
// HEAD.
type Status struct {
	Branch   string // "" when detached
	Head     string // the commit's id, "" before the first
	Upstream string
	Ahead    int
	Behind   int
	// Files changed, untracked ones too, and the lines they add and
	// remove.
	Files   int
	Added   int
	Removed int
	// State is an operation in progress: "merge", "rebase",
	// "cherry-pick" or "revert".
	State string
}

// Status returns the worktree's status.
func (r Repo) Status(ctx context.Context) (Status, error) {
	out, err := run(ctx, r.Root, nil, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return Status{}, err
	}
	var st Status
	var untracked []string
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "":
		case strings.HasPrefix(f, "# branch.oid "):
			if oid := f[len("# branch.oid "):]; oid != "(initial)" {
				st.Head = oid
			}
		case strings.HasPrefix(f, "# branch.head "):
			if h := f[len("# branch.head "):]; h != "(detached)" {
				st.Branch = h
			}
		case strings.HasPrefix(f, "# branch.upstream "):
			st.Upstream = f[len("# branch.upstream "):]
		case strings.HasPrefix(f, "# branch.ab "):
			for _, n := range strings.Fields(f[len("# branch.ab "):]) {
				v, _ := strconv.Atoi(n[1:])
				if n[0] == '+' {
					st.Ahead = v
				} else {
					st.Behind = v
				}
			}
		case f[0] == '1' || f[0] == 'u':
			st.Files++
		case f[0] == '2':
			st.Files++
			i++ // the path it was renamed from
		case f[0] == '?':
			st.Files++
			untracked = append(untracked, f[2:])
		}
	}
	if st.Head != "" {
		out, err := run(ctx, r.Root, nil, "diff", "--no-ext-diff", "--no-textconv", "--numstat", "-z", "HEAD")
		if err != nil {
			return Status{}, err
		}
		for _, n := range parseNumstat(out) {
			st.Added += n.added
			st.Removed += n.removed
		}
	}
	// git diff leaves out untracked files: count their lines here, as
	// many as a glance needs.
	for i, p := range untracked {
		if i == 200 {
			break
		}
		st.Added += countLines(filepath.Join(r.Root, filepath.FromSlash(p)))
	}
	st.State = r.operation()
	return st, nil
}

// operation returns the operation in progress in the worktree, "" for
// none.
func (r Repo) operation() string {
	for _, op := range []struct{ file, name string }{
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
	} {
		if _, err := os.Stat(filepath.Join(r.GitDir, op.file)); err == nil {
			return op.name
		}
	}
	return ""
}

// countLines counts the lines of a text file of up to a megabyte, 0 for a
// larger or binary one.
func countLines(path string) int {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxText {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil || binary(b) {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// DefaultBranch returns the name of the branch the repository's work
// goes into: origin's HEAD, else main or master, "" when none of these
// is. It reads the refs' files, running nothing.
func (r Repo) DefaultBranch() string {
	if b, err := os.ReadFile(filepath.Join(r.Common, "refs", "remotes", "origin", "HEAD")); err == nil {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: refs/remotes/origin/"); ok {
			return ref
		}
	}
	for _, name := range []string{"main", "master"} {
		if r.hasRef("refs/heads/" + name) {
			return name
		}
	}
	return ""
}

// hasRef reports whether the repository has a ref, loose or packed.
func (r Repo) hasRef(ref string) bool {
	if _, err := os.Stat(filepath.Join(r.Common, filepath.FromSlash(ref))); err == nil {
		return true
	}
	b, err := os.ReadFile(filepath.Join(r.Common, "packed-refs"))
	return err == nil && bytes.Contains(b, []byte(" "+ref+"\n"))
}

// Stamp returns what changes with the worktree's HEAD, index and refs, by
// their files' times and sizes: when it is as before, so is the status
// but for the working tree's files.
func (r Repo) Stamp(branch string) string {
	var b strings.Builder
	files := []string{
		filepath.Join(r.GitDir, "HEAD"), filepath.Join(r.GitDir, "index"), filepath.Join(r.GitDir, "MERGE_HEAD"),
		filepath.Join(r.GitDir, "rebase-merge"), filepath.Join(r.GitDir, "rebase-apply"),
		filepath.Join(r.Common, "packed-refs"),
	}
	if branch != "" {
		files = append(files, filepath.Join(r.Common, "refs", "heads", filepath.FromSlash(branch)))
	}
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%d.%d;", fi.ModTime().UnixNano(), fi.Size())
		} else {
			b.WriteString("-;")
		}
	}
	return b.String()
}

// numstat is a line of git diff --numstat.
type numstat struct {
	path           string
	added, removed int
	binary         bool
}

// parseNumstat reads git diff --numstat -z: "added\tremoved\tpath", or
// for a rename "added\tremoved\t" then the old path and the new.
func parseNumstat(out []byte) []numstat {
	var ns []numstat
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		n := numstat{path: parts[2]}
		if parts[0] == "-" {
			n.binary = true
		}
		n.added, _ = strconv.Atoi(parts[0])
		n.removed, _ = strconv.Atoi(parts[1])
		if n.path == "" && i+2 < len(fields) {
			n.path = fields[i+2]
			i += 2
		}
		ns = append(ns, n)
	}
	return ns
}
