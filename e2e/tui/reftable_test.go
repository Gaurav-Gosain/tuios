package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// Repositories that keep their refs in reftable, end to end. git writes the
// placeholder "ref: refs/heads/.invalid" to HEAD in such a repository, so a
// reader of the file sees a branch that does not exist. Large repositories
// are often set up this way: a bare repository with its worktrees beside it,
// and reftable for the ref count.

// reftablePlaceholder is what git writes to HEAD in a reftable repository.
const reftablePlaceholder = "ref: refs/heads/.invalid"

// reftableSeed is a throwaway repository with one commit on main, for the
// reftable repositories of a test to be cloned from. It skips the test when
// this git cannot make one: reftable needs git 2.45 or later.
func reftableSeed(t *testing.T) string {
	t.Helper()
	seed := testutil.GitRepo(t)
	probe := filepath.Join(t.TempDir(), "probe")
	if out, err := exec.Command("git", "init", "-q", "--ref-format=reftable", probe).CombinedOutput(); err != nil {
		t.Skipf("this git cannot make a reftable repository (git 2.45 or later can): %v: %s", err, out)
	}
	return seed
}

// requireReftableHead fails the test when the checkout at dir does not show
// the placeholder in HEAD, which would make the fixture an ordinary
// repository and the test a test of nothing new.
func requireReftableHead(t *testing.T, dir string) {
	t.Helper()
	gitdir := testutil.Git(t, dir, "rev-parse", "--absolute-git-dir")
	data, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil || strings.TrimSpace(string(data)) != reftablePlaceholder {
		t.Fatalf("the fixture at %s is not a reftable checkout: HEAD reads %q (%v)", dir, data, err)
	}
}

// reftableWorktreeRow is the part of a worktree ls --json row this file reads.
type reftableWorktreeRow struct {
	Session string `json:"session"`
	Repo    string `json:"repo"`
	Branch  string `json:"branch"`
}

// waitWorktreeRows polls worktree ls --json until every session in want is
// listed with the repository and branch given for it, and fails with the
// last listing when that does not happen in time.
func waitWorktreeRows(t *testing.T, base string, want map[string]reftableWorktreeRow) {
	t.Helper()
	deadline := time.Now().Add(bootTimeout)
	for {
		out, err := tuiosCLI(t, base, "worktree", "ls", "--json")
		var rows []reftableWorktreeRow
		if err == nil {
			err = json.Unmarshal([]byte(out), &rows)
		}
		got := map[string]reftableWorktreeRow{}
		for _, r := range rows {
			got[r.Session] = r
		}
		var wrong []string
		for session, w := range want {
			if g := got[session]; g.Repo != w.Repo || g.Branch != w.Branch {
				wrong = append(wrong, fmt.Sprintf("%s is %q on %q, want %q on %q",
					session, g.Repo, g.Branch, w.Repo, w.Branch))
			}
		}
		if err == nil && len(wrong) == 0 {
			return
		}
		if time.Now().After(deadline) {
			slices.Sort(wrong)
			t.Fatalf("ASSERTION: worktree ls: %s (%v)\n%s", strings.Join(wrong, "; "), err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestReftableWorktrees: a session in a linked worktree of a bare reftable
// repository is listed and grouped under the project's name and shows its
// real branch, for the three ways a bare repository is commonly kept beside
// its worktrees: <name>/.bare, <name>.git and <name>/git. A branch switched
// in a worktree is what the next session made there reports. A session in a
// main checkout with reftable refs is labelled by its branch.
//
// The test names its sessions itself, so each worktree row keeps that name
// and shows the branch after it (#1245). The rail is wide enough for both,
// since a row too narrow for its branch drops it.
//
// How it could pass wrongly, and what stops it: the fixture falling back to
// the files backend, where HEAD names the branch. Every worktree's HEAD must
// read the placeholder before a session is made in it. The branch switch
// checks that an answer kept between reads is let go when the refs move.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Reftable repositories").
func TestReftableWorktrees(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, reftableRailConfig)
	seed := reftableSeed(t)
	projects := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(projects); err == nil {
		projects = resolved
	}

	layouts := []struct{ repo, bare, tree, branch, session string }{
		{"alpha", "alpha/.bare", "alpha/trees/one", "feat/one", "alpha-one"},
		{"beta", "beta.git", "beta-two", "feat/two", "beta-two"},
		{"gamma", "gamma/git", "gamma/trees/three", "feat/three", "gamma-three"},
	}
	want := map[string]reftableWorktreeRow{}
	for _, l := range layouts {
		bare, tree := filepath.Join(projects, l.bare), filepath.Join(projects, l.tree)
		testutil.Git(t, projects, "clone", "-q", "--bare", "--ref-format=reftable", seed, bare)
		testutil.Git(t, bare, "worktree", "add", "-q", "-b", l.branch, tree)
		requireReftableHead(t, tree)
		want[l.session] = reftableWorktreeRow{Repo: l.repo, Branch: l.branch}
	}

	// A main checkout with reftable refs, whose session is not a worktree and
	// keeps its own row, labelled by its branch.
	checkout := filepath.Join(projects, "epsilon")
	testutil.Git(t, projects, "clone", "-q", "--ref-format=reftable", seed, checkout)
	testutil.Git(t, checkout, "switch", "-q", "-c", "trunk")
	requireReftableHead(t, checkout)

	// A session that is not a worktree, which starts the daemon and is the one
	// the client attaches to.
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, checkout, "new", "epsilon", "--detach"); err != nil {
		t.Fatalf("create the epsilon session: %v: %s", err, out)
	}
	for _, l := range layouts {
		if out, err := tuiosCLIIn(t, base, filepath.Join(projects, l.tree), "new", l.session, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", l.session, err, out)
		}
	}
	waitWorktreeRows(t, base, want)

	// The refs move under the daemon, and the next session made in that
	// worktree reports the branch it is on now.
	alpha := filepath.Join(projects, layouts[0].tree)
	testutil.Git(t, alpha, "switch", "-q", "-c", "feat/later")
	if out, err := tuiosCLIIn(t, base, alpha, "new", "alpha-later", "--detach"); err != nil {
		t.Fatalf("create alpha-later: %v: %s", err, out)
	}
	waitWorktreeRows(t, base, map[string]reftableWorktreeRow{
		"alpha-later": {Repo: "alpha", Branch: "feat/later"},
	})

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	toggleSidebarViaPalette(t, term)

	// Each project's row, and under it its worktree sessions, in order, each
	// with its own branch.
	groups := [][][]string{
		{{"·", "alpha"}, {"·", "├─", "alpha-one", "feat/one"}, {"·", "└─", "alpha-later", "feat/later"}},
		{{"·", "beta"}, {"·", "└─", "beta-two", "feat/two"}},
		{{"·", "gamma"}, {"·", "└─", "gamma-three", "feat/three"}},
	}
	grouped := func(s tuitest.Screen) bool {
		for _, g := range groups {
			top := reftableRailRow(s, g[0])
			if top < 0 {
				return false
			}
			for i, row := range g[1:] {
				if reftableRailRow(s, row) != top+1+i {
					return false
				}
			}
		}
		return railLineWith(s, "epsilon trunk") >= 0
	}
	if err := term.WaitFor(grouped, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never grouped the reftable worktrees by project and branch: %v\n%s", err, term.Snapshot())
	}
	text := term.Screen().Text()
	for _, wrong := range []string{".invalid", "· .bare", "· git", "· beta.git"} {
		if strings.Contains(text, wrong) {
			t.Errorf("ASSERTION: the rail shows %q\n%s", wrong, term.Snapshot())
		}
	}
	saveFrame(t, term, "rail-reftable-worktrees")
	alive(t, term, "after grouping the reftable worktrees")
}

// reftableRailConfig gives the rail room for a worktree row's name and its
// branch. The rail stays off until the test shows it.
const reftableRailConfig = `[appearance.sidebar]
width = 40
`

// reftableRailRow is the screen row whose rail text starts with the words in
// want, or -1. A word is matched whole, so "alpha" does not match
// "alpha-one".
func reftableRailRow(s tuitest.Screen, want []string) int {
	_, rows := s.Size()
	for r := range rows {
		rail, _, _ := strings.Cut(s.Line(r), "│")
		if f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(rail), "▎")); len(f) >= len(want) && slices.Equal(f[:len(want)], want) {
			return r
		}
	}
	return -1
}

// railGitReftableConfig is a rail with the sessions and git sections only.
const railGitReftableConfig = `[appearance.sidebar]
enabled = true
position = "left"
width = 28
sections = "sessions,git"
`

// TestRailGitReftable: the rail's git section, on a pane in a worktree of a
// bare reftable repository kept as <name>.git, names the project, names the
// branch, and shows the commit the branch is ahead of its upstream by.
//
// How it could pass wrongly, and what stops it: the fixture being an
// ordinary checkout, ruled out as in TestReftableWorktrees. The divergence
// can only be drawn when the branch was read from the worktree's reftable
// and its upstream's commit from the repository's.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md, "Reftable repositories").
func TestRailGitReftable(t *testing.T) {
	seed := reftableSeed(t)
	term, base := railClient(t, "gitreft", railGitReftableConfig, startOpts{cols: 120, rows: 40})
	bare, dir := filepath.Join(base, "delta.git"), filepath.Join(base, "delta-ahead")
	testutil.Git(t, base, "clone", "-q", "--bare", "--ref-format=reftable", seed, bare)
	testutil.Git(t, bare, "worktree", "add", "-q", "-b", "feat/ahead", dir)
	// The upstream is the repository's own main, which a bare clone has.
	testutil.Git(t, dir, "branch", "-q", "--set-upstream-to=main")
	if err := os.WriteFile(filepath.Join(dir, "NOTES"), []byte("ahead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "add", "NOTES")
	testutil.Git(t, dir, "commit", "-q", "-m", "ahead")
	requireReftableHead(t, dir)

	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)

	// The section's two rows under its header: the repository, then the
	// branch with its divergence.
	gitRows := func(s tuitest.Screen) (repo, branch string) {
		header := railHeaderRow(s, "git")
		if header < 0 {
			return "", ""
		}
		return railRowText(s, header+1), railRowText(s, header+2)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		repo, branch := gitRows(s)
		return slices.Contains(strings.Fields(repo), "delta") &&
			slices.Contains(strings.Fields(branch), "feat/ahead") && strings.Contains(branch, "↑1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the git section never showed delta, and feat/ahead one commit ahead: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); strings.Contains(text, ".invalid") {
		t.Errorf("ASSERTION: the rail shows the reftable placeholder as a branch\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-git-reftable")

	// The upstream moves and the worktree does not. Only the repository's
	// reftable changes, and the section must still see it. The move is made
	// from the pane, and the line printed after it comes later than the
	// section's refresh interval, so the client asks again after the move.
	moved := testutil.Git(t, dir, "commit-tree", "-p", "main", "-m", "upstream moved", "main^{tree}")
	enterTerminalMode(t, term)
	runInShell(t, term, "git update-ref refs/heads/main "+moved+` && sleep 3 && echo mo""ved`, "moved", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, branch := gitRows(s)
		return slices.Contains(strings.Fields(branch), "feat/ahead") && strings.Contains(branch, "↑1 ↓1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the git section never saw the upstream move: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-git-reftable-upstream-moved")
	alive(t, term, "after reading a reftable checkout")
}
