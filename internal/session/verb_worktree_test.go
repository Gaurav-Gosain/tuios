package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// Every test here works on a repository testutil.GitRepo made under the test's
// own temporary directory, with tuios's worktree directory pointed at another.
// Nothing here touches any other repository.

// worktreeFixture is a daemon, its socket, and a throwaway repository, with
// tuios's worktree directory redirected under the test.
func worktreeFixture(t *testing.T) (*Daemon, string, string) {
	t.Helper()
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(t.TempDir(), "worktrees"))
	d, sp := startTestDaemon(t)
	return d, sp, repo
}

func jsonParams(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// newWorktreeCall makes a worktree session and returns its result.
func newWorktreeCall(t *testing.T, c *verbConn, repo, branch string, extra map[string]any) map[string]any {
	t.Helper()
	params := map[string]any{"repo": repo, "branch": branch}
	for k, v := range extra {
		params[k] = v
	}
	return result(t, c.call(t, `{"id":1,"verb":"new-worktree","params":`+jsonParams(params)+`}`))
}

// fakeClaudeOnPath puts a script named claude on PATH that echoes what it reads, so
// the fan verb can start a harness the registry recognises without the real
// one being installed. The script's process name is claude, which is what the
// claude-code manifest detects.
func fakeClaudeOnPath(t *testing.T) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho ready\nwhile IFS= read -r line; do echo \"GOT: $line\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestNewWorktreeCreatesTheWorktreeAndASessionInIt(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)

	res := newWorktreeCall(t, c, repo, "feat/retry", map[string]any{"base": "main"})
	if res["session"] != "repo-feat-retry" {
		t.Errorf("session = %v, want repo-feat-retry", res["session"])
	}
	if res["created_branch"] != true {
		t.Errorf("created_branch = %v, want true for a branch that did not exist", res["created_branch"])
	}
	path, _ := res["path"].(string)
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		t.Fatalf("the worktree directory %q is not there: %v", path, err)
	}
	if !worktree.BranchExists(repo, "feat/retry") {
		t.Error("the branch was not created in the repository")
	}

	sess := d.manager.GetSession("repo-feat-retry")
	if sess == nil {
		t.Fatal("the session does not exist in the daemon")
	}
	info := sess.Worktree()
	if info == nil {
		t.Fatal("the session carries no worktree record")
	}
	if info.Repo != "repo" || info.Branch != "feat/retry" || info.Path != path || !info.Managed || info.Base != "main" {
		t.Errorf("record = %+v, want repo/feat/retry at %s, managed, base main", info, path)
	}
	// The listing carries it too: that is what the rail groups by.
	listed := result(t, c.call(t, `{"id":2,"verb":"list-sessions"}`))
	sessions, _ := listed["sessions"].([]any)
	var found map[string]any
	for _, s := range sessions {
		row := s.(map[string]any)
		if row["name"] == "repo-feat-retry" {
			found = row
		}
	}
	if found == nil {
		t.Fatal("list-sessions does not list the session")
	}
	wt, _ := found["worktree"].(map[string]any)
	if wt == nil || wt["branch"] != "feat/retry" || wt["repo"] != "repo" {
		t.Errorf("list-sessions worktree = %v, want branch feat/retry of repo", found["worktree"])
	}
}

func TestNewWorktreeChecksOutABranchThatExists(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	testutil.Git(t, repo, "branch", "existing")
	c := dialVerb(t, sp)
	res := newWorktreeCall(t, c, repo, "existing", nil)
	if res["created_branch"] != false {
		t.Errorf("created_branch = %v, want false for a branch that existed", res["created_branch"])
	}
}

func TestNewWorktreeRefusesABadBranchOrRepo(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"new-worktree","params":{"repo":"`+repo+`","branch":"bad..name"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("bad branch: code = %q, want %q", code, ErrVerbInvalidParams)
	}
	resp = c.call(t, `{"id":2,"verb":"new-worktree","params":{"repo":"`+t.TempDir()+`","branch":"ok"}}`)
	if code := errCode(t, resp); code != ErrVerbGitFailed {
		t.Errorf("not a repo: code = %q, want %q", code, ErrVerbGitFailed)
	}
	// A second worktree on the same branch is refused: the directory is there.
	newWorktreeCall(t, c, repo, "twice", nil)
	resp = c.call(t, `{"id":3,"verb":"new-worktree","params":{"repo":"`+repo+`","branch":"twice"}}`)
	if code := errCode(t, resp); code != ErrVerbGitFailed {
		t.Errorf("same branch twice: code = %q, want %q", code, ErrVerbGitFailed)
	}
}

func TestASessionStartedInAWorktreeIsDetected(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	path := filepath.Join(t.TempDir(), "byhand")
	testutil.Git(t, repo, "worktree", "add", "-q", "-b", "by/hand", path)
	c := dialVerb(t, sp)

	// A plain new-session whose first window starts in the worktree: nothing
	// names a worktree, and the daemon works it out from the directory.
	result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"plain","cwd":"`+path+`"}}`))
	listed := result(t, c.call(t, `{"id":2,"verb":"list-worktrees"}`))
	rows, _ := listed["worktrees"].([]any)
	if len(rows) != 1 {
		t.Fatalf("list-worktrees = %v, want the one detected session", listed)
	}
	row := rows[0].(map[string]any)
	if row["session"] != "plain" || row["branch"] != "by/hand" || row["repo"] != "repo" || row["managed"] != false {
		t.Errorf("row = %v, want plain on by/hand of repo, not managed", row)
	}
	// The main checkout is not a worktree session.
	result(t, c.call(t, `{"id":3,"verb":"new-session","params":{"name":"main","cwd":"`+repo+`"}}`))
	listed = result(t, c.call(t, `{"id":4,"verb":"list-worktrees"}`))
	if n, _ := listed["total"].(float64); n != 1 {
		t.Errorf("a session in the main checkout was listed as a worktree: %v", listed)
	}
}

func TestADetectedRecordFollowsTheDirectoryAndAManagedOneStays(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	path := filepath.Join(t.TempDir(), "byhand")
	testutil.Git(t, repo, "worktree", "add", "-q", "-b", "by/hand", path)
	c := dialVerb(t, sp)
	result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"plain","cwd":"`+path+`"}}`))
	plain := d.manager.GetSession("plain")
	if plain.Worktree() == nil {
		t.Fatal("the detected session has no record")
	}
	// The shell moved out of the worktree: the record goes with it.
	plain.refreshWorktree(t.TempDir())
	if plain.Worktree() != nil {
		t.Error("a detected record survived the directory leaving the worktree")
	}
	plain.refreshWorktree(path)
	if plain.Worktree() == nil {
		t.Error("moving back into the worktree did not restore the record")
	}

	newWorktreeCall(t, c, repo, "managed", nil)
	managed := d.manager.GetSession("repo-managed")
	managed.refreshWorktree(t.TempDir())
	if info := managed.Worktree(); info == nil || info.Branch != "managed" {
		t.Errorf("a managed record was changed by a directory change: %+v", info)
	}
}

func TestARemovedWorktreeDirectoryIsListedGoneAndTheSessionIsKept(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	res := newWorktreeCall(t, c, repo, "doomed", nil)
	path := res["path"].(string)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	listed := result(t, c.call(t, `{"id":2,"verb":"list-worktrees"}`))
	rows, _ := listed["worktrees"].([]any)
	if len(rows) != 1 {
		t.Fatalf("list-worktrees = %v, want the session still listed", listed)
	}
	if row := rows[0].(map[string]any); row["gone"] != true {
		t.Errorf("row = %v, want gone true", row)
	}
	if d.manager.GetSession("repo-doomed") == nil {
		t.Fatal("the session was killed because its directory went away")
	}
	// Removing it now skips git, kills the session, and says what git still
	// holds rather than pruning it.
	removed := result(t, c.call(t, `{"id":3,"verb":"remove-worktree","params":{"session":"repo-doomed"}}`))
	if removed["gone"] != true || removed["session_killed"] != true {
		t.Errorf("remove of a gone worktree = %v, want gone and session_killed", removed)
	}
	note, _ := removed["note"].(string)
	if !strings.Contains(note, "git worktree prune") {
		t.Errorf("note = %q, want it to name git worktree prune as the person's step", note)
	}
	if out := testutil.Git(t, repo, "worktree", "list"); !strings.Contains(out, "doomed") {
		t.Errorf("the daemon pruned the worktree itself: %s", out)
	}
}

func TestRemoveWorktreeRefusesUncommittedWorkWithoutForceOrStash(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	res := newWorktreeCall(t, c, repo, "dirty", nil)
	path := res["path"].(string)
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := c.call(t, `{"id":2,"verb":"remove-worktree","params":{"session":"repo-dirty"}}`)
	if code := errCode(t, resp); code != ErrVerbWorktreeDirty {
		t.Fatalf("code = %q, want %q", code, ErrVerbWorktreeDirty)
	}
	e := resp["error"].(map[string]any)
	if msg, _ := e["message"].(string); !strings.Contains(msg, "1 uncommitted change") || !strings.Contains(msg, "Nothing was removed") {
		t.Errorf("message = %q, want the count and that nothing was removed", msg)
	}
	if _, err := os.Stat(filepath.Join(path, "work.txt")); err != nil {
		t.Fatalf("the refusal still removed the work: %v", err)
	}
	if d.manager.GetSession("repo-dirty") == nil {
		t.Fatal("the refusal still killed the session")
	}
	if !worktree.BranchExists(repo, "dirty") {
		t.Fatal("the refusal still deleted the branch")
	}
}

func TestRemoveWorktreeWithStashKeepsTheWorkInTheRepository(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	res := newWorktreeCall(t, c, repo, "stashed", nil)
	path := res["path"].(string)
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed := result(t, c.call(t, `{"id":2,"verb":"remove-worktree","params":{"session":"repo-stashed","stash":true}}`))
	if removed["stashed"] != true || removed["discarded"] != false || removed["session_killed"] != true {
		t.Errorf("result = %v, want stashed, not discarded, session killed", removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if list := testutil.Git(t, repo, "stash", "list"); !strings.Contains(list, "tuios: stashed") {
		t.Errorf("the stash does not hold the work: %q", list)
	}
	if !worktree.BranchExists(repo, "stashed") {
		t.Error("the branch was deleted")
	}
	if d.manager.GetSession("repo-stashed") != nil {
		t.Error("the session is still there")
	}
}

func TestRemoveWorktreeWithForceDiscardsAndKeepsTheBranch(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	res := newWorktreeCall(t, c, repo, "forced", nil)
	path := res["path"].(string)
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	removed := result(t, c.call(t, `{"id":2,"verb":"remove-worktree","params":{"session":"repo-forced","force":true,"keep_session":true}}`))
	if removed["discarded"] != true || removed["session_killed"] != false {
		t.Errorf("result = %v, want discarded and the session kept", removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the worktree is still there: %v", err)
	}
	if !worktree.BranchExists(repo, "forced") {
		t.Error("force deleted the branch; only the worktree may go")
	}
}

func TestRemoveWorktreeRefusesASessionThatIsNotOne(t *testing.T) {
	d, sp, _ := worktreeFixture(t)
	c := dialVerb(t, sp)
	// The window starts in a plain directory on purpose: the daemon's own
	// directory may be a worktree, as it is when this test runs in one.
	result(t, c.call(t, `{"id":1,"verb":"new-session","params":{"name":"plain","cwd":"`+t.TempDir()+`"}}`))
	resp := c.call(t, `{"id":2,"verb":"remove-worktree","params":{"session":"plain"}}`)
	if code := errCode(t, resp); code != ErrVerbNotWorktree {
		t.Errorf("code = %q, want %q", code, ErrVerbNotWorktree)
	}
	if d.manager.GetSession("plain") == nil {
		t.Error("the refusal killed the session")
	}
}

// waitPromptStatus polls a session's record until the prompt reaches status.
func waitPromptStatus(t *testing.T, sess *Session, status string) *WorktreeInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if info := sess.Worktree(); info != nil && info.PromptStatus == status {
			return info
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the prompt never reached %q: %+v", status, sess.Worktree())
	return nil
}

func TestFanStartsAnAgentInEveryWorktreeAndTypesThePromptWhenReady(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	c := dialVerb(t, sp)

	res := result(t, c.call(t, `{"id":1,"verb":"fan","params":{"count":2,"agent":"claude","prompt":"Add a retry to the client.","repo":"`+repo+`","base":"main"}}`))
	if res["group"] != "fan/add-retry-client" || res["command"] != "claude" || res["agent"] != "claude-code" {
		t.Errorf("fan = %v, want group fan/add-retry-client running claude", res)
	}
	rows, _ := res["sessions"].([]any)
	if len(rows) != 2 {
		t.Fatalf("sessions = %v, want 2", res["sessions"])
	}
	first := rows[0].(map[string]any)
	second := rows[1].(map[string]any)
	if first["branch"] != "fan/add-retry-client" || second["branch"] != "fan/add-retry-client-2" {
		t.Errorf("branches = %v, %v; want the stem and stem-2", first["branch"], second["branch"])
	}

	sess := d.manager.GetSession(first["session"].(string))
	if sess == nil {
		t.Fatal("the first fan session does not exist")
	}
	windowID := first["window_id"].(string)
	info := sess.Worktree()
	if info == nil || info.Group != "fan/add-retry-client" || info.PromptStatus != PromptPending || info.Prompt != "Add a retry to the client." {
		t.Fatalf("record = %+v, want the group and a pending prompt", info)
	}

	// The agent is asking something: the prompt must not be typed over it.
	if err := sess.SetDaemonWindowAgentState(windowID, AgentStateNeedsInput, "Asks you to trust the folder."); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if info := sess.Worktree(); info.PromptStatus != PromptPending {
		t.Fatalf("the prompt was typed at an agent that needs input: %+v", info)
	}
	// The agent is at its prompt: now it is typed.
	if err := sess.SetDaemonWindowAgentState(windowID, AgentStateIdle, ""); err != nil {
		t.Fatal(err)
	}
	info = waitPromptStatus(t, sess, PromptSent)
	if info.PromptAt == 0 {
		t.Error("prompt_at is zero after the prompt was sent")
	}
	pty, err := d.resolvePTYForTarget(sess, windowID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if strings.Contains(pty.CaptureContent(true, false), "GOT: Add a retry to the client.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent never received the prompt:\n%s", pty.CaptureContent(true, false))
		}
		time.Sleep(50 * time.Millisecond)
	}

	listed := result(t, c.call(t, `{"id":2,"verb":"list-worktrees","params":{"group":"fan/add-retry-client"}}`))
	if n, _ := listed["total"].(float64); n != 2 {
		t.Errorf("list-worktrees by group = %v, want both sessions", listed)
	}
}

func TestFanGivesUpOnAPromptWhenTheAgentIsNeverReady(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	fakeClaudeOnPath(t)
	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"fan","params":{"count":1,"agent":"claude","prompt":"Wait.","repo":"`+repo+`","name":"slow","ready_timeout":300}}`))
	rows, _ := res["sessions"].([]any)
	first := rows[0].(map[string]any)
	sess := d.manager.GetSession(first["session"].(string))
	if err := sess.SetDaemonWindowAgentState(first["window_id"].(string), AgentStateWorking, ""); err != nil {
		t.Fatal(err)
	}
	info := waitPromptStatus(t, sess, PromptNotSent)
	if !strings.Contains(info.PromptNote, "send-text") {
		t.Errorf("note = %q, want it to say how to send the prompt by hand", info.PromptNote)
	}
}

func TestFanRefusesAnAgentNobodyKnowsAndABadCount(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"fan","params":{"count":2,"agent":"not-an-agent","prompt":"x","repo":"`+repo+`"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("unknown agent: code = %q, want %q", code, ErrVerbInvalidParams)
	}
	e := resp["error"].(map[string]any)
	hint, _ := e["hint"].(map[string]any)
	if accepted, _ := hint["accepted"].([]any); len(accepted) < 20 {
		t.Errorf("the refusal does not list the harnesses: %v", hint)
	}
	for _, count := range []int{0, fanMaxCount + 1} {
		resp = c.call(t, fmt.Sprintf(`{"id":2,"verb":"fan","params":{"count":%d,"agent":"claude","prompt":"x","repo":"%s"}}`, count, repo))
		if code := errCode(t, resp); code != ErrVerbInvalidParams {
			t.Errorf("count %d: code = %q, want %q", count, code, ErrVerbInvalidParams)
		}
	}
	listed := result(t, c.call(t, `{"id":3,"verb":"list-worktrees"}`))
	if n, _ := listed["total"].(float64); n != 0 {
		t.Errorf("a refused fan still created worktrees: %v", listed)
	}
}

func TestFanBranchesSkipNamesThatExist(t *testing.T) {
	repo := testutil.GitRepo(t)
	t.Setenv("TUIOS_WORKTREE_DIR", filepath.Join(t.TempDir(), "worktrees"))
	testutil.Git(t, repo, "branch", "try")
	testutil.Git(t, repo, "branch", "try-3")
	got := fanBranches(repo, "try", 3)
	want := []string{"try-2", "try-4", "try-5"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("fanBranches = %v, want %v", got, want)
	}
}

func TestFanStemReadsLikeThePrompt(t *testing.T) {
	cases := map[string]string{
		"Add a retry to the client.":                       "fan/add-retry-client",
		"Please fix the flaky test in the session package": "fan/fix-flaky-test-session",
		"":    "fan/prompt",
		"!!!": "fan/prompt",
	}
	for in, want := range cases {
		if got := fanStem(in); got != want {
			t.Errorf("fanStem(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestClientSyncCannotClearTheWorktreeRecord pins the record as daemon-owned: a
// client snapshot that omits it, which is every client sync from a client that
// predates it, leaves it in place.
func TestClientSyncCannotClearTheWorktreeRecord(t *testing.T) {
	sess := newTestSession(t)
	if _, err := sess.AddDaemonWindowWith(NewWindowOptions{Cwd: t.TempDir()}, nil); err != nil {
		t.Fatalf("AddDaemonWindowWith: %v", err)
	}
	record := &WorktreeInfo{Info: worktree.Info{Repo: "api", Branch: "x", Path: "/wt/x"}, Managed: true}
	if err := sess.SetWorktree(record); err != nil {
		t.Fatal(err)
	}
	incoming := sess.GetState()
	incoming.BaseVersion = incoming.Version
	incoming.Worktree = nil
	sess.UpdateState(incoming)
	if got := sess.Worktree(); got == nil || got.Branch != "x" {
		t.Fatalf("worktree record after a client sync = %+v, want it kept", got)
	}
}
