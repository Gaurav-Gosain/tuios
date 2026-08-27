package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These run against a real temporary directory, made per test and removed with
// it. Nothing here touches a path outside t.TempDir().
//
// The awkward name below is the whole of the "no shell" claim, checked rather
// than asserted: a file called `a b"c';$(touch pwned)\n` is created, renamed,
// copied, moved, trashed and deleted, and after each one the directory is read
// back to prove that exactly the names that were asked for exist and nothing
// called "pwned" ever appeared.

// awkwardName holds a space, both quotes, a semicolon, a command substitution,
// a backtick, a newline and a leading dash.
const awkwardName = "-a b\"c';$(touch pwned)`id`\nx.txt"

// shellBaitName is the same idea without the quotes that would break a shell
// before it got to the interesting part. A shell handed this one runs three
// commands. Nothing here hands it to one.
const shellBaitName = "pwn $(touch pwned) `touch pwned2` ;touch pwned3.txt"

// mustWrite makes a file with content, failing the test if it cannot.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("could not write %q: %v", path, err)
	}
}

// namesIn is the sorted set of names directly in dir.
func namesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("could not read %q: %v", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestCreatePathMakesAFileAFolderAndANestedPath(t *testing.T) {
	dir := t.TempDir()

	if _, err := createPath(dir, "notes.md"); err != nil {
		t.Fatalf("creating a file: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "notes.md")); err != nil || info.IsDir() {
		t.Errorf("notes.md is not a regular file: %v", err)
	}

	if _, err := createPath(dir, "build/"); err != nil {
		t.Fatalf("creating a folder: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "build")); err != nil || !info.IsDir() {
		t.Errorf("build is not a folder: %v", err)
	}

	// The trailing slash is the only thing that says "folder". Without it the
	// last component is a file even when the path nests.
	if _, err := createPath(dir, "a/b/c.txt"); err != nil {
		t.Fatalf("creating a nested file: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "a", "b", "c.txt")); err != nil || info.IsDir() {
		t.Errorf("a/b/c.txt is not a regular file: %v", err)
	}
}

// TestCreatePathRefusesToLeaveTheFolder is the escape guard. Every one of these
// resolves outside the directory the prompt was opened over, and the operation
// has to refuse rather than write there.
func TestCreatePathRefusesToLeaveTheFolder(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(filepath.Dir(dir), "escaped.txt")

	for _, raw := range []string{
		"..",
		"../escaped.txt",
		"a/../../escaped.txt",
		"/tmp/escaped.txt",
		"",
		"   ",
		".",
		"./",
	} {
		if _, err := createPath(dir, raw); err == nil {
			t.Errorf("createPath(%q) was allowed; it must refuse", raw)
		}
	}
	if _, err := os.Lstat(outside); err == nil {
		t.Fatalf("a refused create still wrote %q", outside)
	}
}

func TestCreatePathRefusesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "keep.txt"), "original")

	if _, err := createPath(dir, "keep.txt"); err == nil {
		t.Fatal("createPath overwrote an existing file")
	}
	body, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if err != nil || string(body) != "original" {
		t.Errorf("the existing file was changed: %q %v", body, err)
	}

	// A folder is the case the check has to catch on its own: MkdirAll is happy
	// to be handed a folder that is already there and says nothing, so a create
	// that leant on it would report success for a folder it did not make.
	if err := os.Mkdir(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if what, err := createPath(dir, "build/"); err == nil {
		t.Errorf("creating a folder that already exists reported %q", what)
	}
	// And a name that is a folder must not be reported as a new file.
	if what, err := createPath(dir, "build"); err == nil {
		t.Errorf("creating a file over an existing folder reported %q", what)
	}
}

// TestAnAwkwardNameIsInert runs the whole set over a name built to break a
// shell, and checks the directory afterwards rather than the return values.
func TestAnAwkwardNameIsInert(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	for _, d := range []string{src, dst} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := createPath(src, awkwardName); err != nil {
		t.Fatalf("creating the awkward name: %v", err)
	}
	if got := namesIn(t, src); len(got) != 1 || got[0] != awkwardName {
		t.Fatalf("src holds %q, want exactly [%q]", got, awkwardName)
	}

	// Copy it next door, then move the copy back with a second name.
	if _, _, err := pastePaths(dst, fileClipboard{Paths: []string{filepath.Join(src, awkwardName)}}); err != nil {
		t.Fatalf("copying the awkward name: %v", err)
	}
	if got := namesIn(t, dst); len(got) != 1 || got[0] != awkwardName {
		t.Fatalf("dst holds %q after a copy, want exactly [%q]", got, awkwardName)
	}

	second := awkwardName + " (renamed)"
	if _, err := renameEntry(dst, awkwardName, second); err != nil {
		t.Fatalf("renaming the awkward name: %v", err)
	}
	if got := namesIn(t, dst); len(got) != 1 || got[0] != second {
		t.Fatalf("dst holds %q after a rename, want exactly [%q]", got, second)
	}

	if _, err := deletePaths([]string{filepath.Join(dst, second)}); err != nil {
		t.Fatalf("deleting the awkward name: %v", err)
	}
	if got := namesIn(t, dst); len(got) != 0 {
		t.Fatalf("dst still holds %q after a delete", got)
	}

	// The name asks a shell to make a file called "pwned" three separate ways.
	// Nothing here runs a shell, so nothing anywhere grew one.
	for _, d := range []string{dir, src, dst} {
		if _, err := os.Lstat(filepath.Join(d, "pwned")); err == nil {
			t.Fatalf("a name was interpreted: %q appeared in %q", "pwned", d)
		}
	}
	if got := namesIn(t, src); len(got) != 1 || got[0] != awkwardName {
		t.Fatalf("src changed under the other operations: %q", got)
	}
}

// TestAShellBaitNameIsNotRun is the injection claim on its own, with a name a
// shell parses cleanly and then obeys. awkwardName above breaks a shell before
// it gets that far, which proves a different thing: this one proves that a name
// a shell would run is not run.
func TestAShellBaitNameIsNotRun(t *testing.T) {
	dir := t.TempDir()

	if _, err := createPath(dir, shellBaitName); err != nil {
		t.Fatalf("creating the bait name: %v", err)
	}
	for _, bait := range []string{"pwned", "pwned2", "pwned3.txt"} {
		if _, err := os.Lstat(filepath.Join(dir, bait)); err == nil {
			t.Fatalf("the name was interpreted: %q appeared", bait)
		}
	}
	if got := namesIn(t, dir); len(got) != 1 || got[0] != shellBaitName {
		t.Fatalf("the folder holds %q, want exactly [%q]", got, shellBaitName)
	}

	// And through the rest of the set, so no single operation is the one that
	// reaches for a shell.
	if _, err := renameEntry(dir, shellBaitName, shellBaitName+" 2"); err != nil {
		t.Fatalf("renaming the bait name: %v", err)
	}
	if _, err := trashPathsPermanentForTest(filepath.Join(dir, shellBaitName+" 2")); err != nil {
		t.Fatalf("deleting the bait name: %v", err)
	}
	for _, bait := range []string{"pwned", "pwned2", "pwned3.txt"} {
		if _, err := os.Lstat(filepath.Join(dir, bait)); err == nil {
			t.Fatalf("the name was interpreted: %q appeared", bait)
		}
	}
	if got := namesIn(t, dir); len(got) != 0 {
		t.Fatalf("the folder holds %q after the delete", got)
	}
}

// trashPathsPermanentForTest is deletePaths for one path, named so the call
// above reads as what it is.
func trashPathsPermanentForTest(path string) (int, error) { return deletePaths([]string{path}) }

func TestRenameEntryRefusesAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "from.txt"), "from")
	mustWrite(t, filepath.Join(dir, "to.txt"), "to")

	if _, err := renameEntry(dir, "from.txt", "to.txt"); err == nil {
		t.Fatal("a rename replaced an existing file")
	}
	body, err := os.ReadFile(filepath.Join(dir, "to.txt"))
	if err != nil || string(body) != "to" {
		t.Errorf("the destination was overwritten: %q %v", body, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "from.txt")); err != nil {
		t.Errorf("the source went missing after a refused rename: %v", err)
	}
}

// TestRenameEntryOnAVanishedTargetSaysSo covers the ordinary race: the listing
// is a snapshot, and the file it named can be gone by the time the prompt is
// answered.
func TestRenameEntryOnAVanishedTargetSaysSo(t *testing.T) {
	dir := t.TempDir()
	_, err := renameEntry(dir, "never-existed.txt", "next.txt")
	if err == nil {
		t.Fatal("renaming a missing file was allowed")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the error was %v, want a not-exist error", err)
	}
	if got := fileOpError(err); !strings.Contains(got, "gone") || !strings.Contains(got, "out of date") {
		t.Errorf("the message is %q; it must say the file is gone and the list is stale", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "next.txt")); err == nil {
		t.Error("a failed rename created the destination anyway")
	}

	// And it stays "gone" when the destination name is taken: the source is
	// what the user pointed at, so the source is what the message is about.
	mustWrite(t, filepath.Join(dir, "taken.txt"), "taken")
	_, err = renameEntry(dir, "never-existed.txt", "taken.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("renaming a missing file onto a taken name reported %v, want a not-exist error", err)
	}
}

// TestPasteNeverOverwrites is the collision rule, and it is why a paste raises
// no confirmation: there is no branch in it that destroys anything.
func TestPasteNeverOverwrites(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mustWrite(t, filepath.Join(src, "report.txt"), "new")
	mustWrite(t, filepath.Join(dst, "report.txt"), "already here")

	done, renamed, err := pastePaths(dst, fileClipboard{Paths: []string{filepath.Join(src, "report.txt")}})
	if err != nil || done != 1 || renamed != 1 {
		t.Fatalf("paste returned done=%d renamed=%d err=%v; want 1, 1, nil", done, renamed, err)
	}
	body, err := os.ReadFile(filepath.Join(dst, "report.txt"))
	if err != nil || string(body) != "already here" {
		t.Fatalf("the file that was already there is now %q (%v)", body, err)
	}
	body, err = os.ReadFile(filepath.Join(dst, "report (1).txt"))
	if err != nil || string(body) != "new" {
		t.Fatalf("the pasted file did not land as \"report (1).txt\": %q %v", body, err)
	}
}

// TestAMoveNeverOverwrites is the same rule on the path where nothing else
// enforces it: os.Rename replaces its destination without a word, so the free
// name picked before the move is the only thing standing between a cut and a
// lost file.
func TestAMoveNeverOverwrites(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mustWrite(t, filepath.Join(src, "report.txt"), "moved in")
	mustWrite(t, filepath.Join(dst, "report.txt"), "already here")

	done, renamed, err := pastePaths(dst, fileClipboard{
		Paths: []string{filepath.Join(src, "report.txt")},
		Move:  true,
	})
	if err != nil || done != 1 || renamed != 1 {
		t.Fatalf("move returned done=%d renamed=%d err=%v; want 1, 1, nil", done, renamed, err)
	}
	body, err := os.ReadFile(filepath.Join(dst, "report.txt"))
	if err != nil || string(body) != "already here" {
		t.Fatalf("the move replaced the file that was already there: %q %v", body, err)
	}
	body, err = os.ReadFile(filepath.Join(dst, "report (1).txt"))
	if err != nil || string(body) != "moved in" {
		t.Fatalf("the moved file did not land beside it: %q %v", body, err)
	}
}

func TestPasteMoveRemovesTheSourceOnlyAfterItLands(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	mustWrite(t, filepath.Join(src, "moved.txt"), "body")

	if _, _, err := pastePaths(dst, fileClipboard{Paths: []string{filepath.Join(src, "moved.txt")}, Move: true}); err != nil {
		t.Fatalf("moving: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(src, "moved.txt")); err == nil {
		t.Error("the source is still there after a move")
	}
	body, err := os.ReadFile(filepath.Join(dst, "moved.txt"))
	if err != nil || string(body) != "body" {
		t.Errorf("the moved file is %q (%v)", body, err)
	}
}

func TestPasteRefusesAFolderIntoItself(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "tree")
	inner := filepath.Join(tree, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tree, "leaf.txt"), "leaf")

	for _, into := range []string{tree, inner} {
		done, _, err := pastePaths(into, fileClipboard{Paths: []string{tree}})
		if err == nil {
			t.Errorf("pasting %q into %q was allowed", tree, into)
		}
		if done != 0 {
			t.Errorf("pasting into itself reported %d done", done)
		}
	}
	// Nothing recursed: the tree still holds exactly what it did.
	if got := namesIn(t, tree); len(got) != 2 {
		t.Errorf("the tree grew under a refused paste: %q", got)
	}
}

// TestAReadOnlyFolderSaysWhatHappened covers the permission path. The folder is
// made read-only for the duration and put back, so t.TempDir can remove it.
func TestAReadOnlyFolderSaysWhatHappened(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only folder anyway")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	_, err := createPath(locked, "nope.txt")
	if err == nil {
		t.Fatal("a create in a read-only folder was allowed")
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("the error was %v, want a permission error", err)
	}
	if got := fileOpError(err); got != "You can not write to that folder." {
		t.Errorf("the message is %q, want the write-permission sentence", got)
	}
}

func TestCopyTreeKeepsASymlinkALink(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "target.txt"), "target")
	if err := os.Symlink("target.txt", filepath.Join(dir, "link")); err != nil {
		t.Skipf("this filesystem has no symlinks: %v", err)
	}
	if err := copyTree(filepath.Join(dir, "link"), filepath.Join(dir, "copy")); err != nil {
		t.Fatalf("copying a symlink: %v", err)
	}
	info, err := os.Lstat(filepath.Join(dir, "copy"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the copy is not a symlink; a link was followed instead of copied")
	}
}

// mkdirAllForTest is os.MkdirAll under a name the test files share.
func mkdirAllForTest(path string) error { return os.MkdirAll(path, 0o755) }
