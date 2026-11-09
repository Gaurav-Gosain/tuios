package tuie2e

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// sortRowRe is the Group menu's sort row: the label, the order in force as
// its note, and its key.
var sortRowRe = regexp.MustCompile(`Sort\s+(needs you|name|recent|session)\s+s`)

// sortRowShown reports whether the Group menu's sort row is on screen.
func sortRowShown(s tuitest.Screen) bool { return sortRowRe.MatchString(s.Text()) }

// menuRow is the screen row whose text holds label inside the open menu,
// below the header's row, and the column the label starts at.
func menuRow(s tuitest.Screen, header int, label string) (row, col int) {
	_, rows := s.Size()
	for y := header + 1; y < rows; y++ {
		line := s.Line(y)
		if i := strings.Index(line, label); i >= 0 {
			return y, len([]rune(line[:i]))
		}
	}
	return -1, -1
}

// TestCockpitHeaderDropdowns drives the agents view's header controls with
// the mouse. A click on "Group: state" opens a menu under it that lists the
// groupings with the current one marked and a key beside each; the pointer
// lights the row it is over; a click on "session" groups by session and
// closes the menu. Esc closes a menu with nothing changed, and so does a
// click outside it, which does nothing else. The Board control's menu picks
// the board.
//
// How it could pass wrongly, written down first:
//   - The choices could show in the key bar, as they did, and not under the
//     control. The rows are looked for below the header, at the control's
//     column, with the key beside each.
//   - The hover could be the cursor that was already there. The pointer goes
//     to a row the cursor is not on, and the test waits for that row's ground
//     to change.
//   - A click outside could close the menu and also act where it landed. It
//     lands on the New agent button, and the sheet must not open.
//
// Negative control: on main a click on the control opens no menu, only key
// hints in the bar, and the wait for the menu's rows fails.
func TestCockpitHeaderDropdowns(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	for _, s := range []string{"home", "docs"} {
		if out, err := tuiosCLI(t, base, "new", s, "--detach"); err != nil {
			t.Fatalf("create session %s: %v\n%s", s, err, out)
		}
	}
	for _, args := range [][]string{
		{"set-window", "-s", "docs", "-w", "0", "--name", "docs-cli"},
		{"set-agent-state", "idle", "-s", "docs", "-w", "docs-cli", "--harness", "claude-code"},
	} {
		if out, err := tuiosCLI(t, base, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 44, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "Group: state", "docs-cli")
	time.Sleep(insertGuard)

	openMenu := func(control string, want ...string) (header, x int) {
		t.Helper()
		header, x = findText(t, term, control)
		mouseClick(t, term, x+1, header, tuitest.MouseLeft, 0)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			for _, w := range want {
				if r, _ := menuRow(s, header, w); r < 0 || r > header+12 {
					return false
				}
			}
			return true
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a click on %q opened no menu under it with %q: %v\n%s", control, want, err, term.Snapshot())
		}
		return header, x
	}

	// The Group menu: the groupings, the current one marked, a key each.
	header, gx := openMenu("Group: state", "state", "session", "machine", "project")
	s := term.Screen()
	for i, name := range []string{"state", "session", "machine", "project"} {
		r, c := menuRow(s, header, name)
		line := s.Line(r)
		if c < gx-1 || !strings.Contains(line, string(rune('1'+i))) {
			t.Errorf("ASSERTION: the menu row %q is not under the control at column %d with its key %d: %q", name, gx, i+1, line)
		}
	}
	if !sortRowShown(s) {
		t.Errorf("ASSERTION: the Group menu has no sort row with the order and its key s\n%s", term.Snapshot())
	}
	if r, _ := menuRow(s, header, "state"); !strings.Contains(s.Line(r), "●") {
		t.Errorf("ASSERTION: the current grouping, state, is not marked: %q", s.Line(r))
	}
	railShot(t, term, "group-menu")

	// The pointer lights the row it is over.
	mr, mc := menuRow(s, header, "machine")
	before := s.Cell(mc, mr).Bg
	mouseHover(t, term, mc, mr)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return s.Cell(mc, mr).Bg != before }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the row under the pointer did not light: %v\n%s", err, term.Snapshot())
	}
	railShot(t, term, "group-menu-hover")

	// A click on session groups by session and closes the menu.
	sr, sc := menuRow(term.Screen(), header, "session")
	mouseClick(t, term, sc, sr, tuitest.MouseLeft, 0)
	waitText(t, term, "the grouping after the click", "Group: session")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !sortRowShown(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the menu stayed open after a pick: %v\n%s", err, term.Snapshot())
	}

	// Esc closes it with nothing changed.
	openMenu("Group: session", "state", "session")
	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !sortRowShown(s) && strings.Contains(s.Text(), "Group: session")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: esc did not close the menu, or changed the grouping: %v\n%s", err, term.Snapshot())
	}

	// A click outside closes it and does nothing else: on New agent, the
	// sheet does not open.
	openMenu("Group: session", "state", "session")
	nr, nc := findText(t, term, "New agent")
	mouseClick(t, term, nc+2, nr, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !sortRowShown(s) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click outside the menu did not close it: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	if strings.Contains(term.Screen().Text(), "Start agent") {
		t.Fatalf("ASSERTION: the click that closed the menu also opened the New agent sheet\n%s", term.Snapshot())
	}

	// The Board control: a menu of the two views, and a key pick.
	openMenu("Board", "Agents", "Board")
	railShot(t, term, "board-menu")
	sendKeys(t, term, "2")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Line(header), "Agents") && !strings.Contains(s.Line(header), "Board")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: 2 in the Board menu did not open the board: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the dropdowns")
}

// TestSpawnSheetListIsTheDropdown opens the New agent sheet's agent list with
// the mouse and checks it is the same dropdown the header menus are: under
// its control, the agent in force marked, a key beside each row, the pointer
// lighting the row it is over, a row's key picking it, and a click outside
// closing it and doing nothing else.
//
// How it could pass wrongly, written down first:
//   - The rows could be the agent chips of the field, not a list. The chips
//     have no keys; the test reads the digit at the end of each row.
//   - A click outside could close the list and also act where it landed. It
//     lands on Cancel, and the sheet must stay open.
//
// Negative control: on main the list has no keys beside its rows and no
// picked mark, and the row check fails.
func TestSpawnSheetListIsTheDropdown(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("create session home: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "home"}, cols: 160, rows: 48, shippedLooks: true,
		env: []string{"TERM=xterm-256color", "COLORTERM=truecolor"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the agents view", "New agent")
	nr, nc := findText(t, term, "New agent")
	mouseClick(t, term, nc+2, nr, tuitest.MouseLeft, 0)
	waitText(t, term, "the New agent sheet", "Start agent", " more ")
	time.Sleep(insertGuard)

	open := func() int {
		t.Helper()
		mr, mc := findText(t, term, " more ")
		mouseClick(t, term, mc+2, mr, tuitest.MouseLeft, 0)
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			r, _ := menuRow(s, mr, "aider")
			return r > mr && regexp.MustCompile(`aider.*\s[1-9]\s*$`).MatchString(strings.TrimRight(s.Line(r), " "))
		}, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a click on more opened no list under it with keys beside its rows: %v\n%s", err, term.Snapshot())
		}
		return mr
	}
	mr := open()
	s := term.Screen()
	if !strings.Contains(s.Text(), "●") {
		t.Errorf("ASSERTION: the agent in force is not marked in the list\n%s", term.Snapshot())
	}
	railShot(t, term, "sheet-list")

	// The pointer lights the row it is over.
	ar, ac := menuRow(s, mr, "amp")
	before := s.Cell(ac, ar).Bg
	mouseHover(t, term, ac, ar)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return s.Cell(ac, ar).Bg != before }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the list row under the pointer did not light: %v\n%s", err, term.Snapshot())
	}

	// A click outside closes the list and does nothing else: on Cancel, the
	// sheet stays open.
	cr, cc := findText(t, term, "Cancel")
	mouseClick(t, term, cc+1, cr, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool { r, _ := menuRow(s, mr, "aider"); return r < 0 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: a click outside the list did not close it: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	if !strings.Contains(term.Screen().Text(), "Start agent") {
		t.Fatalf("ASSERTION: the click that closed the list also cancelled the sheet\n%s", term.Snapshot())
	}

	// A row's key picks it: the key at the end of aider's row. The order
	// follows what this machine has installed, so the key is read.
	open()
	kr, _ := menuRow(term.Screen(), mr, "aider")
	key := regexp.MustCompile(`([1-9])\s*$`).FindStringSubmatch(strings.TrimRight(term.Screen().Line(kr), " │"))
	if key == nil {
		t.Fatalf("ASSERTION: aider's row has no key: %q", term.Screen().Line(kr))
	}
	sendKeys(t, term, key[1])
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r, _ := menuRow(s, mr, "amp")
		return r < 0 && strings.Contains(s.Text(), " aider ")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s in the list did not pick aider and close it: %v\n%s", key[1], err, term.Snapshot())
	}
	alive(t, term, "after the sheet's list")
}
