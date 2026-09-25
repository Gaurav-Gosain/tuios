package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
