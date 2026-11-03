package tuie2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestRailGitJJ: the rail's git section, on a pane in a colocated jj
// workspace, names the stack by its bookmark. jj checks out no branch, so
// git's HEAD there is a bare hash that names nothing. The stack is three
// commits on trunk with the bookmark feat on the first, and an empty
// working-copy commit on top, as jj commit leaves it. The branch row reads
// feat, the two commits above feat that a push of it leaves behind (+2),
// and the three commits ahead of trunk() (↑3). The empty commit is not
// counted.
//
// The bookmark was pushed and then rewritten, so it is ahead of its remote.
// The row still names it feat, with no marker in the name.
//
// The test fails on a build that reads the workspace through git: the
// branch row is missing or names no bookmark. It also fails on a build that
// keeps jj's markers in the name: the row shows feat*.
func TestRailGitJJ(t *testing.T) {
	repo := testutil.JJRepo(t, true)
	for _, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.JJ(t, repo, "commit", "-m", name)
		if name == "one" {
			testutil.JJ(t, repo, "bookmark", "create", "feat", "-r", "@-")
		}
	}
	// Pushed, then rewritten: feat is ahead of feat@origin, which jj marks
	// "feat*" in its own log. The rail shows the name only.
	testutil.JJ(t, repo, "git", "push", "-b", "feat")
	testutil.JJ(t, repo, "describe", "-r", "feat", "-m", "one, reworded")
	term, _ := railClient(t, "gitjj", railGitReftableConfig, startOpts{cols: 120, rows: 40})

	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+repo+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)

	gitRows := func(s tuitest.Screen) (repoRow, branch string) {
		header := railHeaderRow(s, "git")
		if header < 0 {
			return "", ""
		}
		return railRowText(s, header+1), railRowText(s, header+2)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		repoRow, branch := gitRows(s)
		f := strings.Fields(branch)
		return slices.Contains(strings.Fields(repoRow), "repo") &&
			slices.Contains(f, "feat") && slices.Contains(f, "+2") && slices.Contains(f, "↑3")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the git section never showed repo, and feat with +2 above it and ↑3 ahead of trunk(): %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-git-jj")
	alive(t, term, "after reading a jj workspace")
}

// TestRailGitJJProblems: jj lets a divergent or a conflicted commit stand
// without a word, so the repository row of the rail's git section says so.
// The stack first holds a divergent commit, made by two descriptions of one
// commit from the same operation: the row shows ◇. Then the working copy
// moves onto a commit that conflicts: the row shows ✕1, because a conflict
// outranks a divergence.
//
// Negative control: with problemFigure's call cut from gitRows, the row
// shows neither figure.
func TestRailGitJJProblems(t *testing.T) {
	repo := testutil.JJRepo(t, true)
	if err := os.WriteFile(filepath.Join(repo, "one"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, repo, "commit", "-m", "one")
	testutil.JJ(t, repo, "bookmark", "create", "feat", "-r", "@-")
	op := testutil.JJ(t, repo, "op", "log", "--no-graph", "--limit", "1", "-T", "id")
	testutil.JJ(t, repo, "describe", "-r", "feat", "-m", "one, as the person wrote it")
	testutil.JJ(t, repo, "--at-op", op, "--ignore-working-copy", "describe", "-r", "feat", "-m", "one, as the agent wrote it")
	if out := testutil.JJ(t, repo, "log", "--no-graph", "-r", "trunk()..@ & divergent()", "-T", "change_id"); out == "" {
		t.Fatal("precondition: the stack holds no divergent commit")
	}
	term, _ := railClient(t, "gitjjp", railGitReftableConfig, startOpts{cols: 120, rows: 40})
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+repo+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	leaveTerminalMode(t, term)

	repoRow := func(s tuitest.Screen) string {
		if header := railHeaderRow(s, "git"); header >= 0 {
			return railRowText(s, header+1)
		}
		return ""
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(repoRow(s), "◇") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the repository row never showed the divergent commit (◇): %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-git-jj-divergent")

	// Two edits of README from main, the second rebased onto the first.
	testutil.JJ(t, repo, "new", "main", "-m", "x")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := testutil.JJ(t, repo, "log", "--no-graph", "-r", "@", "-T", "change_id")
	testutil.JJ(t, repo, "new", "main", "-m", "y")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, repo, "rebase", "-r", "@", "-d", first)
	if out := testutil.JJ(t, repo, "log", "--no-graph", "-r", "trunk()..@ & conflicts()", "-T", "change_id"); out == "" {
		t.Fatal("precondition: the stack holds no conflicted commit")
	}
	// The section reads again on its refresh, after the pane prints.
	enterTerminalMode(t, term)
	runInShell(t, term, `sleep 3 && echo mo""ved`, "moved", uiTimeout)
	leaveTerminalMode(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return slices.Contains(strings.Fields(repoRow(s)), "✕1") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the repository row never showed the conflicted commit (✕1): %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "rail-git-jj-conflict")
	alive(t, term, "after reading a jj stack with problems")
}

// TestRailJJRepoRow: in a jj repository, colocated or not, a session in the
// main workspace is the repository's row, and a session in a second
// workspace hangs off it. Each row names its stack by bookmark: main and
// feat, never git's detached HEAD. Folding the group hides the second
// workspace's row, and the folded row counts it.
//
// The test fails on a build that finds workspaces through git alone: a
// workspace jj added has no ".git", so its session gets no record, and the
// main workspace's row reads git's HEAD: a bare hash when colocated, nothing
// when not.
func TestRailJJRepoRow(t *testing.T) {
	for _, colocate := range []bool{false, true} {
		name := "plain"
		if colocate {
			name = "colocated"
		}
		t.Run(name, func(t *testing.T) { railJJRepoRow(t, colocate, name) })
	}
}

func railJJRepoRow(t *testing.T, colocate bool, label string) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.JJRepo(t, colocate)
	ws := filepath.Join(filepath.Dir(repo), "ws-feat")
	testutil.JJ(t, repo, "workspace", "add", "--name", "feat", ws)
	if err := os.WriteFile(filepath.Join(ws, "feature"), []byte("f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, ws, "commit", "-m", "feature")
	testutil.JJ(t, ws, "bookmark", "create", "feat", "-r", "@-")
	writeConfig(t, base, `[appearance.sidebar]
enabled = true
width = 30
`)
	if out, err := tuiosCLIIn(t, base, t.TempDir(), "new", "plain", "--detach"); err != nil {
		t.Fatalf("create the plain session: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, ws, "new", "featsess", "--detach"); err != nil {
		t.Fatalf("create the second workspace's session: %v: %s", err, out)
	}
	if out, err := tuiosCLIIn(t, base, repo, "new", "mainsess", "--detach"); err != nil {
		t.Fatalf("create the main workspace's session: %v: %s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "plain"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		head, child := railLineWith(s, "mainsess main"), railLineWith(s, "└─ featsess feat")
		return head >= 0 && child == head+1
	}, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the rows are not \"mainsess main\" with \"featsess feat\" under it: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "rail-jj-repo-row-"+label)

	col, row := railCellColumn(term.Screen(), "▾"), railLineWith(term.Screen(), "▾")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		line := railLineWith(s, "mainsess")
		return railLineWith(s, "feat") < 0 && line >= 0 && strings.Contains(s.Line(line), "+1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the fold mark did not fold the group to +1: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "rail-jj-repo-row-folded-"+label)
	alive(t, term, "after folding the jj repository's row")
}
