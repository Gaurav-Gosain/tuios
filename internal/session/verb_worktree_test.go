package session

import (
	"encoding/json"
	"fmt"
	"maps"
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
	maps.Copy(params, extra)
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

// TestTheStateSnapshotDoesNotShareItsWorktreeRecord: the snapshot a mutation
// publishes is gob-encoded for the wire after the state lock is released, and
// setPromptStatus writes the prompt fields through the record's pointer. A
// shared pointer makes those two a data race, and lets a caller that holds a
// snapshot write into the canonical state without the lock.
//
// NEGATIVE CONTROL: remove the Worktree copy from snapshotStateLocked and the
// write through the snapshot reaches the session.
func TestTheStateSnapshotDoesNotShareItsWorktreeRecord(t *testing.T) {
	sess := newTestSession(t)
	record := &WorktreeInfo{Info: worktree.Info{Repo: "api", Branch: "x", Path: "/wt/x"}}
	if err := sess.SetWorktree(record); err != nil {
		t.Fatal(err)
	}

	snap := sess.GetState()
	if snap.Worktree == nil {
		t.Fatal("the snapshot carries no worktree record")
	}
	if snap.Worktree == record {
		t.Error("the snapshot shares the record it was given, so the caller can write into the state")
	}
	snap.Worktree.PromptStatus = PromptSent

	if got := sess.Worktree(); got.PromptStatus != "" {
		t.Errorf("the session holds prompt status %q after a write through a snapshot, want none", got.PromptStatus)
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

// TestFanRefusesAnAgentThatIsNotInstalledAndABadCount: a name that is neither
// installed nor a harness is refused, and the refusal lists the harnesses in
// case it was one spelled wrong. fan starts any installed program since agents
// took argv, so the list is what exists, not the only accepted values.
func TestFanRefusesAnAgentThatIsNotInstalledAndABadCount(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"fan","params":{"count":2,"agent":"not-an-agent","prompt":"x","repo":"`+repo+`"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("unknown agent: code = %q, want %q", code, ErrVerbInvalidParams)
	}
	e := resp["error"].(map[string]any)
	hint, _ := e["hint"].(map[string]any)
	if available, _ := hint["available"].([]any); len(available) < 20 {
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
