package tuie2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The scrollback browser's output view moves with copy mode's keys: the
// [keybindings.copy_mode] motions and copy mode's rule for gg. A key rebound
// for copy mode moves the browser too, and a pending g is for the very next
// key only, so g k g is not gg.
//
// How this could pass wrongly, written down first:
//   - The cursor line could be read before the key took effect. A step that
//     moves waits for the line indicator ("ln N/M") to show the line it wants.
//   - A key that must not move could be checked before it ran. The line is
//     read only once a search prompt, opened after the keys, is drawn: the
//     keys before it have run by then.
//   - ctrl+n could move for a reason of its own. The default j is unbound by
//     the same config: j j ctrl+n from line 2 must end on 3, not 5.
//   - g k g could end on line 2 because g never armed. gg right after must
//     still reach line 1.
//   - The output could fit the view, which hides the indicator. It is 200
//     lines.
func TestScrollbackBrowserTakesCopyModeKeys(t *testing.T) {
	art := artifactDir(t)
	term, _ := startOverlaySession(t, "border_style = \"rounded\"\n\n[keybindings.copy_mode]\ncopy_mode_down = [\"ctrl+n\"]\n", truecolor)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033]133;A\007$ \033]133;B\007rows\n\033]133;C\007'; seq -f 'row-%g' 1 200; printf '\033]133;D;0\007'`, "row-200", shellTimeout)
	sendKeys(t, term, tuitest.Ctrl('b'), "s")
	if err := term.WaitForText(browserTitle, uiTimeout); err != nil {
		t.Fatalf("the scrollback browser did not open: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "l")

	indicator := regexp.MustCompile(`ln (\d+)/(\d+)`)
	line := func(s tuitest.Screen) (int, int) {
		m := indicator.FindStringSubmatch(s.Text())
		if m == nil {
			return 0, 0
		}
		n, _ := strconv.Atoi(m[1])
		total, _ := strconv.Atoi(m[2])
		return n, total
	}
	waitLine := func(what string, want int) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { n, _ := line(s); return n == want }, uiTimeout); err != nil {
			n, total := line(term.Screen())
			t.Fatalf("%s: the cursor is on line %d of %d, want %d\n%s", what, n, total, want, term.Snapshot())
		}
	}

	waitLine("output mode", 1)
	sendKeys(t, term, tuitest.Ctrl('n'))
	waitLine("ctrl+n, bound to copy_mode_down", 2)

	// settled reads the cursor line once every key sent before it has run:
	// the search prompt it opens is drawn only after them, and esc closes it
	// again.
	//
	// The prompt is "/" on the browser's title row, two spaces after what is
	// there, followed by its caret, a reverse-video cell with no glyph.
	searchOpen := func(s tuitest.Screen) bool {
		_, rows := s.Size()
		for r := range rows {
			if ln := s.Line(r); strings.Contains(ln, browserTitle) {
				return regexp.MustCompile(`  /\s*[│|]?\s*$`).MatchString(ln)
			}
		}
		return false
	}
	settled := func(what string) int {
		t.Helper()
		sendKeys(t, term, "/")
		if err := term.WaitFor(searchOpen, uiTimeout); err != nil {
			t.Fatalf("%s: the search prompt did not open\n%s", what, term.Snapshot())
		}
		n, _ := line(term.Screen())
		sendKeys(t, term, tuitest.Esc)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return !searchOpen(s) }, uiTimeout); err != nil {
			t.Fatalf("%s: esc did not close the search prompt\n%s", what, term.Snapshot())
		}
		return n
	}

	// j is unbound now: j j ctrl+n from line 2 ends on line 3, not 5.
	sendKeys(t, term, "j", "j", tuitest.Ctrl('n'))
	if n := settled("j j ctrl+n"); n != 3 {
		t.Fatalf("j j ctrl+n from line 2 ends on line %d, want 3: j must not move once copy_mode_down is rebound\n%s", n, term.Snapshot())
	}

	// g k g in one burst, inside gg's window. The second g starts a new gg,
	// so the cursor stays where k left it, on line 2, not the top.
	sendKeys(t, term, "g", "k", "g")
	if n := settled("g k g"); n != 2 {
		t.Fatalf("g k g from line 3 ends on line %d, want 2: a pending g is for the very next key only\n%s", n, term.Snapshot())
	}
	saveArtifact(t, term, art, "browser-after-gkg")

	// gg itself still goes to the top.
	sendKeys(t, term, "g", "g")
	waitLine("gg", 1)
	alive(t, term, "after the scrollback browser keys")
}
