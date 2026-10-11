//go:build !windows

package sockpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// Without XDG_RUNTIME_DIR, Dir now uses a folder it did not make,
// /run/user/<uid>, and a client may dial a socket in /tmp/tuios-<uid>. The
// ways that could hand a client a socket someone else controls:
//
//  1. The folder belongs to another user.
//  2. The folder is open to others (group or world bits set).
//  3. The folder is a symlink, which can point anywhere.
//  4. The path is not a folder.
//
// Each must make PrivateDir false, and a /run/user/<uid> that fails it must
// not be used.

func TestPrivateDirRefusesWhatSomeoneElseControls(t *testing.T) {
	uid := os.Getuid()
	base := t.TempDir()
	mk := func(name string, mode os.FileMode) string {
		dir := filepath.Join(base, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	good := mk("good", 0o700)
	if !PrivateDir(good, uid) {
		t.Fatalf("a 0700 folder of this user was refused")
	}
	if PrivateDir(good, uid+1) {
		t.Errorf("a folder owned by another uid was accepted")
	}
	for _, mode := range []os.FileMode{0o755, 0o770, 0o777, 0o701, 0o500} {
		if PrivateDir(mk("m"+strconv.FormatUint(uint64(mode), 8), mode), uid) {
			t.Errorf("a folder with mode %o was accepted", mode)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(link, uid) {
		t.Errorf("a symlink to a private folder was accepted")
	}
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if PrivateDir(file, uid) {
		t.Errorf("a file was accepted as a folder")
	}
	if PrivateDir(filepath.Join(base, "missing"), uid) {
		t.Errorf("a missing folder was accepted")
	}
}

func TestDirUsesRunUserOnlyWhenItIsPrivate(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS has no /run/user")
	}
	run, tmp := t.TempDir(), t.TempDir()
	defer SetRootsForTest(run, tmp)()
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TUIOS_E2E", "")
	legacy := filepath.Join(tmp, "tuios-"+strconv.Itoa(os.Getuid()))
	mine := filepath.Join(run, strconv.Itoa(os.Getuid()))

	if got := Dir(); got != legacy {
		t.Fatalf("with no /run/user/<uid>, Dir is %s, want %s", got, legacy)
	}
	if err := os.Mkdir(mine, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Dir(); got != legacy {
		t.Fatalf("an open /run/user/<uid> was used: Dir is %s", got)
	}
	if err := os.Chmod(mine, 0o700); err != nil {
		t.Fatal(err)
	}
	if got, want := Dir(), filepath.Join(mine, "tuios"); got != want {
		t.Fatalf("Dir is %s, want %s", got, want)
	}
	if got := OtherDir(); got != legacy {
		t.Fatalf("OtherDir is %q, want %s", got, legacy)
	}

	// The variable names the same folder: the other folder is still looked at.
	t.Setenv("XDG_RUNTIME_DIR", mine)
	if got := OtherDir(); got != legacy {
		t.Fatalf("with XDG_RUNTIME_DIR=/run/user/<uid>, OtherDir is %q, want %s", got, legacy)
	}
	// A folder of its own: a separate daemon, and nothing else to look at.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if got := OtherDir(); got != "" {
		t.Fatalf("with a private XDG_RUNTIME_DIR, OtherDir is %q, want none", got)
	}
}
