//go:build unix

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The socket directory is a security boundary: whoever controls it can swap
// the daemon's socket for their own and read every keystroke a client sends.
// Without XDG_RUNTIME_DIR it is /tmp/tuios-<uid>, a name any local user can
// create first. The ways GetSocketPath could hand out a socket in a directory
// someone else controls:
//
//  1. The directory is a symlink into a directory another user owns.
//  2. The directory exists and is open to other users (group or world
//     writable, as another user's pre-created 0777 directory would be).
//  3. The directory belongs to another user. Not reachable here: creating a
//     directory owned by someone else needs root. The check is the uid
//     comparison beside the two below.
//  4. The path exists and is not a directory.
//
// And the case that must keep working: a fresh directory, and one this user
// already made with the right mode.

func socketPathIn(t *testing.T, runtime string) (string, error) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	return GetSocketPath()
}

func TestSocketDirIsCreatedPrivate(t *testing.T) {
	runtime := t.TempDir()
	path, err := socketPathIn(t, runtime)
	if err != nil {
		t.Fatalf("a fresh runtime dir was refused: %v", err)
	}
	if want := filepath.Join(runtime, "tuios", "tuios.sock"); path != want {
		t.Fatalf("socket path %q, want %q", path, want)
	}
	st, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("the socket dir was made %v, want 0700", st.Mode().Perm())
	}
	// A second call finds the directory it made and accepts it.
	if _, err := socketPathIn(t, runtime); err != nil {
		t.Fatalf("the directory made by the first call was refused: %v", err)
	}
}

func TestSocketDirThatIsASymlinkIsRefused(t *testing.T) {
	runtime := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(runtime, "tuios")); err != nil {
		t.Fatal(err)
	}
	if path, err := socketPathIn(t, runtime); err == nil {
		t.Fatalf("a symlinked socket dir was accepted: %s", path)
	}
}

func TestSocketDirOpenToOthersIsClosed(t *testing.T) {
	runtime := t.TempDir()
	dir := filepath.Join(runtime, "tuios")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Mkdir is subject to the umask, so the open mode is set explicitly.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := socketPathIn(t, runtime); err != nil {
		t.Fatalf("this user's own open directory was refused rather than closed: %v", err)
	}
	st, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("the socket dir is still %v after the check", st.Mode().Perm())
	}
}

func TestSocketDirThatIsAFileIsRefused(t *testing.T) {
	runtime := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtime, "tuios"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := socketPathIn(t, runtime)
	if err == nil || !strings.Contains(err.Error(), "tuios") {
		t.Fatalf("a file where the socket dir goes gave %v, want an error naming it", err)
	}
}
