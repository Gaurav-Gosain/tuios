package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The rail's files section, on the host's own grid.
//
// Everything else that covers the section renders the rail in process and reads
// the strings back. This drives it the way a person does: a real shell reports a
// real directory, a real click puts the section on the rail, and the names are
// read off the terminal the client drew into. Model state and pixels disagreeing
// is the failure this codebase keeps hitting, and a rail that lays out correctly
// in a unit test and lands in the wrong columns on screen would pass every other
// test the section has.

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

// TestRailFilesSectionListsThePanesFolder.
//
// Negative control, confirmed red: skip the files section in the layout draw
// loop of sidebarPanelLinesForTree and the wait for the listing times out.
func TestRailFilesSectionListsThePanesFolder(t *testing.T) {
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

	// No gesture opens it. The section is part of the rail's shipped layout and
	// it follows the focused pane, so the listing arrives on its own once the
	// shell has said where it is. That is the change this branch made, and
	// waiting for it rather than clicking for it is what checks it.

	// The listing sits beside the rail's other sections rather than replacing
	// them, which is the design decision this branch changed: both halves are
	// checked, so a listing that took the rail over fails as loudly as one that
	// never appears.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "alpha/") && strings.Contains(txt, sidebarHeader)
	}, uiTimeout); err != nil {
		t.Fatalf("the files section never listed the pane's folder: %v\n%s", err, term.Snapshot())
	}

	snap := term.Screen()
	txt := snap.Text()
	for _, want := range []string{"alpha/", "zulu/", "brief.txt", filepath.Base(dir)} {
		if !strings.Contains(txt, want) {
			t.Errorf("the listing does not show %q:\n%s", want, term.Snapshot())
		}
	}
	// A folder wears a trailing slash and a file does not, which is the whole
	// of `ls -F`'s distinction and is what the row says with no icon at all.
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

	// The footer control is the switch, and it is the way off as well as on: a
	// section the user cannot take off the rail is one they are stuck with.
	col, row, ok := findFooterFiles(term.Screen())
	if !ok {
		t.Fatalf("the rail drew no files control:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+1, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return !strings.Contains(txt, "brief.txt") && strings.Contains(txt, sidebarHeader)
	}, uiTimeout); err != nil {
		t.Fatalf("the footer control did not take the section off the rail: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after browsing the rail's files section")
}

// findFooterFiles locates the footer's "files" control, which is the last row
// of the rail. The word also names the section's own header, so a plain search
// for it finds the header first and clicks a path instead of a control.
func findFooterFiles(s tuitest.Screen) (int, int, bool) {
	lines := strings.Split(s.Text(), "\n")
	for row := len(lines) - 1; row >= 0; row-- {
		if col := strings.Index(lines[row], "files"); col >= 0 {
			return col, row, true
		}
	}
	return 0, 0, false
}
