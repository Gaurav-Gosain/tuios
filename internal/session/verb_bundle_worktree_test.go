package session

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuios/internal/worktree"
)

// readWholeBundle reads a transfer to its end and returns the first reply and
// the bytes.
func readWholeBundle(t *testing.T, c *verbConn, session string, full bool) (map[string]any, []byte) {
	t.Helper()
	first := result(t, c.call(t, `{"id":1,"verb":"bundle-worktree","params":`+jsonParams(map[string]any{"session": session, "full": full})+`}`))
	var data []byte
	reply := first
	for i := 0; ; i++ {
		chunk, err := base64.StdEncoding.DecodeString(reply["content"].(string))
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, chunk...)
		if reply["done"] == true {
			break
		}
		if i > 1000 {
			t.Fatal("the transfer never ended")
		}
		reply = result(t, c.call(t, `{"id":2,"verb":"bundle-worktree","params":`+jsonParams(map[string]any{
			"token": first["token"], "offset": reply["next"],
		})+`}`))
	}
	return first, data
}

func TestBundleWorktreeCarriesCommitsAndUncommittedWork(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	created := newWorktreeCall(t, c, repo, "feat/pull", map[string]any{"base": "main"})
	path := created["path"].(string)
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "commit", "-q", "-am", "work")
	if err := os.WriteFile(filepath.Join(path, "notes.txt"), []byte("not committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, _ := worktree.HeadCommit(path)

	// Small chunks, so the transfer takes several calls.
	old := bundleChunkBytes
	bundleChunkBytes = 64
	t.Cleanup(func() { bundleChunkBytes = old })

	first, data := readWholeBundle(t, c, "repo-feat-pull", false)
	if first["branch"] != "feat/pull" || first["head"] != head || first["full"] != false || first["changes"] != float64(1) {
		t.Errorf("first reply = %v, want branch feat/pull at %s, a thin bundle and one change", first, head)
	}
	if int(first["size"].(float64)) != len(data) {
		t.Fatalf("read %d bytes, size says %d", len(data), int(first["size"].(float64)))
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != first["sha256"] {
		t.Error("the bytes read do not match the transfer's sha256")
	}
	// The transfer is gone after its last chunk.
	if n := d.bundles.count(); n != 0 {
		t.Errorf("%d transfers open after the last chunk", n)
	}

	// The bytes are a bundle and a patch another checkout can use.
	nb := int(first["bundle_bytes"].(float64))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b"), data[:nb], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p"), data[nb:], 0o600); err != nil {
		t.Fatal(err)
	}
	receiver := filepath.Join(t.TempDir(), "receiver")
	testutil.Git(t, filepath.Dir(receiver), "clone", "-q", repo, receiver)
	if !worktree.HasCommit(receiver, first["base_commit"].(string)) {
		t.Fatal("the receiver lacks the base commit")
	}
	if err := worktree.FetchBundle(receiver, filepath.Join(dir, "b"), "feat/pull", "feat/pull"); err != nil {
		t.Fatalf("fetch the bundle: %v", err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if _, err := worktree.Add(receiver, wt, "feat/pull", ""); err != nil {
		t.Fatal(err)
	}
	if err := worktree.ApplyPatch(wt, filepath.Join(dir, "p")); err != nil {
		t.Fatalf("apply the patch: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "notes.txt")); string(got) != "not committed\n" {
		t.Errorf("notes.txt = %q", got)
	}
}

func TestBundleWorktreeIsReadOnlyByTheConnectionThatMadeIt(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	newWorktreeCall(t, c, repo, "feat/own", map[string]any{"base": "main"})
	old := bundleChunkBytes
	bundleChunkBytes = 16
	t.Cleanup(func() { bundleChunkBytes = old })

	owner := dialVerb(t, sp)
	first := result(t, owner.call(t, `{"id":1,"verb":"bundle-worktree","params":{"session":"repo-feat-own","full":true}}`))
	if first["done"] == true {
		t.Fatal("the transfer fit one chunk, so the test proves nothing")
	}
	other := dialVerb(t, sp)
	resp := other.call(t, `{"id":2,"verb":"bundle-worktree","params":`+jsonParams(map[string]any{"token": first["token"], "offset": first["next"]})+`}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Errorf("another connection read the transfer: code = %q", code)
	}

	// The owner going away ends the transfer and removes its files.
	var dir string
	d.bundles.mu.Lock()
	for _, tr := range d.bundles.open {
		dir = tr.dir
	}
	d.bundles.mu.Unlock()
	if dir == "" {
		t.Fatal("no transfer is open")
	}
	_ = owner.conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	for d.bundles.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the transfer outlived the connection that made it")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the transfer's files are still at %s", dir)
	}
}

func TestBundleWorktreeRelease(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	newWorktreeCall(t, c, repo, "feat/rel", map[string]any{"base": "main"})
	old := bundleChunkBytes
	bundleChunkBytes = 16
	t.Cleanup(func() { bundleChunkBytes = old })
	first := result(t, c.call(t, `{"id":1,"verb":"bundle-worktree","params":{"session":"repo-feat-rel","full":true}}`))
	res := result(t, c.call(t, `{"id":2,"verb":"bundle-worktree","params":`+jsonParams(map[string]any{"token": first["token"], "release": true})+`}`))
	if res["released"] != true || d.bundles.count() != 0 {
		t.Errorf("release = %v with %d open, want the transfer gone", res, d.bundles.count())
	}
}

func TestBundleWorktreeRefusesASessionThatIsNotAWorktree(t *testing.T) {
	_, sp, _ := worktreeFixture(t)
	c := dialVerb(t, sp)
	// In a directory that is no repository, so the session is not detected
	// as a worktree whatever directory the test runs from.
	result(t, c.call(t, `{"id":0,"verb":"new-session","params":`+jsonParams(map[string]any{"name": "plain", "cwd": t.TempDir()})+`}`))
	if code := errCode(t, c.call(t, `{"id":1,"verb":"bundle-worktree","params":{"session":"plain"}}`)); code != ErrVerbNotWorktree {
		t.Errorf("code = %q, want %q", code, ErrVerbNotWorktree)
	}
	if code := errCode(t, c.call(t, `{"id":2,"verb":"bundle-worktree","params":{}}`)); code != ErrVerbInvalidParams {
		t.Errorf("no session: code = %q, want %q", code, ErrVerbInvalidParams)
	}
}

// TestBundleWorktreeCarriesTheBranchTheAgentSwitchedTo covers an agent that
// makes its own branch inside a managed worktree. The session still records
// the branch it was made on, and the transfer must carry the one HEAD is on.
func TestBundleWorktreeCarriesTheBranchTheAgentSwitchedTo(t *testing.T) {
	_, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	created := newWorktreeCall(t, c, repo, "feat/made", map[string]any{"base": "main"})
	path := created["path"].(string)
	testutil.Git(t, path, "checkout", "-q", "-b", "feat/agent-own")
	if err := os.WriteFile(filepath.Join(path, "README"), []byte("on the agent's branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, path, "commit", "-q", "-am", "agent work")
	head, _ := worktree.HeadCommit(path)

	first, data := readWholeBundle(t, c, "repo-feat-made", false)
	if first["branch"] != "feat/agent-own" || first["head"] != head {
		t.Fatalf("first reply names branch %v at %v, want feat/agent-own at %s", first["branch"], first["head"], head)
	}

	// What a pull does with it: the named branch is in the bundle and ends
	// at the reported head.
	nb := int(first["bundle_bytes"].(float64))
	bundle := filepath.Join(t.TempDir(), "b")
	if err := os.WriteFile(bundle, data[:nb], 0o600); err != nil {
		t.Fatal(err)
	}
	receiver := filepath.Join(t.TempDir(), "receiver")
	testutil.Git(t, filepath.Dir(receiver), "clone", "-q", repo, receiver)
	if err := worktree.FetchBundle(receiver, bundle, first["branch"].(string), "pulled"); err != nil {
		t.Fatalf("fetch the bundle: %v", err)
	}
	if got, _ := worktree.BranchCommit(receiver, "pulled"); got != head {
		t.Errorf("the bundled branch ends at %s, want the reported head %s", got, head)
	}
}

func TestBundleWorktreeRefusesADetachedHeadTheSessionDoesNotKnowAbout(t *testing.T) {
	d, sp, repo := worktreeFixture(t)
	c := dialVerb(t, sp)
	created := newWorktreeCall(t, c, repo, "feat/detach", map[string]any{"base": "main"})
	testutil.Git(t, created["path"].(string), "checkout", "-q", "--detach")
	resp := c.call(t, `{"id":1,"verb":"bundle-worktree","params":{"session":"repo-feat-detach"}}`)
	if code := errCode(t, resp); code != ErrVerbNotWorktree {
		t.Errorf("a detached HEAD: code = %q, want %q", code, ErrVerbNotWorktree)
	}
	if n := d.bundles.count(); n != 0 {
		t.Errorf("%d transfers open after a refusal", n)
	}
}
