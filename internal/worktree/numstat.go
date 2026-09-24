package worktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
func SnapshotTree(ctx context.Context, path string) (string, error) {
	tmp, err := os.CreateTemp("", "tuios-index-*")
	if err != nil {
		return "", err
	}
	index := tmp.Name()
	_ = tmp.Close()
	// git refuses an empty file as an index, so it starts from nothing.
	_ = os.Remove(index)
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	// A repository with no commit yet has no HEAD to start from, and every
	// file is new, so the index starts empty.
	if _, err := runCtx(ctx, path, env, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err == nil {
		if _, err := runCtx(ctx, path, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
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
	for _, line := range strings.Split(out, "\n") {
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
