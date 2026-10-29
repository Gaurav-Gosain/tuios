package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
	"github.com/adrg/xdg"
)

// DefaultDir is where tuios puts the worktrees it creates when no path
// template is set: $XDG_DATA_HOME/tuios/worktrees, one directory per
// repository under it. TUIOS_WORKTREE_DIR in the daemon's environment moves it.
func DefaultDir() string {
	if override := os.Getenv("TUIOS_WORKTREE_DIR"); override != "" {
		return override
	}
	return filepath.Join(xdg.DataHome, "tuios", "worktrees")
}

// PathFor is the directory a worktree of repoRoot on branch gets. An empty
// template is the default layout, <DefaultDir>/<repo>/<branch slug>. Any other
// template is rendered by RenderPath.
func PathFor(template, repoRoot, branch string) (string, error) {
	if strings.TrimSpace(template) == "" {
		return filepath.Join(DefaultDir(), filepath.Base(repoRoot), Slug(branch)), nil
	}
	return RenderPath(template, repoRoot, branch)
}

// pathTag is one {{ name }} or {{ name | filter }} in a path template. The
// names follow worktrunk's worktree-path, so a layout made by one reads the
// same in the other.
var pathTag = lazyre.New(`\{\{\s*([A-Za-z_]+)\s*(?:\|\s*([A-Za-z_]+)\s*)?\}\}`)

// RenderPath fills a path template, the [worktree] path setting:
//
//	{{ repo_path }}  the main checkout's directory
//	{{ repo }}       its base name
//	{{ branch }}     the branch, as given
//
// "| slug" turns a value into one path component, as Slug does. "| sanitize"
// is another name for it, so a template copied from worktrunk renders. A
// leading ~/ is the home directory, and a relative result resolves from
// repo_path. The template has to use branch, or every branch would land in one
// directory.
func RenderPath(template, repoRoot, branch string) (string, error) {
	// The braces are checked in the template, before anything is filled in:
	// a branch such as a}}b, or a folder with {{ in its name, is a value and
	// not a mistake in the template.
	if rest := pathTag().ReplaceAllString(template, ""); strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return "", fmt.Errorf("%q has a {{ or }} that is not part of a {{ name }} or {{ name | slug }}", template)
	}
	var bad error
	usesBranch := false
	out := pathTag().ReplaceAllStringFunc(template, func(tag string) string {
		m := pathTag().FindStringSubmatch(tag)
		var v string
		switch m[1] {
		case "repo_path":
			v = repoRoot
		case "repo":
			v = filepath.Base(repoRoot)
		case "branch":
			v = branch
			usesBranch = true
		default:
			bad = errors.Join(bad, fmt.Errorf("unknown variable %q: use repo_path, repo or branch", m[1]))
			return ""
		}
		switch m[2] {
		case "":
		case "slug", "sanitize":
			v = Slug(v)
		default:
			bad = errors.Join(bad, fmt.Errorf("unknown filter %q: use slug, or sanitize, its worktrunk name", m[2]))
		}
		return v
	})
	if bad != nil {
		return "", bad
	}
	if !usesBranch {
		return "", fmt.Errorf("%q does not use {{ branch }}, so every branch would get the same directory", template)
	}
	if rest, ok := strings.CutPrefix(out, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		out = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(repoRoot, out)
	}
	return filepath.Clean(out), nil
}

// branchMark stands in for the branch when HoldingDir renders a template.
// Slug leaves it as it is.
const branchMark = "tuios0branch0mark"

// HoldingDir is the folder every branch's worktree goes under: the rendered
// path up to the first folder that depends on the branch. For
// ".claude/worktrees/{{ branch | slug }}" it is <repo>/.claude/worktrees.
func HoldingDir(template, repoRoot string) (string, error) {
	dir, err := PathFor(template, repoRoot, branchMark)
	if err != nil {
		return "", err
	}
	for strings.Contains(dir, branchMark) {
		dir = filepath.Dir(dir)
	}
	return dir, nil
}

// ValidatePathTemplate reports what is wrong with a [worktree] path template,
// or nil for one RenderPath can fill. An empty template is the default layout.
func ValidatePathTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return nil
	}
	_, err := RenderPath(template, filepath.FromSlash("/repo"), "feat/x")
	return err
}

// UnignoredInRepo reports whether path lies inside repoRoot where git does not
// ignore it. The main checkout then lists the worktree as untracked, and a
// git add -A there would take it in. tuios only says so: the ignore files are
// the user's to edit.
func UnignoredInRepo(repoRoot, path string) bool {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// check-ignore exits 0 for an ignored path and 1 for one that is not.
	// Any other failure says nothing either way, and nothing is reported.
	err = childproc.Git(context.Background(), repoRoot, "check-ignore", "-q", "--", rel).Run()
	var exit interface{ ExitCode() int }
	return errors.As(err, &exit) && exit.ExitCode() == 1
}
