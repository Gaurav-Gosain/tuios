package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's file view, on the host's own grid.
//
// Everything else that covers this mode renders the rail in process and reads
// the strings back. This drives it the way a person does: a real shell reports a
// real directory, a real click opens the listing, and the names are read off the
// terminal the client drew into. Model state and pixels disagreeing is the
// failure this codebase keeps hitting, and a rail that lays out correctly in a
// unit test and lands in the wrong columns on screen would pass every other test
// this mode has.

// fileViewFixture builds a directory whose listing has a knowable shape: two
// folders and one file, named so that folders-first and plain alphabetical
// disagree.
func fileViewFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"zulu", "alpha"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestRailFileViewListsThePanesFolder.
//
// Negative control, confirmed red: with the filesView.Open branch removed from
// sidebarPanelLinesForTree the rail keeps drawing "sessions" and the wait for
// the listing times out.
func TestRailFileViewListsThePanesFolder(t *testing.T) {
	dir := fileViewFixture(t)

	term, _ := start(t, startOpts{})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")

	// The pane says where it is, the way a shell with OSC 7 does. The harness
	// shell has no such integration of its own, so the escape is printed
	// outright: it is the same sequence and the same handler either way, and a
	// test that depended on which shell the machine has would be testing that.
	enterTerminalMode(t, term)
	runInShell(t, term, "cd "+dir+" && printf 'at %s\\n' \"$PWD\"", "at "+dir, uiTimeout)
	runInShell(t, term,
		`printf '\033]7;file://%s\033\\marked\n' "$PWD"`, "marked", uiTimeout)
	leaveTerminalMode(t, term)

	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("the rail never came up: %v\n%s", err, term.Snapshot())
	}

	// The way in is the footer's own control, clicked where it is drawn. The
	// rectangle is the renderer's, so a click that lands on the word is a click
	// on the zone.
	col, row, ok := findOnGrid(term.Screen(), "files")
	if !ok {
		t.Fatalf("the rail drew no files control:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)

	// The listing replaces the sections outright, which is the whole design
	// decision: both halves are checked, so a mode drawn on top of the sections
	// fails as loudly as one that never appears.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "alpha/") && !strings.Contains(txt, sidebarHeader)
	}, uiTimeout); err != nil {
		t.Fatalf("the file view never listed the pane's folder: %v\n%s", err, term.Snapshot())
	}

	snap := term.Screen()
	txt := snap.Text()
	for _, want := range []string{"alpha/", "zulu/", "brief.txt", filepath.Base(dir)} {
		if !strings.Contains(txt, want) {
			t.Errorf("the listing does not show %q:\n%s", want, term.Snapshot())
		}
	}
	// A folder wears a trailing slash and a file does not, which is the whole
	// of `ls -F`'s distinction and needs no glyph.
	if strings.Contains(txt, "brief.txt/") {
		t.Errorf("a file was drawn as a folder:\n%s", term.Snapshot())
	}
	// Folders first, so alpha and zulu are both above brief.txt.
	if _, zRow, ok := findOnGrid(snap, "zulu/"); ok {
		if _, bRow, ok := findOnGrid(snap, "brief.txt"); ok && zRow > bRow {
			t.Errorf("zulu/ is on row %d, below brief.txt on row %d", zRow, bRow)
		}
	}

	// Clicking a folder walks the listing into it, and nothing is typed at the
	// pane: the shell is still where it was.
	aCol, aRow, ok := findOnGrid(snap, "alpha/")
	if !ok {
		t.Fatalf("no row to click:\n%s", term.Snapshot())
	}
	mouseClick(t, term, aCol, aRow, tuitest.MouseLeft, 0)
	// alpha holds nothing, so the listing is the way back out and the word for
	// having nothing in it. Both are the proof that the click moved the view
	// rather than that it did nothing at all.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "empty") && strings.Contains(txt, "/alpha")
	}, uiTimeout); err != nil {
		t.Fatalf("clicking a folder did not walk into it: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "cd "+filepath.Join(dir, "alpha")) {
		t.Errorf("clicking a folder typed a cd at the pane:\n%s", term.Snapshot())
	}

	alive(t, term, "after browsing the rail's file view")
}
