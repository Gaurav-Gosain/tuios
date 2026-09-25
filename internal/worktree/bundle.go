package worktree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Moving a worktree's work to another machine: the commits as a git bundle,
// and what is not committed as a binary patch. The machine that has the work
// makes both. The machine that wants it fetches the bundle into a branch of
// its own checkout and applies the patch in a worktree on that branch.
//
// Neither side touches the other's refs, index or working tree. The sender
// reads its worktree through a temporary index, so its own index is as it
// was, and the receiver writes only a branch that did not exist and a
// worktree it just made.

// ErrEmptyBundle reports a bundle with nothing in it: the branch has no
// commits past the ones the receiver is assumed to have.
var ErrEmptyBundle = errors.New("no commits to bundle")

// HeadCommit is the full hash of HEAD in the worktree at path.
func HeadCommit(path string) (string, error) {
	out, err := run(path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CurrentBranch is the branch HEAD points at in the worktree at path, read
// from git now. It is "" when HEAD is detached. A session records the branch
// it was made on, and the agent in it can switch branch or detach since, so
// anything that carries the worktree's work reads this instead.
func CurrentBranch(path string) (string, error) {
	out, err := run(path, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		// symbolic-ref -q exits 1 with no output on a detached HEAD. Any
		// other failure is a real one, so HEAD is read to tell them apart.
		if _, herr := run(path, "rev-parse", "--verify", "-q", "HEAD"); herr == nil {
			return "", nil
		}
		return "", err
	}
	ref := strings.TrimSpace(out)
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return "", nil
	}
	return branch, nil
}

// DeleteBranch removes branch from the repository at dir, whatever it holds.
// It is for undoing a branch this process just made. The branch must pass
// ValidBranch, which refuses a leading hyphen.
func DeleteBranch(dir, branch string) error {
	if err := ValidBranch(branch); err != nil {
		return err
	}
	_, err := run(dir, "branch", "-D", branch)
	return err
}

// MergeBase is the full hash of the best common ancestor of a and b.
func MergeBase(path, a, b string) (string, error) {
	out, err := run(path, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// HasCommit reports whether the repository at dir holds the commit sha.
func HasCommit(dir, sha string) bool {
	if sha == "" || strings.HasPrefix(sha, "-") {
		return false
	}
	_, err := run(dir, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// Bundle writes a git bundle of branch to dest. With exclude, the bundle
// holds only the commits exclude does not reach, and the receiver must have
// exclude. A branch with no such commits is ErrEmptyBundle and writes nothing.
func Bundle(path, branch, exclude, dest string) error {
	args := []string{"bundle", "create", "--quiet", dest, "refs/heads/" + branch}
	if exclude != "" {
		args = append(args, "^"+exclude)
	}
	if _, err := run(path, args...); err != nil {
		if strings.Contains(err.Error(), "empty bundle") {
			return ErrEmptyBundle
		}
		return err
	}
	return nil
}

// WorkingPatch writes the worktree's uncommitted work against HEAD to dest as
// a binary patch: changes to tracked files, staged or not, and untracked
// files that are not ignored. It reports how many paths the patch touches.
//
// The patch is read through a temporary index, so the worktree's own index
// is not touched: what the agent staged stays staged, and nothing is added.
func WorkingPatch(path, dest string) (int, error) {
	tmp, err := os.CreateTemp("", "tuios-index-*")
	if err != nil {
		return 0, err
	}
	index := tmp.Name()
	_ = tmp.Close()
	// git refuses an empty file as an index, so it starts from nothing.
	_ = os.Remove(index)
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	if _, err := runEnv(path, env, "read-tree", "HEAD"); err != nil {
		return 0, err
	}
	if _, err := runEnv(path, env, "add", "--all", "--", "."); err != nil {
		return 0, err
	}
	names, err := runEnv(path, env, "diff", "--cached", "--name-only", "HEAD")
	if err != nil {
		return 0, err
	}
	count := 0
	for line := range strings.SplitSeq(names, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	patch, err := runEnv(path, env, "diff", "--cached", "--binary", "--no-color", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/", "HEAD")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(dest, []byte(patch), 0o600); err != nil {
		return 0, err
	}
	return count, nil
}

// FetchBundle fetches branch src from the bundle file into a new local branch
// dst of the repository at repoRoot.
func FetchBundle(repoRoot, bundle, src, dst string) error {
	abs, err := filepath.Abs(bundle)
	if err != nil {
		return err
	}
	_, err = run(repoRoot, "fetch", "--quiet", "--no-tags", abs, "refs/heads/"+src+":refs/heads/"+dst)
	return err
}

// BranchCommit is the full hash branch points at in the repository at dir.
func BranchCommit(dir, branch string) (string, error) {
	out, err := run(dir, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CreateBranch makes branch at the commit sha in the repository at dir. The
// branch must pass ValidBranch, which refuses a leading hyphen.
func CreateBranch(dir, branch, sha string) error {
	if err := ValidBranch(branch); err != nil {
		return err
	}
	_, err := run(dir, "branch", branch, sha)
	return err
}

// ApplyPatch applies a patch WorkingPatch wrote to the worktree at path,
// leaving the changes uncommitted and unstaged, as they were on the sender.
func ApplyPatch(path, patch string) error {
	abs, err := filepath.Abs(patch)
	if err != nil {
		return err
	}
	_, err = run(path, "apply", "--binary", "--whitespace=nowarn", abs)
	return err
}
