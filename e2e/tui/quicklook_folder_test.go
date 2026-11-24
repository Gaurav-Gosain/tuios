package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// quickLookFolderStart writes ~/proj/walkdir, with a folder deep that holds
// leaf.txt, and the files alpha.txt and beta.txt. It lists ~/proj in the
// rail's files section. The folder is in the test's HOME, so a frame shows
// ~/proj and no scratch folder.
func quickLookFolderStart(t *testing.T) *tuitest.Terminal {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(xdgDir(base, "HOME"), "proj")
	quickLookFolderTree(t, dir)
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"TUIOS_KITTY_GRAPHICS=0"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 1, "opening a shell for the listing")
	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/proj && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
	leaveTerminalMode(t, term)
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "the listing", "walkdir/", "top.txt")
	return term
}

// quickLookFolderTree writes walkdir and top.txt into dir.
func quickLookFolderTree(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"walkdir/deep/leaf.txt": "the leaf text line\n",
		"walkdir/alpha.txt":     "the alpha text line\n",
		"walkdir/beta.txt":      "the beta text line\n",
		"top.txt":               "the top text line\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// quickLookSelected waits for the row of name to be the lit row of a folder
// in quick look: its cells on another ground than the row under it, which
// is another row of the folder or the empty panel.
func quickLookSelected(t *testing.T, term *tuitest.Terminal, why, name string) {
	t.Helper()
	lit := func(s tuitest.Screen) bool {
		col, row, ok := quickLookPanelFind(s, name)
		if !ok {
			return false
		}
		for i := range len(name) {
			if s.Cell(col+i, row).Bg == s.Cell(col+i, row+1).Bg {
				return false
			}
		}
		return true
	}
	if err := term.WaitFor(lit, uiTimeout); err != nil {
		s := term.Screen()
		col, row, ok := quickLookPanelFind(s, name)
		t.Fatalf("ASSERTION: %s: the row %s is not selected (found %v at %d,%d: ground %v, under it %v): %v\n%s", why, name, ok, col, row,
			s.Cell(col, row).Bg, s.Cell(col, row+1).Bg, err, term.Snapshot())
	}
}

// quickLookPanelFind finds want on screen right of the rail, where quick look
// draws, so a row of the rail with the same name does not count.
func quickLookPanelFind(s tuitest.Screen, want string) (col, row int, ok bool) {
	cols, rows := s.Size()
	for y := range rows {
		for x := quickLookPanelLeft; x < cols; x++ {
			if gridMatchesAt(s, x, y, want) {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// quickLookPanelLeft is the first column right of the rail at 140 columns.
const quickLookPanelLeft = 28

// TestQuickLookWalksAFolder opens quick look on a folder of the rail's files
// section and walks it. The rows show each file's size and age. Tab goes
// inside, enter opens the folder deep, and enter on leaf.txt shows its text
// in place. Backspace goes back to deep with leaf.txt selected, and h goes up
// to walkdir with deep selected. A click on alpha.txt selects it, and a
// second click opens it. r shows walkdir in the rail's files section, and
// the preview follows the rail's cursor again. Two backspaces in one write
// keep the keyboard inside, and a click on the rail while walked ends the
// walk. The
// artifacts are the frames quick-look-folder-open, quick-look-folder-inside,
// quick-look-folder-file and quick-look-folder-rail.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on main, the test fails at "the folder rows show no size and age".
//   - with the walked check cut from QuickLookSync, the test fails at
//     "enter did not open deep": the preview goes back to the rail's row.
//   - with quickLookDirPick not called from applyQuickLook, the test fails
//     at "backspace from alpha.txt".
//   - with quickLookFolderClick not called from QuickLookMouseClick, the
//     test fails at "a click on alpha.txt".
//   - with quickLookShow setting inside to false during a walk, the test
//     fails at "two backspaces".
func TestQuickLookWalksAFolder(t *testing.T) {
	term := quickLookFolderStart(t)
	quickLookFocusRail(t, term, "walkdir")
	sendKeys(t, term, " ")
	railLists(t, term, "space did not show walkdir", []string{"walkdir/ ", "deep/", "alpha.txt", "beta.txt", "3 items"}, nil)
	saveCockpitFrames(t, term, "quick-look-folder-open")
	// Each file row ends with its size and its age.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		txt := s.Text()
		return strings.Contains(txt, "20 B") && strings.Contains(txt, "<1m")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the folder rows show no size and age: %v\n%s", err, term.Snapshot())
	}

	sendKeys(t, term, tuitest.Tab)
	railLists(t, term, "tab did not go inside walkdir", []string{"Inside the folder"}, nil)
	quickLookSelected(t, term, "tab", "deep/")
	saveCockpitFrames(t, term, "quick-look-folder-inside")

	// Two backspaces in one write: the second walk starts while the read of
	// ~/proj is out. The keyboard stays inside, on the folder ~/proj left.
	sendKeys(t, term, "\x7f\x7f")
	railLists(t, term, "two backspaces did not keep the keyboard inside the home folder", []string{"~ · 1 item", "Inside the folder"}, nil)
	quickLookSelected(t, term, "two backspaces", "proj/ ")
	sendKeys(t, term, tuitest.Enter)
	railLists(t, term, "enter on proj", []string{"~/proj · 2 items"}, nil)
	quickLookSelected(t, term, "enter on proj", "walkdir/ ")
	sendKeys(t, term, tuitest.Enter)
	railLists(t, term, "enter on walkdir", []string{"walkdir/ ", "3 items", "Inside the folder"}, nil)
	quickLookSelected(t, term, "enter on walkdir", "deep/")

	sendKeys(t, term, tuitest.Enter)
	railLists(t, term, "enter did not open deep", []string{"deep/ ", "leaf.txt", "1 item"}, []string{"alpha.txt"})
	sendKeys(t, term, tuitest.Enter)
	railLists(t, term, "enter on leaf.txt did not show its text", []string{"the leaf text line"}, nil)
	saveCockpitFrames(t, term, "quick-look-folder-file")

	sendKeys(t, term, tuitest.Backspace)
	railLists(t, term, "backspace from leaf.txt did not show deep", []string{"deep/ ", "1 item"}, []string{"the leaf text line"})
	quickLookSelected(t, term, "backspace from leaf.txt", "leaf.txt")
	sendKeys(t, term, "h")
	railLists(t, term, "h from deep did not show walkdir", []string{"walkdir/ ", "3 items"}, nil)
	quickLookSelected(t, term, "h from deep", "deep/")

	col, row, ok := findOnGrid(term.Screen(), "alpha.txt")
	if !ok {
		t.Fatalf("no alpha.txt row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	quickLookSelected(t, term, "a click on alpha.txt", "alpha.txt")
	mouseClick(t, term, col+2, row, tuitest.MouseLeft, 0)
	railLists(t, term, "a click on the selected row did not open alpha.txt", []string{"the alpha text line"}, nil)

	sendKeys(t, term, tuitest.Backspace)
	quickLookSelected(t, term, "backspace from alpha.txt", "alpha.txt")
	sendKeys(t, term, "r")
	// The rail lists walkdir now, and the preview follows its cursor, which is
	// on alpha.txt.
	railLists(t, term, "r did not show walkdir in the rail", []string{"the alpha text line", "deep/"}, []string{"Inside the folder", "top.txt"})
	saveCockpitFrames(t, term, "quick-look-folder-rail")
	// The preview follows the rail's cursor again: j moves it to beta.txt.
	sendKeys(t, term, "j")
	railLists(t, term, "j on the rail after r", []string{"the beta text line"}, []string{"the alpha text line"})

	// Walk again, then click a row of the rail: the walked preview does not
	// stay on screen.
	sendKeys(t, term, tuitest.Tab)
	sendKeys(t, term, tuitest.Backspace)
	railLists(t, term, "backspace from beta.txt", []string{"3 items", "Inside the folder"}, []string{"the beta text line"})
	bc, br, ok := findOnGrid(term.Screen(), "back to pane folder")
	if !ok {
		t.Fatalf("no back row in the rail:\n%s", term.Snapshot())
	}
	mouseClick(t, term, bc+2, br, tuitest.MouseLeft, 0)
	railLists(t, term, "a click on the rail while walked", []string{"top.txt"}, []string{"Inside the folder", "3 items"})
}
