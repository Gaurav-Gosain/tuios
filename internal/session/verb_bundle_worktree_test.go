package session

import (
	"os"
	"runtime"
	"testing"
	"time"
)

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
	// Polled without a pause, so the files are checked the moment the
	// transfer leaves the store: that is when a caller can see it gone, and
	// the files have to be gone by then too. With a pause between polls the
	// check only failed when the removal happened to be slow.
	deadline := time.Now().Add(5 * time.Second)
	for d.bundles.count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the transfer outlived the connection that made it")
		}
		runtime.Gosched()
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the transfer's files are still at %s", dir)
	}
}
