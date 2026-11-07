package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The worktree setup, end to end through the binary a person runs: a
// throwaway repository, a fake agent named claude that writes a mark when it
// starts, and setups that wait on a gate file the test creates. The marks
// say whether, and when, the agent started; worktree ls --json says what the
// daemon recorded; git says what is left in the repository.
//
// Negative controls (NEGATIVE_CONTROLS.md): with undoFailedSetup returning
// before it removes anything, TestWorktreeSetupSyncFailureRemoves fails on
// the worktree folder that is still there. With the async agent started only
// after the setup, as a sync one is, TestWorktreeSetupAsync fails on the
// agent mark that is missing while the setup runs.

// setupFixture is fanFixture with a fake claude that marks its start in
// marks, named by the worktree folder, and a gate path the setups wait on.
func setupFixture(t *testing.T) (base, repo, marks, gate string) {
	t.Helper()
	base, repo = fanFixture(t)
	bin := filepath.Join(t.TempDir(), "bin")
	marks = filepath.Join(t.TempDir(), "marks")
	for _, dir := range []string{bin, marks} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gate = filepath.Join(t.TempDir(), "gate")
	script := "#!/bin/sh\necho agent > '" + marks + "'/\"$(basename \"$PWD\")\"\necho fake agent started\nexec cat >/dev/null\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return base, repo, marks, gate
}

// gatedSetup prints five lines, waits for the gate file, then prints one
// more and exits with code.
func gatedSetup(gate string, code int) string {
	return fmt.Sprintf("printf 'one\\ntwo\\nthree\\nfour\\nfive\\n'; while [ ! -f '%s' ]; do sleep 0.1; done; echo installed; exit %d", gate, code)
}

// setupOf is the setup record of session in worktree ls --json, nil when
// the session has none or is not listed.
func setupOf(t *testing.T, base, session string) map[string]any {
	t.Helper()
	for _, r := range worktreeRows(t, base) {
		if r["session"] == session {
			s, _ := r["setup"].(map[string]any)
			return s
		}
	}
	return nil
}

// waitSetup waits until the setup record of session satisfies ok.
func waitSetup(t *testing.T, base, session, what string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		s := setupOf(t, base, session)
		if s != nil && ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: the setup of %s never got there: %v", what, session, s)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// marked reports whether the fake agent started in the worktree folder dir.
func marked(marks, dir string) bool {
	_, err := os.Stat(filepath.Join(marks, dir))
	return err == nil
}

// tailHas reports whether the setup's tail holds want.
func tailHas(s map[string]any, want string) bool {
	tail, _ := s["tail"].([]any)
	for _, l := range tail {
		if str, _ := l.(string); strings.Contains(str, want) {
			return true
		}
	}
	return false
}

// TestWorktreeSetupSyncHoldsAgent runs [worktree] setup from config.toml. The
// agent waits while the setup runs, the record shows the last four lines,
// and the agent starts once the setup passes. The client attached to the
// session shows the card in both states.
func TestWorktreeSetupSyncHoldsAgent(t *testing.T) {
	base, repo, marks, gate := setupFixture(t)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("[worktree]\nsetup = %q\n", gatedSetup(gate, 0))
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	out, err := tuiosCLI(t, base, "worktree", "new", "feat/sync", "--repo", repo, "--agent", "claude", "--detach")
	if err != nil {
		t.Fatalf("worktree new: %v: %s", err, out)
	}
	if !strings.Contains(out, "The agent starts when it passes.") {
		t.Errorf("worktree new does not say the agent waits for the setup:\n%s", out)
	}

	running := waitSetup(t, base, "repo-feat-sync", "running with its last lines", func(s map[string]any) bool {
		return s["state"] == "running" && tailHas(s, "five")
	})
	if running["agent"] != "waiting" {
		t.Errorf("the agent stage is %v while a sync setup runs, want waiting", running["agent"])
	}
	if tail, _ := running["tail"].([]any); len(tail) != 4 || tailHas(running, "one") {
		t.Errorf("the tail is %v, want the last four lines", running["tail"])
	}
	if marked(marks, "feat-sync") {
		t.Fatalf("the agent started while the sync setup ran")
	}
	table, err := tuiosCLI(t, base, "worktree", "ls")
	if err != nil || !strings.Contains(table, "SETUP") || !strings.Contains(table, "running") {
		t.Errorf("worktree ls has no SETUP column that says running: %v\n%s", err, table)
	}

	term := attachIn(t, base, "repo-feat-sync", startOpts{})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Worktree setup") && strings.Contains(text, "Waits for the setup") && strings.Contains(text, "five")
	}, uiTimeout); err != nil {
		t.Fatalf("the setup card never showed the running setup: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "setup-running")

	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	passed := waitSetup(t, base, "repo-feat-sync", "passed", func(s map[string]any) bool {
		return s["state"] == "passed" && s["agent"] == "started"
	})
	if exit, _ := passed["exit"].(float64); exit != 0 || passed["exit"] == nil {
		t.Errorf("the passed setup's exit is %v, want 0", passed["exit"])
	}
	deadline := time.Now().Add(10 * time.Second)
	for !marked(marks, "feat-sync") {
		if time.Now().After(deadline) {
			t.Fatalf("the agent never started after the setup passed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Passed") && strings.Contains(text, "Started")
	}, uiTimeout); err != nil {
		t.Fatalf("the setup card never showed the pass: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "setup-passed")
}

// TestWorktreeSetupSyncFailureRemoves covers a sync setup that fails before
// the agent starts. The worktree and the branch the call made go, and the
// agent never starts. A branch that was there before stays, and so do a
// worktree and a branch that hold a change the setup made.
func TestWorktreeSetupSyncFailureRemoves(t *testing.T) {
	base, repo, marks, gate := setupFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}
	worktrees := filepath.Join(base, "worktrees", "repo")
	branchThere := func(name string) bool {
		return strings.TrimSpace(gitOut(t, repo, "branch", "--list", name)) != ""
	}

	// A new branch, clean: both go. The client attaches while the setup
	// runs, so the keys the harness sends on attach meet a card no key
	// hides, and the failure lands on a card nobody has hidden.
	out, err := tuiosCLI(t, base, "worktree", "new", "feat/broken", "--repo", repo, "--agent", "claude", "--detach", "--setup", gatedSetup(gate, 3))
	if err != nil {
		t.Fatalf("worktree new: %v: %s", err, out)
	}
	waitSetup(t, base, "repo-feat-broken", "running", func(s map[string]any) bool { return s["state"] == "running" })
	term := attachIn(t, base, "repo-feat-broken", startOpts{})
	if err := term.WaitForText("Worktree setup", uiTimeout); err != nil {
		t.Fatalf("the setup card never showed: %v\n%s", err, term.Snapshot())
	}
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	failed := waitSetup(t, base, "repo-feat-broken", "failed", func(s map[string]any) bool { return s["state"] == "failed" })
	if exit, _ := failed["exit"].(float64); exit != 3 {
		t.Errorf("the failed setup's exit is %v, want 3", failed["exit"])
	}
	if failed["removed"] != true || failed["branch_removed"] != true || failed["agent"] != "not_started" {
		t.Errorf("the failed sync setup's record is %v, want removed, branch_removed and agent not_started", failed)
	}
	if !tailHas(failed, "installed") {
		t.Errorf("the tail lacks the setup's output: %v", failed["tail"])
	}
	if _, err := os.Stat(filepath.Join(worktrees, "feat-broken")); !os.IsNotExist(err) {
		t.Errorf("the worktree folder is still there after the sync setup failed: %v", err)
	}
	if branchThere("feat/broken") {
		t.Errorf("the branch feat/broken is still there after the sync setup failed")
	}
	table, err := tuiosCLI(t, base, "worktree", "ls")
	if err != nil || !strings.Contains(table, "failed, exit 3") {
		t.Errorf("worktree ls does not say failed, exit 3: %v\n%s", err, table)
	}

	// The session stays, with the setup window open on the failure. The
	// card says what was removed.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Failed, exit 3") && strings.Contains(text, "removed") && strings.Contains(text, "The setup exited 3")
	}, uiTimeout); err != nil {
		t.Fatalf("the setup card never showed the failure: %v\n%s", err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "setup-failed")

	// A branch that was there before is kept; its worktree goes.
	gitOut(t, repo, "branch", "feat/old")
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/old", "--repo", repo, "--agent", "claude", "--detach", "--setup", "exit 1"); err != nil {
		t.Fatalf("worktree new feat/old: %v: %s", err, out)
	}
	old := waitSetup(t, base, "repo-feat-old", "failed", func(s map[string]any) bool { return s["state"] == "failed" })
	if old["removed"] != true || old["branch_removed"] == true {
		t.Errorf("a branch that was there before: record %v, want the worktree removed and the branch kept", old)
	}
	if !branchThere("feat/old") {
		t.Errorf("the branch feat/old, which was there before, was deleted")
	}

	// A setup that leaves a change: both are kept.
	if out, err := tuiosCLI(t, base, "worktree", "new", "feat/dirty", "--repo", repo, "--agent", "claude", "--detach", "--setup", "echo half > half.txt; exit 1"); err != nil {
		t.Fatalf("worktree new feat/dirty: %v: %s", err, out)
	}
	dirty := waitSetup(t, base, "repo-feat-dirty", "failed", func(s map[string]any) bool { return s["state"] == "failed" })
	if dirty["removed"] == true || dirty["branch_removed"] == true {
		t.Errorf("a worktree with a change: record %v, want both kept", dirty)
	}
	if note, _ := dirty["note"].(string); !strings.Contains(note, "uncommitted change") {
		t.Errorf("the note does not say why the worktree is kept: %q", note)
	}
	if _, err := os.Stat(filepath.Join(worktrees, "feat-dirty", "half.txt")); err != nil {
		t.Errorf("the change in the worktree is gone: %v", err)
	}
	if !branchThere("feat/dirty") {
		t.Errorf("the branch feat/dirty was deleted with a change in its worktree")
	}

	// No agent started in any of them.
	time.Sleep(time.Second)
	for _, d := range []string{"feat-broken", "feat-old", "feat-dirty"} {
		if marked(marks, d) {
			t.Errorf("the agent started in %s after its sync setup failed", d)
		}
	}
}

// TestWorktreeSetupAsync covers setup_async. The agent starts at once while
// the setup runs. A pass moves the record to passed. A failure, here through
// fan, keeps the worktree and the branch, and the agent goes on.
func TestWorktreeSetupAsync(t *testing.T) {
	base, repo, marks, gate := setupFixture(t)
	if out, err := tuiosCLI(t, base, "new", "plain", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v: %s", err, out)
	}

	out, err := tuiosCLI(t, base, "worktree", "new", "feat/async", "--repo", repo, "--agent", "claude", "--detach", "--setup", gatedSetup(gate, 0), "--setup-async")
	if err != nil {
		t.Fatalf("worktree new: %v: %s", err, out)
	}
	if !strings.Contains(out, "The agent starts now.") {
		t.Errorf("worktree new does not say the agent starts now:\n%s", out)
	}
	running := waitSetup(t, base, "repo-feat-async", "running", func(s map[string]any) bool { return s["state"] == "running" })
	deadline := time.Now().Add(10 * time.Second)
	for !marked(marks, "feat-async") {
		if time.Now().After(deadline) {
			t.Fatalf("the agent did not start while the async setup ran: %v", running)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s := setupOf(t, base, "repo-feat-async"); s["state"] != "running" || s["agent"] != "started" {
		t.Errorf("with the agent up, the record is %v, want running and agent started", s)
	}

	// The fan's setup fails once the gate opens.
	out, err = tuiosCLI(t, base, "fan", "1", "--agent", "claude", "--repo", repo, "--name", "try/async", "--setup", gatedSetup(gate, 4), "--setup-async", "Do the thing.")
	if err != nil {
		t.Fatalf("fan: %v: %s", err, out)
	}
	if !strings.Contains(out, "Each agent starts now.") {
		t.Errorf("fan does not say each agent starts now:\n%s", out)
	}
	if err := os.WriteFile(gate, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitSetup(t, base, "repo-feat-async", "passed", func(s map[string]any) bool { return s["state"] == "passed" })
	failed := waitSetup(t, base, "repo-try-async", "failed", func(s map[string]any) bool { return s["state"] == "failed" })
	if exit, _ := failed["exit"].(float64); exit != 4 {
		t.Errorf("the failed setup's exit is %v, want 4", failed["exit"])
	}
	if failed["removed"] == true || failed["branch_removed"] == true || failed["agent"] != "started" {
		t.Errorf("a failed async setup's record is %v, want nothing removed and the agent started", failed)
	}
	if _, err := os.Stat(filepath.Join(base, "worktrees", "repo", "try-async")); err != nil {
		t.Errorf("the worktree of a failed async setup is gone: %v", err)
	}
	if strings.TrimSpace(gitOut(t, repo, "branch", "--list", "try/async")) == "" {
		t.Errorf("the branch of a failed async setup is gone")
	}
	if !marked(marks, "try-async") {
		t.Errorf("the fan's agent never started")
	}
}
