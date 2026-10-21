package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestPaletteRunsTheActionItFinds. Every action is in the palette behind the
// "#" token, and a row that only ever opened the keybind manager made the
// palette a place to look an action up rather than to do it. Enter on the row
// runs the action, after the palette has closed, and ctrl+e is the way to the
// manager instead.
//
// The run is read from the daemon's window list and the dock, not from the
// palette, because an action that ran against the palette's own state rather
// than the session would close the palette and change nothing else.
//
// Negative control: make enter on an action row open the manager again and
// this fails at the window count, which stays at one.
func TestPaletteRunsTheActionItFinds(t *testing.T) {
	term, base := attachClientBase(t)
	dir := artifactDir(t)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before the action search")
	if err := term.Type("#new_window"); err != nil {
		t.Fatalf("type the token query: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "#new_window") && strings.Contains(s.Text(), "rebind")
	}, uiTimeout); err != nil {
		t.Fatalf("the action search showed no new_window row or no rebind hint: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "1-action-search")

	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("run the action row: %v", err)
	}
	waitPaletteClosed(t, term, "after enter on an action row")
	waitWindowCount(t, term, 2, "enter on the new_window row")
	waitSessionWindows(t, base, "e2e-ctrlp", 2, "enter on the new_window row")
	if strings.Contains(term.Screen().Text(), keybindTitle) {
		t.Fatalf("enter on an action row opened the keybind manager:\n%s", term.Snapshot())
	}
	saveArtifact(t, term, dir, "2-after-enter")

	// The manager is one key away on the same row.
	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("reopen palette: %v", err)
	}
	waitPaletteOpen(t, term, "before the rebind key")
	if err := term.Type("#toggle_zoom"); err != nil {
		t.Fatalf("type the token query: %v", err)
	}
	if err := term.WaitForText("toggle_zoom", uiTimeout); err != nil {
		t.Fatalf("the action search found no toggle_zoom row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('e')); err != nil {
		t.Fatalf("send the rebind key: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("the rebind key did not open the keybind manager: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("Toggle zoom", uiTimeout); err != nil {
		t.Fatalf("the manager did not open on the action that was picked: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "3-rebind-key")
	waitSessionWindows(t, base, "e2e-ctrlp", 2, "the rebind key")
	alive(t, term, "after running and rebinding from the palette")
}

// TestPaletteViewOnlyActionSaysSoInFull. Enter on an action that works only in
// its own view, such as the rail's file_delete_forever, opens the keybind
// manager with a notice saying why. The notice is read in the dock at the
// suite's terminal size and must fit there whole: one cut off with an ellipsis
// says less than nothing about why enter did not run the action.
//
// Negative control: put back the two-sentence notice ("... runs only in its own
// view. Its keys are listed here.") and this fails on the ellipsis the dock
// cuts it with.
func TestPaletteViewOnlyActionSaysSoInFull(t *testing.T) {
	term, _ := attachClientBase(t)
	dir := artifactDir(t)

	if err := term.SendKeys(legacyCtrlP); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	waitPaletteOpen(t, term, "before the action search")
	if err := term.Type("#file_delete_forever"); err != nil {
		t.Fatalf("type the token query: %v", err)
	}
	if err := term.WaitForText("file_delete_forever", uiTimeout); err != nil {
		t.Fatalf("the action search found no file_delete_forever row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("enter on the action row: %v", err)
	}
	if err := term.WaitForText(keybindTitle, uiTimeout); err != nil {
		t.Fatalf("enter on a view-only action did not open the keybind manager: %v\n%s", err, term.Snapshot())
	}
	var line string
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		for _, l := range strings.Split(s.Text(), "\n") {
			if strings.Contains(l, "file_delete_forever") && strings.Contains(l, "own view") {
				line = l
				return true
			}
		}
		return false
	}, uiTimeout); err != nil {
		t.Fatalf("no notice says file_delete_forever works only in its own view: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, dir, "view-only-notice")
	if strings.Contains(line, "…") {
		t.Fatalf("the notice is cut off in the dock: %q\n%s", strings.TrimSpace(line), term.Snapshot())
	}
	if !strings.Contains(line, "file_delete_forever works only in its own view.") {
		t.Fatalf("the notice reads %q, want \"file_delete_forever works only in its own view.\"", strings.TrimSpace(line))
	}
}

// waitSessionWindows waits for the daemon to list want windows in session.
func waitSessionWindows(t *testing.T, base, session string, want int, what string) {
	t.Helper()
	var raw string
	got := -1
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		var err error
		raw, err = tuiosCLI(t, base, "list-windows", "-s", session, "--json")
		var listing struct {
			Windows []json.RawMessage `json:"windows"`
		}
		if err == nil && json.Unmarshal([]byte(raw), &listing) == nil {
			got = len(listing.Windows)
			if got == want {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: the daemon lists %d windows, want %d:\n%s", what, got, want, raw)
}
