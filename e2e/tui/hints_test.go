package tuie2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	hintsURL  = "https://example.com/hint-e2e"
	hintsPath = "/tmp/hint-e2e/main.go:12"
	hintsSHA  = "deadbee42"
	hintsIP   = "10.20.30.40"
)

const hintsAlphabet = "asdfghjkl"

func startHints(t *testing.T, daemon bool, config string, out *lockedBuffer) *tuitest.Terminal {
	t.Helper()
	term, _ := startHintsIn(t, daemon, config, out)
	return term
}

func startHintsIn(t *testing.T, daemon bool, config string, out *lockedBuffer) (*tuitest.Terminal, string) {
	t.Helper()
	base := t.TempDir()
	if config != "" {
		writeConfig(t, base, config)
	}
	opts := startOpts{}
	if out != nil {
		opts.out = out
	}
	if !daemon {
		term := startIn(t, base, opts)
		waitBoot(t, term)
		newWindow(t, term)
		enterTerminalMode(t, term)
		return term, base
	}
	killDaemon(t, base)
	if o, err := tuiosCLI(t, base, "new", "e2e-hints", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, o)
	}
	opts.args = []string{"attach", "e2e-hints"}
	term := startIn(t, base, opts)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	enterTerminalMode(t, term)
	return term, base
}

func openHints(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "F"); err != nil {
		t.Fatalf("send leader F: %v", err)
	}
}

func isHintLabelCell(c tuitest.Cell) bool {
	return c.Bold && c.Bg.Kind != tuitest.ColorDefault &&
		len(c.Content) == 1 && strings.Contains(hintsAlphabet, c.Content)
}

func hintLabelAt(s tuitest.Screen, col, row int) string {
	cols, _ := s.Size()
	first := s.Cell(col, row)
	if !isHintLabelCell(first) {
		return ""
	}
	var b strings.Builder
	for c := col; c < cols && c < col+3; c++ {
		cell := s.Cell(c, row)
		if !isHintLabelCell(cell) || cell.Bg != first.Bg {
			break
		}
		b.WriteString(cell.Content)
	}
	return b.String()
}

func waitHintLabel(t *testing.T, term *tuitest.Terminal, col, row int, want string) string {
	t.Helper()
	var label string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		label = hintLabelAt(s, col, row)
		return label != ""
	}, uiTimeout); err != nil {
		t.Fatalf("no hint label on %q at (%d,%d): %v\n%s", want, col, row, err, term.Snapshot())
	}
	return label
}

func findLastOnGrid(s tuitest.Screen, want string) (col, row int, ok bool) {
	cols, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		for c := range cols {
			if gridMatchesAt(s, c, r, want) {
				return c, r, true
			}
		}
	}
	return 0, 0, false
}

func hintsNoLabels(s tuitest.Screen) bool {
	cols, rows := s.Size()
	for r := range rows {
		for c := range cols {
			if isHintLabelCell(s.Cell(c, r)) {
				return false
			}
		}
	}
	return true
}

func printHintsLine(t *testing.T, term *tuitest.Terminal) map[string][2]int {
	t.Helper()
	line := strings.Join([]string{hintsURL, hintsPath, hintsSHA, hintsIP}, " ")
	runInShell(t, term, "printf '%s\\n' '"+line+"' ; echo HINTS\"\"-READY", "HINTS-READY", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	at := map[string][2]int{}
	for _, want := range []string{hintsURL, hintsPath, hintsSHA, hintsIP} {
		c, r, ok := findLastOnGrid(s, want)
		if !ok {
			t.Fatalf("%q is not on screen:\n%s", want, term.Snapshot())
		}
		at[want] = [2]int{c, r}
	}
	return at
}

// TestHintCopiesTheMatch opens hints, checks a label is drawn on each of the
// four matches, types the label of each in turn and checks the clipboard gets
// exactly that text, once, and that the pane is drawn as it was afterwards.
// Standalone and against a daemon session, which is how tuios ships.
func TestHintCopiesTheMatch(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "daemon"}[daemon], func(t *testing.T) {
			out := &lockedBuffer{}
			term := startHints(t, daemon, "", out)
			at := printHintsLine(t, term)
			before := term.Screen()

			for i, want := range []string{hintsURL, hintsPath, hintsSHA, hintsIP} {
				openHints(t, term)
				pos := at[want]
				label := waitHintLabel(t, term, pos[0], pos[1], want)
				frame := term.Screen()

				seen := map[string]string{}
				for _, other := range []string{hintsURL, hintsPath, hintsSHA, hintsIP} {
					p := at[other]
					l := hintLabelAt(frame, p[0], p[1])
					if l == "" {
						t.Fatalf("%q has no label while hints are open:\n%s", other, term.Snapshot())
					}
					if prev, dup := seen[l]; dup {
						t.Fatalf("%q and %q share the label %q", prev, other, l)
					}
					seen[l] = other
				}
				if c, r, ok := findLastOnGrid(frame, "HINTS-READY"); !ok {
					t.Errorf("the marker line is gone while hints are open:\n%s", term.Snapshot())
				} else if cell := frame.Cell(c, r); cell.Fg.Kind == tuitest.ColorDefault && !cell.Faint {
					t.Errorf("the text around the matches is not dimmed: %+v", cell)
				}
				if tail := len(want) - 1; !gridMatchesAt(frame, pos[0]+len(label), pos[1], want[len(label):]) {
					t.Errorf("the match text after the label changed; want %q from column %d:\n%s",
						want[len(label):], pos[0]+len(label), term.Snapshot())
				} else if !frame.Cell(pos[0]+tail, pos[1]).Bold {
					t.Errorf("the end of %q is not lit", want)
				}
				if i == 0 {
					saveArtifact(t, term, artifactDir(t), "hints-open")
					t.Logf("hints open, labels %v:\n%s", seen, term.Snapshot())
				}

				from := len(clipboardWrites(out))
				if err := term.SendKeys(label); err != nil {
					t.Fatalf("type label %q: %v", label, err)
				}
				waitClipboardSequence(t, term, out, from, want)

				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return hintsNoLabels(s) && gridMatchesAt(s, pos[0], pos[1], want)
				}, uiTimeout); err != nil {
					t.Fatalf("the pane did not go back to normal after the copy: %v\n%s", err, term.Snapshot())
				}
			}
			after := term.Screen()
			for _, want := range []string{hintsURL, hintsPath, hintsSHA, hintsIP} {
				p := at[want]
				if a, b := after.Cell(p[0], p[1]), before.Cell(p[0], p[1]); a.Bg != b.Bg || a.Bold != b.Bold {
					t.Errorf("%q still draws differently after hints closed: %+v, was %+v", want, a, b)
				}
			}
			saveArtifact(t, term, artifactDir(t), "hints-closed")
			alive(t, term, "after copying with hints")
		})
	}
}

// TestHintShiftTypesTheMatch types a label with Shift, which copies the match
// and types it into the pane: here, after a half-typed echo, which the shell
// then runs.
func TestHintShiftTypesTheMatch(t *testing.T) {
	out := &lockedBuffer{}
	term := startHints(t, false, "", out)
	at := printHintsLine(t, term)

	if err := term.SendKeys("echo TYPED-"); err != nil {
		t.Fatalf("type the command: %v", err)
	}
	if err := term.WaitForText("$ echo TYPED-", uiTimeout); err != nil {
		t.Fatalf("the command never reached the prompt: %v\n%s", err, term.Snapshot())
	}

	openHints(t, term)
	pos := at[hintsSHA]
	label := waitHintLabel(t, term, pos[0], pos[1], hintsSHA)
	from := len(clipboardWrites(out))
	if err := term.SendKeys(strings.ToUpper(label)); err != nil {
		t.Fatalf("type %q: %v", strings.ToUpper(label), err)
	}
	waitClipboardSequence(t, term, out, from, hintsSHA)

	if err := term.WaitForText("$ echo TYPED-"+hintsSHA, uiTimeout); err != nil {
		t.Fatalf("the match was not typed at the prompt: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-typed")

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the command: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, ok := findLastOnGrid(s, "TYPED-"+hintsSHA)
		return ok && strings.Count(s.Text(), "TYPED-"+hintsSHA) >= 2
	}, shellTimeout); err != nil {
		t.Fatalf("the shell did not run the typed match: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after typing a match with hints")
}

// TestHintEscClosesWithoutCopying opens hints, presses esc, and checks nothing
// was copied, the labels are gone, and the next key reaches the shell again.
func TestHintEscClosesWithoutCopying(t *testing.T) {
	out := &lockedBuffer{}
	term := startHints(t, false, "", out)
	at := printHintsLine(t, term)
	from := len(clipboardWrites(out))

	openHints(t, term)
	pos := at[hintsURL]
	waitHintLabel(t, term, pos[0], pos[1], hintsURL)

	if err := term.SendKeys("z"); err != nil {
		t.Fatalf("send z: %v", err)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hintsNoLabels(s) && gridMatchesAt(s, pos[0], pos[1], hintsURL)
	}, uiTimeout); err != nil {
		t.Fatalf("esc did not close hints: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(clipboardSettle)
	if got := clipboardSince(out, from); len(got) != 0 {
		t.Fatalf("esc copied something: %q", got)
	}
	if strings.Contains(term.Screen().Text(), "$ z") {
		t.Fatalf("a key pressed while hints were open reached the shell:\n%s", term.Snapshot())
	}
	runInShell(t, term, "echo AFTER-\"\"ESC-OK", "AFTER-ESC-OK", shellTimeout)
	alive(t, term, "after closing hints with esc")
}

// TestHintWrappedURLCopiesWhole prints a URL longer than the pane is wide, so
// the pane wraps it, and checks the label copies the whole address.
func TestHintWrappedURLCopiesWhole(t *testing.T) {
	out := &lockedBuffer{}
	term := startHints(t, false, "", out)

	var parts []string
	for i := 1; i <= 18; i++ {
		parts = append(parts, fmt.Sprintf("segment%d", i))
	}
	url := "https://example.com/" + strings.Join(parts, "-")
	cmd := "printf 'https://example.com/%s\\n' $(printf 'segment%d-' $(seq 1 17))segment18; echo WRAP\"\"-READY"
	runInShell(t, term, cmd, "WRAP-READY", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	head := "https://example.com/segment1-segment2"
	col, row, ok := findOnGrid(term.Screen(), head)
	if !ok {
		t.Fatalf("the URL is not on screen:\n%s", term.Snapshot())
	}
	cols, _ := term.Screen().Size()
	if col+len(url) <= cols {
		t.Fatalf("the URL (%d chars from column %d) fits on one %d-column row, so it did not wrap", len(url), col, cols)
	}

	openHints(t, term)
	label := waitHintLabel(t, term, col, row, url)
	if s := term.Screen(); !s.Cell(1, row+1).Bold {
		t.Errorf("the wrapped half of the URL is not lit:\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-wrapped")

	from := len(clipboardWrites(out))
	if err := term.SendKeys(label); err != nil {
		t.Fatalf("type %q: %v", label, err)
	}
	waitClipboardSequence(t, term, out, from, url)
	alive(t, term, "after copying a wrapped URL")
}

// TestHintCustomPattern adds a pattern in [hints] and checks its match gets a
// label and copies whole, ahead of the number built-in that covers part of it.
func TestHintCustomPattern(t *testing.T) {
	out := &lockedBuffer{}
	term := startHints(t, false, "[hints]\npatterns = ['TICKET-\\d+']\n", out)

	runInShell(t, term, "echo see TICKET-4242 now; echo CUSTOM\"\"-READY", "CUSTOM-READY", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	col, row, ok := findLastOnGrid(term.Screen(), "TICKET-4242")
	if !ok {
		t.Fatalf("the ticket is not on screen:\n%s", term.Snapshot())
	}

	openHints(t, term)
	label := waitHintLabel(t, term, col, row, "TICKET-4242")
	if l := hintLabelAt(term.Screen(), col+len("TICKET-"), row); l != "" {
		t.Errorf("the number inside the ticket got its own label %q", l)
	}
	from := len(clipboardWrites(out))
	if err := term.SendKeys(label); err != nil {
		t.Fatalf("type %q: %v", label, err)
	}
	waitClipboardSequence(t, term, out, from, "TICKET-4242")
	alive(t, term, "after copying a custom match")
}

// TestHintCtrlOpensAPath opens a path with Ctrl and its label. A path opens in
// quick look, the way a file link does, and e there opens it in a new pane
// running $EDITOR; here the editor is tail, so the file's text in the new
// pane is the proof it opened.
func TestHintCtrlOpensAPath(t *testing.T) {
	out := &lockedBuffer{}
	base := t.TempDir()
	dir := t.TempDir()
	file := dir + "/opened.txt"
	if err := os.WriteFile(file, []byte("OPENED-BY-HINTS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	term := startIn(t, base, startOpts{out: out, env: []string{"EDITOR=tail -f"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo "+file+"; echo OPEN\"\"-READY", "OPEN-READY", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	col, row, ok := findLastOnGrid(term.Screen(), file)
	if !ok {
		t.Fatalf("the path is not on screen:\n%s", term.Snapshot())
	}

	openHints(t, term)
	label := waitHintLabel(t, term, col, row, file)
	keys := make([]any, 0, len(label))
	for _, r := range label {
		keys = append(keys, tuitest.Ctrl(r))
	}
	if err := term.SendKeys(keys...); err != nil {
		t.Fatalf("type ctrl+%s: %v", label, err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "OPENED-BY-HINTS") && strings.Contains(s.Text(), "Inside the file")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the path did not open in quick look: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-quick-look")
	if err := term.SendKeys("e"); err != nil {
		t.Fatalf("e: %v", err)
	}
	waitWindowCount(t, term, 2, "opening the path in a new pane from quick look")
	if err := term.WaitForText("OPENED-BY-HINTS", shellTimeout); err != nil {
		t.Fatalf("the file did not open in the editor pane: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-opened")
	alive(t, term, "after opening a path with hints")
}

// TestHintWideCharacters puts matches after wide glyphs and a label on top of
// one. The label must sit on the match's first cell, and a label written over
// half of a wide glyph must not push the rest of the row over.
func TestHintWideCharacters(t *testing.T) {
	out := &lockedBuffer{}
	term := startHints(t, false, "[hints]\npatterns = ['漢字-\\d+']\n", out)

	const wideURL = "https://example.com/wide-e2e"
	runInShell(t, term, "echo 日本語 "+wideURL+" 漢字-42 tail-marker; echo WIDE\"\"-READY", "WIDE-READY", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	before := term.Screen()
	ucol, row, ok := findLastOnGrid(before, wideURL)
	if !ok {
		t.Fatalf("the URL is not on screen:\n%s", term.Snapshot())
	}
	tcol, trow, ok := findLastOnGrid(before, "tail-marker")
	if !ok || trow != row {
		t.Fatalf("the marker is not on the URL's row:\n%s", term.Snapshot())
	}
	kcol := ucol + len(wideURL) + 1
	if c := before.Cell(kcol, row); c.Content != "漢" || c.Width != 2 {
		t.Fatalf("expected the wide glyph at column %d, got %+v", kcol, c)
	}

	openHints(t, term)
	urlLabel := waitHintLabel(t, term, ucol, row, wideURL)
	wideLabel := waitHintLabel(t, term, kcol, row, "漢字-42")
	frame := term.Screen()
	if !gridMatchesAt(frame, tcol, row, "tail-marker") {
		t.Fatalf("the label on the wide glyph moved the rest of the row:\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-wide")

	from := len(clipboardWrites(out))
	if err := term.SendKeys(wideLabel); err != nil {
		t.Fatalf("type %q: %v", wideLabel, err)
	}
	waitClipboardSequence(t, term, out, from, "漢字-42")

	openHints(t, term)
	waitHintLabel(t, term, ucol, row, wideURL)
	from = len(clipboardWrites(out))
	if err := term.SendKeys(urlLabel); err != nil {
		t.Fatalf("type %q: %v", urlLabel, err)
	}
	waitClipboardSequence(t, term, out, from, wideURL)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		c := s.Cell(kcol, row)
		return hintsNoLabels(s) && c.Content == "漢" && c.Width == 2
	}, uiTimeout); err != nil {
		t.Fatalf("the wide glyph did not come back after hints closed: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after hints over wide glyphs")
}

func waitHintsUp(t *testing.T, term *tuitest.Terminal, at map[string][2]int) {
	t.Helper()
	p := at[hintsURL]
	waitHintLabel(t, term, p[0], p[1], hintsURL)
}

func waitHintsGone(t *testing.T, term *tuitest.Terminal, why string) {
	t.Helper()
	if err := term.WaitFor(hintsNoLabels, uiTimeout); err != nil {
		t.Fatalf("hints stayed on screen %s: %v\n%s", why, err, term.Snapshot())
	}
}

// TestHintsCloseWhenTheirPaneExits opens hints over a pane whose shell exits a
// moment later. Hints must go with the pane, and the next keys must reach the
// window manager: before this, hints stayed open on a pane that was gone, so
// every key after it was taken as a label letter and dropped.
func TestHintsCloseWhenTheirPaneExits(t *testing.T) {
	term := startHints(t, false, "", nil)
	at := printHintsLine(t, term)
	if err := term.SendKeys("sleep 2; exit", tuitest.Enter); err != nil {
		t.Fatalf("type the exit: %v", err)
	}
	openHints(t, term)
	waitHintsUp(t, term, at)

	waitWindowCount(t, term, 0, "the pane exiting under hints")
	waitHintsGone(t, term, "after their pane exited")

	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo AFTER-\"\"EXIT-OK", "AFTER-EXIT-OK", shellTimeout)
	alive(t, term, "after hints outlived nothing")
}

// TestHintsCloseOnWorkspaceSwitch switches the workspace from the CLI while
// hints are open. Coming back shows the pane as it is, and keys reach its
// shell.
func TestHintsCloseOnWorkspaceSwitch(t *testing.T) {
	term, base := startHintsIn(t, true, "", nil)
	at := printHintsLine(t, term)
	openHints(t, term)
	waitHintsUp(t, term, at)

	if out, err := tuiosCLI(t, base, "select-workspace", "2"); err != nil {
		t.Fatalf("select workspace 2: %v\n%s", err, out)
	}
	waitHintsGone(t, term, "after the workspace changed")
	if out, err := tuiosCLI(t, base, "select-workspace", "1"); err != nil {
		t.Fatalf("select workspace 1: %v\n%s", err, out)
	}
	p := at[hintsURL]
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hintsNoLabels(s) && gridMatchesAt(s, p[0], p[1], hintsURL)
	}, uiTimeout); err != nil {
		t.Fatalf("the pane did not come back without labels: %v\n%s", err, term.Snapshot())
	}
	runInShell(t, term, "echo AFTER-S\"\"WITCH-OK", "AFTER-SWITCH-OK", shellTimeout)
	alive(t, term, "after a workspace switch under hints")
}

// TestHintsDropAPasteAndCloseOnWheel pastes while hints are open, which must
// not reach the shell, and turns the wheel, which closes hints.
func TestHintsDropAPasteAndCloseOnWheel(t *testing.T) {
	term := startHints(t, false, "", nil)
	at := printHintsLine(t, term)

	openHints(t, term)
	waitHintsUp(t, term, at)
	const pasted = "PASTED-UNDER-HINTS"
	if err := term.Type("\x1b[200~" + pasted + "\x1b[201~"); err != nil {
		t.Fatalf("send a bracketed paste: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if hintsNoLabels(term.Screen()) {
		t.Fatalf("a paste closed hints:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	waitHintsGone(t, term, "after esc")
	runInShell(t, term, "echo AFTER-P\"\"ASTE-OK", "AFTER-PASTE-OK", shellTimeout)
	if strings.Contains(term.Screen().Text(), pasted) {
		t.Fatalf("the paste reached the shell under hints:\n%s", term.Snapshot())
	}

	at = printHintsLineAt(t, term)
	openHints(t, term)
	waitHintsUp(t, term, at)
	p := at[hintsURL]
	wheelAt(t, term, p[0]+2, p[1], tuitest.MouseWheelUp, 1)
	waitHintsGone(t, term, "after the wheel")
	alive(t, term, "after a wheel under hints")
}

func printHintsLineAt(t *testing.T, term *tuitest.Terminal) map[string][2]int {
	t.Helper()
	s := term.Screen()
	at := map[string][2]int{}
	for _, want := range []string{hintsURL, hintsPath, hintsSHA, hintsIP} {
		c, r, ok := findLastOnGrid(s, want)
		if !ok {
			t.Fatalf("%q is not on screen:\n%s", want, term.Snapshot())
		}
		at[want] = [2]int{c, r}
	}
	return at
}

// TestHintFullLineDoesNotJoinTheNext prints a URL that fills its row exactly
// and ends with a newline, then a line that a URL could carry on into. A full
// last column looks the same as a wrap, so only the emulator's own record of
// the wrap can tell them apart, and the label must copy the first line alone.
//
// Negative control: joining rows on a full last column, as hints did first,
// copies the URL with "tail/more" glued to its end.
func TestHintFullLineDoesNotJoinTheNext(t *testing.T) {
	out := &lockedBuffer{}
	term := startHints(t, false, "", out)
	cols, _ := term.Screen().Size()
	width := cols - 2
	const scheme = "https://example.com/"
	url := scheme + strings.Repeat("0", width-len(scheme))
	cmd := fmt.Sprintf("printf '%s%%0%dd\\ntail/more\\n' 0; echo FULL\"\"-READY", scheme, width-len(scheme))
	runInShell(t, term, cmd, "FULL-READY", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	col, row, ok := findLastOnGrid(term.Screen(), url)
	if !ok || col+len(url) != cols-1 {
		t.Fatalf("the URL does not fill its row exactly (column %d, %d chars, %d columns):\n%s",
			col, len(url), cols, term.Snapshot())
	}
	if !gridMatchesAt(term.Screen(), 1, row+1, "tail/more") {
		t.Fatalf("the next line is not right under the URL:\n%s", term.Snapshot())
	}

	openHints(t, term)
	label := waitHintLabel(t, term, col, row, url)
	from := len(clipboardWrites(out))
	if err := term.SendKeys(label); err != nil {
		t.Fatalf("type %q: %v", label, err)
	}
	waitClipboardSequence(t, term, out, from, url)
	alive(t, term, "after a full line under hints")
}

// TestHintsAllPanesLabelEveryPane is issue #248 on the real render path: two
// tiled panes under shared borders, hints.all_panes on, and the leader and F
// pressed in the left pane. Both panes show labels. The UUID in the right pane
// is typed with Shift, and it lands at the prompt of the left pane, the pane
// the person is in, and on the clipboard.
//
// How this could pass wrongly: the UUID could be typed into the right pane and
// still be on screen. The check reads the column it lands in, which must be in
// the left half. The label on the right pane could be a stale cell. The frame
// is read only after a label shows on both panes, and the pane is checked for
// no labels after the label is typed.
func TestHintsAllPanesLabelEveryPane(t *testing.T) {
	out := &lockedBuffer{}
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\nshared_borders = true\n\n[hints]\nall_panes = true\n")
	term := startIn(t, base, startOpts{out: out, cols: 140, rows: 36})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)
	enableTiling(t, term)

	const uuid = "30c0acf5-8dd0-48f2-8d86-cf0aae99aa4a"
	enterTerminalMode(t, term)
	runInShell(t, term, "printf '30c0acf5-8dd0-%s\\n' 48f2-8d86-cf0aae99aa4a; echo RIGHT\"\"-READY", "RIGHT-READY", shellTimeout)
	leaveTerminalMode(t, term)
	if err := term.SendKeys("h"); err != nil {
		t.Fatalf("focus the left pane: %v", err)
	}
	enterTerminalMode(t, term)
	runInShell(t, term, "printf 'deadbee%s\\n' 42; echo LEFT-\"\"READY", "LEFT-READY", shellTimeout)
	if err := term.SendKeys("echo TYPED-"); err != nil {
		t.Fatalf("type the command: %v", err)
	}
	if err := term.WaitForText("$ echo TYPED-", uiTimeout); err != nil {
		t.Fatalf("the command never reached the prompt: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	s := term.Screen()
	cols, _ := s.Size()
	ucol, urow, ok := findLastOnGrid(s, uuid)
	if !ok || ucol < cols/2 {
		t.Fatalf("the UUID is not in the right pane (column %d of %d):\n%s", ucol, cols, term.Snapshot())
	}
	scol, srow, ok := findLastOnGrid(s, hintsSHA)
	if !ok || scol >= cols/2 {
		t.Fatalf("the hash is not in the left pane (column %d of %d):\n%s", scol, cols, term.Snapshot())
	}

	openHints(t, term)
	uLabel := waitHintLabel(t, term, ucol, urow, uuid)
	sLabel := waitHintLabel(t, term, scol, srow, hintsSHA)
	if uLabel == sLabel {
		t.Fatalf("the two panes share the label %q", uLabel)
	}
	if len(sLabel) > len(uLabel) {
		t.Errorf("the focused pane has a longer label (%q) than the other pane (%q)", sLabel, uLabel)
	}
	saveArtifact(t, term, artifactDir(t), "hints-all-panes")
	t.Logf("labels: focused pane %q, other pane %q:\n%s", sLabel, uLabel, term.Snapshot())

	from := len(clipboardWrites(out))
	if err := term.SendKeys(strings.ToUpper(uLabel)); err != nil {
		t.Fatalf("type %q: %v", strings.ToUpper(uLabel), err)
	}
	waitClipboardSequence(t, term, out, from, uuid)
	var tcol int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		c, _, ok := findLastOnGrid(s, "TYPED-"+uuid)
		tcol = c
		return ok && hintsNoLabels(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the UUID was not typed at a prompt: %v\n%s", err, term.Snapshot())
	}
	if tcol >= cols/2 {
		t.Fatalf("the UUID was typed into the right pane (column %d), not the focused left pane:\n%s", tcol, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-all-panes-typed")
	alive(t, term, "after hints on all panes")
}

// TestHintsAllPanesLabelOnlyWhatShows is the layout tuios starts in with
// tiling off: plain panes that overlap, stacked by their Z. The second pane
// covers part of the first, and part of the first pane's hashes with it. A
// hash under the second pane must get no label, because typing a label
// nobody can see would copy text nobody can see.
//
// The labels are handed out shortest first, and nine or fewer labels are
// the first letters of the alphabet. So when every label is on screen, the
// labels on screen are exactly the first n letters. A hidden label takes one
// of those letters off the screen. The test also types the first letter
// with no label on screen, which must copy nothing.
func TestHintsAllPanesLabelOnlyWhatShows(t *testing.T) {
	out := &lockedBuffer{}
	base := t.TempDir()
	writeConfig(t, base, "[startup]\ntiled = false\n\n[hints]\nall_panes = true\n")
	term := startIn(t, base, startOpts{out: out, cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	if settledTiling(t, term) {
		t.Fatalf("the session is tiled, so no pane covers another:\n%s", term.Snapshot())
	}
	enterTerminalMode(t, term)
	runInShell(t, term, `clear; for i in 1 2 3 4 5 6; do printf "%*sdeadbee1$i\n" $((i*8)) ''; done; echo COVER""-READY`,
		"COVER-READY", shellTimeout)
	leaveTerminalMode(t, term)
	newWindow(t, term)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v", err)
	}
	c2, r2, ok := 0, 0, false
	scr := term.Screen()
	scols, srows := scr.Size()
	for r := range srows {
		for c := range scols {
			if scr.Cell(c, r).Content == "╭" {
				c2, r2, ok = c, r, true
			}
		}
	}
	if !ok {
		t.Fatalf("no title bar for the second pane:\n%s", term.Snapshot())
	}
	mouseDrag(t, term, c2+20, r2, c2+50, r2+2, tuitest.MouseLeft, 0)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled after the drag: %v", err)
	}
	var shown, hidden []string
	for i := 1; i <= 6; i++ {
		hash := fmt.Sprintf("deadbee1%d", i)
		if _, _, ok := findLastOnGrid(term.Screen(), hash[4:]); ok {
			shown = append(shown, hash)
		} else {
			hidden = append(hidden, hash)
		}
	}
	if len(hidden) == 0 || len(shown) == 0 {
		t.Fatalf("the second pane must cover some hashes and not all (shown %v, hidden %v):\n%s",
			shown, hidden, term.Snapshot())
	}

	openHints(t, term)
	var labels map[string]bool
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		labels = map[string]bool{}
		cols, rows := s.Size()
		for r := range rows - 1 {
			for c := range cols {
				if l := hintLabelAt(s, c, r); l != "" && (c == 0 || !isHintLabelCell(s.Cell(c-1, r))) {
					labels[l] = true
				}
			}
		}
		return len(labels) >= len(shown)
	}, uiTimeout); err != nil {
		t.Fatalf("hints never showed a label per visible hash: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "hints-all-panes-covered")
	t.Logf("labels on screen %v, hashes hidden %v:\n%s", labels, hidden, term.Snapshot())
	n := len(labels)
	if n > 8 {
		t.Fatalf("the check needs fewer than nine labels, got %v", labels)
	}
	for _, r := range hintsAlphabet[:n] {
		if !labels[string(r)] {
			t.Errorf("the label %q is not on screen: a match nobody can see has it (labels %v)", string(r), labels)
		}
	}

	next := ""
	for _, r := range hintsAlphabet {
		if !labels[string(r)] {
			next = string(r)
			break
		}
	}
	from := len(clipboardWrites(out))
	if err := term.SendKeys(next); err != nil {
		t.Fatalf("type %q: %v", next, err)
	}
	time.Sleep(clipboardSettle)
	if got := clipboardSince(out, from); len(got) != 0 {
		t.Fatalf("typing %q, which is on no label, copied %q", next, got)
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	waitHintsGone(t, term, "after esc")
	alive(t, term, "after hints over overlapping panes")
}

// TestHintsFromCopyMode scrolls back in copy mode until a URL that has left
// the screen is in view, opens hints there with s, and copies
// the URL by its label. The labels go on the scrolled-back view, the one the
// person is reading, and the copy leaves copy mode as a yank does. Esc from
// hints keeps copy mode. Against a daemon session, which is how tuios ships.
func TestHintsFromCopyMode(t *testing.T) {
	term, out := startWhitespaceClient(t, true)
	runInShell(t, term, "printf '%s\\n' '"+hintsURL+"'; seq 1 80; echo SCROLLED\"\"-PAST", "SCROLLED-PAST", shellTimeout)
	if strings.Contains(term.Screen().Text(), hintsURL) {
		t.Fatalf("the URL is still on the screen, so the test would not scroll back:\n%s", term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	waitCopyCursor(t, term, "prefix+[")
	if err := term.SendKeys("g", "g"); err != nil {
		t.Fatalf("send gg: %v", err)
	}
	var col, row int
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		var ok bool
		col, row, ok = findLastOnGrid(s, hintsURL)
		return ok
	}, uiTimeout); err != nil {
		t.Fatalf("gg in copy mode never showed the URL: %v\n%s", err, term.Snapshot())
	}

	sendEach(t, term, "s")
	waitHintLabel(t, term, col, row, hintsURL)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return hintLabelAt(s, col, row) == "" && gridMatchesAt(s, col, row, hintsURL)
	}, uiTimeout); err != nil {
		t.Fatalf("the label stayed on the URL after esc: %v\n%s", err, term.Snapshot())
	}
	waitCopyCursor(t, term, "after esc from hints")
	if _, _, ok := findLastOnGrid(term.Screen(), hintsURL); !ok {
		t.Fatalf("esc from hints left the scrolled-back view:\n%s", term.Snapshot())
	}

	sendEach(t, term, "s")
	label := waitHintLabel(t, term, col, row, hintsURL)
	saveArtifact(t, term, artifactDir(t), "hints-in-copy-mode")
	from := len(clipboardWrites(out))
	if err := term.SendKeys(label); err != nil {
		t.Fatalf("type label %q: %v", label, err)
	}
	waitClipboardSequence(t, term, out, from, hintsURL)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		_, _, scrolled := findLastOnGrid(s, hintsURL)
		return hintsNoLabels(s) && !scrolled && strings.Contains(s.Text(), "SCROLLED-PAST")
	}, uiTimeout); err != nil {
		t.Fatalf("copying from hints did not leave copy mode: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after hints from copy mode")
}
