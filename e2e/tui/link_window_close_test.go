package tuie2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A tuios://window/<id>/close link is an order to close a pane, printed by
// whatever program felt like printing one, with visible text the same program
// also chose. The confirmation is what stands between the two: the dialog
// names the pane the address really names, and the pane dies only when the
// destructive row runs. These tests drive the click as the outer terminal
// would report it and read the pane list the daemon holds, the same way a
// script would see the aftermath.

// victimShortID trims the full id new-window --print-id prints down to the
// 8-character short form a link may carry.
func victimShortID(t *testing.T, full string) string {
	t.Helper()
	id := strings.TrimSpace(full)
	if len(id) < 8 {
		t.Fatalf("new-window --print-id printed %q, no short id in it", full)
	}
	return id[:8]
}

// sessionWindowIDs lists the session's window ids the way a script sees them.
func sessionWindowIDs(t *testing.T, base, session string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			ID string `json:"id"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-windows JSON: %v\n%s", err, out)
	}
	ids := make([]string, 0, len(listing.Windows))
	for _, w := range listing.Windows {
		ids = append(ids, w.ID)
	}
	return ids
}

// waitVictimGone polls the screen until the victim pane's border title is
// gone. The daemon's list-windows does not report the windows of a session a
// client is attached to, so the screen is the observable.
func waitVictimGone(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if !strings.Contains(term.Snapshot(), "victim-pane") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the pane survived the confirmed close:\n%s", term.Snapshot())
}

// findRowLast returns the screen cell of the last line containing want,
// scanning from the bottom. The close dialog's question repeats the
// destructive row's label, and the row is the one further down.
func findRowLast(t *testing.T, term *tuitest.Terminal, want string) (int, int) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := rows - 1; r >= 0; r-- {
		if c := strings.Index(s.Line(r), want); c >= 0 {
			return c, r
		}
	}
	t.Fatalf("%q is not on screen\n%s", want, term.Snapshot())
	return 0, 0
}

// TestCtrlClickCloseLinkConfirms drives the close half end to end: a link
// whose text says nothing about closing raises a dialog naming the pane,
// cancel leaves the pane alive, and confirming closes it.
//
// Negative control: with the confirmation dropped from openWindowLink (the
// pane closed on the click), the first WaitForText fails with no dialog, and
// the pane is already gone from the daemon's list.
func TestCtrlClickCloseLinkConfirms(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "linkclose", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "new-window", "-s", "linkclose", "victim-pane", "--print-id")
	if err != nil {
		t.Fatalf("create the victim pane: %v\n%s", err, out)
	}
	victim := victimShortID(t, out)

	term := attachIn(t, base, "linkclose", startOpts{env: []string{"TUIOS_DBG_MOUSE=1"}})
	enterTerminalMode(t, term)
	runInShell(t, term, fmt.Sprintf("printf 'see the \\033]8;;tuios://window/%s/close\\033\\\\docs\\033]8;;\\033\\\\\\n'; echo LIN\"\"KSET", victim), "LINKSET", uiTimeout)
	leaveTerminalMode(t, term)

	col, row := mustFind(t, term, "see the docs")
	mouseClick(t, term, col+10, row, tuitest.MouseLeft, tuitest.ModCtrl)
	if err := term.WaitForText("Close pane", uiTimeout); err != nil {
		t.Fatalf("the close link raised no confirmation:\n%s", term.Snapshot())
	}

	// A click anywhere outside the panel dismisses it, and the pane stays.
	mouseClick(t, term, 5, 3, tuitest.MouseLeft, 0)
	if !strings.Contains(term.Snapshot(), "victim-pane") {
		t.Error("cancel closed the pane")
	}

	// A click that names the pane again, confirmed by clicking the dialog's
	// destructive row: a click on a row runs it. Enter alone runs the row the
	// cursor is on, and the dialog opens on Cancel.
	mouseClick(t, term, col+10, row, tuitest.MouseLeft, tuitest.ModCtrl)
	if err := term.WaitForText("Close pane", uiTimeout); err != nil {
		t.Fatalf("the second close link raised no confirmation:\n%s", term.Snapshot())
	}
	dialogCol, dialogRow := findRowLast(t, term, "   Close pane")
	mouseClick(t, term, dialogCol, dialogRow, tuitest.MouseLeft, 0)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled after the confirmed close: %v\n%s", err, term.Snapshot())
	}
	waitVictimGone(t, term)
}

// TestShortCloseLinkRefused checks the guard against short ids: an address
// below the 8-character short form is not an address tuios answers, so the
// click says so instead of resolving to whichever pane the prefix fits.
func TestShortCloseLinkRefused(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "linkshort", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "new-window", "-s", "linkshort", "victim-pane", "--print-id")
	if err != nil {
		t.Fatalf("create the victim pane: %v\n%s", err, out)
	}
	victim := victimShortID(t, out)

	term := attachIn(t, base, "linkshort", startOpts{})
	enterTerminalMode(t, term)
	runInShell(t, term, fmt.Sprintf("printf 'close it \\033]8;;tuios://window/%.7s/close\\033\\\\here\\033]8;;\\033\\\\\\n'; echo LIN\"\"KSET", victim), "LINKSET", uiTimeout)
	leaveTerminalMode(t, term)

	col, row := mustFind(t, term, "close it")
	mouseClick(t, term, col+10, row, tuitest.MouseLeft, tuitest.ModCtrl)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	if strings.Contains(term.Snapshot(), "Close pane") {
		t.Error("a short-id close link raised the confirmation")
	}
	if !strings.Contains(term.Snapshot(), "victim-pane") {
		t.Error("the victim pane is gone and nothing confirmed it")
	}
}
