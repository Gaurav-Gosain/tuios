package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const renameClearSession = "rnclr"

// TestRenameDialogClears drives the rename dialog's clear keys against a real
// client attached to a daemon, and reads every outcome back from list-windows
// rather than the screen, because the screen keeps showing the old name on
// the title bar while the dialog is up.
//
//   - ctrl+u empties a field that opened holding the old name, and what is
//     typed after it is the whole new name.
//   - ctrl+c on a field with text empties it and leaves the dialog open.
//   - ctrl+c on an empty field closes the dialog and renames nothing.
//   - the clear hint in the dialog's border empties the field when clicked.
//   - ctrl+u empties the help overlay's search query and stays in search.
//
// How this could pass wrongly: a clear that closed the dialog and a typed
// name that then went nowhere would leave the old name, so each step requires
// the dialog still up after the clear and the new name in list-windows after
// enter. A ctrl+c that committed the empty field instead of cancelling would
// clear the name, so the cancel step requires the name unchanged.
func TestRenameDialogClears(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", renameClearSession, "--detach"); err != nil {
		t.Fatalf("create %s: %v: %s", renameClearSession, err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", renameClearSession, "--name", "oldname"); err != nil {
		t.Fatalf("name the window: %v: %s", err, out)
	}
	art := artifactDir(t)

	term := startIn(t, base, startOpts{args: []string{"attach", renameClearSession}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return countWindows(s) == 1
	}, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
		t.Fatalf("normalise to window mode: %v", err)
	}
	if err := term.WaitForText("Window management mode", uiTimeout); err != nil {
		t.Fatalf("client never settled in window management mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)

	// ctrl+u: the field opens holding the old name and empties.
	openRenameOn(t, term, "oldname")
	sendKeys(t, term, tuitest.Ctrl('u'))
	waitRenameField(t, term, "ctrl+u left the dialog open with an empty field", "")
	saveArtifact(t, term, art, "rename-after-ctrl-u")
	sendKeys(t, term, "fresh")
	waitRenameField(t, term, "the field took the typed name", "fresh")
	sendKeys(t, term, tuitest.Enter)
	waitWindowName(t, base, term, "fresh")

	// ctrl+c with text: the field empties and the dialog stays.
	openRenameOn(t, term, "fresh")
	sendKeys(t, term, tuitest.Ctrl('c'))
	waitRenameField(t, term, "ctrl+c with text left the dialog open with an empty field", "")
	saveArtifact(t, term, art, "rename-after-ctrl-c-with-text")
	sendKeys(t, term, "second")
	waitRenameField(t, term, "the field took the typed name", "second")
	sendKeys(t, term, tuitest.Enter)
	waitWindowName(t, base, term, "second")

	// ctrl+c on an empty field: the dialog closes and the name is kept.
	openRenameOn(t, term, "second")
	sendKeys(t, term, tuitest.Ctrl('c'))
	waitRenameField(t, term, "the first ctrl+c emptied the field", "")
	sendKeys(t, term, tuitest.Ctrl('c'))
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !renameDialogUp(s) }, uiTimeout); err != nil {
		t.Fatalf("ctrl+c on an empty field did not close the dialog: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, art, "rename-after-ctrl-c-empty")
	time.Sleep(time.Second)
	if got := windowName(t, base); got != "second" {
		t.Fatalf("ctrl+c on an empty field renamed the window to %q, want it left as second\n%s", got, term.Snapshot())
	}

	// The clear hint, clicked.
	openRenameOn(t, term, "second")
	row, col := renameHint(t, term, "clear")
	mouseClick(t, term, col, row, tuitest.MouseLeft, 0)
	waitRenameField(t, term, "a click on the clear hint left the dialog open with an empty field", "")
	saveArtifact(t, term, art, "rename-after-clear-click")
	sendKeys(t, term, "third")
	sendKeys(t, term, tuitest.Enter)
	waitWindowName(t, base, term, "third")

	// The help overlay's search field takes the same clear key.
	sendKeys(t, term, tuitest.Ctrl('b'), "?")
	if err := term.WaitForText("Keybindings", uiTimeout); err != nil {
		t.Fatalf("the help never opened: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, "/", "qqzzx")
	if err := term.WaitForText("qqzzx", uiTimeout); err != nil {
		t.Fatalf("the help search never showed the query: %v\n%s", err, term.Snapshot())
	}
	sendKeys(t, term, tuitest.Ctrl('u'))
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Keybindings") && !strings.Contains(text, "qqzzx")
	}, uiTimeout); err != nil {
		t.Fatalf("ctrl+u did not clear the help search query: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, art, "help-search-after-ctrl-u")
	// Still searching: what is typed next is a query, and it finds.
	sendKeys(t, term, "rename")
	if err := term.WaitForText("Rename window", uiTimeout); err != nil {
		t.Fatalf("the help left search after ctrl+u: %v\n%s", err, term.Snapshot())
	}

	alive(t, term, "after clearing the rename dialog")
}

// openRenameOn opens the focused window's rename dialog with r and waits for
// its field to hold the name it is seeded with.
func openRenameOn(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	time.Sleep(insertGuard)
	sendKeys(t, term, "r")
	waitRenameField(t, term, "the rename dialog opened on the window's name", name)
}

// renameField is what the dialog's field holds, read off the row under its
// title border between the dialog's own side borders, and whether the dialog
// is up at all.
func renameField(s tuitest.Screen) (string, bool) {
	if !renameDialogUp(s) {
		return "", false
	}
	_, rows := s.Size()
	for r := 0; r+1 < rows; r++ {
		title := []rune(s.Line(r))
		at := strings.Index(string(title), "╭─ rename")
		if at < 0 {
			continue
		}
		left := len([]rune(string(title)[:at]))
		right := left + 1
		for right < len(title) && title[right] != '╮' {
			right++
		}
		field := []rune(s.Line(r + 1))
		if right >= len(field) {
			return "", false
		}
		text := strings.TrimSpace(string(field[left+1 : right]))
		return strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(text, "›"), ">")), true
	}
	return "", false
}

// waitRenameField waits for the dialog to be up with its field holding
// exactly want.
func waitRenameField(t *testing.T, term *tuitest.Terminal, what, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		field, ok := renameField(s)
		return ok && field == want
	}, uiTimeout); err != nil {
		t.Fatalf("%s: field never read %q: %v\n%s", what, want, err, term.Snapshot())
	}
}

// renameHint is where the hint labelled label sits on the dialog's bottom
// border, in cells.
func renameHint(t *testing.T, term *tuitest.Terminal, label string) (row, col int) {
	t.Helper()
	s := term.Screen()
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if !strings.Contains(line, "esc cancel") {
			continue
		}
		if b := strings.Index(line, " "+label+" "); b >= 0 {
			return r, len([]rune(line[:b+1]))
		}
	}
	t.Fatalf("the rename dialog shows no %q hint\n%s", label, term.Snapshot())
	return 0, 0
}

func windowName(t *testing.T, base string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "-s", renameClearSession, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			Name string `json:"custom_name"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("list-windows JSON: %v\n%s", err, out)
	}
	if len(listing.Windows) != 1 {
		t.Fatalf("list-windows lists %d windows, want 1\n%s", len(listing.Windows), out)
	}
	return listing.Windows[0].Name
}

// waitWindowName waits for the daemon to hold want as the window's name.
func waitWindowName(t *testing.T, base string, term *tuitest.Terminal, want string) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(uiTimeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if got = windowName(t, base); got == want {
			return
		}
	}
	t.Fatalf("list-windows names the window %q, want %q\n%s", got, want, term.Snapshot())
}
