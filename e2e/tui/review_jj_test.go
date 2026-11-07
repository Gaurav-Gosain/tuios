package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestReviewJJStack runs tuios review on a session whose pane is in a jj
// workspace with no ".git" and sparse patterns: git alone cannot find the
// repository, and git's index, where there is one, holds every path outside
// the patterns as if deleted. With no base named, the review covers the whole
// stack from where it left trunk(): the committed work and the working-copy
// commit, and nothing the patterns hide. A bookmark names a base too. The
// index modes refuse, because jj has no staging area.
//
// The test fails on a build that reviews through git alone: it finds no
// repository under the pane.
//
// Negative control: with the jj check cut from review.IndexOptions, both
// index modes answer with a diff of jj's exported index.
func TestReviewJJStack(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.JJRepo(t, false)
	write := func(name, body string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/a", "a\n")
	write("other/hidden", "x\n")
	testutil.JJ(t, repo, "commit", "-m", "trunk files")
	testutil.JJ(t, repo, "bookmark", "set", "main", "-r", "@-")
	testutil.JJ(t, repo, "git", "push", "-b", "main")
	testutil.JJ(t, repo, "sparse", "set", "--clear", "--add", "src")
	if _, err := os.Stat(filepath.Join(repo, "other/hidden")); !os.IsNotExist(err) {
		t.Fatal("precondition: the sparse patterns left other/hidden on disk")
	}
	write("src/committed", "c\n")
	testutil.JJ(t, repo, "commit", "-m", "committed")
	write("src/a", "a\nchanged\n")

	if out, err := tuiosCLIInDir(t, base, repo, []string{"PWD=" + repo}, "new", "jjrev", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "review", "jjrev")
	if err != nil {
		t.Fatalf("ASSERTION: review: %v: %s", err, out)
	}
	saveText(t, "review-jj-stack", out)
	for _, want := range []string{"against trunk()", "2 files", "A  src/committed", "M  src/a", "+ changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("ASSERTION: tuios review lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "other/hidden") {
		t.Errorf("ASSERTION: tuios review shows a path the sparse patterns hide:\n%s", out)
	}

	out, err = tuiosCLI(t, base, "review", "jjrev", "--base", "main@origin")
	if err != nil || !strings.Contains(out, "A  src/committed") {
		t.Errorf("ASSERTION: review against a remote bookmark: %v:\n%s", err, out)
	}

	// jj has no staging area, and git's index there is jj's export: the
	// index modes refuse and say why, and the plain review still works.
	for _, mode := range []string{"unstaged", "staged"} {
		out, err = tuiosCLI(t, base, "call", "review-diff", `{"session":"jjrev","index":"`+mode+`"}`)
		if err == nil || !strings.Contains(out, "no staging area") {
			t.Errorf("ASSERTION: review-diff with index %s in a jj workspace = %v:\n%s\nwant a refusal that names the staging area", mode, err, out)
		}
	}
	if out, err = tuiosCLI(t, base, "call", "review-diff", `{"session":"jjrev"}`); err != nil {
		t.Errorf("review-diff without index after the refusals: %v:\n%s", err, out)
	}
}

// TestReviewJJConflict reviews a jj workspace whose working-copy commit has a
// conflict. jj stores that commit's git tree with a .jjconflict-* folder for
// each side and a JJ-CONFLICT-README, none of which is on disk; the file on
// disk holds the conflict markers. The review shows the file as it is on
// disk.
//
// Negative control: with SnapshotTree taking jj's tree as it is (no
// jjMaterialize), the review lists .jjconflict-base-0/README and
// JJ-CONFLICT-README, and README without markers.
func TestReviewJJConflict(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.JJRepo(t, false)
	readme := filepath.Join(repo, "README")
	testutil.JJ(t, repo, "new", "main", "-m", "x")
	if err := os.WriteFile(readme, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, repo, "new", "main", "-m", "y")
	if err := os.WriteFile(readme, []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.JJ(t, repo, "rebase", "-r", "@", "-d", "description(exact:'x\n')")
	if body, _ := os.ReadFile(readme); !strings.Contains(string(body), "<<<<<<<") {
		t.Fatalf("precondition: README holds no conflict markers:\n%s", body)
	}

	if out, err := tuiosCLIInDir(t, base, repo, []string{"PWD=" + repo}, "new", "jjconf", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "review", "jjconf")
	if err != nil {
		t.Fatalf("ASSERTION: review: %v: %s", err, out)
	}
	saveText(t, "review-jj-conflict", out)
	if !strings.Contains(out, "+ <<<<<<<") {
		t.Errorf("ASSERTION: the review does not show README's conflict markers:\n%s", out)
	}
	for _, bad := range []string{".jjconflict", "JJ-CONFLICT-README"} {
		if strings.Contains(out, bad) {
			t.Errorf("ASSERTION: the review shows %q, which is not on disk:\n%s", bad, out)
		}
	}
}

// TestCheckpointJJRestore takes turn checkpoints in a plain session whose
// pane is in a jj workspace with no ".git", and restores the first one. git
// gets jj's git directory there, and needs the workspace as its work tree to
// write files back.
//
// The test fails on a build whose GitEnv sets GIT_DIR alone: the restore
// stops with "git checkout-index: fatal: this operation must be run in a work tree".
func TestCheckpointJJRestore(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.JJRepo(t, false)
	tr := newCheckpointTranscript(t, base, repo)
	if out, err := tuiosCLIIn(t, base, repo, "new", "agent", "--detach"); err != nil {
		t.Fatalf("new session in the workspace: %v: %s", err, out)
	}
	notes := filepath.Join(repo, "notes.txt")
	for n, content := range []string{"first\n", "first\nsecond\n"} {
		tr.ok("set-agent-state", "-s", "agent", "working")
		if err := os.WriteFile(notes, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tr.ok("set-agent-state", "-s", "agent", "done", "-m", "turn")
		tr.waitCheckpoints(n + 1)
	}
	if out, err := tr.run("checkpoint", "restore", "-s", "agent", "1"); err != nil {
		t.Fatalf("ASSERTION: checkpoint restore 1: %v\n%s", err, out)
	}
	if got := readFile(t, notes); got != "first\n" {
		t.Fatalf("ASSERTION: after restoring checkpoint 1, notes.txt is %q, want %q", got, "first\n")
	}
}
