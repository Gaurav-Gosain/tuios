package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// These tests hit the stash the way it will actually be used and the way it will
// actually fail. The caps are exercised by writing files past them rather than
// by reading the constants back, because a constant asserted against itself
// passes whatever the code does with it.

// newStore builds a store rooted under a temp directory, with the socket path it
// derives its root from pointed at that directory. Everything written by these
// tests lives under it.
func newStore(t *testing.T) (*stashStore, string) {
	t.Helper()
	base := t.TempDir()
	return newStashStore(func() string { return filepath.Join(base, "tuios.sock") }), base
}

// writeBytes writes n bytes to path, with the first eight bytes set from seed so
// two files of the same size still have different content and do not dedup.
func writeBytes(t *testing.T, path string, n int, seed byte) {
	t.Helper()
	buf := make([]byte, n)
	for i := range 8 {
		if i < len(buf) {
			buf[i] = seed + byte(i)
		}
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestStashHasNoPermanentFallback is the other half of that promise. With
// XDG_RUNTIME_DIR unset the socket, and therefore the stash, must still land in
// a per-boot directory and never anywhere under the user's home.
func TestStashHasNoPermanentFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")

	sock, err := GetSocketPath()
	if err != nil {
		t.Fatalf("GetSocketPath: %v", err)
	}
	root := filepath.Join(filepath.Dir(sock), "stash")
	if !strings.HasPrefix(root, "/tmp/") {
		t.Errorf("with no XDG_RUNTIME_DIR the stash root is %s, which is not a per-boot directory", root)
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(root, home+string(os.PathSeparator)) {
		t.Errorf("the stash root fell back to %s, inside the home directory", root)
	}
}

// TestStashUnwritableRootFails checks the other unhappy XDG case: a runtime
// directory the daemon cannot write. It has to fail and say so, not quietly
// store somewhere else.
func TestStashUnwritableRootFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a 0500 directory")
	}
	base := t.TempDir()
	locked := filepath.Join(base, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	s := newStashStore(func() string { return filepath.Join(locked, "tuios.sock") })
	src := filepath.Join(base, "note.txt")
	writeBytes(t, src, 16, 1)

	if _, err := s.put("sess-1", src, nil); err == nil {
		t.Fatal("a put into an unwritable runtime directory succeeded")
	}
	entries, err := os.ReadDir(locked)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the failed put left %d entries behind", len(entries))
	}
}

// TestStashPerFileCapIsHit writes a file past the per-file cap and checks the
// put is refused and stores nothing. The cap is hit rather than read back.
func TestStashPerFileCapIsHit(t *testing.T) {
	s, base := newStore(t)
	big := filepath.Join(base, "big.bin")
	writeBytes(t, big, stashMaxFileBytes+1, 3)

	_, err := s.put("sess-1", big, nil)
	if err == nil {
		t.Fatal("a file over the per-file cap was stored")
	}
	// The refusal names the real size, which is the reason the size is checked
	// before the copy starts rather than only bounded during it. A caller told
	// only "too big" has to guess by how much.
	if !strings.Contains(err.Error(), fmt.Sprintf("%d bytes", stashMaxFileBytes+1)) {
		t.Errorf("the refusal does not say the size: %v", err)
	}

	listing, lerr := s.list("sess-1", nil)
	if lerr != nil {
		t.Fatalf("list: %v", lerr)
	}
	if len(listing.Entries) != 0 || listing.Bytes != 0 {
		t.Errorf("the refused put left %d entries and %d bytes", len(listing.Entries), listing.Bytes)
	}
	// And nothing half-written was left in the directory either.
	if names, err := os.ReadDir(listing.Dir); err == nil && len(names) != 0 {
		t.Errorf("the refused put left %d files on disk", len(names))
	}

	// One byte under the cap still goes in, so the boundary is the cap and not
	// something smaller.
	ok := filepath.Join(base, "ok.bin")
	writeBytes(t, ok, stashMaxFileBytes, 4)
	if _, err := s.put("sess-1", ok, nil); err != nil {
		t.Fatalf("a file exactly at the cap was refused: %v", err)
	}
}

// TestStashSessionCapEvictsOldestUnreferenced fills a session past its cap and
// checks what comes out: the oldest file goes, a file a message still names does
// not, and the eviction is counted.
//
// It writes a quarter of a gigabyte through the store on purpose. The cap is the
// feature; asserting the constant would prove nothing about the code that spends
// it.
func TestStashSessionCapEvictsOldestUnreferenced(t *testing.T) {
	s, base := newStore(t)

	// One source file, rewritten each round, so the test needs 16 MiB of source
	// disk rather than 272.
	src := filepath.Join(base, "chunk.bin")
	const chunk = stashMaxFileBytes
	const rounds = stashMaxSessionBytes / chunk // 16 files fill the cap exactly

	var paths []string
	for i := range rounds {
		writeBytes(t, src, chunk, byte(i+1))
		res, err := s.put("sess-1", src, nil)
		if err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
		if res.Evicted != 0 {
			t.Fatalf("put %d evicted %d files while still under the cap", i, res.Evicted)
		}
		paths = append(paths, res.Entry.Path)
	}

	filled, err := s.list("sess-1", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if filled.Bytes != stashMaxSessionBytes {
		t.Fatalf("the store holds %d bytes, want the cap %d", filled.Bytes, stashMaxSessionBytes)
	}

	// The oldest file is referenced by a message, so it must survive and the
	// second oldest must go instead.
	referenced := map[string]bool{paths[0]: true}

	writeBytes(t, src, chunk, 200)
	res, err := s.put("sess-1", src, func() map[string]bool { return referenced })
	if err != nil {
		t.Fatalf("the put that should have evicted failed: %v", err)
	}
	if res.Evicted != 1 {
		t.Errorf("the put evicted %d files, want 1", res.Evicted)
	}
	if res.Evictions != 1 {
		t.Errorf("the session counts %d evictions, want 1", res.Evictions)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Errorf("the referenced file was evicted: %v", err)
	}
	if _, err := os.Stat(paths[1]); !os.IsNotExist(err) {
		t.Errorf("the oldest unreferenced file is still on disk (%v)", err)
	}
	if res.Bytes > stashMaxSessionBytes {
		t.Errorf("the store holds %d bytes, over the cap %d", res.Bytes, stashMaxSessionBytes)
	}

	// With every file referenced there is nothing to take, so the put is refused
	// rather than breaking a message that still reads.
	all := map[string]bool{}
	after, err := s.list("sess-1", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range after.Entries {
		all[e.Path] = true
	}
	writeBytes(t, src, chunk, 201)
	if _, err := s.put("sess-1", src, func() map[string]bool { return all }); err == nil {
		t.Error("a put succeeded with the cap full and every file referenced")
	}
	if final, err := s.list("sess-1", nil); err == nil && final.Bytes > stashMaxSessionBytes {
		t.Errorf("the store holds %d bytes, over the cap", final.Bytes)
	}
}

// TestStashConcurrentPutsKeepTheBooks runs many writers at once and checks the
// totals still add up. It is the test the race detector reads.
func TestStashConcurrentPutsKeepTheBooks(t *testing.T) {
	s, base := newStore(t)

	const writers = 16
	const size = 64 << 10
	srcs := make([]string, writers)
	for i := range writers {
		srcs[i] = filepath.Join(base, fmt.Sprintf("w%d.bin", i))
		writeBytes(t, srcs[i], size, byte(i+1))
	}

	var wg sync.WaitGroup
	errs := make(chan error, writers*2)
	for i := range writers {
		// Each file is put twice from two goroutines, so the dedup path and the
		// store path race each other as well.
		for range 2 {
			src := srcs[i]
			wg.Go(func() {
				if _, err := s.put("sess-1", src, func() map[string]bool { return nil }); err != nil {
					errs <- err
				}
			})
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent put: %v", err)
	}

	listing, err := s.list("sess-1", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listing.Entries) != writers {
		t.Errorf("the store holds %d entries, want %d", len(listing.Entries), writers)
	}
	if listing.Bytes != int64(writers*size) {
		t.Errorf("the store counts %d bytes, want %d", listing.Bytes, writers*size)
	}
	var onDisk int64
	files, err := os.ReadDir(listing.Dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, f := range files {
		info, err := f.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", f.Name(), err)
		}
		onDisk += info.Size()
	}
	if onDisk != listing.Bytes {
		t.Errorf("the directory holds %d bytes but the store counts %d", onDisk, listing.Bytes)
	}
}

// TestStashAwkwardPaths covers the inputs an agent will actually hand it by
// accident.
func TestStashAwkwardPaths(t *testing.T) {
	s, base := newStore(t)

	t.Run("relative", func(t *testing.T) {
		if _, err := s.put("sess-1", "notes.txt", nil); err == nil {
			t.Fatal("a relative path was accepted")
		}
	})

	t.Run("missing", func(t *testing.T) {
		if _, err := s.put("sess-1", filepath.Join(base, "nope.txt"), nil); err == nil {
			t.Fatal("a path with no file was accepted")
		}
	})

	t.Run("directory", func(t *testing.T) {
		dir := filepath.Join(base, "adir")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := s.put("sess-1", dir, nil); err == nil {
			t.Fatal("a directory was accepted")
		}
	})

	t.Run("unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can read a 0000 file")
		}
		p := filepath.Join(base, "secret.txt")
		writeBytes(t, p, 32, 5)
		if err := os.Chmod(p, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
		if _, err := s.put("sess-1", p, nil); err == nil {
			t.Fatal("an unreadable file was accepted")
		}
	})

	t.Run("not a regular file", func(t *testing.T) {
		p := filepath.Join(base, "fifo")
		if err := makeFIFO(p); err != nil {
			t.Skipf("cannot make a fifo here: %v", err)
		}
		if _, err := s.put("sess-1", p, nil); err == nil {
			t.Fatal("a fifo was accepted")
		}
	})

	t.Run("spaces and shell metacharacters", func(t *testing.T) {
		name := `a file; rm -rf $HOME & 'quoted' "double" |pipe*.txt`
		p := filepath.Join(base, name)
		writeBytes(t, p, 128, 11)
		res, err := s.put("sess-1", p, nil)
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		// The stored name is the hash and a sanitised extension, so none of the
		// source's punctuation reaches a path the daemon builds.
		if strings.ContainsAny(res.Entry.Name, " ;&*'\"|$/") {
			t.Errorf("the stored name carried the source's punctuation: %q", res.Entry.Name)
		}
		if !strings.HasSuffix(res.Entry.Name, ".txt") {
			t.Errorf("the stored name lost its extension: %q", res.Entry.Name)
		}
		if _, err := os.Stat(res.Entry.Path); err != nil {
			t.Errorf("the stored file is not at the path that was printed: %v", err)
		}
	})

	t.Run("extension that is not one", func(t *testing.T) {
		p := filepath.Join(base, "dump.tar gz")
		writeBytes(t, p, 64, 13)
		res, err := s.put("sess-1", p, nil)
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		if strings.Contains(res.Entry.Name, " ") {
			t.Errorf("a space reached the stored name: %q", res.Entry.Name)
		}
	})
}

// quote renders a string as a JSON string literal, so a test can build a request
// line around a path with punctuation in it.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestStashForgetDeletesTheSessionDirectory is half the lifetime promise: the
// store goes when the session does.
func TestStashForgetDeletesTheSessionDirectory(t *testing.T) {
	s, base := newStore(t)
	src := filepath.Join(base, "a.txt")
	writeBytes(t, src, 256, 1)

	res, err := s.put("sess-1", src, nil)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	other, err := s.put("sess-2", src, nil)
	if err != nil {
		t.Fatalf("put into a second session: %v", err)
	}

	s.forget("sess-1")

	if _, err := os.Stat(res.Entry.Path); !os.IsNotExist(err) {
		t.Errorf("the killed session's file is still there (%v)", err)
	}
	if _, err := os.Stat(filepath.Dir(res.Entry.Path)); !os.IsNotExist(err) {
		t.Error("the killed session's directory is still there")
	}
	// The other session is untouched, which is why the boxes are per session.
	if _, err := os.Stat(other.Entry.Path); err != nil {
		t.Errorf("another session's file went with it: %v", err)
	}
}

// TestStashSweepClearsEverything is the other half: nothing survives the daemon.
func TestStashSweepClearsEverything(t *testing.T) {
	s, base := newStore(t)
	src := filepath.Join(base, "a.txt")
	writeBytes(t, src, 256, 1)

	res, err := s.put("sess-1", src, nil)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	root := filepath.Join(base, "stash")

	s.sweep()

	if _, err := os.Stat(res.Entry.Path); !os.IsNotExist(err) {
		t.Errorf("a stashed file survived the sweep (%v)", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("the stash root survived the sweep")
	}
	// And the store is usable again afterwards, which is what makes the sweep
	// safe to run at start as well as at shutdown.
	if _, err := s.put("sess-1", src, nil); err != nil {
		t.Errorf("the store is unusable after a sweep: %v", err)
	}
}

// TestStashedFileVanishesWithTheSession is the lifetime promise measured through
// the verbs: kill the session, and the files are gone.
func TestStashedFileVanishesWithTheSession(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "doomed")
	c := dialVerb(t, sp)

	src := filepath.Join(t.TempDir(), "a.txt")
	writeBytes(t, src, 128, 51)
	put := result(t, c.call(t, `{"id":1,"verb":"stash-put","params":{"session":"doomed","path":`+quote(src)+`}}`))
	stored := put["path"].(string)
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("the file was not stored: %v", err)
	}

	result(t, c.call(t, `{"id":2,"verb":"kill-session","params":{"session":"doomed"}}`))

	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("a stashed file outlived its session (%v)", err)
	}
	if _, err := os.Stat(filepath.Dir(stored)); !os.IsNotExist(err) {
		t.Error("the killed session's stash directory is still there")
	}
	// The source is untouched. The stash copies; it does not move.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("killing the session deleted the source file: %v", err)
	}
}

// TestStashedFileVanishesWithTheDaemon is the other lifetime case, and the one
// that matters for a machine that keeps running: stopping the daemon takes the
// files with it.
func TestStashedFileVanishesWithTheDaemon(t *testing.T) {
	d, sp := startTestDaemon(t)
	makeSessionWithWindow(t, d, "shutting")
	c := dialVerb(t, sp)

	src := filepath.Join(t.TempDir(), "a.txt")
	writeBytes(t, src, 128, 61)
	put := result(t, c.call(t, `{"id":1,"verb":"stash-put","params":{"session":"shutting","path":`+quote(src)+`}}`))
	stored := put["path"].(string)
	root := filepath.Dir(filepath.Dir(stored))

	d.Stop()

	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("a stashed file outlived the daemon (%v)", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("the stash root outlived the daemon")
	}
}

// TestStashSweepClearsAnUncleanPredecessor covers the case the shutdown path
// cannot: a daemon that was killed left files behind, and the next daemon has to
// remove them before it serves anything.
func TestStashSweepClearsAnUncleanPredecessor(t *testing.T) {
	runtimeDir := testutil.RuntimeDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Cleanup(useResurrectionDir(t.TempDir()))

	// Residue from a daemon that never got to run its shutdown.
	leftoverDir := filepath.Join(runtimeDir, "tuios", "stash", "gone-session")
	if err := os.MkdirAll(leftoverDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	leftover := filepath.Join(leftoverDir, "deadbeef.txt")
	if err := os.WriteFile(leftover, []byte("orphan"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	d := NewDaemon(&DaemonConfig{Version: "test", DisableAutoRestore: true})
	if err := d.Start(); err != nil {
		t.Fatalf("daemon Start: %v", err)
	}
	t.Cleanup(d.Stop)

	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("a previous daemon's stashed file survived the next daemon's start (%v)", err)
	}
}
