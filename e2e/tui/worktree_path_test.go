package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// [worktree] path, end to end through the binary a person runs (issue #1278):
// a throwaway repository, a config that keeps worktrees in the repository's
// own .worktrees folder, and the daemon the CLI starts. TUIOS_WORKTREE_DIR is
// set as well, so a worktree that lands there is the old layout winning.
//
// Each step's output, git status and git worktree list go to
// worktree-path.txt in the artifact directory.
//
// Negative controls: with worktreePathFor in internal/session/verb_worktree.go
// passing "" as the template, feat/a lands under TUIOS_WORKTREE_DIR and this
// fails at the first path check. With UnignoredInRepo returning false, it
// fails at "no warning for a worktree git does not ignore". With the brace
// check run on the rendered path, x}}y is refused. With the hint naming
// the first folder under the repository, it names /.claude/. With sanitize
// unknown, the .claude/worktrees template is refused.
func TestWorktreePathTemplate(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	defaultDir := filepath.Join(base, "worktrees")
	t.Setenv("TUIOS_WORKTREE_DIR", defaultDir)
	writeConfig(t, base, "[worktree]\npath = \"{{ repo_path }}/.worktrees/{{ branch | slug }}\"\n")
	// The test's own git reads the same ignore files the daemon's does: a
	// .worktrees/ in the person's ~/.config/git/ignore would hide the
	// untracked folder from git status here and not from the daemon.
	t.Setenv("XDG_CONFIG_HOME", xdgDir(base, "XDG_CONFIG_HOME"))
	var log strings.Builder
	record := func(step, out string) {
		fmt.Fprintf(&log, "== %s\n%s\n", step, out)
	}
	t.Cleanup(func() {
		status, _ := exec.Command("git", "-C", repo, "status", "--porcelain").CombinedOutput()
		list, _ := exec.Command("git", "-C", repo, "worktree", "list").CombinedOutput()
		record("git status --porcelain", string(status))
		record("git worktree list", string(list))
		_ = os.WriteFile(filepath.Join(artifactDir(t), "worktree-path.txt"), []byte(log.String()), 0o644)
	})

	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}

	// Inside the repository and not ignored: made where the template says,
	// with a warning that names what to ignore.
	out, err := tuiosCLI(t, base, "worktree", "new", "feat/a", "--repo", repo, "--detach")
	record("worktree new feat/a", out)
	if err != nil {
		t.Fatalf("worktree new feat/a: %v: %s", err, out)
	}
	pathA := filepath.Join(repo, ".worktrees", "feat-a")
	if !strings.Contains(out, "in "+pathA+".") {
		t.Fatalf("worktree new did not put feat/a in %s:\n%s", pathA, out)
	}
	if branch := strings.TrimSpace(gitOut(t, pathA, "branch", "--show-current")); branch != "feat/a" {
		t.Fatalf("%s is on %q, want feat/a", pathA, branch)
	}
	if _, err := os.Stat(filepath.Join(defaultDir, "repo", "feat-a")); err == nil {
		t.Fatalf("feat/a was also made under TUIOS_WORKTREE_DIR, which the template should win over")
	}
	if !strings.Contains(out, "Warning: ") || !strings.Contains(out, "Add /.worktrees/ to .gitignore or .git/info/exclude") {
		t.Fatalf("no warning for a worktree git does not ignore:\n%s", out)
	}
	if status := gitOut(t, repo, "status", "--porcelain"); !strings.Contains(status, "?? .worktrees/") {
		t.Fatalf("git status in the main checkout = %q, want .worktrees/ untracked, the case the warning is for", status)
	}
	for _, f := range []string{".gitignore", filepath.Join(".git", "info", "exclude")} {
		data, _ := os.ReadFile(filepath.Join(repo, f))
		if strings.Contains(string(data), ".worktrees") {
			t.Fatalf("tuios wrote .worktrees into %s; the ignore files are the user's:\n%s", f, data)
		}
	}

	// Ignored by the person: no warning.
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("/.worktrees/\n")
	_ = f.Close()
	out, err = tuiosCLI(t, base, "worktree", "new", "feat/b", "--repo", repo, "--detach")
	record("worktree new feat/b, .worktrees ignored", out)
	if err != nil {
		t.Fatalf("worktree new feat/b: %v: %s", err, out)
	}
	if !strings.Contains(out, "in "+filepath.Join(repo, ".worktrees", "feat-b")+".") {
		t.Fatalf("worktree new did not put feat/b under .worktrees:\n%s", out)
	}
	if strings.Contains(out, "Warning") {
		t.Fatalf("a warning for a worktree git ignores:\n%s", out)
	}

	// A relative template, applied to the running daemon. It starts at the
	// main checkout, and {{ repo }} is its base name.
	writeConfig(t, base, "[worktree]\npath = \".trees/{{ repo }}-{{ branch | slug }}\"\n")
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("config apply: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "worktree", "new", "feat/c", "--repo", repo, "--detach")
	record("worktree new feat/c, relative template", out)
	if err != nil {
		t.Fatalf("worktree new feat/c: %v: %s", err, out)
	}
	if !strings.Contains(out, "in "+filepath.Join(repo, ".trees", "repo-feat-c")+".") {
		t.Fatalf("the relative template did not start at the main checkout:\n%s", out)
	}

	// worktrunk's name for the filter, and a layout under a folder people
	// commit files in: the hint names the folder that holds the worktrees,
	// not .claude.
	writeConfig(t, base, "[worktree]\npath = \".claude/worktrees/{{ branch | sanitize }}\"\n")
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("config apply: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "worktree", "new", "feat/e", "--repo", repo, "--detach")
	record("worktree new feat/e, .claude/worktrees with sanitize", out)
	if err != nil {
		t.Fatalf("worktree new feat/e: %v: %s", err, out)
	}
	if !strings.Contains(out, "in "+filepath.Join(repo, ".claude", "worktrees", "feat-e")+".") {
		t.Fatalf("worktree new did not put feat/e in .claude/worktrees/feat-e:\n%s", out)
	}
	if !strings.Contains(out, "Add /.claude/worktrees/ to ") {
		t.Fatalf("the hint does not name .claude/worktrees, the folder that holds the worktrees:\n%s", out)
	}

	// A branch with }} in its name, which git takes, under a template that
	// uses the branch as it is. The braces are the branch's, not a mistake in
	// the template.
	writeConfig(t, base, "[worktree]\npath = \".worktrees/{{ branch }}\"\n")
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("config apply: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "worktree", "new", "x}}y", "--repo", repo, "--detach")
	record("worktree new x}}y, raw branch", out)
	if err != nil {
		t.Fatalf("worktree new x}}y: %v: %s", err, out)
	}
	if !strings.Contains(out, "in "+filepath.Join(repo, ".worktrees", "x}}y")+".") {
		t.Fatalf("worktree new did not put x}}y in .worktrees/x}}y:\n%s", out)
	}

	// A stray }} in the template itself is still a mistake in it.
	writeConfig(t, base, "[worktree]\npath = \".worktrees/{{ branch }}}}\"\n")
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("config apply: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "worktree", "new", "feat/f", "--repo", repo, "--detach")
	record("worktree new feat/f, stray }} in the template", out)
	if err == nil || !strings.Contains(out, "has a {{ or }} that is not part of") {
		t.Fatalf("a stray }} in the template was not refused: %v:\n%s", err, out)
	}

	// A template without {{ branch }} would give every branch one folder:
	// refused, and nothing made.
	writeConfig(t, base, "[worktree]\npath = \"{{ repo_path }}/.worktrees/fixed\"\n")
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("config apply: %v: %s", err, out)
	}
	out, err = tuiosCLI(t, base, "worktree", "new", "feat/d", "--repo", repo, "--detach")
	record("worktree new feat/d, template without branch", out)
	if err == nil || !strings.Contains(out, "[worktree] path") || !strings.Contains(out, "{{ branch }}") {
		t.Fatalf("a template without {{ branch }} was not refused by name: %v:\n%s", err, out)
	}
	if exec.Command("git", "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/feat/d").Run() == nil {
		t.Fatalf("the refused create still made branch feat/d")
	}
}

// TestFanUsesWorktreePathTemplate is the fan's half: every worktree of a fan
// goes where [worktree] path says, and the name it skips as taken is the one
// on that path.
//
// Negative control: with verbFan passing "" to fanBranches, the fan skips
// no name, and this fails when it is refused because .worktrees/try-retry
// already exists.
func TestFanUsesWorktreePathTemplate(t *testing.T) {
	base, repo := fanFixture(t)
	writeConfig(t, base, "[worktree]\npath = \"{{ repo_path }}/.worktrees/{{ branch | slug }}\"\n")
	// A folder in the way of the first name: the fan takes the next two.
	if err := os.MkdirAll(filepath.Join(repo, ".worktrees", "try-retry"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/retry", "Add a retry to the client.")
	if err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	_ = os.WriteFile(filepath.Join(artifactDir(t), "fan.txt"), []byte(out), 0o644)
	for _, want := range []string{
		filepath.Join(repo, ".worktrees", "try-retry-2"),
		filepath.Join(repo, ".worktrees", "try-retry-3"),
		"Warning: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("fan output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "Warning: "); n != 1 {
		t.Errorf("fan said the warning %d times, want once:\n%s", n, out)
	}
}
