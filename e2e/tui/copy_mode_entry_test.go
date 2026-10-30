package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Copy mode entry and directional search against the real binary
// (discussion #233).
//
// How these could pass wrongly, written down first:
//   - The copy cursor could be found on a cell that only looks like it. The
//     cursor is configured magenta, a colour nothing else on screen uses, and
//     exactly one such cell must exist.
//   - The prompt row could be the middle row by chance. The pane is filled
//     with output first, so the prompt is on the pane's last row, far from the
//     middle.
//   - A search could match the command line rather than the output. The
//     commands spell the word with a quote gap, so only the output holds it.

// copyColorOpts runs tuios with truecolor, so the magenta ground reaches the
// screen as itself and not as the nearest palette colour.
var copyColorOpts = startOpts{env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}}

const copyCursorConfig = "[appearance.selection]\ncursor_bg = \"#ff00ff\"\n"

// copyCursorCell is the one cell drawn with the magenta copy cursor ground.
func copyCursorCell(s tuitest.Screen) (row, col int, ok bool) {
	cols, rows := s.Size()
	found := 0
	for r := range rows {
		for c := range cols {
			bg := s.Cell(c, r).Bg
			if bg.Kind == tuitest.ColorRGB && bg.R == 0xff && bg.G == 0 && bg.B == 0xff {
				row, col = r, c
				found++
			}
		}
	}
	return row, col, found == 1
}

// lastRowWith is the last screen row whose text contains s, or -1.
func lastRowWith(sc tuitest.Screen, s string) int {
	_, rows := sc.Size()
	for r := rows - 1; r >= 0; r-- {
		if strings.Contains(sc.Line(r), s) {
			return r
		}
	}
	return -1
}

func waitCopyCursor(t *testing.T, term *tuitest.Terminal, what string) (int, int) {
	t.Helper()
	var row, col int
	err := term.WaitFor(func(s tuitest.Screen) bool {
		var ok bool
		row, col, ok = copyCursorCell(s)
		return ok
	}, uiTimeout)
	if err != nil {
		t.Fatalf("%s: no copy cursor on screen: %v\n%s", what, err, term.Snapshot())
	}
	return row, col
}

// Entering copy mode puts its cursor on the prompt line, where the terminal
// cursor is, as tmux does.
func TestCopyModeEntersOnPromptLine(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig)
	term := startIn(t, base, copyColorOpts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `for i in $(seq 1 80); do echo "ln$i"; done`, "ln80", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	row, _ := waitCopyCursor(t, term, "prefix+[")
	last := lastRowWith(term.Screen(), "ln80")
	if last < 0 {
		t.Fatalf("the output's last line is not on screen\n%s", term.Snapshot())
	}
	if row != last+1 {
		t.Fatalf("the copy cursor is on row %d, want the prompt row %d below ln80\n%s", row, last+1, term.Snapshot())
	}
	if line := term.Screen().Line(row); !strings.Contains(line, "$") {
		t.Fatalf("the copy cursor row is not the prompt: %q\n%s", line, term.Snapshot())
	}
}

// A key bound to copy_mode_search_backward opens copy mode with the ? prompt,
// so the next keys search up from the prompt: tmux's
// "bind-key b copy-mode \; send-key ?".
func TestCopyModeSearchBackwardBinding(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig+
		"\n[keybindings.prefix_mode]\ncopy_mode_search_backward = [\"g\"]\n")
	term := startIn(t, base, copyColorOpts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf 'nee''dle-one\nfiller\nnee''dle-%s\nfiller\n' two`, "dle-two", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	if err := term.SendKeys(tuitest.Ctrl('b'), "g"); err != nil {
		t.Fatalf("send prefix+g: %v", err)
	}
	waitCopyCursor(t, term, "prefix+g")
	if err := term.SendKeys("needle"); err != nil {
		t.Fatalf("type query: %v", err)
	}
	// Backward from the prompt, the nearest match is the second of two.
	// The prompt draws a cursor cell between the query and the count.
	err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "?needle") && strings.Contains(s.Text(), "[2/2]")
	}, uiTimeout)
	if err != nil {
		t.Fatalf("prefix+g did not open a backward search prompt: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	two := lastRowWith(term.Screen(), "needle-two")
	err = term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r == two
	}, uiTimeout)
	if err != nil {
		t.Fatalf("the search did not land on needle-two (row %d)\n%s", two, term.Snapshot())
	}

	// n follows the search up to the first match.
	one := lastRowWith(term.Screen(), "needle-one")
	if err := term.SendKeys("n"); err != nil {
		t.Fatalf("send n: %v", err)
	}
	err = term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r == one
	}, uiTimeout)
	if err != nil {
		t.Fatalf("n after ? did not move up to needle-one (row %d)\n%s", one, term.Snapshot())
	}
}

// colOfText is the column on row where text starts, one ASCII character a
// cell, or -1.
func colOfText(s tuitest.Screen, row int, text string) int {
	cols, _ := s.Size()
	for c := 0; c+len(text) <= cols; c++ {
		k := 0
		for k < len(text) && s.Cell(c+k, row).Content == text[k:k+1] {
			k++
		}
		if k == len(text) {
			return c
		}
	}
	return -1
}

// A search match in the scrollback puts the copy cursor on the column the
// match is drawn in, after cells of several runes too.
//
// How this could pass wrongly, written down first:
//   - The match could be on the screen and not in the scrollback, which is
//     read by another path. Sixty lines follow the needles, so both are in
//     the history when the search runs.
//   - The cursor column could be compared with a column found the same wrong
//     way. The expected column is read from the screen's own cells.
//   - The column check could be wrong for any line. The ASCII needle is the
//     positive half: the same check on a line of one rune a cell.
//
// The search used to count runes as columns, so each "e" with a combining
// accent before the match, one cell of two runes, put the cursor one column
// to the right of the match.
func TestCopyModeSearchColumnInScrollback(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, copyCursorConfig+
		"\n[keybindings.prefix_mode]\ncopy_mode_search_backward = [\"g\"]\n")
	term := startIn(t, base, copyColorOpts)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term,
		`printf 'abcd nee''dle-ascii\ne\314\201e\314\201e\314\201e\314\201 nee''dle-cm\n'; seq 1 60; echo 'fil''led'`,
		"filled", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	for _, needle := range []string{"needle-ascii", "needle-cm"} {
		if err := term.SendKeys(tuitest.Ctrl('b'), "g"); err != nil {
			t.Fatalf("send prefix+g: %v", err)
		}
		// The prompt opens on the prompt row, the pane's last, at the cursor,
		// and covers the copy cursor there.
		err := term.WaitFor(func(s tuitest.Screen) bool {
			r := lastRowWith(s, "filled")
			return r >= 0 && strings.Contains(s.Line(r+1), "$ ?")
		}, uiTimeout)
		if err != nil {
			t.Fatalf("prefix+g did not open a backward search prompt: %v\n%s", err, term.Snapshot())
		}
		if err := term.SendKeys(needle); err != nil {
			t.Fatalf("type query: %v", err)
		}
		err = term.WaitFor(func(s tuitest.Screen) bool {
			return strings.Contains(s.Text(), "?"+needle) && strings.Contains(s.Text(), "[1/1]")
		}, uiTimeout)
		if err != nil {
			t.Fatalf("%s: the search found no single match: %v\n%s", needle, err, term.Snapshot())
		}
		if err := term.SendKeys(tuitest.Enter); err != nil {
			t.Fatalf("send enter: %v", err)
		}
		var row, col, want int
		err = term.WaitFor(func(s tuitest.Screen) bool {
			var ok bool
			row, col, ok = copyCursorCell(s)
			if !ok {
				return false
			}
			want = colOfText(s, row, needle)
			return want >= 0 && col == want
		}, uiTimeout)
		if err != nil {
			t.Fatalf("%s: the copy cursor is at row %d column %d, want the match's column %d\n%s",
				needle, row, col, want, term.Snapshot())
		}
		if err := term.SendKeys("q"); err != nil {
			t.Fatalf("send q: %v", err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			_, _, ok := copyCursorCell(s)
			return !ok
		}, uiTimeout); err != nil {
			t.Fatalf("q did not leave copy mode\n%s", term.Snapshot())
		}
	}
}

// copyModeLeaveConfig binds alt+h and alt+l to the terminal-mode focus keys,
// as tmux users do with M-h and M-l (discussion #281). ctrl+u and ctrl+c also
// focus the left pane, and copy mode reads both itself.
const copyModeLeaveConfig = copyCursorConfig + `
[keybindings.terminal_mode]
terminal_focus_left = ["alt+h", "ctrl+u", "ctrl+c"]
terminal_focus_right = ["alt+l"]
`

// Ctrl+C leaves copy mode from normal mode, a selection, the search prompt and
// a pending f/t, in single and multi copy mode, and copies nothing, as in
// tmux's vi mode. A bound focus key moves focus out of a pane in copy mode, and
// that pane stays in copy mode, as tmux does. A focus key that copy mode reads
// itself keeps its copy-mode meaning, and Ctrl+B stays page up.
//
// How this could pass wrongly, written down first:
//   - Focus could look moved when copy mode ate the key. The new pane's shell
//     must run a command, which a pane in copy mode never does.
//   - The old pane could have left copy mode and the cursor found again on
//     a new entry. No key enters copy mode between the focus moves, and the
//     cursor must be on the row and column it was on.
//   - Ctrl+C in a selection could yank and then leave. Each clipboard write
//     is counted, and none may follow it.
//   - A clashing key could look kept because the focus had not moved yet. The
//     copy cursor must move first, which shows the key was read, and focus is
//     read after that.
//   - Ctrl+C at a prompt could look handled when the key that opened the
//     prompt did nothing. The search text and the save prompt are on screen
//     before the key goes in.
//
// Negative control: NEGATIVE_CONTROLS.md lists one cut per state and per key
// rule, each with the check that catches it.
func TestCopyModeCtrlCAndFocusKeys(t *testing.T) {
	const session = "cmleave"
	base := t.TempDir()
	writeConfig(t, base, copyModeLeaveConfig)
	out := &lockedBuffer{}
	term := startIn(t, base, startOpts{cols: 160, rows: 45, args: []string{"new", session}, env: copyColorOpts.env, out: out})
	waitBoot(t, term)
	for range 2 {
		newWindow(t, term)
	}
	waitWindowCount(t, term, 2, "copy mode leave setup")
	enableTiling(t, term)
	rects, right := focusedLayout(t, base, session)
	left := ""
	for _, r := range rects {
		if r.ID != right {
			left = r.ID
		}
	}
	if right == "" || left == "" {
		t.Fatalf("want two panes with one focused, got %+v", rects)
	}
	enterTerminalMode(t, term)
	runInShell(t, term, `printf 'right-%s\n' pane`, "right-pane", shellTimeout)
	time.Sleep(300 * time.Millisecond)

	// alt+h from copy mode focuses the left pane, which takes typing.
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	row, col := waitCopyCursor(t, term, "prefix+[")
	if err := term.SendKeys(tuitest.Alt('h')); err != nil {
		t.Fatalf("send alt+h: %v", err)
	}
	if got := waitFocusChange(t, base, session, right); got != left {
		t.Fatalf("alt+h in copy mode left focus on %s, want the left pane %s\n%s", got, left, term.Snapshot())
	}
	runInShell(t, term, `echo `+splitMarker("LEFTTYPED"), "LEFTTYPED", shellTimeout)
	saveFrame(t, term, "copy-mode-focus-left")

	// alt+l goes back. The right pane is still in copy mode, where it was.
	if err := term.SendKeys(tuitest.Alt('l')); err != nil {
		t.Fatalf("send alt+l: %v", err)
	}
	if got := waitFocusChange(t, base, session, left); got != right {
		t.Fatalf("alt+l left focus on %s, want the right pane %s\n%s", got, right, term.Snapshot())
	}
	if r, c := waitCopyCursor(t, term, "back on the right pane"); r != row || c != col {
		t.Fatalf("the copy cursor is at %d,%d, want %d,%d where the pane left it\n%s", r, c, row, col, term.Snapshot())
	}

	// ctrl+u is bound to a focus key, and copy mode reads it, so it moves the
	// copy cursor and the focus stays.
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatalf("send ctrl+u: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r < row
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+u did not move the copy cursor up from row %d\n%s", row, term.Snapshot())
	}
	if _, got := focusedLayout(t, base, session); got != right {
		t.Fatalf("ctrl+u in copy mode moved focus to %s, want it to stay on %s\n%s", got, right, term.Snapshot())
	}

	// ctrl+b is page up, and the next key is a copy-mode key. No prefix opens.
	if err := term.SendKeys(tuitest.Ctrl('d')); err != nil {
		t.Fatalf("send ctrl+d: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r > row
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+d did not move the copy cursor below row %d\n%s", row, term.Snapshot())
	}
	low, _ := waitCopyCursor(t, term, "after ctrl+d")
	if err := term.SendKeys(tuitest.Ctrl('b')); err != nil {
		t.Fatalf("send ctrl+b: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _, ok := copyCursorCell(s)
		return ok && r < low
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+b did not page the copy cursor up from row %d\n%s", low, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Left); err != nil {
		t.Fatalf("send left: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, got := focusedLayout(t, base, session); got != right {
		t.Fatalf("ctrl+b and left moved focus to %s, want it to stay on %s\n%s", got, right, term.Snapshot())
	}

	// ctrl+c from normal mode leaves copy mode.
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	waitNoCopyCursor(t, term, "ctrl+c in normal mode")

	// ctrl+c from a selection leaves copy mode and copies nothing.
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[ again")
	from := len(clipboardWrites(out))
	for _, k := range []string{"k", "v", "l", "l"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatalf("send %s: %v", k, err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("hjkl extend", uiTimeout); err != nil {
		t.Fatalf("v did not start a selection: %v\n%s", err, term.Snapshot())
	}
	waitCopyCursor(t, term, "in the selection")
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	waitNoCopyCursor(t, term, "ctrl+c in a selection")
	time.Sleep(clipboardSettle)
	if writes := clipboardSince(out, from); len(writes) != 0 {
		t.Fatalf("ctrl+c in a selection copied %q", writes)
	}
	runInShell(t, term, `echo `+splitMarker("RIGHTTYPED"), "RIGHTTYPED", shellTimeout)

	enterCopy := func(what string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
			t.Fatalf("send prefix+[: %v", err)
		}
		waitCopyCursor(t, term, what)
	}

	// ctrl+c from the search prompt leaves copy mode.
	enterCopy("prefix+[ for the search prompt")
	if err := term.SendKeys("/zqzq"); err != nil {
		t.Fatalf("type a search: %v", err)
	}
	if err := term.WaitForText("/zqzq", uiTimeout); err != nil {
		t.Fatalf("the search prompt did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	waitNoCopyCursor(t, term, "ctrl+c at the search prompt")

	// ctrl+c from a pending f leaves copy mode.
	enterCopy("prefix+[ for f")
	if err := term.SendKeys("f"); err != nil {
		t.Fatalf("send f: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
		t.Fatalf("send ctrl+c: %v", err)
	}
	waitNoCopyCursor(t, term, "ctrl+c after f")
	runInShell(t, term, `echo `+splitMarker("AFTERPENDING"), "AFTERPENDING", shellTimeout)

	// Multi copy mode, both panes. Each ctrl+c leaves it in both.
	leaveTerminalMode(t, term)
	togglePaletteMultifocus(t, term)
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	togglePaletteMultifocus(t, term)
	if err := term.WaitForText("Multifocus: 2 windows", uiTimeout); err != nil {
		t.Fatalf("the multifocus set never held two panes: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)
	enterMulti := func(what string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
			t.Fatalf("send prefix+[: %v", err)
		}
		if err := term.WaitForText("MULTI 2", uiTimeout); err != nil {
			t.Fatalf("%s: prefix+[ did not enter multi copy mode: %v\n%s", what, err, term.Snapshot())
		}
	}
	leaveMulti := func(what string) {
		t.Helper()
		if err := term.SendKeys(tuitest.Ctrl('c')); err != nil {
			t.Fatalf("send ctrl+c: %v", err)
		}
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "MULTI") }, uiTimeout); err != nil {
			t.Fatalf("ctrl+c %s did not leave multi copy mode\n%s", what, term.Snapshot())
		}
		waitNoCopyCursor(t, term, "ctrl+c "+what)
	}

	enterMulti("normal mode")
	leaveMulti("in multi normal mode")

	enterMulti("a selection")
	from = len(clipboardWrites(out))
	if err := term.SendKeys("V"); err != nil {
		t.Fatalf("send V: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	leaveMulti("in a multi selection")
	time.Sleep(clipboardSettle)
	if writes := clipboardSince(out, from); len(writes) != 0 {
		t.Fatalf("ctrl+c in a multi selection copied %q", writes)
	}

	enterMulti("the search prompt")
	if err := term.SendKeys("/zqzq"); err != nil {
		t.Fatalf("type a search: %v", err)
	}
	if err := term.WaitForText("/zqzq", uiTimeout); err != nil {
		t.Fatalf("the multi search prompt did not open: %v\n%s", err, term.Snapshot())
	}
	leaveMulti("at the multi search prompt")

	// A search that no pane matched parks every pane, and they still leave.
	enterMulti("a search with no match")
	if err := term.SendKeys("/zqzq", tuitest.Enter); err != nil {
		t.Fatalf("search: %v", err)
	}
	if err := term.WaitForText("found in 0 of 2 panes", uiTimeout); err != nil {
		t.Fatalf("the search did not report 0 of 2 panes: %v\n%s", err, term.Snapshot())
	}
	leaveMulti("with every pane parked")

	// The save prompt takes a bound focus key as text, so focus stays, and
	// ctrl+c leaves from there too.
	enterMulti("the save prompt")
	if err := term.SendKeys("V"); err != nil {
		t.Fatalf("send V: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys("Y"); err != nil {
		t.Fatalf("send Y: %v", err)
	}
	if err := term.WaitForText("Save to:", uiTimeout); err != nil {
		t.Fatalf("Y did not open the save prompt: %v\n%s", err, term.Snapshot())
	}
	_, before := focusedLayout(t, base, session)
	if err := term.SendKeys(tuitest.Alt('h'), tuitest.Alt('l')); err != nil {
		t.Fatalf("send the focus keys: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, got := focusedLayout(t, base, session); got != before {
		t.Fatalf("a focus key at the save prompt moved focus to %s, want it to stay on %s\n%s", got, before, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "Save to:") {
		t.Fatalf("a focus key closed the save prompt\n%s", term.Snapshot())
	}
	leaveMulti("at the save prompt")
	alive(t, term, "after ctrl+c")
}

// waitNoCopyCursor waits until no copy cursor is drawn.
func waitNoCopyCursor(t *testing.T, term *tuitest.Terminal, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := copyCursorCell(s)
		return !ok
	}, uiTimeout); err != nil {
		t.Fatalf("%s did not leave copy mode\n%s", what, term.Snapshot())
	}
}
