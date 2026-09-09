package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuitest"
)

// The fan-out, end to end through the binary a person runs: a throwaway
// repository, a fake agent named claude on PATH, and the daemon the CLI
// starts. Nothing here touches any repository but the one testutil.GitRepo
// makes under the test.

// fanFixture is an isolation root, a repository, and a fake claude on PATH.
// The agent timers are shortened so a quiet fake agent is called ready in
// seconds rather than the shipped half minute.
func fanFixture(t *testing.T) (base, repo string) {
	t.Helper()
	base = t.TempDir()
	killDaemon(t, base)
	repo = testutil.GitRepo(t)
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho fake claude ready\nwhile IFS= read -r line; do echo \"GOT: $line\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUIOS_AGENT_STALL_SECONDS", "2")
	t.Setenv("TUIOS_AGENT_DETECT_SECONDS", "1")
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	return base, repo
}

// worktreeRows reads `tuios worktree ls --json`.
func worktreeRows(t *testing.T, base string, args ...string) []map[string]any {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"worktree", "ls", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("worktree ls: %v: %s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("worktree ls --json did not print JSON: %v\n%s", err, out)
	}
	return rows
}

func TestFanTypesPromptWhenReady(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/retry", "Add a retry to the client.")
	if err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	for _, want := range []string{"Started 2 agents on try/retry", "repo-try-retry ", "repo-try-retry-2 ", "tuios fan keep"} {
		if !strings.Contains(out, want) {
			t.Errorf("fan output lacks %q:\n%s", want, out)
		}
	}

	// The prompt is typed once the agent is quiet, which the shortened stall
	// window turns into a couple of seconds.
	deadline := time.Now().Add(30 * time.Second)
	for {
		rows := worktreeRows(t, base, "--group", "try/retry")
		sent := 0
		for _, r := range rows {
			if r["prompt_status"] == "sent" {
				sent++
			}
		}
		if len(rows) == 2 && sent == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the prompts were never sent: %v", rows)
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, name := range []string{"repo-try-retry", "repo-try-retry-2"} {
		pane, err := tuiosCLI(t, base, "capture-pane", "-s", name)
		if err != nil {
			t.Fatalf("capture-pane %s: %v: %s", name, err, pane)
		}
		if !strings.Contains(pane, "GOT: Add a retry to the client.") {
			t.Errorf("the agent in %s never received the prompt:\n%s", name, pane)
		}
	}

	// The rail shows the group under the repository, from a client attached to
	// the first agent's session.
	term := attachIn(t, base, "repo-try-retry", startOpts{})
	toggleSidebarViaPalette(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "try/retry-2") && strings.Contains(text, "GOT: Add a retry")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed the fan sessions: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "fan-in-progress")
	t.Logf("fan-out in progress:\n%s", term.Snapshot())
}

func TestFanKeepRefusesDirtySibling(t *testing.T) {
	base, repo := fanFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	out, err := tuiosCLI(t, base, "fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/keep", "Do the thing.")
	if err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	rows := worktreeRows(t, base, "--group", "try/keep")
	if len(rows) != 2 {
		t.Fatalf("worktree ls = %v, want 2 rows", rows)
	}
	var loser string
	for _, r := range rows {
		if r["session"] == "repo-try-keep" {
			loser = r["path"].(string)
		}
	}
	work := filepath.Join(loser, "work.txt")
	if err := os.WriteFile(work, []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No flag: the dirty sibling is left alone and the command says why.
	out, err = tuiosCLI(t, base, "fan", "keep", "repo-try-keep-2")
	if err == nil {
		t.Fatalf("fan keep exited 0 with a dirty sibling:\n%s", out)
	}
	for _, want := range []string{"Kept repo-try-keep-2", "holds 1 uncommitted change", "Nothing was removed", "--stash"} {
		if !strings.Contains(out, want) {
			t.Errorf("fan keep output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the refusal still removed the sibling's work: %v", err)
	}
	if rows := worktreeRows(t, base, "--group", "try/keep"); len(rows) != 2 {
		t.Fatalf("the refusal still killed a session: %v", rows)
	}

	// --stash: the work goes into the repository's stash and the sibling goes.
	out, err = tuiosCLI(t, base, "fan", "keep", "repo-try-keep-2", "--stash")
	if err != nil {
		t.Fatalf("fan keep --stash: %v: %s", err, out)
	}
	for _, want := range []string{"Removed worktree", "Branch try/keep is kept", "git stash as 'tuios: try/keep'", "Killed session 'repo-try-keep'"} {
		if !strings.Contains(out, want) {
			t.Errorf("fan keep --stash output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(loser); !os.IsNotExist(err) {
		t.Errorf("the sibling's worktree is still there: %v", err)
	}
	rows = worktreeRows(t, base, "--group", "try/keep")
	if len(rows) != 1 || rows[0]["session"] != "repo-try-keep-2" {
		t.Errorf("worktree ls after keep = %v, want only the winner", rows)
	}
	stash := gitOut(t, repo, "stash", "list")
	if !strings.Contains(stash, "tuios: try/keep") {
		t.Errorf("the stash does not hold the sibling's work: %q", stash)
	}
	branches := gitOut(t, repo, "branch", "--list", "try/keep*")
	if !strings.Contains(branches, "try/keep") || !strings.Contains(branches, "try/keep-2") {
		t.Errorf("a branch was deleted: %q", branches)
	}
}

// gitOut runs a read-only git command in the throwaway repository.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
