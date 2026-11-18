package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestQuickLookTakesTheMouse opens quick look from a path in the right pane
// of two, with focus_follows_mouse on. The pointer over the panel is the
// panel's: a hover over it where the left pane lies under it keeps the focus
// on the right pane. A drag over the text in the panel selects and the
// release copies, with no copy mode key first, in a text file and in a
// Markdown file. The artifacts are the frames quick-look-hover and
// quick-look-drag-markdown.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on main, the test fails at "a hover over quick look moved the focus
//     to the pane under it".
//   - with QuickLookTakesMotion not called from handleMouseMotion, the same.
//   - with quickLookCopyPress not called from handleMouseClick, the test
//     fails at "a drag over the text in quick look".
//   - with the Markdown case cut from quickLookCopyBegin, the test fails at
//     "a drag over Markdown in quick look".
func TestQuickLookTakesTheMouse(t *testing.T) {
	const session = "qlmouse"
	base := t.TempDir()
	pathsProject(t, base)
	md := "# Notes heading\n\nThe first paragraph of the notes.\n\n- one item\n- two item\n"
	if err := os.WriteFile(filepath.Join(xdgDir(base, "HOME"), "proj", "notes.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, base, "[appearance]\nfocus_follows_mouse = true\n")
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, out: out, shippedLooks: true, args: []string{"new", session},
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor", "TUIOS_KITTY_GRAPHICS=0"}})
	killDaemon(t, base)
	useShippedLooks(base)
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	waitWindowCount(t, term, 2, "two panes for the hover")
	enableTiling(t, term)
	focused := func() string {
		wl, err := daemonWindows(base, session)
		if err != nil {
			t.Fatalf("list-windows: %v", err)
		}
		return wl.FocusedWindowID
	}
	// Put the pointer on the right pane, so the focus is there.
	mouseHover(t, term, 120, 20)
	// The focus has settled once two reads in a row name the same pane.
	right := ""
	waitUntil(t, "the focus settles on the right pane", uiTimeout, func() (bool, string) {
		cur := focused()
		same := cur != "" && cur == right
		right = cur
		return same, term.Snapshot()
	})

	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/proj && clear && echo CD''DONE", "CDDONE", uiTimeout)
	runInShell(t, term, "printf 'at %s:%s\\nmd %s\\n' "+pathsFile+" 120 ./notes.md", "md ./notes.md", uiTimeout)
	leaveTerminalMode(t, term)
	_ = term.WaitStable(uiTimeout)
	col, row := mustFind(t, term, pathsFile+":120")
	mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitQuickLookAt(t, term, "ctrl+click on the path")
	if got := focused(); got != right {
		t.Fatalf("the focus is on %s after the click, want the right pane %s", got, right)
	}

	// The panel's text starts left of the middle, over the left pane.
	tc, tr := mustFind(t, term, "line 121 of os.go")
	if tc+2 >= 70 {
		t.Fatalf("the panel does not reach over the left pane: its text starts at column %d\n%s", tc, term.Snapshot())
	}
	mouseHover(t, term, tc+2, tr)
	stayTrue(t, "a hover over quick look moved the focus to the pane under it", time.Second, func() (bool, string) {
		return focused() == right, fmt.Sprintf("focused %s, want %s\n%s", focused(), right, term.Snapshot())
	})
	saveCockpitFrames(t, term, "quick-look-hover")

	// A drag over the text selects it, and the release copies.
	from := len(clipboardWrites(out))
	mouseDrag(t, term, tc, tr, tc+len("line 121")-1, tr, tuitest.MouseLeft, 0)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "quick-look-drag-text")
	if got := rawWrite(t, term, out, from, "ASSERTION: a drag over the text in quick look"); got != "line 121" {
		t.Fatalf("ASSERTION: a drag over the text in quick look copied %q, want %q", got, "line 121")
	}
	if got := focused(); got != right {
		t.Fatalf("ASSERTION: the drag over quick look moved the focus to %s, want %s", got, right)
	}

	// The Markdown file: the same drag over its paragraph. The first esc
	// ends the selection, and the second closes quick look.
	sendEach(t, term, tuitest.Esc)
	if !strings.Contains(term.Screen().Text(), "line 121 of os.go") {
		t.Fatalf("ASSERTION: the esc that ends the selection closed quick look\n%s", term.Snapshot())
	}
	sendKeys(t, term, tuitest.Esc)
	waitQuickLookClosed(t, term)
	mc, mr := mustFind(t, term, "md ./notes.md")
	mouseClick(t, term, mc+6, mr, tuitest.MouseLeft, tuitest.ModCtrl)
	railLists(t, term, "ctrl+click on notes.md", []string{"Notes heading", "The first paragraph"}, nil)
	pc, pr := mustFind(t, term, "The first paragraph")
	from = len(clipboardWrites(out))
	mouseDrag(t, term, pc+4, pr, pc+len("The first paragraph")-1, pr, tuitest.MouseLeft, 0)
	_ = term.WaitStable(uiTimeout)
	saveCockpitFrames(t, term, "quick-look-drag-markdown")
	if got := rawWrite(t, term, out, from, "ASSERTION: a drag over Markdown in quick look"); got != "first paragraph" {
		t.Fatalf("ASSERTION: a drag over Markdown in quick look copied %q, want %q", got, "first paragraph")
	}
	if !strings.Contains(term.Screen().Text(), "Notes heading") {
		t.Fatalf("ASSERTION: quick look closed after the drag\n%s", term.Snapshot())
	}
	alive(t, term, "after the quick look mouse")
}
