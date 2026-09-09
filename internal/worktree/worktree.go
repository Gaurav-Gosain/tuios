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
// gitdir holds "commondir" and "HEAD".
package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/adrg/xdg"
)

// Info is what a worktree is, as far as the rest of tuios needs to know.
type Info struct {
	// Repo is the repository's name: the base name of its main checkout.
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
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return Info{}, false
	}
	for p := dir; ; {
		entry := filepath.Join(p, ".git")
		st, err := os.Lstat(entry)
		if err == nil {
			if st.IsDir() {
				return Info{}, false
			}
			if st.Mode().IsRegular() {
				return readLinked(p, entry)
			}
		}
		parent := filepath.Dir(p)
		if parent == p {
			return Info{}, false
		}
		p = parent
	}
}

// readLinked reads a worktree's ".git" file and the gitdir it names.
func readLinked(root, gitFile string) (Info, bool) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return Info{}, false
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return Info{}, false
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	gitdir = filepath.Clean(gitdir)
	// Only a gitdir under <common>/worktrees/<name> is a linked worktree. A
	// submodule also uses a ".git" file, and its gitdir sits under modules/.
	if filepath.Base(filepath.Dir(gitdir)) != "worktrees" {
		return Info{}, false
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return Info{}, false
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitdir, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	repoRoot := commonDir
	if filepath.Base(commonDir) == ".git" {
		repoRoot = filepath.Dir(commonDir)
	}
	branch := readHead(gitdir)
	if branch == "" {
		return Info{}, false
	}
	return Info{
		Repo:     filepath.Base(repoRoot),
		RepoRoot: repoRoot,
		Branch:   branch,
		Path:     root,
	}, true
}

// readHead names the branch a gitdir's HEAD points at, or "detached@<hash>".
func readHead(gitdir string) string {
	data, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(data))
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

// DefaultDir is where tuios puts the worktrees it creates:
// $XDG_DATA_HOME/tuios/worktrees, one directory per repository under it.
func DefaultDir() string {
	if override := os.Getenv("TUIOS_WORKTREE_DIR"); override != "" {
		return override
	}
	return filepath.Join(xdg.DataHome, "tuios", "worktrees")
}

// PathFor is the directory a worktree of repoRoot on branch gets under dir.
func PathFor(dir, repoRoot, branch string) string {
	return filepath.Join(dir, filepath.Base(repoRoot), Slug(branch))
}

var slugStrip = regexp.MustCompile(`[^A-Za-z0-9._]+`)

// Slug turns a branch name into one path component and one session-name
// component: every run of characters outside [A-Za-z0-9._] becomes one
// hyphen. "feat/retry" reads as "feat-retry".
func Slug(branch string) string {
	s := slugStrip.ReplaceAllString(branch, "-")
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
	cmd := exec.Command("git", "check-ref-format", "--branch", name)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if BranchExists(repoRoot, branch) {
		if _, err := run(repoRoot, "worktree", "add", path, branch); err != nil {
			return false, err
		}
		return false, nil
	}
	args := []string{"worktree", "add", "-b", branch, path}
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
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, nil
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
		for _, f := range strings.Split(u, "\n") {
			b.WriteString("  " + f + "\n")
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
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Nothing here is interactive, and a git that waits on a terminal for a
	// credential or an editor would hang the daemon.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
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
