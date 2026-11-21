package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
// Markdown file. The ruler on the panel's right edge marks the Markdown
// file's headings, a drag of its thumb moves the view to the end, and a
// click on its track moves the view there. In a log read in windows, a
// click on the track reads the window at that place. The artifacts are the
// frames quick-look-hover, quick-look-drag-markdown, quick-look-ruler-marks,
// quick-look-ruler-drag, quick-look-ruler-click and quick-look-ruler-log.
//
// NEGATIVE CONTROLS (e2e/tui/NEGATIVE_CONTROLS.md):
//   - on main, the test fails at "a hover over quick look moved the focus
//     to the pane under it".
//   - with QuickLookTakesMotion not called from handleMouseMotion, the same.
//   - with quickLookCopyPress not called from handleMouseClick, the test
//     fails at "a drag over the text in quick look".
//   - with the Markdown case cut from quickLookCopyBegin, the test fails at
//     "a drag over Markdown in quick look".
//   - with quickLookRulerClick not called from QuickLookMouseClick, the
//     test fails at "a drag of the ruler's thumb".
//   - with the quickLookAt case cut from quickLookWindow, the test fails at
//     "a click on the log's ruler".
func TestQuickLookTakesTheMouse(t *testing.T) {
	const session = "qlmouse"
	base := t.TempDir()
	pathsProject(t, base)
	var md strings.Builder
	md.WriteString("# Notes heading\n\nThe first paragraph of the notes.\n\n- one item\n- two item\n")
	for part := 1; part <= quickLookRulerParts; part++ {
		fmt.Fprintf(&md, "\n## Part %d\n", part)
		for p := 1; p <= 6; p++ {
			fmt.Fprintf(&md, "\nParagraph %d of part %d.\n", p, part)
		}
	}
	md.WriteString("\nThe end of the notes.\n")
	if err := os.WriteFile(filepath.Join(xdgDir(base, "HOME"), "proj", "notes.md"), []byte(md.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for i := range quickLookRulerLogRows {
		fmt.Fprintf(&log, "big row %06d %s\n", i, strings.Repeat(".", 48))
	}
	if err := os.WriteFile(filepath.Join(xdgDir(base, "HOME"), "proj", "big.log"), []byte(log.String()), 0o644); err != nil {
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
	mouseHover(t, term, 120, 20)
	right := ""
	waitUntil(t, "the focus settles on the right pane", uiTimeout, func() (bool, string) {
		cur := focused()
		same := cur != "" && cur == right
		right = cur
		return same, term.Snapshot()
	})

	enterTerminalMode(t, term)
	runInShell(t, term, "cd ~/proj && clear && echo CD''DONE", "CDDONE", uiTimeout)
	runInShell(t, term, "printf 'at %s:%s\\nmd %s\\nlog %s\\n' "+pathsFile+" 120 ./notes.md ./big.log", "log ./big.log", uiTimeout)
	leaveTerminalMode(t, term)
	_ = term.WaitStable(uiTimeout)
	col, row := mustFind(t, term, pathsFile+":120")
	mouseClick(t, term, col+5, row, tuitest.MouseLeft, tuitest.ModCtrl)
	waitQuickLookAt(t, term, "ctrl+click on the path")
	if got := focused(); got != right {
		t.Fatalf("the focus is on %s after the click, want the right pane %s", got, right)
	}

	tc, tr := mustFind(t, term, "line 121 of os.go")
	if tc+2 >= 70 {
		t.Fatalf("the panel does not reach over the left pane: its text starts at column %d\n%s", tc, term.Snapshot())
	}
	mouseHover(t, term, tc+2, tr)
	stayTrue(t, "a hover over quick look moved the focus to the pane under it", time.Second, func() (bool, string) {
		return focused() == right, fmt.Sprintf("focused %s, want %s\n%s", focused(), right, term.Snapshot())
	})
	saveCockpitFrames(t, term, "quick-look-hover")

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

	sendEach(t, term, tuitest.Esc)
	var g quickLookRulerCells
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		g = quickLookRulerRight(s, pc, pr)
		return g.rows > 0 && strings.Count(g.cells, "━") == 1+quickLookRulerParts
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the ruler does not mark the title and the %d parts: %q: %v\n%s", quickLookRulerParts, g.cells, err, term.Snapshot())
	}
	if !strings.HasPrefix(g.cells, "━") {
		t.Fatalf("ASSERTION: the title on the first line has no mark on the ruler's first row: %q", g.cells)
	}
	saveCockpitFrames(t, term, "quick-look-ruler-marks")

	thumb := strings.Index(g.cells, "┃")
	if thumb < 0 {
		t.Fatalf("no thumb on the ruler: %q", g.cells)
	}
	mouseDrag(t, term, g.x, g.top+thumb, g.x, g.top+g.rows-1, tuitest.MouseLeft, 0)
	railLists(t, term, "a drag of the ruler's thumb did not show the end", []string{"The end of the notes."}, []string{"Notes heading"})
	saveCockpitFrames(t, term, "quick-look-ruler-drag")

	mouseClick(t, term, g.x, g.top, tuitest.MouseLeft, 0)
	railLists(t, term, "a click on the top of the ruler did not show the start", []string{"Notes heading"}, []string{"The end of the notes."})
	mid := g.top + g.rows*2/3
	mouseClick(t, term, g.x, mid, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		g = quickLookRulerRight(s, pc, pr)
		at := mid - g.top
		return !strings.Contains(text, "Notes heading") && !strings.Contains(text, "The end of the notes.") &&
			at < len([]rune(g.cells)) && []rune(g.cells)[at] != '│'
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click in the ruler's middle did not take the thumb there: %q: %v\n%s", g.cells, err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "quick-look-ruler-click")

	sendKeys(t, term, tuitest.Esc)
	waitQuickLookClosed(t, term)
	lc, lr := mustFind(t, term, "log ./big.log")
	mouseClick(t, term, lc+6, lr, tuitest.MouseLeft, tuitest.ModCtrl)
	railLists(t, term, "ctrl+click on big.log", []string{"big row 000000"}, nil)
	rc, rr := mustFind(t, term, "big row 000001")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		g = quickLookRulerRight(s, rc, rr)
		return g.rows > 0
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the log has no ruler: %v\n%s", err, term.Snapshot())
	}
	mouseClick(t, term, g.x, g.top+g.rows/2, tuitest.MouseLeft, 0)
	var shown int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		m := quickLookBigRow.FindStringSubmatch(s.Text())
		if m == nil {
			return false
		}
		shown, _ = strconv.Atoi(m[1])
		return shown > quickLookRulerLogRows/4 && shown < quickLookRulerLogRows*3/4
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click on the log's ruler did not read the middle of the log: the top row is %d: %v\n%s", shown, err, term.Snapshot())
	}
	saveCockpitFrames(t, term, "quick-look-ruler-log")
	alive(t, term, "after the quick look mouse")
}

const (
	quickLookRulerParts   = 6
	quickLookRulerLogRows = 40000
)

var quickLookBigRow = regexp.MustCompile(`big row (\d{6})`)

type quickLookRulerCells struct {
	x, top, rows int
	cells        string
}

func quickLookRulerRight(s tuitest.Screen, col, row int) quickLookRulerCells {
	ruler := func(x, y int) bool {
		switch s.Cell(x, y).Content {
		case "│", "┃", "━":
			return true
		}
		return false
	}
	cols, rows := s.Size()
	x := col
	for x < cols && s.Cell(x, row).Content != " " && s.Cell(x, row).Content != "" {
		x++
	}
	for x < cols && !ruler(x, row) {
		x++
	}
	if x >= cols {
		return quickLookRulerCells{}
	}
	top, bottom := row, row
	for top > 0 && ruler(x, top-1) {
		top--
	}
	for bottom+1 < rows && ruler(x, bottom+1) {
		bottom++
	}
	var b strings.Builder
	for y := top; y <= bottom; y++ {
		b.WriteString(s.Cell(x, y).Content)
	}
	return quickLookRulerCells{x: x, top: top, rows: bottom - top + 1, cells: b.String()}
}
