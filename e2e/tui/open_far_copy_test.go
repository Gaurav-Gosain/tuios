package tuie2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// o on a file of another machine copies it here and opens the copy. The steps
// run at the end of TestQuickLookOnAFarFile, which has two daemons over the
// fake ssh link and the recording opener.
//
// How this could pass wrongly, written down first:
//   - The opener could run on the far path. The two machines share a disk in
//     this harness, so that path is a real file here too. The record must name
//     the copy under this client's cache, never the far path.
//   - The copy could be a stale file, or no copy at all. Its bytes are compared
//     with the far file after each open, and after a far change.
//   - A second open could copy again and look the same. The daemon's list of
//     copies must hold one copy of notes.md after the second open, and two
//     after the far file changed.
//   - A folder copy could follow a link out of the folder. inner/escape names
//     a file outside it, and the copy must not hold it.
//   - The size ask could be skipped, or open with no second yes. The record
//     must not change between the first yes and the second.
//   - A crafted name could land outside the cache. ..\..\x.txt must land in
//     the copy's own folder, as one name.
//
// NEGATIVE CONTROL: see NEGATIVE_CONTROLS.md, "o opens a copy of a far file".

// farCopyBig is the size of the file over the ask limit the fixture sets.
const farCopyBig = 2 << 20

// farCopyCrafted is a far name that a client that joined it as a path on
// Windows would read as two folders up.
const farCopyCrafted = `..\..\x.txt`

// farCopyFixture writes the far files the steps open, and sets the ask limit
// to 1 MiB.
func farCopyFixture(t *testing.T, base, top string) {
	t.Helper()
	for name, body := range map[string]string{
		"notes.md":     "# notes\none\n",
		farCopyCrafted: "crafted\n",
	} {
		if err := os.WriteFile(filepath.Join(top, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(top, "zbig.bin"), make([]byte, farCopyBig), 0o600); err != nil {
		t.Fatal(err)
	}
	// A link in the far folder to a file outside it.
	outside := filepath.Join(filepath.Dir(top), "outside-secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(top, "inner", "escape")); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(base, "XDG_CONFIG_HOME", "tuios", "config.toml")
	f, err := os.OpenFile(cfg, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("\n[files]\nopen_copy_ask_mb = 1\n"); err != nil {
		t.Fatal(err)
	}
}

// farCopyPath is where the copy of build's path lands: the cache, the host,
// a hash of the host and the far path, and the name.
func farCopyPath(base, farPath, name string) string {
	sum := sha256.Sum256([]byte("build\x00" + farPath))
	return filepath.Join(farCopyRoot(base), "build", hex.EncodeToString(sum[:8]), name)
}

// farCopyRoot is the folder every copy is under.
func farCopyRoot(base string) string {
	return filepath.Join(xdgDir(base, "XDG_CACHE_HOME"), "tuios", "opened")
}

// farCopies counts the daemon's copies of name.
func farCopies(t *testing.T, base string, env []string, name string) int {
	t.Helper()
	out, err := tuiosCLIEnv(t, base, env, "transfers", "--json")
	if err != nil {
		t.Fatalf("tuios transfers --json: %v\n%s", err, out)
	}
	var list struct {
		Transfers []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"transfers"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("tuios transfers --json: %v\n%s", err, out)
	}
	n := 0
	for _, r := range list.Transfers {
		if r.Name == name {
			n++
		}
	}
	return n
}

// takeOffer clicks the dock message that holds text.
func takeOffer(t *testing.T, term *tuitest.Terminal, text string) {
	t.Helper()
	waitForAll(t, term, uiTimeout, "the offer", text)
	col, row, ok := findOnGrid(term.Screen(), text)
	if !ok {
		t.Fatalf("no offer %q on screen:\n%s", text, term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
}

// sameCopyBytes fails unless the copy holds the far file's bytes.
func sameCopyBytes(t *testing.T, copyPath, farPath, what string) {
	t.Helper()
	got, err := os.ReadFile(copyPath)
	if err != nil {
		t.Fatalf("%s: the copy is not there: %v", what, err)
	}
	want, err := os.ReadFile(farPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s: the copy holds %q, the far file %q", what, got, want)
	}
}

// openFarCopies is the steps: open a far file, open it again, open it after
// it changed, a folder, a file over the ask limit, and a crafted name.
func openFarCopies(t *testing.T, term *tuitest.Terminal, base string, env []string, top, record string) {
	t.Helper()
	art := artifactDir(t)
	var want []string
	open := func(name string) {
		t.Helper()
		col, row, ok := findOnGrid(term.Screen(), name)
		if !ok {
			t.Fatalf("no %s row:\n%s", name, term.Snapshot())
		}
		mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
		clickNoteGone(t, term)
		sendKeys(t, term, "o")
	}

	// The offer from the start of the test was left in the dock: not now.
	// notes.md, opened the first time: the copy is made, then opened.
	notes := filepath.Join(top, "notes.md")
	notesCopy := farCopyPath(base, notes, "notes.md")
	open("notes.md")
	takeOffer(t, term, "notes.md is on build. Click to open a copy here.")
	want = append(want, "xdg-open "+notesCopy)
	waitOpenRecord(t, term, record, want, "the first open of a far file")
	sameCopyBytes(t, notesCopy, notes, "the first open")
	waitForAll(t, term, uiTimeout, "the note of the copy", "Opened a copy of notes.md with xdg-open.")
	copiesFrame(t, term, "open-far-copy-opened")
	saveArtifact(t, term, art, "open-far-copy-opened")
	if n := farCopies(t, base, env, "notes.md"); n != 1 {
		t.Fatalf("the first open made %d copies of notes.md, want 1", n)
	}

	// Again, with the far file as it was: the copy here opens, and nothing
	// is copied. The jump key takes the offer this time.
	open("notes.md")
	waitForAll(t, term, uiTimeout, "the second offer", "notes.md is on build. Click to open a copy here.")
	sendKeys(t, term, tuitest.Ctrl('b'), "j")
	want = append(want, "xdg-open "+notesCopy)
	waitOpenRecord(t, term, record, want, "the second open of a far file")
	if n := farCopies(t, base, env, "notes.md"); n != 1 {
		t.Fatalf("the second open of an unchanged file made a copy: %d copies of notes.md, want 1", n)
	}

	// The far file changes: the next open copies it again.
	if err := os.WriteFile(notes, []byte("# notes\none\ntwo, from build\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(notes, later, later); err != nil {
		t.Fatal(err)
	}
	open("notes.md")
	takeOffer(t, term, "notes.md is on build. Click to open a copy here.")
	want = append(want, "xdg-open "+notesCopy)
	waitOpenRecord(t, term, record, want, "the open after the far file changed")
	sameCopyBytes(t, notesCopy, notes, "the open after the far file changed")
	if n := farCopies(t, base, env, "notes.md"); n != 2 {
		t.Fatalf("the open after a far change made %d copies of notes.md in all, want 2", n)
	}

	// A folder, from the row's menu: the copy holds the folder's files and
	// not the link that names a file outside it.
	inner := filepath.Join(top, "inner")
	innerCopy := farCopyPath(base, inner, "inner")
	col, row, ok := findOnGrid(term.Screen(), "inner/")
	if !ok {
		t.Fatalf("no inner/ row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col, row, tuitest.MouseRight, 0)
	waitForAll(t, term, uiTimeout, "the folder menu", "Open with system")
	col, row, _ = findOnGrid(term.Screen(), "Open with system")
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	takeOffer(t, term, "inner is on build. Click to open a copy here.")
	want = append(want, "xdg-open "+innerCopy)
	waitOpenRecord(t, term, record, want, "the open of a far folder")
	for _, f := range []string{"inner-keep.txt", "inner-gone.txt"} {
		sameCopyBytes(t, filepath.Join(innerCopy, f), filepath.Join(inner, f), "the folder copy")
	}
	if _, err := os.Lstat(filepath.Join(innerCopy, "escape")); err == nil {
		t.Fatalf("the folder copy holds the link to a file outside the far folder")
	}
	railLists(t, term, "the rail left the far folder", []string{"notes.md", "zbig.bin"}, nil)

	// A file over the ask limit: the first yes shows the size and asks
	// again, and only the second copies it.
	big := filepath.Join(top, "zbig.bin")
	bigCopy := farCopyPath(base, big, "zbig.bin")
	open("zbig.bin")
	takeOffer(t, term, "zbig.bin is on build. Click to open a copy here.")
	waitForAll(t, term, uiTimeout, "the size ask", "zbig.bin on build is 2.00 MiB. Click to copy it here and open it.")
	copiesFrame(t, term, "open-far-copy-size-ask")
	time.Sleep(500 * time.Millisecond)
	waitOpenRecord(t, term, record, want, "the size ask opened the file before the second yes")
	if _, err := os.Stat(bigCopy); err == nil {
		t.Fatalf("the size ask copied the file before the second yes")
	}
	takeOffer(t, term, "zbig.bin on build is 2.00 MiB.")
	want = append(want, "xdg-open "+bigCopy)
	waitOpenRecord(t, term, record, want, "the open of a file over the ask limit")
	sameCopyBytes(t, bigCopy, big, "the copy of a file over the ask limit")

	// A crafted name lands in the copy's own folder, as one name.
	crafted := filepath.Join(top, farCopyCrafted)
	craftedCopy := farCopyPath(base, crafted, ".._.._x.txt")
	open(farCopyCrafted)
	takeOffer(t, term, farCopyCrafted+" is on build.")
	want = append(want, "xdg-open "+craftedCopy)
	waitOpenRecord(t, term, record, want, "the open of a crafted name")
	sameCopyBytes(t, craftedCopy, crafted, "the copy of a crafted name")
	rel, err := filepath.Rel(farCopyRoot(base), craftedCopy)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Count(rel, string(filepath.Separator)) != 2 {
		t.Fatalf("the crafted name landed at %s, outside the copy's own folder", craftedCopy)
	}
	copiesFrame(t, term, "open-far-copy-crafted")

	// clear-opened deletes them all.
	out, err := tuiosCLIEnv(t, base, env, "transfers", "clear-opened")
	if err != nil || !strings.Contains(out, "Deleted 4 copies of far files") {
		t.Fatalf("tuios transfers clear-opened: %v\n%s", err, out)
	}
	if _, err := os.Stat(farCopyRoot(base)); err == nil {
		t.Fatalf("clear-opened left %s", farCopyRoot(base))
	}
}
