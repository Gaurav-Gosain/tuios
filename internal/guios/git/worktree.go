package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Linked worktrees of a session's own (plans/git.md, Phase 3): what kero
// writes to a repository besides a revert, adding the worktree and its
// branch as a session starts in it, and removing them as it closes.

// Main returns the repository's main checkout, "" for a bare repository's
// or a submodule's worktree, whose git directory is no .git of one.
func (r Repo) Main() string {
	if filepath.Base(r.Common) != ".git" {
		return ""
	}
	return filepath.Dir(r.Common)
}

// CheckBranch reports whether name may name a new branch.
func CheckBranch(ctx context.Context, dir, name string) error {
	if name == "" || strings.HasPrefix(name, "-") {
		return errors.New("a branch needs a name")
	}
	if _, err := run(ctx, dir, nil, "check-ref-format", "--branch", name); err != nil {
		return errors.New("“" + name + "” can't name a branch")
	}
	return nil
}

// HasBranch reports whether the repository has a local branch name.
func (r Repo) HasBranch(name string) bool { return r.hasRef("refs/heads/" + name) }

// AddWorktree checks out a new branch from base in a linked worktree at
// path; an existing branch, with base "", as it is.
func (r Repo) AddWorktree(ctx context.Context, path, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add"}
	if base != "" {
		args = append(args, "-b", branch, "--", path, base)
	} else {
		args = append(args, "--", path, branch)
	}
	_, err := run(ctx, r.Root, nil, args...)
	return err
}

// Exclude adds a pattern to the repository's info/exclude, which ignores
// it as .gitignore would, without a change of the repository's own.
func (r Repo) Exclude(pattern string) error {
	path := filepath.Join(r.Common, "info", "exclude")
	b, _ := os.ReadFile(path)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == pattern {
			return nil
		}
	}
	if len(b) > 0 && b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	b = append(b, pattern+"\n"...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// WorktreeState is what removing a linked worktree would lose.
type WorktreeState struct {
	// Files changed, untracked ones too, which removing discards.
	Files int
	// Unpushed are the commits of its branch on neither base nor any
	// remote's branch, which deleting the branch would lose; Merged, that
	// base has every commit of the branch.
	Unpushed int
	Merged   bool
}

// State returns what removing the worktree, a linked one on branch from
// base, would lose.
func (r Repo) State(ctx context.Context, branch, base string) (WorktreeState, error) {
	st, err := r.Status(ctx)
	if err != nil {
		return WorktreeState{}, err
	}
	ws := WorktreeState{Files: st.Files}
	if branch == "" || !r.HasBranch(branch) {
		return ws, nil
	}
	ref := "refs/heads/" + branch
	args := []string{"rev-list", "--count", ref, "--not", "--remotes"}
	if base != "" {
		args = append(args, base)
	}
	out, err := run(ctx, r.Root, nil, args...)
	if err != nil {
		return WorktreeState{}, err
	}
	ws.Unpushed, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	if base != "" {
		_, err := run(ctx, r.Root, nil, "merge-base", "--is-ancestor", ref, base)
		ws.Merged = err == nil
	}
	return ws, nil
}

// RemoveWorktree removes the linked worktree at path, what it holds
// included, from the repository whose main checkout is main; then its
// branch, when base has every commit of it.
func RemoveWorktree(ctx context.Context, main, path, branch, base string) error {
	if _, err := run(ctx, main, nil, "worktree", "remove", "--force", "--", path); err != nil {
		return err
	}
	if branch == "" || base == "" {
		return nil
	}
	ref := "refs/heads/" + branch
	if _, err := run(ctx, main, nil, "merge-base", "--is-ancestor", ref, base); err != nil {
		return nil // not merged: the branch stays, with its commits
	}
	_, err := run(ctx, main, nil, "branch", "-D", "--", branch)
	return err
}
