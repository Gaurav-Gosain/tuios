package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// Return notes: one line on a session or a pane that says what comes next,
// set by tuios note, shown when a person comes back, and kept across a
// daemon restart.

// noteLine is the screen line that holds every marker, or "" when none does.
// A rail row and a switcher row each sit on one line, so a marker pair on one
// line ties a note to the session it is drawn for.
func noteLine(s tuitest.Screen, markers ...string) string {
	for line := range strings.SplitSeq(s.Text(), "\n") {
		if screenHas1(line, markers...) {
			return line
		}
	}
	return ""
}

// screenHas1 reports whether line holds every marker.
func screenHas1(line string, markers ...string) bool {
	for _, m := range markers {
		if !strings.Contains(line, m) {
			return false
		}
	}
	return true
}

// noteGet runs tuios note get and returns what it printed.
func noteGet(t *testing.T, base string, args ...string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"note", "get", "-s", "e2e-note"}, args...)...)
	if err != nil {
		t.Fatalf("note get %v: %v: %s", args, err, out)
	}
	return strings.TrimRight(out, "\n")
}

// notePNG saves the screen as a picture for the pull request.
func notePNG(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	savePNG(t, term.Screen(), shot.XTermPalette(), artifactDir(t), name)
	saveArtifact(t, term, artifactDir(t), name)
}

// TestReturnNotesComeBackWithTheSession sets a note on a detached session and
// on one of its panes with the CLI, then reads them where a person comes
// back: the dock on attach, the rail row, the session switcher and the Inbox
// focus. The daemon restarts and the notes are still there. A clear takes the
// note off the rail, and a note changed while attached is not announced.
//
// How it could pass wrongly, and what stops it:
//   - the dock text typed by the shell, not drawn by tuios: nothing is typed
//     into any pane, and the notice carries the "Note: " prefix only the
//     client adds.
//   - the rail match made on the dock line, which can name the session too:
//     the rail is read only after the notice is gone.
//   - the note kept by the client and not the daemon: the restart starts a
//     new daemon and a new client, so only the saved state can bring it back.
//   - the pane note found through the session note: the two differ, and the
//     Inbox focus must show the pane's.
//   - the clear sent but the rail drawn from a stale listing: the row must
//     lose the note on screen.
//   - a notice for every note change: a note set while attached must not
//     reach the dock.
//
// NEGATIVE CONTROL (e2e/tui/NEGATIVE_CONTROLS.md, "Return notes").
func TestReturnNotesComeBackWithTheSession(t *testing.T) {
	const (
		note     = "next: run the migration"
		paneNote = "waiting for CI"
		notice   = "Note: " + note
	)
	base := t.TempDir()
	killDaemon(t, base)
	useShippedLooks(base)

	if out, err := tuiosCLI(t, base, "new", "e2e-note", "--detach"); err != nil {
		t.Fatalf("create e2e-note: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "worker", "-s", "e2e-note", "--", "/bin/sh", "-c", "sleep 900"); err != nil {
		t.Fatalf("open worker: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "working", "-s", "e2e-note", "-w", "worker",
		"--harness", "claude-code", "-m", "Edit main.go"); err != nil {
		t.Fatalf("report worker: %v: %s", err, out)
	}

	// The CLI cleans a note to one line and caps it.
	if out, err := tuiosCLI(t, base, "note", "set", "-s", "e2e-note", "one\nline\ttwo"); err != nil {
		t.Fatalf("note set: %v: %s", err, out)
	}
	if got := noteGet(t, base); got != "one line two" {
		t.Fatalf("ASSERTION: a note with a newline and a tab reads back %q, want %q", got, "one line two")
	}
	if _, err := tuiosCLI(t, base, "note", "set", "-s", "e2e-note", strings.Repeat("x", 300)); err != nil {
		t.Fatalf("note set long: %v", err)
	}
	if got := noteGet(t, base); len(got) != 200 {
		t.Fatalf("ASSERTION: a 300-byte note reads back %d bytes, want 200", len(got))
	}

	out, err := tuiosCLI(t, base, "note", "set", "-s", "e2e-note", "next:", "run", "the", "migration")
	if err != nil {
		t.Fatalf("note set: %v: %s", err, out)
	}
	if !strings.Contains(out, "Set the note of session e2e-note: "+note) {
		t.Fatalf("ASSERTION: note set said %q", out)
	}
	if out, err := tuiosCLI(t, base, "note", "set", "-s", "e2e-note", "-w", "worker", paneNote); err != nil {
		t.Fatalf("note set -w: %v: %s", err, out)
	}
	if got := noteGet(t, base); got != note {
		t.Fatalf("ASSERTION: note get printed %q, want %q", got, note)
	}
	if got := noteGet(t, base, "-w", "worker"); got != paneNote {
		t.Fatalf("ASSERTION: note get -w worker printed %q, want %q", got, paneNote)
	}
	lsOut, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v: %s", err, lsOut)
	}
	var rows []struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(lsOut), &rows); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, lsOut)
	}
	listed := false
	for _, r := range rows {
		listed = listed || (r.Name == "e2e-note" && r.Note == note)
	}
	if !listed {
		t.Fatalf("ASSERTION: ls --json does not carry the note:\n%s", lsOut)
	}

	// Attach: the dock says the note.
	term := startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", "e2e-note"}})
	if err := term.WaitForText(notice, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock did not show %q on attach: %v\n%s", notice, err, term.Snapshot())
	}
	notePNG(t, term, "attach-notice")

	// The rail, read once the notice is gone.
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), notice) }, 20*time.Second); err != nil {
		t.Fatalf("the notice never left the dock: %v\n%s", err, term.Snapshot())
	}
	windowManagementMode(t, term)
	if noteLine(term.Screen(), "e2e-note") == "" {
		toggleSidebarViaPalette(t, term)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return noteLine(s, "e2e-note", "next:") != "" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail row of e2e-note does not show its note: %v\n%s", err, term.Snapshot())
	}
	notePNG(t, term, "rail")

	// The session switcher.
	sendKeys(t, term, tuitest.Ctrl('b'), "S")
	if err := term.WaitFor(func(s tuitest.Screen) bool { return noteLine(s, "e2e-note", note) != "" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the switcher row of e2e-note does not show its note: %v\n%s", err, term.Snapshot())
	}
	notePNG(t, term, "switcher")
	sendKeys(t, term, tuitest.Esc)

	// The Inbox focus shows the pane's note, not the session's.
	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	if err := term.WaitForText(paneNote, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox focus does not show the pane's note: %v\n%s", err, term.Snapshot())
	}
	notePNG(t, term, "inbox")
	sendKeys(t, term, tuitest.Esc)

	// A restart: kill-server saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	waitExit(t, term, "after kill-server")
	if out, err := tuiosCLI(t, base, "new", "e2e-trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}
	waitForSessionInfo(t, base, "e2e-note")
	if got := noteGet(t, base); got != note {
		t.Fatalf("ASSERTION: after a daemon restart note get printed %q, want %q", got, note)
	}
	if got := noteGet(t, base, "-w", "worker"); got != paneNote {
		t.Fatalf("ASSERTION: after a daemon restart note get -w worker printed %q, want %q", got, paneNote)
	}

	term = startIn(t, base, startOpts{cols: 120, rows: 36, args: []string{"attach", "e2e-note"}})
	if err := term.WaitForText(notice, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: after a restart the dock did not show %q on attach: %v\n%s", notice, err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), notice) }, 20*time.Second); err != nil {
		t.Fatalf("the notice never left the dock: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return noteLine(s, "e2e-note", "next:") != "" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: after a restart the rail row does not show the note: %v\n%s", err, term.Snapshot())
	}

	// A clear takes the note off the rail.
	if out, err := tuiosCLI(t, base, "note", "clear", "-s", "e2e-note"); err != nil || !strings.Contains(out, "Removed the note of session e2e-note") {
		t.Fatalf("note clear: %v: %s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return noteLine(s, "e2e-note") != "" && noteLine(s, "e2e-note", "next:") == ""
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail row kept the note after a clear: %v\n%s", err, term.Snapshot())
	}
	if got := noteGet(t, base); got != "" {
		t.Fatalf("ASSERTION: after a clear note get printed %q, want nothing", got)
	}
	notePNG(t, term, "cleared")

	// A note set while attached shows on the rail, and not in the dock.
	if out, err := tuiosCLI(t, base, "note", "set", "-s", "e2e-note", "next: ship it"); err != nil {
		t.Fatalf("note set: %v: %s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return noteLine(s, "e2e-note", "next: s") != "" }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail did not show a note set while attached: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	if strings.Contains(term.Screen().Text(), "Note: next: ship it") {
		t.Fatalf("ASSERTION: a note set while attached was announced in the dock\n%s", term.Snapshot())
	}
	alive(t, term, "after the notes")
}
