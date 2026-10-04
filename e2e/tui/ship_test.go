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
	"github.com/Gaurav-Gosain/tuitest"
)

// The ship verbs end to end, through the binary a person runs: a throwaway
// repository with a bare repository as its origin, a worktree session made
// with tuios worktree new, a fake gh on PATH that records its arguments and
// answers with canned JSON, and a fake gpg that signs anything, so the
// person's signing config can be seen to be used. Nothing here reaches a
// network or a real GitHub.

// shipFixture is what the ship tests share.
type shipFixture struct {
	t        *testing.T
	base     string
	repo     string
	remote   string
	fakes    string
	worktree string
	session  string
	tr       *checkpointTranscript
}

// shipCanned is the pull request the fake gh opens: one check still running
// and one commit status that passed, so its rollup is pending.
const shipCanned = `{"number":7,"url":"https://github.example/o/r/pull/7","state":"OPEN","statusCheckRollup":[` +
	`{"__typename":"CheckRun","name":"test","status":"IN_PROGRESS","conclusion":""},` +
	`{"__typename":"StatusContext","context":"lint","state":"SUCCESS"}]}`

// shipCannedPass is the same pull request once its checks passed.
const shipCannedPass = `{"number":7,"url":"https://github.example/o/r/pull/7","state":"OPEN","statusCheckRollup":[` +
	`{"__typename":"CheckRun","name":"test","status":"COMPLETED","conclusion":"SUCCESS"},` +
	`{"__typename":"StatusContext","context":"lint","state":"SUCCESS"}]}`

// newShipFixture makes the repository, its bare origin, the fakes, the
// person's git identity and signing setup, and a worktree session on
// feat/ship. The identity is in the global config the test points git at,
// and the variables testutil.GitRepo sets to override it are removed, so a
// commit that names the person did so by reading their config.
func newShipFixture(t *testing.T) *shipFixture {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	repo := testutil.GitRepo(t)
	f := &shipFixture{t: t, base: base, repo: repo, session: "repo-feat-ship"}
	f.fakes = filepath.Join(t.TempDir(), "fakes")
	if err := os.MkdirAll(f.fakes, 0o755); err != nil {
		t.Fatal(err)
	}
	gh := "#!/bin/sh\n" +
		"d='" + f.fakes + "'\n" +
		"for a in \"$@\"; do printf '%s\\037' \"$a\"; done >> \"$d/gh.log\"\necho >> \"$d/gh.log\"\n" +
		"case \"$1 $2\" in\n" +
		"'auth status') echo 'Logged in to github.example' >&2; exit 0;;\n" +
		"'pr view') if [ -f \"$d/pr.json\" ]; then cat \"$d/pr.json\"; exit 0; fi; echo \"no pull requests found for branch \\\"$3\\\"\" >&2; exit 1;;\n" +
		"'pr create') cp \"$d/created.json\" \"$d/pr.json\"; echo 'https://github.example/o/r/pull/7'; exit 0;;\n" +
		"esac\necho \"fake gh: unknown command $*\" >&2; exit 2\n"
	gpg := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + f.fakes + "/gpg.log'\ncat > /dev/null\n" +
		"printf '\\n[GNUPG:] SIG_CREATED D 1 8 00 1700000000 0\\n' >&2\n" +
		"printf -- '-----BEGIN PGP SIGNATURE-----\\n\\nZmFrZQ==\\n-----END PGP SIGNATURE-----\\n'\n"
	for name, body := range map[string]string{"gh": gh, "gpg": gpg, "created.json": shipCanned} {
		if err := os.WriteFile(filepath.Join(f.fakes, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", f.fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(base, "worktrees"))
	t.Setenv("TUIOS_PR_POLL_SECONDS", "1")

	// The person's identity and signing setup, in their global config. The
	// identity is not an address: the test writes no email address anywhere.
	for _, kv := range [][2]string{
		{"user.name", "Ship Tester"},
		{"user.email", "ship-tester"},
		{"user.signingkey", "ship-tester-key"},
		{"commit.gpgsign", "true"},
		{"gpg.program", filepath.Join(f.fakes, "gpg")},
	} {
		testutil.Git(t, repo, "config", "--global", kv[0], kv[1])
	}
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}

	f.remote = filepath.Join(t.TempDir(), "origin.git")
	testutil.Git(t, repo, "init", "-q", "--bare", "-b", "main", f.remote)
	testutil.Git(t, repo, "remote", "add", "origin", f.remote)
	testutil.Git(t, repo, "push", "-q", "-u", "origin", "main")

	f.tr = newCheckpointTranscript(t, base, repo)
	f.tr.ok("new", "main", "--detach")
	f.tr.ok("worktree", "new", "feat/ship", "--repo", repo, "--detach")
	f.worktree = filepath.Join(base, "worktrees", "repo", "feat-ship")
	if _, err := os.Stat(f.worktree); err != nil {
		t.Fatalf("the worktree is not at %s: %v", f.worktree, err)
	}
	return f
}

// write puts content in a file of the worktree.
func (f *shipFixture) write(name, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.worktree, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// ghCalls are the fake gh's calls, one argument list each.
func (f *shipFixture) ghCalls() [][]string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.fakes, "gh.log"))
	if err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	var calls [][]string
	for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		calls = append(calls, strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f"))
	}
	return calls
}

// jsonFailure reads the --json form of a failed ship command.
func jsonFailure(t *testing.T, out string) (code string, files []string) {
	t.Helper()
	var res struct {
		Success bool     `json:"success"`
		Code    string   `json:"code"`
		Files   []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Success {
		t.Fatalf("want a JSON failure, got:\n%s", out)
	}
	return res.Code, res.Files
}

// TestShipFromWorktreeToPullRequest takes an agent's work in a worktree
// session to a merged change and a pull request:
//
//   - ship commit is refused while the agent works, and then commits every
//     change on the worktree's branch with the person's own name, through
//     their signing program, with the message as given and nothing added;
//   - ship merge fast-forwards main in the main checkout;
//   - a merge that conflicts is aborted: main and the main checkout are as
//     they were, no merge is left in progress, and the conflicting file is
//     named; a merge into a dirty main checkout is refused;
//   - ship push sends nothing without a confirmation, and with --yes reaches
//     the bare origin;
//   - ship pr calls gh with the head, base, title, body and draft flag asked
//     for, and records the pull request it reads back;
//   - worktree ls --json and ls --json carry the pull request, and the rail
//     shows the badge on the agent's row, which the poll moves from pending
//     to pass when gh's answer changes.
//
// Every call goes to transcript.txt, the fake gh's calls to gh-calls.txt, and
// the rail's frames to the frames directory when TUIOS_E2E_FRAMES is set.
//
// Negative controls (NEGATIVE_CONTROLS.md): with the git reset --merge that
// undoes a failed merge taken out of abortMerge, the main checkout is left
// mid-merge with conflict markers in README and the conflict checks fail.
// With the confirm token check taken out of shipOutboundGate, push without
// --yes reaches the origin. With the kickPRPoll call taken out of
// handleAttach, the badge stays on pending. With the commit run under the
// checkpoint identity and --no-gpg-sign, the identity and signature checks
// fail.
func TestShipFromWorktreeToPullRequest(t *testing.T) {
	f := newShipFixture(t)
	tr := f.tr
	t.Cleanup(func() {
		var b strings.Builder
		for _, c := range f.ghCalls() {
			b.WriteString(strings.Join(c, " ") + "\n")
		}
		_ = os.WriteFile(filepath.Join(artifactDir(t), "gh-calls.txt"), []byte(b.String()), 0o644)
	})

	// The agent writes a file and is still working: the commit waits.
	f.write("hello.txt", "hello from the agent\n")
	tr.ok("set-agent-state", "-s", f.session, "working")
	if out, err := tr.run("ship", "commit", "-s", f.session, "-m", "Add the hello file"); err == nil || !strings.Contains(out, "not_ready") && !strings.Contains(out, "working") {
		t.Fatalf("ship commit while the agent works = %v:\n%s\nwant a refusal", err, out)
	}
	tr.ok("set-agent-state", "-s", f.session, "done", "-m", "Added the hello file")
	out := tr.ok("ship", "commit", "-s", f.session, "-m", "Add the hello file")
	if !strings.Contains(out, "Committed") || !strings.Contains(out, "feat/ship") {
		t.Errorf("ship commit printed:\n%s", out)
	}
	who := testutil.Git(t, f.worktree, "log", "-1", "--format=%an|%ae|%cn|%ce")
	if who != "Ship Tester|ship-tester|Ship Tester|ship-tester" {
		t.Errorf("the commit is by %q, want the person's own identity from their config", who)
	}
	if msg := testutil.Git(t, f.worktree, "log", "-1", "--format=%B"); msg != "Add the hello file" {
		t.Errorf("the commit message is %q, want exactly the message given, with nothing added", msg)
	}
	if raw := testutil.Git(t, f.worktree, "cat-file", "-p", "HEAD"); !strings.Contains(raw, "gpgsig") {
		t.Errorf("the commit is not signed, so the person's commit.gpgsign was not used:\n%s", raw)
	}
	if status := testutil.Git(t, f.worktree, "status", "--porcelain"); status != "" {
		t.Errorf("the worktree is not clean after the commit:\n%s", status)
	}
	if out, err := tr.run("ship", "commit", "-s", f.session, "-m", "again", "--json"); err == nil {
		t.Errorf("a commit with nothing to commit succeeded:\n%s", out)
	} else if code, _ := jsonFailure(t, out); code != "nothing_to_commit" {
		t.Errorf("a commit with nothing to commit failed with %q, want nothing_to_commit", code)
	}
	tip := testutil.Git(t, f.worktree, "rev-parse", "HEAD")

	// The merge fast-forwards main in the main checkout.
	out = tr.ok("ship", "merge", "-s", f.session)
	if !strings.Contains(out, "Merged feat/ship into main") || !strings.Contains(out, "fast-forward") {
		t.Errorf("ship merge printed:\n%s", out)
	}
	if got := testutil.Git(t, f.repo, "rev-parse", "main"); got != tip {
		t.Fatalf("main is at %s after the merge, want the branch's %s", got, tip)
	}
	if got := readFile(t, filepath.Join(f.repo, "hello.txt")); got != "hello from the agent\n" {
		t.Errorf("the main checkout's hello.txt = %q", got)
	}

	// Both sides change README: the merge conflicts and is aborted.
	f.write("README", "the agent's README\n")
	tr.ok("ship", "commit", "-s", f.session, "-m", "Rewrite the README")
	if err := os.WriteFile(filepath.Join(f.repo, "README"), []byte("the person's README\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.repo, "commit", "-q", "-am", "The person's README")
	before := gitState(t, f.repo)
	out, err := tr.run("ship", "merge", "-s", f.session, "--json")
	if err == nil {
		t.Fatalf("a conflicting merge succeeded:\n%s", out)
	}
	if code, files := jsonFailure(t, out); code != "merge_conflict" || len(files) != 1 || files[0] != "README" {
		t.Errorf("the conflicting merge failed with %q naming %v, want merge_conflict naming README", code, files)
	}
	out, _ = tr.run("ship", "merge", "-s", f.session)
	if !strings.Contains(out, "README") || !strings.Contains(out, "aborted") {
		t.Errorf("the conflicting merge's message does not name the file and say it was aborted:\n%s", out)
	}
	if after := gitState(t, f.repo); after != before {
		t.Errorf("the aborted merge changed the main checkout.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if status := testutil.Git(t, f.repo, "status", "--porcelain"); status != "" {
		t.Errorf("the main checkout is not clean after the aborted merge:\n%s", status)
	}
	if _, err := os.Stat(filepath.Join(f.repo, ".git", "MERGE_HEAD")); err == nil {
		t.Errorf("the aborted merge left a merge in progress")
	}
	if got := readFile(t, filepath.Join(f.repo, "README")); got != "the person's README\n" {
		t.Errorf("the aborted merge left README as %q", got)
	}

	// A dirty main checkout is refused before anything is merged.
	if err := os.WriteFile(filepath.Join(f.repo, "hello.txt"), []byte("edited by the person\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = tr.run("ship", "merge", "-s", f.session, "--json")
	if err == nil {
		t.Fatalf("a merge into a dirty main checkout succeeded:\n%s", out)
	}
	if code, _ := jsonFailure(t, out); code != "checkout_dirty" {
		t.Errorf("the merge into a dirty checkout failed with %q, want checkout_dirty", code)
	}
	if got := readFile(t, filepath.Join(f.repo, "hello.txt")); got != "edited by the person\n" {
		t.Errorf("the refused merge touched the person's edit: %q", got)
	}
	testutil.Git(t, f.repo, "checkout", "--", "hello.txt")

	// A push sends nothing until it is confirmed. The CLI's stdin is not a
	// terminal here, so without --yes it cannot ask.
	branchTip := testutil.Git(t, f.worktree, "rev-parse", "HEAD")
	out, err = tr.run("ship", "push", "-s", f.session)
	if refs := testutil.Git(t, f.remote, "for-each-ref", "--format=%(refname)", "refs/heads/feat/"); refs != "" {
		t.Fatalf("the unconfirmed push reached the origin: %s", refs)
	}
	if err == nil || !strings.Contains(out, "Nothing was sent") || !strings.Contains(out, "push feat/ship at "+branchTip[:7]+" to origin") {
		t.Fatalf("ship push without --yes = %v:\n%s\nwant the list of what it would send and a refusal", err, out)
	}
	out = tr.ok("ship", "push", "-s", f.session, "--yes")
	if !strings.Contains(out, "Pushed feat/ship at "+branchTip[:7]+" to origin") {
		t.Errorf("ship push printed:\n%s", out)
	}
	if got := testutil.Git(t, f.remote, "rev-parse", "refs/heads/feat/ship"); got != branchTip {
		t.Fatalf("the origin's feat/ship is %s, want %s", got, branchTip)
	}
	if up := testutil.Git(t, f.worktree, "rev-parse", "--abbrev-ref", "feat/ship@{upstream}"); up != "origin/feat/ship" {
		t.Errorf("the branch's upstream is %q, want origin/feat/ship", up)
	}

	// The pull request, through the fake gh.
	if out := tr.ok("ship", "status", "-s", f.session, "--refresh"); !strings.Contains(out, "no pull request is known") {
		t.Errorf("ship status before the pull request printed:\n%s", out)
	}
	out = tr.ok("ship", "pr", "-s", f.session, "--yes", "--draft", "--title", "Add the hello file", "--body", "Adds hello.txt.")
	if !strings.Contains(out, "Opened pull request #7") {
		t.Errorf("ship pr printed:\n%s", out)
	}
	var create, view []string
	for _, c := range f.ghCalls() {
		switch {
		case len(c) > 1 && c[0] == "pr" && c[1] == "create":
			create = c
		case len(c) > 1 && c[0] == "pr" && c[1] == "view":
			view = c
		}
	}
	wantCreate := []string{"pr", "create", "--head", "feat/ship", "--base", "main", "--title", "Add the hello file", "--body", "Adds hello.txt.", "--draft"}
	if strings.Join(create, "\x1f") != strings.Join(wantCreate, "\x1f") {
		t.Errorf("gh was called with %q, want %q", create, wantCreate)
	}
	wantView := []string{"pr", "view", "feat/ship", "--json", "state,statusCheckRollup,url,number"}
	if strings.Join(view, "\x1f") != strings.Join(wantView, "\x1f") {
		t.Errorf("gh pr view was called with %q, want %q", view, wantView)
	}
	out = tr.ok("ship", "status", "-s", f.session)
	if !strings.Contains(out, "PR #7 open pending") || !strings.Contains(out, "1 passed, 0 failed, 1 pending") {
		t.Errorf("ship status printed:\n%s", out)
	}

	// The listings carry it.
	rows := worktreeRows(t, f.base)
	var pr map[string]any
	for _, r := range rows {
		if r["session"] == f.session {
			pr, _ = r["pr"].(map[string]any)
		}
	}
	if pr == nil || pr["number"] != float64(7) || pr["state"] != "open" || pr["checks"] != "pending" {
		t.Errorf("worktree ls --json carries pr %v, want #7 open pending", pr)
	}
	var sessions []struct {
		Name     string `json:"name"`
		Worktree *struct {
			PR *struct {
				Number int    `json:"number"`
				Checks string `json:"checks"`
			} `json:"pr"`
		} `json:"worktree"`
	}
	lsOut := tr.ok("ls", "--json")
	if err := json.Unmarshal([]byte(lsOut), &sessions); err != nil {
		t.Fatalf("ls --json printed no JSON: %v\n%s", err, lsOut)
	}
	listed := false
	for _, s := range sessions {
		if s.Name == f.session && s.Worktree != nil && s.Worktree.PR != nil && s.Worktree.PR.Number == 7 && s.Worktree.PR.Checks == "pending" {
			listed = true
		}
	}
	if !listed {
		t.Errorf("ls --json does not carry the pull request on %s:\n%s", f.session, lsOut)
	}

	// The badge on the rail, and the poll that moves it.
	writeConfig(t, f.base, "[appearance.sidebar]\nenabled = true\n")
	term := startIn(t, f.base, startOpts{args: []string{"attach", f.session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	waitText(t, term, "the pull request badge on the agent's row", "PR #7 open pending")
	saveFrame(t, term, "ship-badge-pending")
	if err := os.WriteFile(filepath.Join(f.fakes, "pr.json"), []byte(shipCannedPass), 0o644); err != nil {
		t.Fatal(err)
	}
	waitText(t, term, "the badge after the checks passed", "PR #7 open pass")
	saveFrame(t, term, "ship-badge-pass")
	tr.log("the rail showed PR #7 open pending, then PR #7 open pass after gh's answer changed")
}

// TestShipPushFromAPaneAsksThePerson runs ship push from a shell inside a
// pane, the way an agent would. The daemon does not count that caller as the
// person, so the push waits on a question in the Inbox, which pops up on the
// client showing the pane. Nothing is pushed until the person answers allow
// with a key, and then the branch reaches the origin.
//
// Negative control (NEGATIVE_CONTROLS.md): with shipCallerIsPerson answering
// true for every caller, the push goes out with no question, and the wait for
// the question fails.
func TestShipPushFromAPaneAsksThePerson(t *testing.T) {
	f := newShipFixture(t)
	f.write("note.txt", "a note\n")
	f.tr.ok("ship", "commit", "-s", f.session, "-m", "Add a note")
	tip := testutil.Git(t, f.worktree, "rev-parse", "HEAD")

	term := startIn(t, f.base, startOpts{args: []string{"attach", f.session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	line := fmt.Sprintf("%s ship push --yes --wait 60s > %s 2>&1\n", tuiosBin, filepath.Join(f.fakes, "pane-push.txt"))
	f.tr.ok("send-text", "-s", f.session, "-w", "0", line)

	question := "Push feat/ship (" + tip[:7] + ") to origin?"
	waitText(t, term, "the push question in the Inbox", question)
	saveFrame(t, term, "ship-push-question")
	if refs := testutil.Git(t, f.remote, "for-each-ref", "--format=%(refname)", "refs/heads/feat/"); refs != "" {
		t.Fatalf("the push went out before the person answered: %s", refs)
	}
	time.Sleep(answerSettle)
	if err := term.SendKeys("1"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := exec.Command("git", "--git-dir", f.remote, "rev-parse", "-q", "--verify", "refs/heads/feat/ship").Output()
		if err == nil && strings.TrimSpace(string(got)) == tip {
			break
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(filepath.Join(f.fakes, "pane-push.txt"))
			t.Fatalf("the allowed push never reached the origin. The pane's command printed:\n%s\n%s", data, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	f.tr.log("the push from the pane waited on the Inbox question %q and went out after the person allowed it", question)
}

// TestFanKeepMergesTheKeptAttempt keeps one attempt of a fan with --merge:
// its commit lands on main in the main checkout, and the other attempt is
// removed. With the main checkout dirty, the keep is refused before any
// sibling is removed.
//
// Negative control (NEGATIVE_CONTROLS.md): with the merge taken out of
// verbKeepFan, main stays where it was and the check on it fails.
func TestFanKeepMergesTheKeptAttempt(t *testing.T) {
	base, repo := fanFixture(t)
	tr := newCheckpointTranscript(t, base, repo)
	tr.ok("new", "plain", "--detach")
	tr.ok("fan", "2", "--agent", "claude", "--repo", repo, "--name", "try/ship", "Do the thing.")
	rows := worktreeRows(t, base, "--group", "try/ship")
	if len(rows) != 2 {
		t.Fatalf("worktree ls = %v, want 2 rows", rows)
	}
	var winner string
	for _, r := range rows {
		if r["session"] == "repo-try-ship-2" {
			winner = r["path"].(string)
		}
	}
	if err := os.WriteFile(filepath.Join(winner, "done.txt"), []byte("the winning attempt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr.ok("ship", "commit", "-s", "repo-try-ship-2", "-m", "Do the thing", "--force")
	tip := testutil.Git(t, winner, "rev-parse", "HEAD")

	// A dirty main checkout refuses the merge, and nothing is removed.
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tr.run("fan", "keep", "repo-try-ship-2", "--merge"); err == nil || !strings.Contains(out, "uncommitted") {
		t.Fatalf("fan keep --merge into a dirty checkout = %v:\n%s\nwant a refusal", err, out)
	}
	if rows := worktreeRows(t, base, "--group", "try/ship"); len(rows) != 2 {
		t.Fatalf("the refused keep removed a sibling: %v", rows)
	}
	testutil.Git(t, repo, "checkout", "--", "README")

	out := tr.ok("fan", "keep", "repo-try-ship-2", "--merge")
	for _, want := range []string{"Kept repo-try-ship-2", "Merged try/ship-2 into main", "Killed session 'repo-try-ship'"} {
		if !strings.Contains(out, want) {
			t.Errorf("fan keep --merge output lacks %q:\n%s", want, out)
		}
	}
	if got := testutil.Git(t, repo, "rev-parse", "main"); got != tip {
		t.Errorf("main is at %s after fan keep --merge, want the kept attempt's %s", got, tip)
	}
	if rows := worktreeRows(t, base, "--group", "try/ship"); len(rows) != 1 || rows[0]["session"] != "repo-try-ship-2" {
		t.Errorf("worktree ls after keep = %v, want only the kept attempt", rows)
	}
}
