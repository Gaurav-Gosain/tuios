package tuie2e

import (
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

// programRepo gives the repository at repo programs of its own, each of which
// appends its name to marker when git runs it: a clean and a smudge filter
// and a textconv on *.txt, diff.external, and the hooks git runs on add, on a
// ref update and on checkout. It then changes a.txt, so git has a file to
// filter.
func programRepo(t *testing.T, repo, marker string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.txt filter=probe diff=probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", ".gitattributes", "a.txt")
	testutil.Git(t, repo, "commit", "-q", "-m", "attributes")
	probe := func(name string) string { return "echo " + name + " >> '" + marker + "'; cat" }
	for _, kv := range [][2]string{
		{"filter.probe.clean", probe("clean")},
		{"filter.probe.smudge", probe("smudge")},
		{"filter.probe.required", "true"},
		{"diff.probe.textconv", "echo textconv >> '" + marker + "'; cat"},
		{"diff.external", "echo external >> '" + marker + "'"},
	} {
		testutil.Git(t, repo, "config", kv[0], kv[1])
	}
	for _, hook := range []string{"post-index-change", "reference-transaction", "post-checkout"} {
		path := filepath.Join(repo, ".git", "hooks", hook)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho hook-"+hook+" >> '"+marker+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// programsRan reads and clears marker.
func programsRan(marker string) []string {
	data, _ := os.ReadFile(marker)
	_ = os.Remove(marker)
	var out []string
	for _, f := range strings.Fields(string(data)) {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// TestGitRunsNoProgramOfThePaneRepository is a security boundary through the
// real binary. The pane's repository names programs git runs. A checkpoint
// on the agent's state change, a review, a fan's worktree add, fan compare's
// counts and tuios worktree diff run git there, in the daemon or in the CLI,
// and none of those programs may run.
//
// Negative control (NEGATIVE_CONTROLS.md): on a build before the fix every
// step runs the probes.
func TestGitRunsNoProgramOfThePaneRepository(t *testing.T) {
	base, repo := fanFixture(t)
	marker := filepath.Join(base, "ran")
	programRepo(t, repo, marker)
	_ = programsRan(marker)
	if out, err := tuiosCLI(t, base, "new", "agent", "--detach", "--cwd", repo); err != nil {
		t.Fatalf("new session in the repository: %v: %s", err, out)
	}
	step := func(name string, run func()) {
		t.Helper()
		_ = programsRan(marker)
		run()
		if ran := programsRan(marker); len(ran) > 0 {
			t.Errorf("ASSERTION: %s ran the repository's own programs: %v", name, ran)
		}
	}

	step("a checkpoint on the agent's state change", func() {
		for _, state := range []string{"working", "done"} {
			if out, err := tuiosCLI(t, base, "set-agent-state", state, "-s", "agent", "-w", "0"); err != nil {
				t.Fatalf("set-agent-state %s: %v\n%s", state, err, out)
			}
			time.Sleep(300 * time.Millisecond)
		}
		deadline := time.Now().Add(20 * time.Second)
		for testutil.Git(t, repo, "for-each-ref", "refs/tuios/checkpoints") == "" {
			if time.Now().After(deadline) {
				t.Fatal("no checkpoint was taken on the state change")
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	step("tuios review", func() {
		if out, err := tuiosCLI(t, base, "review", "agent", "--stat"); err != nil || !strings.Contains(out, "a.txt") {
			t.Fatalf("review: %v:\n%s", err, out)
		}
	})
	var wt string
	step("fan's worktree add", func() {
		if out, err := tuiosCLI(t, base, "fan", "1", "--agent", "claude", "--repo", repo, "--name", "try/probe", "Do the thing."); err != nil {
			t.Fatalf("fan: %v: %s", err, out)
		}
		for _, r := range worktreeRows(t, base, "--group", "try/probe") {
			wt, _ = r["path"].(string)
		}
		if wt == "" {
			t.Fatal("the fan made no worktree")
		}
	})
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	step("fan compare", func() {
		if out, err := tuiosCLI(t, base, "fan", "compare", "repo-try-probe"); err != nil || !strings.Contains(out, "1 file") {
			t.Fatalf("fan compare: %v:\n%s", err, out)
		}
	})
	step("tuios worktree diff", func() {
		if out, err := tuiosCLI(t, base, "worktree", "diff", "repo-try-probe"); err != nil || !strings.Contains(out, "a.txt") {
			t.Errorf("ASSERTION: worktree diff does not show a.txt: %v:\n%s", err, out)
		}
	})
}

// TestSpawnSheetIgnoresAReportedFolder is a security boundary. A pane whose
// shell is in one repository prints an OSC 7 report that names another,
// which holds a clean filter and a post-checkout hook. The New agent sheet
// reads the shell's own folder: it names the real repository, its read runs
// none of the other's programs, and Start makes the worktree from the real
// repository.
//
// Negative control (NEGATIVE_CONTROLS.md): on a build before the fix the
// sheet says "from otherrepo", its read runs the clean filter in the client,
// and Start runs the post-checkout hook in the daemon.
func TestSpawnSheetIgnoresAReportedFolder(t *testing.T) {
	base, repo := fanFixture(t)
	t.Setenv("PATH", strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]+string(os.PathListSeparator)+"/usr/bin:/bin")
	marker := filepath.Join(base, "ran")
	other := filepath.Join(base, "otherrepo")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, other, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(other, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, other, "add", "README")
	testutil.Git(t, other, "commit", "-q", "-m", "first")
	programRepo(t, other, marker)
	_ = programsRan(marker)
	if err := os.WriteFile(filepath.Join(repo, "README.txt"), []byte("\x1b]7;file://localhost"+other+"\x07look\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach", "--cwd", repo); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 200, rows: 55, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	runInShell(t, term, "cat README.txt", "look", uiTimeout)
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the cockpit", "Agents", "New agent")
	if err := term.SendKeys("n"); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the spawn sheet", "Where it works", "Start agent")
	waitSpawnRead(t, term)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), " / main") }, uiTimeout); err != nil {
		t.Fatalf("the sheet never named a repository: %v\n%s", err, term.Snapshot())
	}
	if s := term.Screen().Text(); strings.Contains(s, "otherrepo") || !strings.Contains(s, "from "+filepath.Base(repo)+" / ") {
		t.Errorf("ASSERTION: the sheet does not name the pane's repository:\n%s", term.Snapshot())
	}
	if ran := programsRan(marker); len(ran) > 0 {
		t.Errorf("ASSERTION: opening the sheet ran the reported repository's programs: %v", ran)
	}

	if err := term.SendKeys("Look around."); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the task", "Look around.")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(40 * time.Second)
	var rows []map[string]any
	for {
		rows = worktreeRows(t, base)
		if len(rows) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: Start made no worktree:\n%s", term.Snapshot())
		}
		time.Sleep(500 * time.Millisecond)
	}
	for _, r := range rows {
		if root, _ := r["repo_root"].(string); root != "" && root != repo {
			t.Errorf("ASSERTION: Start made a worktree of %s, not of the pane's repository %s", root, repo)
		}
	}
	if ran := programsRan(marker); len(ran) > 0 {
		t.Errorf("ASSERTION: Start ran the reported repository's programs: %v", ran)
	}
	if out, err := exec.Command("git", "-C", other, "worktree", "list").CombinedOutput(); err == nil && strings.Count(string(out), "\n") > 1 {
		t.Errorf("ASSERTION: the reported repository got a worktree:\n%s", out)
	}
	alive(t, term, "after the sheet")
}

// TestCheckpointIgnoresAReportedFolderWithoutProcessCwd is a security
// boundary for a daemon that cannot read a shell's folder, as on native
// Windows (TUIOS_E2E_NO_PROCESS_CWD). The pane starts in one repository and
// prints an OSC 7 report for a second one, which holds probes. The checkpoint
// on its state change is taken in the folder the pane started in, and runs
// none of the second repository's programs.
//
// Negative control (NEGATIVE_CONTROLS.md): on a build before the fix the
// checkpoint is taken in the second repository and runs its probes.
func TestCheckpointIgnoresAReportedFolderWithoutProcessCwd(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	marker := filepath.Join(base, "ran")
	other := filepath.Join(base, "otherrepo")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, other, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(other, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, other, "add", "README")
	testutil.Git(t, other, "commit", "-q", "-m", "first")
	programRepo(t, other, marker)
	_ = programsRan(marker)
	if err := os.WriteFile(filepath.Join(repo, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{"TUIOS_E2E=1", "TUIOS_E2E_NO_PROCESS_CWD=1"}
	if out, err := tuiosCLIEnv(t, base, env, "new", "osc", "--detach", "--cwd", repo); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	win := firstWindow(t, base, "osc").ID
	if err := paneSend(base, "osc", win, `printf '\033]7;file://localhost%s\033\\' '`+other+"'\n"); err != nil {
		t.Fatalf("send the OSC 7 report: %v", err)
	}
	waitAnnounced(t, base, "osc", win, other)
	for _, state := range []string{"working", "done"} {
		if out, err := tuiosCLIEnv(t, base, env, "set-agent-state", state, "-s", "osc", "-w", win); err != nil {
			t.Fatalf("set-agent-state %s: %v\n%s", state, err, out)
		}
		time.Sleep(300 * time.Millisecond)
	}
	deadline := time.Now().Add(20 * time.Second)
	for testutil.Git(t, repo, "for-each-ref", "refs/tuios/checkpoints") == "" &&
		testutil.Git(t, other, "for-each-ref", "refs/tuios/checkpoints") == "" {
		if time.Now().After(deadline) {
			t.Fatal("no checkpoint was taken on the state change")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if testutil.Git(t, repo, "for-each-ref", "refs/tuios/checkpoints") == "" {
		t.Errorf("ASSERTION: the checkpoint was not taken in the folder the pane started in")
	}
	if refs := testutil.Git(t, other, "for-each-ref", "refs/tuios/checkpoints"); refs != "" {
		t.Errorf("ASSERTION: the checkpoint was taken in the reported folder:\n%s", refs)
	}
	if ran := programsRan(marker); len(ran) > 0 {
		t.Errorf("ASSERTION: the checkpoint ran the reported repository's programs: %v", ran)
	}
}
