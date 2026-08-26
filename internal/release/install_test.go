package release

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestStageAndCommitReplaceAFileThatIsOpen is the claim the whole staging
// arrangement exists for: the daemon holds the binary open, and the replacement
// has to land anyway.
//
// The open handle stands in for the running daemon. A process that already
// opened the file keeps reading the bytes it opened, and the name points at the
// new file, which is exactly what a rename gives and what writing in place does
// not.
//
// Negative control: replace Stage and Commit with os.WriteFile onto the target
// and this fails on Linux with ETXTBSY for a real executable, and fails here on
// the old-content check for any file, because the open handle would see the new
// bytes.
func TestStageAndCommitReplaceAFileThatIsOpen(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tuios")
	if err := os.WriteFile(target, []byte("old build"), 0o755); err != nil {
		t.Fatal(err)
	}

	open, err := os.Open(target) // #nosec G304 - the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = open.Close() }()

	s, err := Stage(target, []byte("new build"), BinaryMode(target))
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// Staging alone must change nothing: everything is downloaded and staged
	// before anything is committed, so a failure part way leaves the old pair.
	if got, _ := os.ReadFile(target); string(got) != "old build" { // #nosec G304
		t.Fatalf("staging changed the target to %q", got)
	}
	if err := s.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if got, _ := os.ReadFile(target); string(got) != "new build" { // #nosec G304
		t.Errorf("the target holds %q after the commit", got)
	}
	held, err := io.ReadAll(open)
	if err != nil {
		t.Fatal(err)
	}
	if string(held) != "old build" {
		t.Errorf("the already-open handle now reads %q; the file was written in place, not renamed over", held)
	}
}

// TestStageKeepsTheModeTheBinaryHad. The install script chmods 0755 in a home
// directory and leaves whatever /usr/local/bin was set to alone, so imposing a
// mode here would silently change how the binary is shared.
//
// Negative control: pass a literal 0755 to Stage instead of BinaryMode(target)
// and this fails.
func TestStageKeepsTheModeTheBinaryHad(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not comparable on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "tuios")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o750); err != nil {
		t.Fatal(err)
	}

	s, err := Stage(target, []byte("new"), BinaryMode(target))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("mode is %v after the update, want 0750", got)
	}
}

// TestBinaryModeDefaultsToExecutable, for the case where there is nothing there
// to copy a mode from. A default of 0600 would install a binary its own owner
// could not run.
//
// Negative control: return 0 or 0600 and this fails.
func TestBinaryModeDefaultsToExecutable(t *testing.T) {
	if got := BinaryMode(filepath.Join(t.TempDir(), "absent")); got != 0o755 {
		t.Errorf("BinaryMode on a missing file = %v, want 0755", got)
	}
}

// TestDiscardLeavesNothingBehind, so an update that fails after staging does
// not leave a forty megabyte file in a directory on PATH.
//
// Negative control: make Discard a no-op and this fails.
func TestDiscardLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tuios")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Stage(target, []byte("new"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	s.Discard()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "tuios" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the directory holds %v after a discard, want only tuios", names)
	}
}

// TestDiscardAfterCommitIsSafe, because the installer defers a discard over
// every staged file and most of them will have been committed.
//
// Negative control: have Discard remove Target rather than the staged file and
// this deletes the freshly installed binary.
func TestDiscardAfterCommitIsSafe(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tuios")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Stage(target, []byte("new"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	s.Discard()
	if got, err := os.ReadFile(target); err != nil || string(got) != "new" { // #nosec G304
		t.Errorf("after commit and discard the target is %q (%v)", got, err)
	}
}

// TestWritableAnswersForTheDirectoryNotTheFile. The rename needs a writable
// directory, and a read-only file inside one can still be replaced.
//
// Negative control: test the target file's permission bits instead and this
// fails on the read-only file.
func TestWritableAnswersForTheDirectoryNotTheFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a mode 0500 directory")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "tuios")
	if err := os.WriteFile(target, []byte("old"), 0o400); err != nil {
		t.Fatal(err)
	}
	if !Writable(dir) {
		t.Error("a writable directory holding a read-only file was reported unwritable")
	}

	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	if Writable(locked) {
		t.Error("a directory this user cannot write to was reported writable")
	}
}
