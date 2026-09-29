package tuie2e

import (
	"encoding/json"
	"regexp"
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

// startScratchOuter starts a client on session work with one pane, on the
// shipped [startup] settings: no default window, window mode. The popup must
// not need either to be usable.
func startScratchOuter(t *testing.T) (*tuitest.Terminal, string) {
	t.Helper()
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "work"}})
	waitBoot(t, term)
	newWindow(t, term)
	waitSessionWindows(t, term, base, "work", 1, "before the popup")
	return term, base
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

// scratchCapture is the scratch session's focused pane, as text.
func scratchCapture(t *testing.T, base string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "capture-pane", "-s", "scratch", "-S")
	if err != nil {
		t.Fatalf("capture the scratch pane: %v\n%s", err, out)
	}
	return out
}

// sttySize finds the last "SZ<tag>=rows cols" line a shell printed.
func sttySize(capture, tag string) string {
	re := regexp.MustCompile(`SZ` + tag + `=(\d+ \d+)`)
	m := re.FindAllStringSubmatch(capture, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// TestScratchPopupKeepsItsSession opens the scratch popup, types into the
// session it shows, hides it from inside the popup, and shows it again. The
// text the shell printed is still there, and the session is the same one.
//
// It runs on the shipped [startup] settings, so the popup itself has to make
// a session with a pane and put the keyboard in it. The first command runs
// at once, and the size it sees is the size the pane keeps. Nothing but the
// typed commands reaches the shell.
func TestScratchPopupKeepsItsSession(t *testing.T) {
	term, base := startScratchOuter(t)

	// Show. The popup takes the keyboard in terminal mode, and so does the
	// client inside it, so the keys typed next reach the scratch shell.
	toggleScratch(t, term)
	first := waitSession(t, base, "scratch")
	waitSessionWindows(t, term, base, "work", 2, "with the popup open")
	tries := typeUntil(t, term, "echo SZ1=$(stty size) SCRATCH-$((6*7))", "SCRATCH-42")
	t.Logf("the scratch popup with text typed into it (%d tries):\n%s", tries, term.Snapshot())

	// The size the first command saw is the size the pane settles at.
	time.Sleep(time.Second)
	typeUntil(t, term, "echo SZ2=$(stty size) DONE-$((2*3))", "DONE-6")
	capture := scratchCapture(t, base)
	sz1, sz2 := sttySize(capture, "1"), sttySize(capture, "2")
	if sz1 == "" || sz1 != sz2 {
		t.Errorf("the first command saw %q and the settled pane is %q\n%s", sz1, sz2, capture)
	}
	// Late replies to the inner client's own terminal probe must not reach
	// the shell as keys.
	for _, junk := range []string{"support animation", "ENOTSUPPORTED", "Gi=", "rgb:"} {
		if strings.Contains(capture, junk) {
			t.Errorf("the scratch shell received %q as input:\n%s", junk, capture)
		}
	}

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

	// Show again. The marker on screen is what the session kept, and the
	// keyboard is in the shell again, on a session that is not new.
	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 2, "with the popup shown again")
	if err := term.WaitForText("SCRATCH-42", bootTimeout); err != nil {
		t.Fatalf("the scratch session lost its text across a hide: %v\n%s", err, term.Snapshot())
	}
	if again := waitSession(t, base, "scratch"); again.ID != first.ID {
		t.Fatalf("the show made a new scratch session: id %s, then %s", first.ID, again.ID)
	}
	typeUntil(t, term, "echo AGAIN-$((7*8))", "AGAIN-56")
	t.Logf("the scratch popup shown again and typed into:\n%s", term.Snapshot())

	// And hide once more, to leave the layout as it was.
	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 1, "after the second hide")
	alive(t, term, "after showing and hiding the scratch popup")
}

// TestScratchPopupRefusesMutualNesting covers #238 around the popup, both
// ways round.
//
// Inside the popup, the scratch shell attaches work, the session the popup
// is shown in. That shows work inside itself, so the attach is refused there
// and says why.
//
// Then the loop is built the other way: a client of work runs in the scratch
// session while the popup is hidden. The next show would put scratch inside
// work inside scratch, so the popup's own attach is refused. --hold keeps
// that message on the screen until enter.
func TestScratchPopupRefusesMutualNesting(t *testing.T) {
	term, base := startScratchOuter(t)

	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 2, "with the popup open")
	typeUntil(t, term, "echo READY-$((3*3))", "READY-9")
	// The binary under test by its path: a bare tuios is whatever the
	// machine has installed.
	if err := term.SendKeys(tuiosBin+" attach work", tuitest.Enter); err != nil {
		t.Fatalf("type the attach: %v", err)
	}
	if err := term.WaitForText("would show tuios inside itself", uiTimeout); err != nil {
		t.Fatalf("no refusal for work inside the scratch popup: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the refusal inside the popup:\n%s", term.Snapshot())
	// The refused client probed its terminal before the daemon refused it.
	// Every answer to that probe came before it gave up, so none is left
	// for the shell to read as a command.
	if err := term.SendKeys("echo AFTER-$((4*4))", tuitest.Enter); err != nil {
		t.Fatalf("type after the refusal: %v", err)
	}
	if err := term.WaitForText("AFTER-16", uiTimeout); err != nil {
		t.Fatalf("the shell did not run a command after the refusal: %v\n%s", err, term.Snapshot())
	}
	c := scratchCapture(t, base)
	after := c[strings.LastIndex(c, "attach work"):]
	if strings.Contains(after, "Gi=") || strings.Contains(after, "not found") {
		t.Errorf("the refused client left terminal replies for the shell:\n%s", c)
	}
	alive(t, term, "after the refused attach inside the popup")

	// Hide, then make the scratch session show work.
	toggleScratch(t, term)
	waitSessionWindows(t, term, base, "work", 1, "after hiding the popup")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "scratch", tuiosBin+" attach work\r"); err != nil {
		t.Fatalf("attach work from the scratch shell: %v\n%s", err, out)
	}
	deadline := time.Now().Add(bootTimeout)
	for !scratchShowsAClient(t, base) {
		if time.Now().After(deadline) {
			t.Fatalf("the scratch shell never attached work\n%s", scratchCapture(t, base))
		}
		time.Sleep(200 * time.Millisecond)
	}

	// The show now refuses in the popup, and the popup waits for enter.
	toggleScratch(t, term)
	if err := term.WaitForText("Press enter to close.", bootTimeout); err != nil {
		t.Fatalf("the refused attach was not held in the popup: %v\n%s", err, term.Snapshot())
	}
	if text := term.Screen().Text(); !strings.Contains(text, "inside") {
		t.Errorf("the held message does not say why:\n%s", term.Snapshot())
	} else if strings.Contains(text, "Gi=") {
		t.Errorf("terminal replies reached the held popup after its probe:\n%s", term.Snapshot())
	}
	time.Sleep(time.Second)
	if n := sessionWindows(t, base, "work"); n != 2 {
		t.Fatalf("the held popup closed by itself: work holds %d windows\n%s", n, term.Snapshot())
	}
	t.Logf("the held refusal:\n%s", term.Snapshot())
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("send enter: %v", err)
	}
	waitSessionWindows(t, term, base, "work", 1, "after enter closed the held popup")
	alive(t, term, "after the held refusal")
}

// scratchShowsAClient reports whether the scratch pane draws a tuios client
// instead of its shell: the command line is gone and a dock is there.
func scratchShowsAClient(t *testing.T, base string) bool {
	t.Helper()
	out, err := tuiosCLI(t, base, "capture-pane", "-s", "scratch")
	if err != nil {
		return false
	}
	return !strings.Contains(out, " attach work") && dockStatus.MatchString(out)
}

// TestScratchPopupRefusesInsideTheScratchSession presses the key in a client
// of the scratch session itself. It would show the session inside itself, so
// the dock says so and nothing opens.
func TestScratchPopupRefusesInsideTheScratchSession(t *testing.T) {
	term, base := start(t, startOpts{cols: 120, rows: 40, args: []string{"new", "scratch"}})
	waitBoot(t, term)
	newWindow(t, term)

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
