package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestWorktreeJJSession runs a worktree session in a jj repository with no
// ".git", through the binary a person runs, from start to removal.
//
//   - worktree new makes a jj workspace, and the session's branch is a
//     bookmark on its working copy.
//   - Each agent turn leaves a checkpoint of jj's working-copy commit, and a
//     restore writes turn 1's file back.
//   - ship commit runs jj commit and takes the bookmark along. ship merge
//     fast-forwards main. ship push sends the bookmark to origin.
//   - worktree rm refuses while the workspace holds a file jj did not
//     record (a new file over 1 MiB), and with --force forgets the
//     workspace. The committed work stays in the repository.
//
// The test fails on a build without jj worktrees: worktree new runs git
// worktree add, which finds no git repository.
//
// Negative control: with the cache lookup in worktree.Merged moved after
// jjMergedTips, the repeated listing runs jj log -r coalesce(...) again.
//
// Negative control: on the build before the refusal, the first worktree rm
// deletes big.bin with the workspace.
//
// Negative control: with the main-workspace check removed from
// worktree.Merged, the main session reads as merged into trunk() before any
// work, because the bookmark main is on a commit that changes something.
func TestWorktreeJJSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.JJRepo(t, false)
	origin := filepath.Join(filepath.Dir(repo), "origin.git")
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	// A jj in front of the real one logs every call, the daemon's included.
	realJJ, err := exec.LookPath("jj")
	if err != nil {
		t.Fatal(err)
	}
	bin, jjLog := t.TempDir(), filepath.Join(t.TempDir(), "jj.log")
	wrapper := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + jjLog + "'\nexec '" + realJJ + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "jj"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tr := newCheckpointTranscript(t, base, repo)
	jj := func(args ...string) string {
		out := testutil.JJ(t, repo, append([]string{"--ignore-working-copy"}, args...)...)
		tr.log("jj %s\n%s", strings.Join(args, " "), out)
		return out
	}
	commitOf := func(revset string) string {
		return jj("log", "--no-graph", "-r", revset, "-T", "commit_id")
	}

	tr.ok("new", "main", "--detach")
	tr.ok("worktree", "new", "feat/jj", "--repo", repo, "--detach")
	const session = "repo-feat-jj"
	ws := filepath.Join(base, "worktrees", "repo", "feat-jj")
	if st, err := os.Stat(filepath.Join(ws, ".jj")); err != nil || !st.IsDir() {
		t.Fatalf("ASSERTION: %s is not a jj workspace: %v", ws, err)
	}
	if list := jj("workspace", "list"); !strings.Contains(list, "feat-jj") {
		t.Fatalf("ASSERTION: jj workspace list lacks feat-jj:\n%s", list)
	}
	// The main workspace is not a branch that landed in trunk().
	var rows []struct {
		Session string `json:"session"`
		Branch  string `json:"branch"`
		Merged  bool   `json:"merged"`
	}
	out := tr.ok("worktree", "ls", "--json")
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("worktree ls --json: %v\n%s", err, out)
	}
	for _, r := range rows {
		if r.Session == session && r.Branch != "feat/jj" {
			t.Errorf("ASSERTION: %s is on %q, want feat/jj", session, r.Branch)
		}
		if r.Merged {
			t.Errorf("ASSERTION: %s on %s reads as merged before any work", r.Session, r.Branch)
		}
	}

	// A listing polled while nothing moves answers merged from its cache,
	// which jj's operation log keys: the merged check runs no jj.
	if err := os.Truncate(jjLog, 0); err != nil {
		t.Fatal(err)
	}
	tr.ok("worktree", "ls", "--json")
	calls, err := os.ReadFile(jjLog)
	if err != nil {
		t.Fatal(err)
	}
	tr.log("jj calls of a repeated listing:\n%s", calls)
	if strings.Contains(string(calls), "coalesce(") {
		t.Errorf("ASSERTION: a repeated listing ran the merged check's jj again:\n%s", calls)
	}

	// Two agent turns, each leaving a checkpoint.
	checkpoints := func() int {
		out, err := tuiosCLIIn(t, base, repo, "checkpoint", "list", "-s", session, "--json")
		var res struct {
			Checkpoints []json.RawMessage `json:"checkpoints"`
		}
		if err != nil || json.Unmarshal([]byte(out), &res) != nil {
			t.Fatalf("checkpoint list: %v\n%s", err, out)
		}
		return len(res.Checkpoints)
	}
	notes := filepath.Join(ws, "notes.txt")
	for n, content := range []string{"first\n", "first\nsecond\n"} {
		tr.ok("set-agent-state", "-s", session, "working")
		if err := os.WriteFile(notes, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tr.ok("set-agent-state", "-s", session, "done", "-m", "turn")
		waitUntil(t, fmt.Sprintf("turn %d leaves a checkpoint", n+1), 20*time.Second, func() (bool, string) {
			got := checkpoints()
			return got >= n+1, fmt.Sprintf("%d checkpoints", got)
		})
	}
	tr.ok("checkpoint", "restore", "-s", session, "1")
	if got := readFile(t, notes); got != "first\n" {
		t.Fatalf("ASSERTION: after restoring checkpoint 1, notes.txt is %q", got)
	}

	out = tr.ok("ship", "commit", "-s", session, "-m", "Add the notes")
	if !strings.Contains(out, "feat/jj") {
		t.Errorf("ship commit printed:\n%s", out)
	}
	tip := commitOf(`bookmarks(exact:"feat/jj")`)
	if desc := jj("log", "--no-graph", "-r", tip, "-T", "description"); strings.TrimSpace(desc) != "Add the notes" {
		t.Errorf("ASSERTION: feat/jj is on %q, want the new commit", desc)
	}

	out = tr.ok("ship", "merge", "-s", session)
	if !strings.Contains(out, "Merged feat/jj into main") {
		t.Errorf("ship merge printed:\n%s", out)
	}
	if got := commitOf(`bookmarks(exact:"main")`); got != tip {
		t.Errorf("ASSERTION: main is at %s after the merge, want %s", got, tip)
	}

	tr.ok("ship", "push", "-s", session, "--yes")
	if got := testutil.Git(t, origin, "rev-parse", "refs/heads/feat/jj"); got != tip {
		t.Errorf("ASSERTION: origin's feat/jj is %s, want %s", got, tip)
	}

	// jj leaves a new file over 1 MiB out of the snapshot, so forgetting the
	// workspace would lose it: rm without force refuses and keeps it.
	big := filepath.Join(ws, "big.bin")
	if err := os.WriteFile(big, make([]byte, 2<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tr.run("worktree", "rm", session); err == nil {
		t.Fatalf("ASSERTION: worktree rm deleted a workspace holding a file jj did not record:\n%s", out)
	} else if !strings.Contains(out, "big.bin") || !strings.Contains(out, "--force") {
		t.Errorf("ASSERTION: the refusal names neither big.bin nor --force:\n%s", out)
	}
	if _, err := os.Stat(big); err != nil {
		t.Fatalf("ASSERTION: the refused rm lost big.bin: %v", err)
	}

	tr.ok("worktree", "rm", session, "--force")
	if _, err := os.Stat(ws); !os.IsNotExist(err) {
		t.Errorf("ASSERTION: the workspace folder is still there: %v", err)
	}
	if list := jj("workspace", "list"); strings.Contains(list, "feat-jj") {
		t.Errorf("ASSERTION: jj still lists the workspace:\n%s", list)
	}
	if got := commitOf(tip); got != tip {
		t.Errorf("ASSERTION: the committed work is gone from the repository")
	}
}
