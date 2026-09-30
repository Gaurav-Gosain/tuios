package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Multi copy mode against the real binary: three panes printing different
// text, all in the multifocus set, one search across them, one V, one y, and
// the clipboard write read off the wire as OSC 52. Then the same selection in
// markdown and json, and saved to a file.
//
// How this could pass wrongly, written down first:
//   - The search could match the command line that printed the text rather
//     than the output. The commands spell the word with a printf gap, so only
//     the output holds it.
//   - The yank could be plain copy mode on the focused pane. Two panes'
//     lines are required, in window order, and the pane with no match must
//     add nothing.
//   - A stale clipboard write could satisfy the check. Each check counts the
//     writes before the key and requires exactly one new one.

// multiCopyLines is what each pane prints, in window order. The third pane has
// no line the search finds.
var multiCopyLines = []string{
	"LLDP neighbor swp1 rack-sw-01",
	"LLDP neighbor swp2 rack-sw-01",
	"no carrier on eth0",
}

// printfSpelled prints line without the command line holding it: the shell
// joins the two halves only in the output. A line with "LLDP" is split there,
// so the copy-mode search for "LLDP" cannot match the command line either.
func printfSpelled(line string) string {
	spelled := strings.Replace(line, "LLDP", "LL\"\"DP", 1)
	if spelled == line {
		spelled = splitMarker(line)
	}
	return "printf '%s\\n' \"" + spelled + "\""
}

// togglePaletteMultifocus adds the focused pane to the multifocus set through
// the palette.
func togglePaletteMultifocus(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "for Toggle multifocus")
	if err := term.SendKeys("Toggle multifocus"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("activate palette entry: %v", err)
	}
	waitPaletteClosed(t, term, "after Toggle multifocus")
	if err := term.WaitForText("Multifocus", uiTimeout); err != nil {
		t.Fatalf("no multifocus message: %v\n%s", err, term.Snapshot())
	}
}

// setUpMultiCopyPanes opens one pane per line and prints its line in it, then
// adds every pane to the multifocus set. The set comes last because typing in
// terminal mode goes to every pane of the set. It returns in window-management
// mode with a pane of the set focused.
func setUpMultiCopyPanes(t *testing.T, term *tuitest.Terminal, haveOne bool) {
	t.Helper()
	for i, line := range multiCopyLines {
		if i > 0 || !haveOne {
			newWindow(t, term)
		}
		enterTerminalMode(t, term)
		runInShell(t, term, printfSpelled(line), line, shellTimeout)
		leaveTerminalMode(t, term)
	}
	for i := range multiCopyLines {
		if i > 0 {
			// tab is the next window in window-management mode.
			if err := term.SendKeys(tuitest.Tab); err != nil {
				t.Fatalf("send tab: %v", err)
			}
			time.Sleep(300 * time.Millisecond)
		}
		togglePaletteMultifocus(t, term)
	}
	if err := term.WaitForText("Multifocus: 3 windows", uiTimeout); err != nil {
		t.Fatalf("the multifocus set never held three panes: %v\n%s", err, term.Snapshot())
	}
}

// yankMulti selects the line under each pane's cursor with V and yanks it, and
// returns the one clipboard write the yank made.
func yankMulti(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, what string) string {
	t.Helper()
	from := len(clipboardWrites(out))
	if err := term.SendKeys("V"); err != nil {
		t.Fatalf("send V: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("send y: %v", err)
	}
	deadline := time.Now().Add(uiTimeout)
	for len(clipboardWrites(out)) <= from {
		if time.Now().After(deadline) {
			t.Fatalf("%s: the yank wrote nothing to the clipboard\n%s", what, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(clipboardSettle)
	writes := clipboardWrites(out)[from:]
	if len(writes) != 1 {
		t.Fatalf("%s: the yank wrote the clipboard %d times, want once: %q", what, len(writes), writes)
	}
	return writes[0]
}

// outputCell is where a pane's output line starts on screen: the first cell
// after a pane border whose text begins with line. The command line that
// printed it holds a printf gap, so it never matches.
func outputCell(t *testing.T, s tuitest.Screen, line string) (row, col int) {
	t.Helper()
	_, rows := s.Size()
	for r := range rows {
		text := s.Line(r)
		if i := strings.Index(text, "│"+line); i >= 0 {
			return r, len([]rune(text[:i])) + 1
		}
	}
	t.Fatalf("no pane shows %q", line)
	return -1, -1
}

// saveMultiCopyFrame writes the screen as text to TUIOS_E2E_FRAME_DIR when it is set,
// with every cell drawn on a background shown as a block under it, so the
// selection is visible in a plain text frame.
func saveMultiCopyFrame(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAME_DIR")
	if dir == "" {
		return
	}
	s := term.Screen()
	cols, rows := s.Size()
	var b strings.Builder
	for r := range rows {
		b.WriteString(s.Line(r))
		b.WriteByte('\n')
		var marks strings.Builder
		any := false
		for c := range cols {
			if s.Cell(c, r).Bg.Kind != tuitest.ColorDefault && r < rows-2 {
				marks.WriteString("^")
				any = true
			} else {
				marks.WriteString(" ")
			}
		}
		if any {
			b.WriteString(strings.TrimRight(marks.String(), " "))
			b.WriteByte('\n')
		}
	}
	base := t.Name()[strings.LastIndex(t.Name(), "/")+1:]
	_ = os.WriteFile(filepath.Join(dir, base+"-"+name+".txt"), []byte(b.String()), 0o644)
}

func runMultiCopyE2E(t *testing.T, term *tuitest.Terminal, out *lockedBuffer, home string) {
	t.Helper()

	// The copy-mode key with multifocus on is multi copy mode.
	if err := term.SendKeys(tuitest.Ctrl('b'), "["); err != nil {
		t.Fatalf("send prefix+[: %v", err)
	}
	if err := term.WaitForText("MULTI 3", uiTimeout); err != nil {
		t.Fatalf("prefix+[ with three panes in multifocus did not enter multi copy mode: %v\n%s", err, term.Snapshot())
	}

	// Before the search every pane of the set wears the multifocus border.
	s0 := term.Screen()
	r1, c1 := outputCell(t, s0, multiCopyLines[0])
	r3, c3 := outputCell(t, s0, multiCopyLines[2])
	if a, c := s0.Cell(c1-1, r1).Fg, s0.Cell(c3-1, r3).Fg; a != c {
		t.Fatalf("before the search the panes' borders differ: %+v and %+v\n%s", a, c, term.Snapshot())
	}

	if err := term.SendKeys("/lldp", tuitest.Enter); err != nil {
		t.Fatalf("type search: %v", err)
	}
	if err := term.WaitForText("found in 2 of 3 panes", uiTimeout); err != nil {
		t.Fatalf("the search did not report 2 of 3 panes: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("MULTI 2/3", uiTimeout); err != nil {
		t.Fatalf("the dock does not show 2/3 panes matched: %v\n%s", err, term.Snapshot())
	}

	// The pane without a match drops the multifocus border.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(c1-1, r1).Fg != s.Cell(c3-1, r3).Fg
	}, uiTimeout); err != nil {
		t.Fatalf("the pane without a match looks the same as a matched one: %v\n%s", err, term.Snapshot())
	}

	// V highlights the matched line in each matched pane and nothing in the
	// pane without a match.
	if err := term.SendKeys("V"); err != nil {
		t.Fatalf("send V: %v", err)
	}
	// Five cells in, clear of the copy cursor, which sits on the match's
	// first cell and has a background of its own.
	r2, c2 := outputCell(t, term.Screen(), multiCopyLines[1])
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return s.Cell(c1+5, r1).Bg.Kind != tuitest.ColorDefault && s.Cell(c2+5, r2).Bg.Kind != tuitest.ColorDefault
	}, uiTimeout); err != nil {
		t.Fatalf("V did not highlight the match in both matched panes: %v\n%s", err, term.Snapshot())
	}
	if bg := term.Screen().Cell(c3+5, r3).Bg; bg.Kind != tuitest.ColorDefault {
		t.Fatalf("V highlighted the pane without a match\n%s", term.Snapshot())
	}
	saveMultiCopyFrame(t, term, "selected")
	if os.Getenv("TUIOS_E2E_FRAME_DIR") != "" {
		// Once the message has gone, the dock shows the keys.
		if err := term.WaitForText("yank all", 3*uiTimeout); err == nil {
			saveMultiCopyFrame(t, term, "help")
		}
	}
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return s.Cell(c1+5, r1).Bg.Kind == tuitest.ColorDefault }, uiTimeout); err != nil {
		t.Fatalf("esc did not clear the selection: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Screen().Text(), "MULTI") {
		t.Fatalf("esc in visual mode left multi copy mode; it must only end the selection\n%s", term.Snapshot())
	}

	// plain: the two lines, one pane after the other, nothing added.
	if got, want := yankMulti(t, term, out, "plain"), multiCopyLines[0]+"\n"+multiCopyLines[1]+"\n"; got != want {
		t.Fatalf("plain yank = %q, want %q\n%s", got, want, term.Snapshot())
	}
	if err := term.WaitForText("Copied 2 panes as plain", uiTimeout); err != nil {
		t.Errorf("no copy message: %v\n%s", err, term.Snapshot())
	}

	// markdown: a header per pane and a fenced block.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	if err := term.WaitForText("format: markdown", uiTimeout); err != nil {
		t.Fatalf("tab did not change the format to markdown: %v\n%s", err, term.Snapshot())
	}
	md := yankMulti(t, term, out, "markdown")
	mdShape := regexp.MustCompile("(?s)^## Pane \\d+[^\n]*\n\n```\n" + regexp.QuoteMeta(multiCopyLines[0]) +
		"\n```\n\n## Pane \\d+[^\n]*\n\n```\n" + regexp.QuoteMeta(multiCopyLines[1]) + "\n```\n$")
	if !mdShape.MatchString(md) {
		t.Fatalf("markdown yank =\n%s", md)
	}

	// json: one object per pane with the documented keys.
	if err := term.SendKeys(tuitest.Tab); err != nil {
		t.Fatalf("send tab: %v", err)
	}
	if err := term.WaitForText("format: json", uiTimeout); err != nil {
		t.Fatalf("tab did not change the format to json: %v\n%s", err, term.Snapshot())
	}
	checkJSON := func(what, js string) {
		t.Helper()
		var panes []struct {
			Pane     int      `json:"pane"`
			WindowID string   `json:"window_id"`
			Title    *string  `json:"title"`
			Lines    []string `json:"lines"`
		}
		if err := json.Unmarshal([]byte(js), &panes); err != nil {
			t.Fatalf("%s is not JSON: %v\n%s", what, err, js)
		}
		if len(panes) != 2 || panes[0].Pane >= panes[1].Pane || panes[0].WindowID == "" || panes[0].Title == nil ||
			strings.Join(panes[0].Lines, "|") != multiCopyLines[0] || strings.Join(panes[1].Lines, "|") != multiCopyLines[1] {
			t.Fatalf("%s = %s", what, js)
		}
	}
	checkJSON("json yank", yankMulti(t, term, out, "json"))

	// Y saves to a file instead, with a default path in the home directory.
	if err := term.SendKeys("V"); err != nil {
		t.Fatalf("send V: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	before := len(clipboardWrites(out))
	if err := term.SendKeys("Y"); err != nil {
		t.Fatalf("send Y: %v", err)
	}
	if err := term.WaitForText("Save to: ~/tuios-copy-", uiTimeout); err != nil {
		t.Fatalf("Y did not open the save prompt: %v\n%s", err, term.Snapshot())
	}
	saveMultiCopyFrame(t, term, "save-prompt")
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	if err := term.WaitForText("Saved to ~/tuios-copy-", uiTimeout); err != nil {
		t.Fatalf("no saved message: %v\n%s", err, term.Snapshot())
	}
	files, _ := filepath.Glob(filepath.Join(home, "tuios-copy-*.json"))
	if len(files) != 1 {
		t.Fatalf("want one saved file in %s, found %v", home, files)
	}
	if !strings.Contains(term.Screen().Text(), "Saved to ~/"+filepath.Base(files[0])) {
		t.Errorf("the saved message does not name the file %s\n%s", files[0], term.Snapshot())
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	checkJSON("saved file", string(data))
	if n := len(clipboardWrites(out)); n != before {
		t.Errorf("saving to a file also wrote the clipboard")
	}

	// q leaves copy mode in every pane.
	if err := term.SendKeys("q"); err != nil {
		t.Fatalf("send q: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "MULTI") }, uiTimeout); err != nil {
		t.Fatalf("q did not leave multi copy mode: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after multi copy mode")
}

func TestMultiCopyModeYanksEveryPane(t *testing.T) {
	out := &lockedBuffer{}
	term, base := start(t, startOpts{out: out})
	waitBoot(t, term)
	setUpMultiCopyPanes(t, term, false)
	runMultiCopyE2E(t, term, out, xdgDir(base, "HOME"))
}

// The same through a daemon session, which is how tuios ships.
func TestMultiCopyModeYanksEveryPaneDaemon(t *testing.T) {
	out := &lockedBuffer{}
	base := t.TempDir()
	killDaemon(t, base)
	if o, err := tuiosCLI(t, base, "new", "e2e-mcopy", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, o)
	}
	term := startIn(t, base, startOpts{out: out, args: []string{"attach", "e2e-mcopy"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	setUpMultiCopyPanes(t, term, true)
	runMultiCopyE2E(t, term, out, xdgDir(base, "HOME"))
}
