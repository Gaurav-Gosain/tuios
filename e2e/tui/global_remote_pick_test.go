package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Picks made while the client is attached to a session on another machine.
//
// The picker lists machines as this machine's daemon knows them: "this
// machine" is the one the user sits at, and alpha and beta are hosts in this
// machine's [hosts] table. The calls behind a pick used to go to whichever
// daemon the client was attached to. From a session on alpha, "this machine"
// made a session or a pane on alpha, and a pane asked of a host went to this
// machine's daemon, into a session of the same name, where the user could not
// see it.

// daemonSessionNames is the sessions the daemon at base holds.
func daemonSessionNames(t *testing.T, base string, env []string) []string {
	t.Helper()
	out, err := cliWithin(t, base, env, "ls", "--json")
	if err != nil {
		return nil
	}
	var v []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(out), &v) != nil {
		t.Fatalf("ls --json is not a list of sessions:\n%s", out)
	}
	names := make([]string, 0, len(v))
	for _, s := range v {
		names = append(names, s.Name)
	}
	return names
}

// newSessionName waits for a session on the daemon at base that is not in
// before.
func newSessionName(t *testing.T, base string, env []string, before []string, within time.Duration) (string, bool) {
	t.Helper()
	old := map[string]bool{}
	for _, n := range before {
		old[n] = true
	}
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, n := range daemonSessionNames(t, base, env) {
			if !old[n] {
				return n, true
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return "", false
}

// attachOnAlpha starts a client attached to alpha's session sess.
func attachOnAlpha(t *testing.T, f *globalFleet, sess string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, f.here, startOpts{args: []string{"attach", "--host", "alpha", sess}, env: f.env})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──") || strings.Contains(s.Text(), welcomeText)
	}, bootTimeout); err != nil {
		t.Fatalf("the client never drew alpha's session: %v\n%s", err, term.Snapshot())
	}
	return term
}

// openNewSessionPicker opens the "New session on" picker, by the palette or by
// the dock's "+".
func openNewSessionPicker(t *testing.T, term *tuitest.Terminal, byDock bool) {
	t.Helper()
	if !byDock {
		openPaletteRow(t, term, "new session", "New session")
		return
	}
	s := term.Screen()
	_, rows := s.Size()
	line := s.Line(rows - 1)
	col := -1
	for i, r := range []rune(line) {
		if r == '+' {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("the dock has no + control:\n%s", term.Snapshot())
	}
	clickAt(t, term, col, rows-1, 1)
}

// TestTheSessionPickerMakesTheSessionWhereItWasAskedFor starts on alpha each
// time and makes a session on this machine, and a global session, by the
// palette and the dock, by enter and by click. Each must land on this
// machine's daemon and nowhere else.
func TestTheSessionPickerMakesTheSessionWhereItWasAskedFor(t *testing.T) {
	f := startGlobalFleet(t)
	rows := []string{"this machine", "global"}
	loops := pickLoops(16)
	var wrong []string
	for i := range loops {
		row := i % len(rows)
		click := (i/len(rows))%2 == 1
		byDock := (i/(2*len(rows)))%2 == 1
		path := "palette New session"
		if byDock {
			path = "dock +"
		}
		how := pickHow(i, path, rows[row], click)
		t.Logf("%s", how)

		term := attachOnAlpha(t, f, "far")
		hereBefore := daemonSessionNames(t, f.here, f.env)
		alphaBefore := daemonSessionNames(t, f.alpha, f.env)
		openNewSessionPicker(t, term, byDock)
		// The rows are this machine, global, then the hosts.
		choosePickerRow(t, term, pickerSessionTitle, rows[row], row, click)
		waitPickerClosed(t, term, pickerSessionTitle)

		name, madeHere := newSessionName(t, f.here, f.env, hereBefore, 10*time.Second)
		if onAlpha, ok := newSessionName(t, f.alpha, f.env, alphaBefore, time.Second); ok {
			wrong = append(wrong, fmt.Sprintf("%s: the session %q was made on alpha", how, onAlpha))
		} else if !madeHere {
			wrong = append(wrong, how+": no session was made on this machine")
		} else if rows[row] == "global" && !strings.HasPrefix(name, "global") {
			wrong = append(wrong, fmt.Sprintf("%s: the session made here is %q, not a global one", how, name))
		} else if rows[row] == "global" {
			// A new global session asks for its first pane at once.
			if err := term.WaitFor(func(s tuitest.Screen) bool {
				return pickerOpen(s, pickerWindowTitle)
			}, uiTimeout); err != nil {
				wrong = append(wrong, how+": the new global session did not ask for its first pane")
			}
		}
		_ = term.Close()
	}
	if len(wrong) > 0 {
		t.Fatalf("ASSERTION: %d of %d picks made the session on the wrong machine:\n%s",
			len(wrong), loops, strings.Join(wrong, "\n"))
	}
	t.Logf("%d of %d picks made the session on this machine", loops, loops)
}

// TestAPaneInAGlobalSessionOnAnotherMachineRunsOnThePickedMachine is the
// window picker in a global session that alpha holds. alpha's row is the
// session's own machine, and beta's is a machine alpha reaches over its own
// link. This machine is one alpha has no link to, so that row must say so.
func TestAPaneInAGlobalSessionOnAnotherMachineRunsOnThePickedMachine(t *testing.T) {
	f := startGlobalFleet(t)
	if out, err := tuiosCLIEnv(t, f.alpha, f.env, "new", "global", "--global"); err != nil {
		t.Fatalf("create alpha's global session: %v\n%s", err, out)
	}
	waitForHostListing(t, f.alpha, func(s string) bool { return hostLineUp(s, "beta") },
		"alpha never reported beta up")
	term := attachOnAlpha(t, f, "global")

	rows := []string{"this machine", "alpha", "beta"}
	loops := pickLoops(24)
	var wrong []string
	for i := range loops {
		path := newWindowPaths[i%len(newWindowPaths)]
		// this machine is picked less often: it makes nothing, so it tells
		// less on each pick.
		row := 1 + (i/len(newWindowPaths))%2
		if i%4 == 3 {
			row = 0
		}
		click := (i/(2*len(newWindowPaths)))%2 == 1
		how := pickHow(i, path.name, rows[row], click)
		t.Logf("%s", how)

		alphaBefore := sessionWindowHosts(t, f.alpha, f.env, "global")
		hereBefore := daemonSessionNames(t, f.here, f.env)
		path.open(t, term)
		choosePickerRow(t, term, pickerWindowTitle, rows[row], row, click)
		waitPickerClosed(t, term, pickerWindowTitle)

		if row == 0 {
			if err := term.WaitForText("alpha cannot open a pane on this machine", uiTimeout); err != nil {
				wrong = append(wrong, how+": no notice said this machine cannot be reached from alpha")
			}
			if id, _, ok := waitNewWindow(t, f.alpha, f.env, "global", alphaBefore, 2*time.Second); ok {
				wrong = append(wrong, fmt.Sprintf("%s: a pane was made on %s", how, f.paneMachine(t, f.alpha, "global", id)))
			}
			if n, ok := newSessionName(t, f.here, f.env, hereBefore, 100*time.Millisecond); ok {
				wrong = append(wrong, fmt.Sprintf("%s: a session %q was made on this machine", how, n))
			}
			continue
		}
		id, host, ok := waitNewWindow(t, f.alpha, f.env, "global", alphaBefore, 20*time.Second)
		if !ok {
			t.Logf("%s: no pane:\n%s", how, term.Snapshot())
			wrong = append(wrong, how+": no pane appeared in alpha's global session")
			continue
		}
		if got := f.paneMachine(t, f.alpha, "global", id); got != rows[row] {
			wrong = append(wrong, fmt.Sprintf("%s: the pane runs on %s (recorded host %q)", how, got, host))
		}
	}
	if len(wrong) > 0 {
		t.Fatalf("ASSERTION: %d of %d picks went wrong:\n%s\n%s",
			len(wrong), loops, strings.Join(wrong, "\n"), term.Snapshot())
	}
	t.Logf("%d of %d picks landed on the picked machine", loops, loops)
}

// TestAPickOfAHostWhoseLinkIsDownSaysSo opens the picker while beta is up,
// takes beta off the network, and then picks beta. The list is a snapshot, so
// the row still looks up. The pick must say beta is unavailable, in words,
// and make no pane. The second pick opens the picker with beta already down
// and clicks the row. Last, beta comes back and must be pickable at once:
// this client used to stop hearing host changes after the first one, and read
// link states up to a minute old.
func TestAPickOfAHostWhoseLinkIsDownSaysSo(t *testing.T) {
	f := startGlobalFleet(t)
	if out, err := tuiosCLIEnv(t, f.here, f.env, "new", "global", "--global"); err != nil {
		t.Fatalf("create the global session: %v\n%s", err, out)
	}
	term := startIn(t, f.here, startOpts{args: []string{"attach", "global"}, env: f.env})
	if err := term.WaitForText(welcomeText, bootTimeout); err != nil {
		t.Fatalf("the client never attached: %v\n%s", err, term.Snapshot())
	}
	for i, click := range []bool{false, true} {
		if i == 1 {
			// alpha's link was broken too, and it redials. The picker opens
			// only while a second machine is up, so this pick waits for it.
			waitForHostListing(t, f.here, func(s string) bool { return hostLineUp(s, "alpha") },
				"alpha never came back")
		}
		before := sessionWindowHosts(t, f.here, f.env, "global")
		newWindowPaths[0].open(t, term)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return pickerOpen(s, pickerWindowTitle) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: the picker never opened, so this client did not hear that alpha is up: %v\n%s", err, term.Snapshot())
		}
		if i == 0 {
			takeOffline(t, f.here, "someone@betabox")
			waitForHostListing(t, f.here, func(s string) bool {
				return !hostLineUp(s, "beta") && strings.Contains(s, "beta")
			}, "this machine never saw beta go down")
		}
		choosePickerRow(t, term, pickerWindowTitle, "beta", 2, click)
		if err := term.WaitForText("beta is unavailable", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: a pick of beta with its link down did not say beta is unavailable: %v\n%s", err, term.Snapshot())
		}
		if _, _, ok := waitNewWindow(t, f.here, f.env, "global", before, 2*time.Second); ok {
			t.Fatalf("ASSERTION: a pane was made for a pick of a machine that is down:\n%s", term.Snapshot())
		}
		if pickerOpen(term.Screen(), pickerWindowTitle) {
			_ = term.SendKeys(tuitest.Esc)
		}
	}

	// beta comes back. This client must hear it and let beta be picked
	// again, without waiting for its minute poll.
	if err := os.Remove(filepath.Join(f.here, "down-someone@betabox")); err != nil {
		t.Fatal(err)
	}
	waitForHostListing(t, f.here, func(s string) bool { return hostLineUp(s, "beta") },
		"this machine never saw beta come back")
	// The client hears of it by a push a moment after the daemon does. The
	// picker's list is taken when it opens, so it is opened until beta's row
	// reads up, within the time a push takes and well inside the minute poll.
	deadline := time.Now().Add(uiTimeout)
	for {
		newWindowPaths[0].open(t, term)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return pickerOpen(s, pickerWindowTitle) }, uiTimeout); err != nil {
			t.Fatalf("the picker never opened: %v\n%s", err, term.Snapshot())
		}
		if betaRowUp(term.Screen()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: beta is up again and the picker still lists it as down:\n%s", term.Snapshot())
		}
		_ = term.SendKeys(tuitest.Esc)
		waitPickerClosed(t, term, pickerWindowTitle)
		time.Sleep(500 * time.Millisecond)
	}
	before := sessionWindowHosts(t, f.here, f.env, "global")
	choosePickerRow(t, term, pickerWindowTitle, "beta", 2, false)
	id, _, ok := waitNewWindow(t, f.here, f.env, "global", before, 3*time.Second)
	if !ok {
		// Whatever the pick said is on screen now, before it times out.
		said := term.Snapshot()
		if id, _, ok = waitNewWindow(t, f.here, f.env, "global", before, 17*time.Second); !ok {
			t.Fatalf("ASSERTION: beta is up again and a pick of it made no pane:\n%s", said)
		}
	}
	if got := f.paneMachine(t, f.here, "global", id); got != "beta" {
		t.Fatalf("ASSERTION: the pane runs on %s, not beta", got)
	}
}

// betaRowUp reports whether the picker's beta row shows a session count,
// which is what a row whose link is up shows in place of the link's state.
func betaRowUp(s tuitest.Screen) bool {
	for _, line := range strings.Split(s.Text(), "\n") {
		if strings.Contains(line, " beta ") && strings.Contains(line, "session") {
			return true
		}
	}
	return false
}
