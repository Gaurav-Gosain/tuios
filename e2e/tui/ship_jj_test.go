package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// TestShipJJRefusals covers what ship refuses in a jj workspace, and that
// each refusal changes nothing:
//
//   - ship merge --squash: a squash is jj squash's work.
//   - ship merge after main moved on: only a fast-forward is done, and the
//     message names the jj rebase to run.
//   - ship push after the bookmark moved while the Inbox question waited:
//     the person allowed one commit, so nothing is pushed. git pushes the
//     allowed commit by hash; jj pushes a bookmark, so it refuses instead.
//
// Negative controls: with the squash check in jjMerge cut, the squash merge
// fast-forwards main; with the commit check in jjPush cut, the later commit
// reaches origin.
func TestShipJJRefusals(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.JJRepo(t, false)
	origin := filepath.Join(filepath.Dir(repo), "origin.git")
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	tr := newCheckpointTranscript(t, base, repo)
	commitOf := func(revset string) string {
		return testutil.JJ(t, repo, "--ignore-working-copy", "log", "--no-graph", "-r", revset, "-T", "commit_id")
	}
	tr.ok("new", "main", "--detach")
	tr.ok("worktree", "new", "feat/jj", "--repo", repo, "--detach")
	const session = "repo-feat-jj"
	ws := filepath.Join(base, "worktrees", "repo", "feat-jj")
	write := func(dir, name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(ws, "a.txt")
	tr.ok("ship", "commit", "-s", session, "-m", "Add a")
	tip := commitOf(`bookmarks(exact:"feat/jj")`)
	mainBefore := commitOf(`bookmarks(exact:"main")`)

	// A squash merge is refused, and main stays where it was.
	out, err := tr.run("ship", "merge", "-s", session, "--squash")
	if err == nil || !strings.Contains(out, "jj squash") {
		t.Errorf("ASSERTION: ship merge --squash = %v:\n%s\nwant a refusal naming jj squash", err, out)
	}
	if got := commitOf(`bookmarks(exact:"main")`); got != mainBefore {
		t.Errorf("ASSERTION: the refused squash moved main from %s to %s", mainBefore, got)
	}

	// The person commits on main: feat/jj is no longer a fast-forward.
	write(repo, "person.txt")
	testutil.JJ(t, repo, "commit", "-m", "The person's file")
	testutil.JJ(t, repo, "bookmark", "set", "main", "-r", "@-")
	moved := commitOf(`bookmarks(exact:"main")`)
	out, err = tr.run("ship", "merge", "-s", session)
	if err == nil || !strings.Contains(out, "jj rebase -b feat/jj -d main") {
		t.Errorf("ASSERTION: ship merge onto a moved main = %v:\n%s\nwant a refusal naming the jj rebase", err, out)
	}
	if got := commitOf(`bookmarks(exact:"main")`); got != moved {
		t.Errorf("ASSERTION: the refused merge moved main from %s to %s", moved, got)
	}

	// The push from the pane waits for the person. The agent commits again
	// while the question waits, and the answer then sends nothing.
	term := startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	pushOut := filepath.Join(t.TempDir(), "pane-push.txt")
	tr.ok("send-text", "-s", session, "-w", "0", fmt.Sprintf("%s ship push --yes --wait 60s > %s 2>&1\n", tuiosBin, pushOut))
	question := "Push feat/jj (" + tip[:7] + ") to origin"
	waitText(t, term, "the push question in the Inbox", question)
	saveFrame(t, term, "ship-jj-push-question")
	write(ws, "later.txt")
	tr.ok("ship", "commit", "-s", session, "-m", "Add a later file", "--force")
	if later := commitOf(`bookmarks(exact:"feat/jj")`); later == tip {
		t.Fatal("precondition: the second commit did not move feat/jj")
	}
	time.Sleep(answerSettle)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		data, _ := os.ReadFile(pushOut)
		if strings.Contains(string(data), "nothing was pushed") {
			tr.log("the pane's push printed:\n%s", data)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the push never refused the moved bookmark. It printed:\n%s\n%s", data, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := exec.Command("git", "--git-dir", origin, "rev-parse", "-q", "--verify", "refs/heads/feat/jj").Run(); err == nil {
		t.Errorf("ASSERTION: feat/jj reached origin after the bookmark moved")
	}
}
