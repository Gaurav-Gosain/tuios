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
