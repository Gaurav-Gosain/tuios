package tuie2e

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Write roots and the read deny list, over a real link.
//
// TestALinkCannotWriteKeysOrLeaveTheHome gives build a wide root (~), so it
// proves the deny list. These prove the other half: that the default root is
// one receive folder, that a link cannot read keys out, and that the escapes
// the review found (a renamed ancestor, a stowed dotfile, a case-folded deny
// name) are closed.

// TestALinkWritesOnlyInItsRoots leaves build on the default policy: no
// files_roots, so a linked machine writes only to the receive folder. A copy
// into the home folder is refused and the refusal says where writes may land
// and how to allow more. A copy into the receive folder works.
func TestALinkWritesOnlyInItsRoots(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	// A policy that sets allow but not files_roots: the default receive
	// folder is the only write root.
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"files\"]\n")
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	src := filepath.Join(base, "report.pdf")
	if err := os.WriteFile(src, []byte("a report\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var outside transferRow
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": "~/report.pdf"}, "conflict": "replace",
	}, &outside)
	outside = waitTransferEnd(t, base, outside.ID, 30*time.Second)
	if outside.State != "failed" || outside.Code != "forbidden" {
		t.Fatalf("ASSERTION: a copy to build:~ ended %s (%s), want failed as forbidden", outside.State, outside.Code)
	}
	if !strings.Contains(outside.Error, "Downloads/tuios") || !strings.Contains(outside.Error, "files_roots") {
		t.Errorf("ASSERTION: the refusal does not say where writes land or how to allow more: %q", outside.Error)
	}
	if _, err := os.Stat(filepath.Join(farHome, "report.pdf")); err == nil {
		t.Errorf("ASSERTION: the file arrived in build's home folder")
	}

	// The receive folder is created on first use and the copy lands there.
	var inside transferRow
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": "~/Downloads/tuios/report.pdf"}, "conflict": "replace",
	}, &inside)
	inside = waitTransferEnd(t, base, inside.ID, 30*time.Second)
	if inside.State != "done" || !inside.Verified {
		t.Fatalf("ASSERTION: a copy to the receive folder ended %s (%s): %s", inside.State, inside.Code, inside.Error)
	}
	if _, err := os.Stat(filepath.Join(farHome, "Downloads", "tuios", "report.pdf")); err != nil {
		t.Errorf("ASSERTION: the copy did not land in the receive folder: %v", err)
	}
	saveTransferArtifact(t, "files-roots", map[string]any{"outside": outside, "inside": inside})
}

// TestALinkCannotWriteThroughARenamedAncestor is the review's finding 1: the
// deny list must refuse a write to a folder that holds a denied one, or a
// link could rename ~/.config out, write a fish start file under the new
// name, and rename it back. build has a wide root, so only the deny list
// stands between the link and the start file.
func TestALinkCannotWriteThroughARenamedAncestor(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	rc := filepath.Join(farHome, ".config", "fish", "config.fish")
	if err := os.MkdirAll(filepath.Dir(rc), 0o700); err != nil {
		t.Fatal(err)
	}
	const keep = "# the person's own config\n"
	if err := os.WriteFile(rc, []byte(keep), 0o644); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	far := dialHostVerbs(t, base)
	if _, err := far.call("file-rename", map[string]any{"from": "~/.config", "to": "~/cfg"}); !forbidden(err) {
		t.Errorf("ASSERTION: a rename of ~/.config, which holds tuios and fish config, was not refused: %v", err)
	}
	if _, err := far.call("file-remove", map[string]any{"path": "~/.config", "recursive": true}); !forbidden(err) {
		t.Errorf("ASSERTION: a remove of ~/.config was not refused: %v", err)
	}
	if got, _ := os.ReadFile(rc); string(got) != keep {
		t.Errorf("ASSERTION: build's fish config changed: %q", got)
	}
	if _, err := os.Stat(rc); err != nil {
		t.Errorf("ASSERTION: build's fish config was removed: %v", err)
	}
}

// TestALinkCannotWriteAStowedDotfile is the review's finding 2: a dotfile
// kept as a symlink into a plain folder (GNU stow, chezmoi, yadm) must not be
// writable through its target. build has a wide root.
func TestALinkCannotWriteAStowedDotfile(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	if err := os.MkdirAll(filepath.Join(farHome, "dotfiles", "ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	const keep = "ssh-ed25519 AAAA the person's own key\n"
	if err := os.WriteFile(filepath.Join(farHome, "dotfiles", "ssh", "authorized_keys"), []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farHome, "dotfiles", "bashrc"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(farHome, "dotfiles", "ssh"), filepath.Join(farHome, ".ssh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(farHome, "dotfiles", "bashrc"), filepath.Join(farHome, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	src := filepath.Join(base, "evil")
	if err := os.WriteFile(src, []byte("ssh-ed25519 AAAA the attacker's key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dst := range []string{"~/dotfiles/ssh/authorized_keys", "~/dotfiles/bashrc"} {
		var row transferRow
		dialFileVerbs(t, base).must("transfer-start", map[string]any{
			"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": dst}, "conflict": "replace",
		}, &row)
		row = waitTransferEnd(t, base, row.ID, 30*time.Second)
		if row.State != "failed" || row.Code != "forbidden" {
			t.Errorf("ASSERTION: a copy to the stow target build:%s ended %s (%s), want forbidden", dst, row.State, row.Code)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(farHome, ".ssh", "authorized_keys")); string(got) != keep {
		t.Errorf("ASSERTION: build's authorized_keys changed through a stow symlink: %q", got)
	}
}

// TestALinkCannotReadKeys is the review's finding 4: the read verbs are
// confined too. A link may read an ordinary file in the home folder and may
// not read a private key or the tuios state. A .pub key stays readable.
func TestALinkCannotReadKeys(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	if err := os.MkdirAll(filepath.Join(farHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farHome, ".ssh", "id_ed25519"), []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farHome, ".ssh", "id_ed25519.pub"), []byte("ssh-ed25519 AAAA public\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farHome, "notes.txt"), []byte("ordinary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	far := dialHostVerbs(t, base)
	if _, err := far.call("file-read", map[string]any{"path": "~/.ssh/id_ed25519"}); !forbidden(err) {
		t.Errorf("ASSERTION: a link read a private key: %v", err)
	}
	if _, err := far.call("file-hash", map[string]any{"path": "~/.ssh/id_ed25519"}); !forbidden(err) {
		t.Errorf("ASSERTION: a link hashed a private key: %v", err)
	}
	if _, err := far.call("file-read", map[string]any{"path": "~/.ssh/id_ed25519.pub"}); err != nil {
		t.Errorf("ASSERTION: a link could not read a public key: %v", err)
	}
	if _, err := far.call("file-read", map[string]any{"path": "~/notes.txt"}); err != nil {
		t.Errorf("ASSERTION: a link could not read an ordinary file: %v", err)
	}
}

// TestADarwinCaseFoldedDenyNameIsRefused is the review's finding 3: on a disk
// that folds case, a deny name written with a fold that strings.ToLower
// misses, such as the long s, still opens the real folder, so it must be
// refused too. build has a wide root.
func TestADarwinCaseFoldedDenyNameIsRefused(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the fold is the APFS one; this runs on macOS")
	}
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	if err := os.MkdirAll(filepath.Join(farHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	const keep = "ssh-ed25519 AAAA the person's own key\n"
	if err := os.WriteFile(filepath.Join(farHome, ".ssh", "authorized_keys"), []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	src := filepath.Join(base, "evil")
	if err := os.WriteFile(src, []byte("ssh-ed25519 AAAA the attacker's key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// U+017F LATIN SMALL LETTER LONG S folds to s on APFS.
	var row transferRow
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": "~/.ſsh/authorized_keys"}, "conflict": "replace",
	}, &row)
	row = waitTransferEnd(t, base, row.ID, 30*time.Second)
	if row.State != "failed" || row.Code != "forbidden" {
		t.Errorf("ASSERTION: a copy to ~/.%csh/authorized_keys ended %s (%s), want forbidden", 0x017f, row.State, row.Code)
	}
	if got, _ := os.ReadFile(filepath.Join(farHome, ".ssh", "authorized_keys")); string(got) != keep {
		t.Errorf("ASSERTION: build's authorized_keys changed through a case-folded name: %q", got)
	}
}

func forbidden(err error) bool {
	var vf *verbFailure
	return errors.As(err, &vf) && vf.Code == "forbidden"
}
