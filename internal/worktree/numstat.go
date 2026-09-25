package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Comparing the attempts of a fan: how much each one changed against its
// base, and what two of them did differently.
//
// Both read a worktree's working state, committed or not, untracked files
// included and ignored files left out, as one git tree. The tree is written
// through a temporary index, so the worktree's own index and files are as they
// were: what the agent staged stays staged and nothing is added. The objects
// the tree needs are written to the repository's object store as loose
// objects, which git gc collects like any other unreferenced object.

// Numstat is how much a worktree changed against a base: the files that
// differ and the lines added and removed in them. A binary file counts as a
// file with no lines.
type Numstat struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// SnapshotTree writes the working state of the worktree at path as a git tree
// and returns its hash.
//
// The temporary index starts as a copy of the worktree's own, so git's stat
// cache holds and only the files that changed since the agent last touched
// the index are hashed again. A worktree with no index, or one git cannot use
// from a copy (a split index keeps part of itself beside the original), starts
// from HEAD instead, which hashes every tracked file.
func SnapshotTree(ctx context.Context, path string) (string, error) {
	tmp, err := os.CreateTemp("", "tuios-index-*")
	if err != nil {
		return "", err
	}
	index := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	if copyIndex(ctx, path, index) {
		if tree, err := addAndWriteTree(ctx, path, env); err == nil {
			return tree, nil
		} else if ctx.Err() != nil {
			return "", err
		}
	}
	// git refuses an empty file as an index, so it starts from nothing.
	_ = os.Remove(index)
	// A repository with no commit yet has no HEAD to start from, and every
	// file is new, so the index starts empty.
	if _, err := runCtx(ctx, path, env, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		if _, err := runCtx(ctx, path, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	return addAndWriteTree(ctx, path, env)
}

// copyIndex copies the index of the worktree at path over dest, and reports
// whether it did. The worktree's index is only read.
func copyIndex(ctx context.Context, path, dest string) bool {
	out, err := runCtx(ctx, path, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return false
	}
	src := strings.TrimSpace(out)
	if src == "" {
		return false
	}
	if !filepath.IsAbs(src) {
		src = filepath.Join(path, src)
	}
	info, err := os.Stat(src)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(src)
	if err != nil || len(data) == 0 {
		return false
	}
	if os.WriteFile(dest, data, 0o600) != nil {
		return false
	}
	// git trusts an entry's stat data only when the file is older than the
	// index. The copy keeps the original's time, so a file changed just after
	// the agent's last git call is still hashed again.
	_ = os.Chtimes(dest, info.ModTime(), info.ModTime())
	return true
}

// addAndWriteTree stages every file of the worktree at path into the index env
// names and writes it as a tree.
func addAndWriteTree(ctx context.Context, path string, env []string) (string, error) {
	if _, err := runCtx(ctx, path, env, "add", "--all", "--", "."); err != nil {
		return "", err
	}
	out, err := runCtx(ctx, path, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// WorkingNumstat counts what the worktree at path changed against base, a
// commit or ref: its commits and its uncommitted work together.
func WorkingNumstat(ctx context.Context, path, base string) (Numstat, error) {
	if strings.HasPrefix(base, "-") {
		return Numstat{}, fmt.Errorf("base %q is not a ref", base)
	}
	tree, err := SnapshotTree(ctx, path)
	if err != nil {
		return Numstat{}, err
	}
	out, err := runCtx(ctx, path, nil, "diff", "--numstat", "--no-color", "--no-ext-diff", base, tree, "--")
	if err != nil {
		return Numstat{}, err
	}
	return ParseNumstat(out), nil
}

// ParseNumstat reads the output of git diff --numstat: one line per file,
// added and removed counts first, "-" for both on a binary file.
func ParseNumstat(out string) Numstat {
	var n Numstat
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(fields) < 3 {
			continue
		}
		n.Files++
		if a, err := strconv.Atoi(fields[0]); err == nil {
			n.Added += a
		}
		if r, err := strconv.Atoi(fields[1]); err == nil {
			n.Removed += r
		}
	}
	return n
}

// DiffWorking is what the worktree at b has that the worktree at a does not:
// a unified diff of their working states, from a to b. Both must be worktrees
// of one repository, which share its object store. With stat it is the
// summary form.
func DiffWorking(ctx context.Context, a, b string, stat bool) (string, error) {
	ta, err := SnapshotTree(ctx, a)
	if err != nil {
		return "", err
	}
	tb, err := SnapshotTree(ctx, b)
	if err != nil {
		return "", err
	}
	args := []string{"diff", "--no-color", "--no-ext-diff"}
	if stat {
		args = append(args, "--stat")
	}
	args = append(args, ta, tb, "--")
	return runCtx(ctx, a, nil, args...)
}

// HeadCommitCtx is HeadCommit bounded by ctx.
func HeadCommitCtx(ctx context.Context, path string) (string, error) {
	out, err := runCtx(ctx, path, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// MergeBaseCtx is MergeBase bounded by ctx.
func MergeBaseCtx(ctx context.Context, path, a, b string) (string, error) {
	if strings.HasPrefix(a, "-") || strings.HasPrefix(b, "-") {
		return "", fmt.Errorf("merge-base of %q and %q: not a ref", a, b)
	}
	out, err := runCtx(ctx, path, nil, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// AheadCtx is Ahead bounded by ctx.
func AheadCtx(ctx context.Context, path, base string) (int, error) {
	out, err := runCtx(ctx, path, nil, "rev-list", "--count", base+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// ChangesCtx is Changes bounded by ctx.
func ChangesCtx(ctx context.Context, path string) (int, error) {
	out, err := runCtx(ctx, path, nil, "status", "--porcelain", "--untracked-files=all")
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

// runCtx is runEnv bounded by ctx: a git that takes longer than the caller
// can wait is killed.
func runCtx(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	cmd.Env = append(cmd.Env, env...)
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
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return stdout.String(), nil
}
