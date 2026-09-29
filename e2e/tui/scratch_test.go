package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// The scratch popup (toggle_scratch, leader g), driven through a real PTY.
//
// How these could pass wrongly, written down first:
//   - The text could be on screen because the popup never closed. Each hide
//     waits for the marker to leave the screen and for the outer session to
//     hold one window again, before the next show.
//   - The text could come back because the show ran the command again. The
//     marker is computed by the shell (6*7) once, and the second show types
//     nothing.
//   - The toggle could hide the popup because the inner client took the key
//     and quit. The scratch session must still be listed after every hide.
//   - The session could be recreated on each show. The test reads its id on
//     the first show and requires the same id after the second.

// scratchSession is one row of `tuios ls --json`.
type scratchSession struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// listedSessions returns the daemon's sessions by name.
func listedSessions(t *testing.T, base string) map[string]scratchSession {
	t.Helper()
	out, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		return nil
	}
	var rows []scratchSession
	if json.Unmarshal([]byte(out), &rows) != nil {
		return nil
	}
	byName := map[string]scratchSession{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	return byName
}

// waitSession waits until the daemon lists name and returns its row.
func waitSession(t *testing.T, base, name string) scratchSession {
	t.Helper()
	deadline := time.Now().Add(bootTimeout)
	for time.Now().Before(deadline) {
		if s, ok := listedSessions(t, base)[name]; ok {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the daemon never listed session %q", name)
	return scratchSession{}
}

// sessionWindows counts the windows of one session, popups included.
func sessionWindows(t *testing.T, base, session string) int {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-windows", "--json", "--session", session)
	if err != nil {
		return -1
	}
	rects, ok := parseWindows(out)
	if !ok {
		return -1
	}
	return len(rects)
}

// waitSessionWindows waits until session holds n windows.
func waitSessionWindows(t *testing.T, term *tuitest.Terminal, base, session string, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		if sessionWindows(t, base, session) == n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: session %s never held %d windows (last %d)\n%s",
		what, session, n, sessionWindows(t, base, session), term.Snapshot())
}

// toggleScratch presses the leader and g.
func toggleScratch(t *testing.T, term *tuitest.Terminal) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('b'), "g"); err != nil {
		t.Fatalf("send leader g: %v", err)
	}
}

// TestScratchPopupKeepsItsSession opens the scratch popup, types into the
// session it shows, hides it from inside the popup, and shows it again. The
// text the shell printed is still there, and the session is the same one.
func TestScratchPopupKeepsItsSession(t *testing.T) {
	base := t.TempDir()
	// The scratch session is made by the popup's `tuios attach -c`, which
	// reads this file too: it opens with a shell and in terminal mode, so the
	// test can type into it.
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\nstart_in_terminal_mode = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitWindowCount(t, term, 1, "after starting session work")
	waitSessionWindows(t, term, base, "work", 1, "before the popup")

	// Show. The popup takes the keyboard in terminal mode, so the keys typed
	// next reach the inner client, and from it the scratch shell.
	toggleScratch(t, term)
	first := waitSession(t, base, "scratch")
	waitSessionWindows(t, term, base, "work", 2, "with the popup open")
	waitSessionWindows(t, term, base, "scratch", 1, "the scratch shell")
	// The inner client draws its own dock inside the popup. Its session name
	// on screen says it attached.
	if err := term.WaitForText("scratch", uiTimeout); err != nil {
		t.Fatalf("the popup never showed the scratch session: %v\n%s", err, term.Snapshot())
	}
	// The inner client enters terminal mode once its window arrives. Retry
	// the command until the shell computes the marker, as a user retypes.
	deadline := time.Now().Add(bootTimeout)
	for {
		if err := term.SendKeys("echo SCRATCH-$((6*7))", tuitest.Enter); err != nil {
			t.Fatalf("type into the popup: %v", err)
		}
		if err := term.WaitForText("SCRATCH-42", 2*time.Second); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the scratch shell never printed the marker\n%s", term.Snapshot())
		}
	}
	t.Logf("the scratch popup with text typed into it:\n%s", term.Snapshot())

	// Hide, from inside the popup: the keyboard is still in it. The outer
	// client takes the leader before the popup's pane sees it.
	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 1, "after hiding the popup")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "SCRATCH-42")
	}, uiTimeout); err != nil {
		t.Fatalf("the popup stayed on screen after the toggle: %v\n%s", err, term.Snapshot())
	}
	if _, ok := listedSessions(t, base)["scratch"]; !ok {
		t.Fatalf("hiding the popup ended the scratch session\n%s", term.Snapshot())
	}
	t.Logf("the layout with the popup hidden:\n%s", term.Snapshot())

	// Show again. Nothing is typed: the marker on screen is what the session
	// kept.
	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 2, "with the popup shown again")
	if err := term.WaitForText("SCRATCH-42", bootTimeout); err != nil {
		t.Fatalf("the scratch session lost its text across a hide: %v\n%s", err, term.Snapshot())
	}
	if again := waitSession(t, base, "scratch"); again.ID != first.ID {
		t.Fatalf("the show made a new scratch session: id %s, then %s", first.ID, again.ID)
	}
	t.Logf("the scratch popup shown again:\n%s", term.Snapshot())

	// And hide once more, to leave the layout as it was.
	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 1, "after the second hide")
	alive(t, term, "after showing and hiding the scratch popup")
}

// TestScratchPopupRefusesInsideTheScratchSession presses the key in a client
// of the scratch session itself. It would show the session inside itself, so
// the dock says so and nothing opens.
func TestScratchPopupRefusesInsideTheScratchSession(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[startup]\nopen_default_window = true\ntiled = true\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40, args: []string{"new", "scratch"}})
	waitWindowCount(t, term, 1, "after starting session scratch")

	toggleScratch(t, term)
	if err := term.WaitForText("This is the scratch session", uiTimeout); err != nil {
		t.Fatalf("no refusal in the scratch session: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the refusal:\n%s", term.Snapshot())
	time.Sleep(500 * time.Millisecond)
	if n := sessionWindows(t, base, "scratch"); n != 1 {
		t.Fatalf("the refused toggle left %d windows, want 1", n)
	}
	alive(t, term, "after a refused toggle")
}
