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
	t.Setenv("PATH", strings.SplitN(os.Getenv("PATH"), string(os.PathListSeparator), 2)[0]+string(os.PathListSeparator)+agentFreePath("/usr/bin", "/bin"))
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

// TestRailGitIgnoresAReportedFolder is a security boundary. The rail's git
// section runs git in the folder it shows. A pane whose shell is in one
// repository prints an OSC 7 report that names another. The files section
// follows the report, which shows the client took it. The git section stays
// on the shell's repository and follows a branch change made there after
// the report. When the shell itself moves into the other repository, the git
// section follows it there.
//
// How it could pass wrongly, and what stops it: the section could still show
// the shell's repository only because it had not looked again since the
// report. It must show the branch the shell's repository moved to after the
// report was taken, so it did look again.
//
// Negative control (NEGATIVE_CONTROLS.md): with GitSyncCmd reading
// filesWantDir instead of gitWantDir, the section names the reported
// repository's branch.
func TestRailGitIgnoresAReportedFolder(t *testing.T) {
	const config = `[appearance.sidebar]
enabled = true
position = "left"
width = 28
sections = "sessions,files,git"
git_refresh_ms = 500
`
	term, base := railClient(t, "railgit", config, startOpts{cols: 120, rows: 40})
	shell := filepath.Join(base, "shellrepo")
	told := filepath.Join(base, "toldrepo")
	for dir, branch := range map[string]string{shell: "shell-a", told: "told-c"} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, dir, "init", "-q", "-b", branch)
	}
	if err := os.WriteFile(filepath.Join(told, "told-only.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shell, "README.txt"), []byte("\x1b]7;file://localhost"+told+"\x07look\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	railWait := func(what string, cond func(s tuitest.Screen) bool) {
		t.Helper()
		if err := term.WaitFor(cond, uiTimeout); err != nil {
			t.Fatalf("%s: %v\n%s", what, err, term.Snapshot())
		}
	}
	onRail := func(s tuitest.Screen, text string) bool { return railRowOf(s, text) >= 0 }

	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+shell+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mar""ked`, "marked", uiTimeout)
	railWait("the git section never showed the shell's repository", func(s tuitest.Screen) bool { return onRail(s, "shell-a") })

	runInShell(t, term, "cat README.txt", "look", uiTimeout)
	railWait("the files section never took the report", func(s tuitest.Screen) bool { return onRail(s, "told-only.txt") })
	testutil.Git(t, shell, "symbolic-ref", "HEAD", "refs/heads/shell-b")
	railWait("the git section never looked again", func(s tuitest.Screen) bool { return onRail(s, "shell-b") || onRail(s, "told-c") })
	if s := term.Screen(); onRail(s, "told-c") || !onRail(s, "shell-b") {
		t.Errorf("ASSERTION: the git section follows the OSC 7 report, not the shell's repository:\n%s", term.Snapshot())
	}

	// The positive half: a shell that really is in the other repository
	// moves the section there.
	runInShell(t, term, "cd "+told+` && printf '\033]7;file://%s\033\\%s\n' "$PWD" mo""ved`, "moved", uiTimeout)
	railWait("ASSERTION: the git section never followed the shell into the other repository", func(s tuitest.Screen) bool { return onRail(s, "told-c") })
	alive(t, term, "after the git section")
}

// TestShipFromAPaneRunsNoRepositoryHook is a security boundary. A ship verb
// that a pane calls needs no person, so its git runs none of the hooks of the
// repository the pane can write. ship commit, ship merge and ship push run
// from a shell inside the worktree session's pane and run no hook, the push
// too once the person allows it in the Inbox: the person allowed the push,
// not the repository's programs. The same verbs from the person's CLI run
// the hooks, as the person's own git would.
//
// Negative control (NEGATIVE_CONTROLS.md): with shipContext marking every
// caller's git as the person's (childproc.AsPerson), the pane's ship commit
// runs pre-commit and post-commit; with the shipMerge call in verbShipMerge,
// or the shipPush call in verbShipPush, passing true for the person, the
// pane's ship merge runs post-merge and its push runs pre-push.
func TestShipFromAPaneRunsNoRepositoryHook(t *testing.T) {
	f := newShipFixture(t)
	marker := filepath.Join(f.base, "ran")
	for _, hook := range []string{"pre-commit", "post-commit", "post-merge", "pre-push"} {
		path := filepath.Join(f.repo, ".git", "hooks", hook)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+hook+" >> '"+marker+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = programsRan(marker)
	head := func(dir string) string { return testutil.Git(t, dir, "rev-parse", "HEAD") }

	f.write("pane.txt", "from the pane\n")
	if out := inPane(t, f.base, f.session, "0", "ship commit -s "+f.session+" -m 'From the pane'"); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ship commit from the pane: %s", out)
	}
	if msg := testutil.Git(t, f.worktree, "log", "-1", "--format=%s"); msg != "From the pane" {
		t.Fatalf("the pane's ship commit made no commit: the last one is %q", msg)
	}
	if ran := programsRan(marker); len(ran) > 0 {
		t.Errorf("ASSERTION: ship commit from a pane ran the repository's hooks: %v", ran)
	}
	if out := inPane(t, f.base, f.session, "0", "ship merge -s "+f.session); !strings.Contains(out, "EXIT=0") {
		t.Fatalf("ship merge from the pane: %s", out)
	}
	if got, want := testutil.Git(t, f.repo, "rev-parse", "main"), head(f.worktree); got != want {
		t.Fatalf("the pane's ship merge left main at %s, want %s", got, want)
	}
	if ran := programsRan(marker); len(ran) > 0 {
		t.Errorf("ASSERTION: ship merge from a pane ran the repository's hooks: %v", ran)
	}

	term := startIn(t, f.base, startOpts{args: []string{"attach", f.session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	tip := head(f.worktree)
	pushOut := filepath.Join(f.fakes, "pane-push.txt")
	f.tr.ok("send-text", "-s", f.session, "-w", "0", tuiosBin+" ship push --yes --wait 60s > "+pushOut+" 2>&1\n")
	waitText(t, term, "the push question in the Inbox", "Push feat/ship ("+tip[:7]+") to origin")
	pause(t, "the Inbox takes an answer key only after the question has been on screen for a moment", answerSettle)
	sendKeys(t, term, "1")
	waitForFileText(t, pushOut, func(s string) bool { return strings.Contains(s, "Pushed feat/ship") }, "the pane's push never went out after the person allowed it")
	if got := testutil.Git(t, f.repo, "--git-dir", f.remote, "rev-parse", "refs/heads/feat/ship"); got != tip {
		t.Fatalf("the pane's push left the origin's feat/ship at %s, want %s", got, tip)
	}
	if ran := programsRan(marker); len(ran) > 0 {
		t.Errorf("ASSERTION: ship push from a pane ran the repository's hooks: %v", ran)
	}

	// The positive half: the person's own ship verbs run the hooks.
	f.write("person.txt", "from the person\n")
	f.tr.ok("ship", "commit", "-s", f.session, "-m", "From the person")
	if ran := programsRan(marker); !slices.Contains(ran, "pre-commit") || !slices.Contains(ran, "post-commit") {
		t.Errorf("ASSERTION: the person's ship commit did not run the repository's hooks; it ran %v", ran)
	}
	f.tr.ok("ship", "merge", "-s", f.session)
	if got, want := testutil.Git(t, f.repo, "rev-parse", "main"), head(f.worktree); got != want {
		t.Fatalf("the person's ship merge left main at %s, want %s", got, want)
	}
	if ran := programsRan(marker); !slices.Contains(ran, "post-merge") {
		t.Errorf("ASSERTION: the person's ship merge did not run the repository's post-merge hook; it ran %v", ran)
	}
	f.tr.ok("ship", "push", "-s", f.session, "--yes")
	if got, want := testutil.Git(t, f.repo, "--git-dir", f.remote, "rev-parse", "refs/heads/feat/ship"), head(f.worktree); got != want {
		t.Fatalf("the person's ship push left the origin's feat/ship at %s, want %s", got, want)
	}
	if ran := programsRan(marker); !slices.Contains(ran, "pre-push") {
		t.Errorf("ASSERTION: the person's ship push did not run the repository's pre-push hook; it ran %v", ran)
	}
}

// TestCheckpointIgnoresAReportedFolderWithoutProcessCwd is a security
// boundary for a daemon that cannot read a shell's folder, as on native
// Windows (knob no_process_cwd). The pane starts in one repository and
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

	env := []string{"TUIOS_E2E=1", knob("no_process_cwd=1")}
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
