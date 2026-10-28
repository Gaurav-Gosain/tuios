// Package worktree is what tuios knows about git worktrees: how to tell that a
// directory is one, how to make one, and how to take one away without losing
// work that was never committed.
//
// It is the only package that runs git. Everything above it (the daemon's
// worktree verbs, the fan-out, the rail) works from the Info this package
// returns and from the errors it explains, so the git command lines live in one
// place and the tests that create throwaway repositories cover all of them.
//
// Detection reads files and never runs git, because it runs from the daemon on
// every session whose directory changes, and a subprocess per session per
// change is the kind of cost that makes an idle daemon busy. A linked worktree
// is recognisable without git: its ".git" is a file naming the gitdir, and that
// gitdir holds "commondir" and "HEAD". A repository whose refs are in reftable
// is the exception: HEAD holds a placeholder, and the branch is asked of git
// through gitstate, which asks again only when the refs have moved.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// Info is what a worktree is, as far as the rest of tuios needs to know.
type Info struct {
	// Repo is the repository's name: the base name of its main checkout, or
	// of a bare repository's project directory (see gitstate.RepoName).
	Repo string `json:"repo"`
	// RepoRoot is the main checkout, the directory holding the shared .git.
	RepoRoot string `json:"repo_root"`
	// Branch is the branch the worktree has checked out. A detached HEAD
	// reads as "detached@<short hash>".
	Branch string `json:"branch"`
	// Path is the worktree's own root directory.
	Path string `json:"path"`
}

// Detect reports whether dir lies inside a linked git worktree, and which one.
//
// It walks up from dir looking for a ".git" entry. A ".git" file names a
// linked worktree, and the answer is read from the gitdir it points to. A
// ".git" directory is the main checkout, which is not a worktree in the sense
// this package means: the sessions the rail groups are the ones a repository
// was split into, not the repository itself.
func Detect(dir string) (Info, bool) {
	info, main, ok := DetectEither(dir)
	if main {
		return Info{}, false
	}
	return info, ok
}

// DetectEither is the one walk up from dir that Detect's answer and the
// main checkout's share: a single Locate, answering either question. main says the answer is the
// repository's main checkout — the directory whose .git is the repository's
// own git directory, which Detect deliberately reports nothing for, because
// the sessions it groups are the ones a repository was split into, not the
// repository itself; the rail's group_main option wants the repository itself
// in the group too, and this is what it reads. A linked worktree is the
// answer with main false, and a directory outside any repository is no
// answer at all.
func DetectEither(dir string) (info Info, main, ok bool) {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return Info{}, false, false
	}
	gitdir, commonDir, root, found := gitstate.Locate(dir)
	if !found {
		return Info{}, false, false
	}
	main = gitdir == commonDir
	// The entry under root tells the two apart. A submodule also answers
	// gitdir == commonDir: its .git file names a gitdir under modules/ that
	// carries no commondir pointer, and that gitdir is not a working tree, so
	// only a .git directory says main checkout. Only a gitdir under
	// <common>/worktrees/<name> is a linked worktree; a gitdir under modules/
	// is the submodule again.
	if main {
		if st, err := os.Stat(filepath.Join(root, ".git")); err != nil || !st.IsDir() {
			return Info{}, false, false
		}
	} else if filepath.Base(filepath.Dir(gitdir)) != "worktrees" {
		return Info{}, false, false
	}
	repoRoot := commonDir
	if filepath.Base(commonDir) == ".git" {
		repoRoot = filepath.Dir(commonDir)
	}
	branch := readHead(gitdir, commonDir, root)
	if branch == "" {
		return Info{}, false, false
	}
	return Info{
		Repo:     gitstate.RepoName(commonDir),
		RepoRoot: repoRoot,
		Branch:   branch,
		Path:     root,
	}, main, true
}

// readHead names the branch a gitdir's HEAD points at, or "detached@<hash>".
// root is the worktree, where git is asked when the refs are in reftable.
func readHead(gitdir, commonDir, root string) string {
	data, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
	if gitstate.IsReftableHead(head) {
		branch, sha, detached := gitstate.ReftableHead(gitdir, commonDir, root)
		if detached {
			return "detached@" + sha[:7]
		}
		return branch
	}
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref
	}
	if len(head) >= 7 {
		return "detached@" + head[:7]
	}
	return ""
}

// Root finds the main checkout of the repository dir belongs to, through git.
// dir may be the main checkout, a linked worktree, or any directory under
// either. It is the one detection that runs git, because the caller is about
// to run git anyway to add a worktree.
func Root(dir string) (string, error) {
	out, err := run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	common = filepath.Clean(common)
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common), nil
	}
	return common, nil
}

var slugStrip = lazyre.New(`[^A-Za-z0-9._]+`)

// Slug turns a branch name into one path component and one session-name
// component: every run of characters outside [A-Za-z0-9._] becomes one
// hyphen. "feat/retry" reads as "feat-retry".
func Slug(branch string) string {
	s := slugStrip().ReplaceAllString(branch, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "branch"
	}
	return s
}

// SessionName is the session a worktree session is addressed by. A session
// name cannot hold a slash, so the branch is slugged and joined to the
// repository with a hyphen: "tuios" on "feat/retry" is "tuios-feat-retry".
func SessionName(repo, branch string) string {
	return Slug(repo) + "-" + Slug(branch)
}

// ValidBranch reports whether git would accept name as a branch name.
func ValidBranch(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("branch name is empty")
	}
	cmd := childproc.Git(context.Background(), "", "check-ref-format", "--branch", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}

// BranchExists reports whether repoRoot has a local branch by that name.
func BranchExists(repoRoot, branch string) bool {
	_, err := run(repoRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// Add creates a worktree of repoRoot at path on branch. A branch that exists
// is checked out. One that does not is created from base, or from HEAD when
// base is empty. It reports whether the branch was created.
//
// A path that already exists is refused rather than reused: a directory that
// is already there is somebody's, and git would refuse it too.
func Add(repoRoot, path, branch, base string) (created bool, err error) {
	if _, err := os.Lstat(path); err == nil {
		return false, fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a folder in the user's own tree, with ordinary permissions
		return false, err
	}
	// "--" ends the options, so a base or a path that starts with "-",
	// such as --force, is never read as one.
	if BranchExists(repoRoot, branch) {
		if _, err := run(repoRoot, "worktree", "add", "--", path, branch); err != nil {
			return false, err
		}
		return false, nil
	}
	if strings.HasPrefix(base, "-") {
		return false, fmt.Errorf("base %q starts with -, which git would read as an option", base)
	}
	args := []string{"worktree", "add", "-b", branch, "--", path}
	if base != "" {
		args = append(args, base)
	}
	if _, err := run(repoRoot, args...); err != nil {
		return false, err
	}
	return true, nil
}

// Changes counts the paths in a worktree that differ from HEAD, untracked
// files included. Zero means nothing would be lost by removing it.
func Changes(path string) (int, error) {
	out, err := run(path, "status", "--porcelain", "--untracked-files=all")
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

// statusEntries reads `git status --porcelain -z --untracked-files=all
// --ignored=matching` in the worktree at path and returns the ignored and the
// untracked paths it lists, the status markers stripped. -z ends every record
// at NUL and keeps git from quoting, so a path with a space or a non-ASCII
// character survives the round trip. Splitting on a newline would cut such a
// path in half, and a quoted name would lstat as its quoted form and fail the
// walk a clean worktree never fails.
func statusEntries(path string) (ignored, untracked []string, err error) {
	out, err := run(path, "status", "--porcelain", "-z", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return nil, nil, err
	}
	for record := range strings.SplitSeq(out, "\x00") {
		if entry, ok := strings.CutPrefix(record, "!! "); ok {
			ignored = append(ignored, entry)
		} else if entry, ok := strings.CutPrefix(record, "?? "); ok {
			untracked = append(untracked, entry)
		}
	}
	return ignored, untracked, nil
}

// Ignored scans the paths a removal would take unsaved: git never stashes
// them, and git worktree remove deletes them without --force. --ignored=matching
// reports a matched directory as one entry however many files sit in it, so the
// entry is walked to count the files a person deleting the worktree is owed.
func Ignored(path string) (int, error) {
	entries, _, err := statusEntries(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, entry := range entries {
		c, err := countFiles(filepath.Join(path, entry))
		if err != nil {
			return 0, err
		}
		n += c
	}
	return n, nil
}

// NestedRepo reports the first ignored or untracked path in a worktree that
// holds a git repository — a .git of its own — whose history no stash and no
// count can save: git worktree remove deletes it with everything around it.
// Empty when there is none. Untracked paths are in the scan because --stash
// saves their files but not a nested repository's .git.
func NestedRepo(path string) (string, error) {
	ignored, untracked, err := statusEntries(path)
	if err != nil {
		return "", err
	}
	for _, entry := range append(ignored, untracked...) {
		nested, err := findNestedRepo(filepath.Join(path, entry))
		if err != nil {
			return "", err
		}
		if nested {
			return entry, nil
		}
	}
	return "", nil
}

// countFiles counts the files under dir, descending into nothing named .git:
// a nested repository's internals are not the worktree's files, and the
// removal deletes them either way.
//
// ponytail: walks every ignored and untracked directory in full; the removal
// reads and deletes them all anyway, so a lazy walk is the same order of cost.
func countFiles(dir string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		n++
		return nil
	})
	return n, err
}

// findNestedRepo reports whether dir holds a .git anywhere below it, stopping
// at the first one. A .git directory is a repository; a .git file (a linked
// worktree's) points at one just as well.
func findNestedRepo(dir string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.Name() == ".git" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found, err
}

// OwnCommits counts the commits the worktree's HEAD holds that no branch,
// tag or remote holds. On a branch they are the branch's own commits, and
// keeping the branch keeps them; on a detached HEAD nothing holds them once
// the worktree is gone.
func OwnCommits(path string) (int, error) {
	out, err := run(path, "rev-list", "--count", "HEAD", "--not", "--branches", "--tags", "--remotes")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Ahead counts the commits on the worktree's HEAD that base does not have.
func Ahead(path, base string) (int, error) {
	out, err := run(path, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Stash moves every uncommitted change in the worktree, untracked files
// included, into the repository's stash under message. The worktree is clean
// afterwards and the changes are still in the repository, where "git stash
// list" finds them by the message.
func Stash(path, message string) error {
	_, err := run(path, "stash", "push", "--include-untracked", "-m", message)
	return err
}

// Remove takes a worktree away with "git worktree remove". Without force git
// refuses a worktree holding uncommitted changes, and so does this. The
// branch is never touched: it stays in the repository with every commit the
// worktree made.
func Remove(repoRoot, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := run(repoRoot, args...)
	return err
}

// Diff is the worktree's uncommitted change against HEAD, followed by the
// untracked files git diff does not show. With stat it is the summary form.
func Diff(path string, stat bool) (string, error) {
	args := []string{"diff", "HEAD"}
	if stat {
		args = append(args, "--stat")
	}
	diff, err := run(path, args...)
	if err != nil {
		return "", err
	}
	untracked, err := run(path, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(diff)
	if u := strings.TrimSpace(untracked); u != "" {
		if b.Len() > 0 && !strings.HasSuffix(diff, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("untracked:\n")
		for f := range strings.SplitSeq(u, "\n") {
			b.WriteString("  ")
			b.WriteString(f)
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

// Log lists the commits the worktree made on top of base, one line each.
func Log(path, base string) (string, error) {
	return run(path, "log", "--oneline", base+"..HEAD")
}

// run executes git in dir and returns its stdout. A failure carries git's own
// stderr, which is the message a person needs.
//
//go:noinline
func run(dir string, args ...string) (string, error) {
	return runEnv(dir, nil, args...)
}

// runEnv is run with extra environment entries.
func runEnv(dir string, env []string, args ...string) (string, error) {
	// Git sets nothing interactive: a git that waits on a terminal for a
	// credential or an editor would hang the daemon.
	cmd := childproc.Git(context.Background(), dir, args...)
	cmd.Env = append(cmd.Env, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}

// Entry is one checkout of a repository, as git worktree list reports it.
type Entry struct {
	// Path is the checkout's directory.
	Path string
	// Branch is the branch checked out there, empty when the checkout is
	// detached or bare.
	Branch   string
	Bare     bool
	Detached bool
	Prunable bool
	// Linked is true for every checkout but the repository's main one.
	Linked bool
}

// List returns every checkout of the repository dir is in, the main one
// first, as git worktree list --porcelain reports them.
func List(dir string) ([]Entry, error) {
	out, err := run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out), nil
}

// parseWorktreeList reads git worktree list --porcelain: one block per
// checkout, blocks split by a blank line, the first block the main checkout.
func parseWorktreeList(out string) []Entry {
	var entries []Entry
	var cur *Entry
	for line := range strings.SplitSeq(out, "\n") {
		key, value, _ := strings.Cut(strings.TrimRight(line, "\r"), " ")
		switch key {
		case "worktree":
			entries = append(entries, Entry{Path: value, Linked: len(entries) > 0})
			cur = &entries[len(entries)-1]
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(value, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		case "prunable":
			if cur != nil {
				cur.Prunable = true
			}
		}
	}
	return entries
}
