package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The scratch terminal (toggle_scratch, leader g), driven through a real PTY.
//
// How these could pass wrongly, written down first:
//   - The text could be on screen because the pane never hid. Each hide waits
//     for the marker to leave the screen and for the dock to count one window
//     again, before the next show.
//   - The text could come back because the show ran the command again. The
//     marker is computed by the shell (6*7) once, and the show types nothing.
//   - The show could make a new pane each time. The test reads the pane's id
//     on the first show and requires the same id after every show.
//   - The popup could hold a nested tuios again. The daemon must list no
//     session but "work", and the pane itself must hold the shell's output and
//     no dock.

// scratchRow is the scratch terminal's row in `tuios list-windows --json`.
type scratchRow struct {
	ID        string `json:"window_id"`
	Minimized bool   `json:"minimized"`
	Scratch   bool   `json:"scratch"`
	Workspace int    `json:"workspace"`
}

// scratchRowOf returns the scratch terminal's row in session work, and
// whether there is one.
func scratchRowOf(t *testing.T, base string) (scratchRow, bool) {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", "work")
	if err != nil {
		return scratchRow{}, false
	}
	var res struct {
		Windows []scratchRow `json:"windows"`
	}
	if json.Unmarshal([]byte(out), &res) != nil {
		return scratchRow{}, false
	}
	for _, w := range res.Windows {
		if w.Scratch {
			return w, true
		}
	}
	return scratchRow{}, false
}

// waitScratch waits until the scratch terminal is listed, or is gone when
// gone is set, and returns its row. Whether the group is on the screen is the
// client's view, not session state, so hidden is read off the screen by the
// callers; the argument stays so the calls read as what they check.
func waitScratch(t *testing.T, term *tuitest.Terminal, base string, hidden, gone bool, what string) scratchRow {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		row, ok := scratchRowOf(t, base)
		switch {
		case gone && !ok:
			return row
		case !gone && ok:
			_ = hidden
			return row
		}
		time.Sleep(100 * time.Millisecond)
	}
	row, ok := scratchRowOf(t, base)
	t.Fatalf("%s: the scratch terminal is not as expected (listed %v, row %+v)\n%s", what, ok, row, term.Snapshot())
	return scratchRow{}
}

// toggleScratch presses the leader and g.
func toggleScratch(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "g"); err != nil {
		t.Fatalf("send leader g: %v", err)
	}
}

// typeUntil types cmd and enter until want is on the screen, as a person
// retypes a command that went nowhere. It returns how many tries it took.
func typeUntil(t *testing.T, term *tuitest.Terminal, cmd, want string) int {
	t.Helper()
	deadline := time.Now().Add(bootTimeout)
	for try := 1; ; try++ {
		if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
			t.Fatalf("type %q: %v", cmd, err)
		}
		if err := term.WaitForText(want, 2*time.Second); err == nil {
			return try
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never printed %q\n%s", cmd, want, term.Snapshot())
		}
	}
}

// waitCount waits until the dock counts n windows on the workspace.
func waitCount(t *testing.T, term *tuitest.Terminal, n int, what string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == n }, uiTimeout); err != nil {
		t.Fatalf("%s: the dock never counted %d windows (got %d)\n%s", what, n, countWindows(term.Screen()), term.Snapshot())
	}
}

// dockRows is the text of the bottom rows, where the dock is drawn.
func dockRows(s tuitest.Screen) string {
	_, rows := s.Size()
	var b strings.Builder
	for r := max(0, rows-2); r < rows; r++ {
		b.WriteString(s.Line(r))
		b.WriteByte('\n')
	}
	return b.String()
}

// assertHiddenEverywhere checks the places a user reads windows from: the
// dock, and the list-windows table. The JSON keeps the row, marked.
func assertHiddenEverywhere(t *testing.T, term *tuitest.Terminal, base, id string) {
	t.Helper()
	waitCount(t, term, 1, "with the scratch terminal hidden")
	if dock := dockRows(term.Screen()); strings.Contains(dock, "scratch") {
		t.Errorf("the dock shows the hidden scratch terminal:\n%s", dock)
	}
	table, err := tuiosCLI(t, base, "list-windows", "--session", "work")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, table)
	}
	if strings.Contains(table, "scratch") || !strings.Contains(table, "1 window(s)") {
		t.Errorf("the list-windows table shows the hidden scratch terminal:\n%s", table)
	}
	if row := waitScratch(t, term, base, true, false, "hidden"); row.ID != id {
		t.Errorf("the hidden pane is %s, want %s", row.ID, id)
	}
}

// startScratchOuter starts a client on session work with one pane, on the
// shipped [startup] settings: no default window, window mode.
func startScratchOuter(t *testing.T, base string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitBoot(t, term)
	newWindow(t, term)
	return term
}

// TestScratchTerminalShowsOneShell is the scratch key end to end in a daemon
// session: show, type, hide, the lists, show again, a hide from inside the
// popup, a detach and reattach, and a shell that exits.
func TestScratchTerminalShowsOneShell(t *testing.T) {
	base := t.TempDir()
	term := startScratchOuter(t, base)

	// Show. One bordered terminal named scratch, with the keyboard in it.
	toggleScratch(t, term)
	first := waitScratch(t, term, base, false, false, "the first show")
	waitCount(t, term, 1, "with the scratch terminal shown, which the count leaves out")
	tries := typeUntil(t, term, "echo SCRATCH-$((6*7))", "SCRATCH-42")
	t.Logf("the scratch terminal with text typed into it (%d tries):\n%s", tries, term.Snapshot())

	// A shell, not a nested tuios: no session but work, and the pane holds
	// the shell's output and no dock of its own.
	out, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	var sessions []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &sessions); err != nil || len(sessions) != 1 || sessions[0].Name != "work" {
		t.Fatalf("sessions = %s, want only work", out)
	}
	pane, err := tuiosCLI(t, base, "capture-pane", "-s", "work", "-w", first.ID)
	if err != nil {
		t.Fatalf("capture the scratch pane: %v\n%s", err, pane)
	}
	if !strings.Contains(pane, "SCRATCH-42") || dockStatus.MatchString(pane) ||
		strings.Contains(pane, "Terminal mode") || strings.Contains(pane, "Window management mode") {
		t.Fatalf("the scratch pane is not a plain shell:\n%s", pane)
	}
	if n := strings.Count(term.Screen().Text(), "scratch"); n != 1 {
		t.Errorf("the screen names scratch %d times, want once, on the popup border\n%s", n, term.Snapshot())
	}

	// Hide, from inside the popup: the keyboard is still in it.
	toggleScratch(t, term)
	waitGone(t, term, "after the hide", "SCRATCH-42")
	assertHiddenEverywhere(t, term, base, first.ID)
	t.Logf("the layout with the scratch terminal hidden:\n%s", term.Snapshot())

	// Show again. The marker is what the shell kept.
	toggleScratch(t, term)
	if err := term.WaitForText("SCRATCH-42", uiTimeout); err != nil {
		t.Fatalf("the scratch terminal lost its text across a hide: %v\n%s", err, term.Snapshot())
	}
	if again := waitScratch(t, term, base, false, false, "the second show"); again.ID != first.ID {
		t.Fatalf("the show made a new pane: %s, then %s", first.ID, again.ID)
	}
	typeUntil(t, term, "echo AGAIN-$((7*8))", "AGAIN-56")
	t.Logf("the scratch terminal shown again and typed into:\n%s", term.Snapshot())

	// Detach with the popup on the screen, and attach again with a bare
	// tuios attach. It picks the most recently active session. The first
	// design made a session called scratch, which then won this pick, so a
	// bare attach landed in scratch instead of work. The scratch terminal is
	// a pane of work, so work is the only session to pick.
	if err := term.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("send leader d: %v", err)
	}
	waitExit(t, term, "after leader d")
	term = startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"attach"}})
	if err := term.WaitForText("AGAIN-56", bootTimeout); err != nil {
		t.Fatalf("the scratch terminal did not come back on reattach: %v\n%s", err, term.Snapshot())
	}
	if row := waitScratch(t, term, base, false, false, "after the reattach"); row.ID != first.ID {
		t.Fatalf("the reattach made a new pane: %s, then %s", first.ID, row.ID)
	}
	if out, err := tuiosCLI(t, base, "ls", "--json"); err != nil || strings.Count(out, `"name"`) != 1 || !strings.Contains(out, `"work"`) {
		t.Fatalf("after the bare attach the daemon lists %s, want only work", out)
	}
	t.Logf("after the reattach:\n%s", term.Snapshot())
	time.Sleep(insertGuard)
	toggleScratch(t, term)
	waitGone(t, term, "hide after the reattach", "AGAIN-56")
	assertHiddenEverywhere(t, term, base, first.ID)
	toggleScratch(t, term)
	if err := term.WaitForText("SCRATCH-42", uiTimeout); err != nil {
		t.Fatalf("the show after the reattach lost the text: %v\n%s", err, term.Snapshot())
	}

	// The shell exits. The pane goes, and the next press starts a new one.
	typeUntilGone(t, term, "exit", base)
	waitGone(t, term, "after exit", "SCRATCH-42")
	toggleScratch(t, term)
	fresh := waitScratch(t, term, base, false, false, "the show after exit")
	if fresh.ID == first.ID {
		t.Fatalf("the show after exit kept pane %s", fresh.ID)
	}
	typeUntil(t, term, "echo FRESH-$((5*5))", "FRESH-25")
	if strings.Contains(term.Screen().Text(), "SCRATCH-42") {
		t.Errorf("the new scratch shell shows the old one's text\n%s", term.Snapshot())
	}
	t.Logf("a new scratch shell after exit:\n%s", term.Snapshot())
	alive(t, term, "after the scratch terminal round trip")
}

// typeUntilGone types exit into the scratch shell until the pane is gone.
func typeUntilGone(t *testing.T, term *tuitest.Terminal, cmd, base string) {
	t.Helper()
	if err := term.SendKeys(cmd, tuitest.Enter); err != nil {
		t.Fatalf("type %q: %v", cmd, err)
	}
	waitScratch(t, term, base, false, true, "after exit")
}

// TestScratchTerminalWithoutDaemon is the same key in a session without a
// daemon: show, type, hide, show again with the text kept.
func TestScratchTerminalWithoutDaemon(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	toggleScratch(t, term)
	waitCount(t, term, 1, "with the scratch terminal shown, which the count leaves out")
	typeUntil(t, term, "echo LOCAL-$((6*7))", "LOCAL-42")
	t.Logf("the local scratch terminal:\n%s", term.Snapshot())

	toggleScratch(t, term)
	waitGone(t, term, "after the hide", "LOCAL-42")
	waitCount(t, term, 1, "with the scratch terminal hidden")
	if dock := dockRows(term.Screen()); strings.Contains(dock, "scratch") {
		t.Errorf("the dock shows the hidden scratch terminal:\n%s", dock)
	}

	toggleScratch(t, term)
	if err := term.WaitForText("LOCAL-42", uiTimeout); err != nil {
		t.Fatalf("the local scratch terminal lost its text: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the local round trip")
}

// TestScratchTerminalSurvivesCloseAndFocus covers the ways a key could end or
// reach the scratch terminal behind the user's back.
//
// Esc in window mode closes a popup, but a scratch pane is not one: esc
// leaves the group on the screen and the shell running. A focus-window on a
// pane of the hidden group shows the group before it takes the keys, so
// nothing is typed into a pane nobody sees. A relative focus never lands on a
// hidden group's pane.
func TestScratchTerminalSurvivesCloseAndFocus(t *testing.T) {
	base := t.TempDir()
	term := startScratchOuter(t, base)

	toggleScratch(t, term)
	first := waitScratch(t, term, base, false, false, "the first show")
	typeUntil(t, term, "echo KEEP-$((6*7))", "KEEP-42")

	// Esc to window mode, then esc on the scratch pane: nothing closes.
	windowManagementMode(t, term)
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("send esc: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if !strings.Contains(term.Screen().Text(), "KEEP-42") {
		t.Fatalf("esc hid or closed the scratch group\n%s", term.Snapshot())
	}
	if row := waitScratch(t, term, base, false, false, "after esc"); row.ID != first.ID {
		t.Fatalf("esc closed the scratch pane: %s, then %s", first.ID, row.ID)
	}
	t.Logf("after esc on the scratch pane:\n%s", term.Snapshot())
	toggleScratch(t, term)
	waitGone(t, term, "the scratch key", "KEEP-42")

	// A relative focus walks the visible panes only.
	for range 3 {
		if out, err := tuiosCLI(t, base, "focus-window", "-s", "work", "--relative", "next"); err != nil {
			t.Logf("focus-window --relative next: %v %s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if row := waitScratch(t, term, base, true, false, "after a relative focus"); row.ID != first.ID {
		t.Fatalf("the hidden pane changed: %s", row.ID)
	}
	if strings.Contains(term.Screen().Text(), "KEEP-42") {
		t.Fatalf("a relative focus showed the hidden scratch terminal\n%s", term.Snapshot())
	}

	// A focus by id shows it, with the text it kept, and takes the keys.
	if out, err := tuiosCLI(t, base, "focus-window", "-s", "work", first.ID); err != nil {
		t.Fatalf("focus-window: %v %s", err, out)
	}
	if err := term.WaitForText("KEEP-42", uiTimeout); err != nil {
		t.Fatalf("focus-window did not show the scratch terminal: %v\n%s", err, term.Snapshot())
	}
	waitScratch(t, term, base, false, false, "after focus-window")
	typeUntil(t, term, "echo SEEN-$((3*3))", "SEEN-9")
	t.Logf("after focus-window on the hidden scratch terminal:\n%s", term.Snapshot())
	alive(t, term, "after esc and focus-window")
}
