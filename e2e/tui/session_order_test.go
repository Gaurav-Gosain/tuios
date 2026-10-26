package tuie2e

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// railOrder reports whether the rail on s lists the names top to bottom in
// this order. The rail sits at the top of the screen, so a name's topmost row
// is its rail row.
func railOrder(s tuitest.Screen, names ...string) bool {
	prev := -1
	for _, n := range names {
		r := screenRowOf(s, n)
		if r < 0 || r <= prev {
			return false
		}
		prev = r
	}
	return true
}

// lsOrder is the session names tuios ls lists, in its order.
func lsOrder(t *testing.T, base string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	var sessions []sessionInfo
	if err := json.Unmarshal([]byte(out), &sessions); err != nil {
		t.Fatalf("ls --json: %v\n%s", err, out)
	}
	var names []string
	for _, s := range sessions {
		names = append(names, s.Name)
	}
	return names
}

// TestSessionOrderIsTheDaemons: a session moved on one client's rail moves on
// every client's rail, at once, and in tuios ls. The order is the daemon's,
// not each client's.
//
//  1. Sessions alpha, bravo and charlie, made in that order. Client A attaches
//     to alpha and client B to bravo, each with the rail open. Both list
//     alpha, bravo, charlie.
//  2. On A, ctrl+b e puts the keyboard on the rail, on alpha's row, and
//     shift+down twice moves alpha to the bottom. A lists bravo, charlie,
//     alpha, and so must B, which nobody touched, within two seconds: the
//     daemon pushes the change, it does not wait for the listing's next look.
//     tuios ls lists the same.
//  3. tuios set-session-order charlie alpha, from a third connection, moves
//     both rails to charlie, alpha, bravo.
//
// The frames "client-a" and "client-b" show both rails after step 2, under
// TUIOS_E2E_FRAMES.
//
// Negative control: with Manager.ListSessions ignoring the ranks, the daemon
// takes the order and lists the sessions in creation order, so A drops its
// own copy and alpha goes back to the top of A's rail. With commitSessionOrder
// not sending the order (no queueSidebarCmd), A shows the move and B does
// not. A build of origin/main fails the same way. The runs are in
// NEGATIVE_CONTROLS.md.
func TestSessionOrderIsTheDaemons(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
		// Creation order is the order before any move, and it is kept to the
		// second in places: keep the three apart.
		time.Sleep(50 * time.Millisecond)
	}
	open := func(sess string) *tuitest.Terminal {
		t.Helper()
		term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", sess}})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
			t.Fatalf("client never attached to %s: %v\n%s", sess, err, term.Snapshot())
		}
		clientShows(t, base, sess)
		toggleSidebarViaPalette(t, term)
		waitForAll(t, term, uiTimeout, "the rail with every session", sidebarHeader, "alpha", "bravo", "charlie")
		return term
	}
	a := open("alpha")
	b := open("bravo")
	for name, term := range map[string]*tuitest.Terminal{"A": a, "B": b} {
		if err := term.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, "alpha", "bravo", "charlie") }, uiTimeout); err != nil {
			t.Fatalf("setup: client %s does not list alpha, bravo, charlie: %v\n%s", name, err, term.Snapshot())
		}
	}

	// 2. Move alpha to the bottom on A.
	if err := a.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatalf("ctrl+b e: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	for range 2 {
		if err := a.SendKeys("\x1b[1;2B"); err != nil {
			t.Fatalf("shift+down: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := a.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, "bravo", "charlie", "alpha") }, uiTimeout); err != nil {
		t.Fatalf("setup: shift+down twice on A did not move alpha to the bottom of A's rail: %v\n%s", err, a.Snapshot())
	}
	moved := time.Now()
	if err := b.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, "bravo", "charlie", "alpha") }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: client B does not list the order A set: %v\n%s", err, b.Snapshot())
	}
	if took := time.Since(moved); took > 2*time.Second {
		t.Fatalf("ASSERTION: client B took %v to list the order A set, want under 2s", took)
	}
	if got := strings.Join(lsOrder(t, base), ","); got != "bravo,charlie,alpha" {
		t.Fatalf("ASSERTION: tuios ls lists %s, want bravo,charlie,alpha", got)
	}
	railShot(t, a, "client-a")
	railShot(t, b, "client-b")

	// 3. From the command line.
	if out, err := tuiosCLI(t, base, "set-session-order", "charlie", "alpha"); err != nil || !strings.Contains(out, "charlie, alpha, bravo") {
		t.Fatalf("ASSERTION: set-session-order charlie alpha: %v\n%s", err, out)
	}
	for name, term := range map[string]*tuitest.Terminal{"A": a, "B": b} {
		if err := term.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, "charlie", "alpha", "bravo") }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: client %s does not list charlie, alpha, bravo after set-session-order: %v\n%s", name, err, term.Snapshot())
		}
	}
}

// wantLsOrder waits until tuios ls lists want first, in that order.
func wantLsOrder(t *testing.T, base, what string, want ...string) {
	t.Helper()
	var got []string
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		got = lsOrder(t, base)
		if len(got) >= len(want) && slices.Equal(got[:len(want)], want) {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: %s: tuios ls lists %v, want %v first", what, got, want)
}

// TestSessionOrderAnOlderClientDoesNotUndoIt: a client from before the
// daemon's order keeps its own, and neither it nor what it leaves behind moves
// the daemon's. TUIOS_SESSION_ORDER_LEGACY=1 makes a client behave as that
// build; TUIOS_E2E_OLD_CLIENT names a real one to use instead.
//
//  1. tuios set-session-order bravo charlie alpha.
//  2. The older client, on bravo, moves bravo to the bottom of its own rail,
//     which it keeps in its sidebar state file, and opens a pane in bravo,
//     whose state it then pushes with no rank in it. tuios ls must still list
//     bravo, charlie, alpha.
//  3. A new client, started after that, reads the same state file. Its rail
//     must list bravo, charlie, alpha, the daemon's order, not the file's.
//
// Negative control: with retainDaemonExclusive not keeping the rank, the
// older client's push takes bravo's rank away and tuios ls lists bravo last.
// With sidebarSessionOrderFor applying the kept order over the daemon's, the
// new client lists bravo last. The runs are in NEGATIVE_CONTROLS.md.
func TestSessionOrderAnOlderClientDoesNotUndoIt(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "set-session-order", "bravo", "charlie", "alpha"); err != nil {
		t.Fatalf("set-session-order: %v\n%s", err, out)
	}
	wantLsOrder(t, base, "after set-session-order", "bravo", "charlie", "alpha")

	open := func(sess string, env []string) *tuitest.Terminal {
		t.Helper()
		var term *tuitest.Terminal
		withBinary("TUIOS_E2E_OLD_CLIENT", func() {
			term = startIn(t, base, startOpts{cols: 140, rows: 40, env: env, args: []string{"attach", sess}})
		})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
			t.Fatalf("client never attached to %s: %v\n%s", sess, err, term.Snapshot())
		}
		toggleSidebarViaPalette(t, term)
		waitForAll(t, term, uiTimeout, "the rail with every session", sidebarHeader, "alpha", "bravo", "charlie")
		return term
	}

	// 2. The older client.
	old := open("bravo", []string{"TUIOS_SESSION_ORDER_LEGACY=1"})
	if err := old.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatalf("ctrl+b e: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	for range 2 {
		if err := old.SendKeys("\x1b[1;2B"); err != nil {
			t.Fatalf("shift+down: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := old.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, "charlie", "alpha", "bravo") }, uiTimeout); err != nil {
		t.Fatalf("setup: the older client did not move bravo to the bottom of its rail: %v\n%s", err, old.Snapshot())
	}
	// esc leaves the rail. A pause after it, or the terminal reads esc and
	// the leader as one alt+ctrl+b.
	if err := old.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("esc: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := old.SendKeys(tuitest.Ctrl('b'), "c"); err != nil {
		t.Fatalf("ctrl+b c: %v", err)
	}
	waitForSettledGeometryIn(t, base, "bravo", 2)
	time.Sleep(time.Second)
	wantLsOrder(t, base, "after the older client moved bravo and pushed bravo's state", "bravo", "charlie", "alpha")

	// 3. A new client reads the state file the older one wrote.
	fresh := open("alpha", nil)
	time.Sleep(time.Second)
	if !railOrder(fresh.Screen(), "bravo", "charlie", "alpha") {
		t.Fatalf("ASSERTION: a new client lists another order than the daemon's bravo, charlie, alpha\n%s", fresh.Snapshot())
	}
}

// TestSessionOrderSurvivesTheDaemon: the order is saved with the sessions,
// and a restore puts it back. charlie, alpha, bravo is neither the creation
// order nor the name order, so only the saved ranks can bring it back.
//
// Negative control: with the restore not passing the saved rank to the new
// session (Rank left out of the SessionConfig in daemon_resurrect.go), the
// restored sessions list in creation order. The run is in
// NEGATIVE_CONTROLS.md.
func TestSessionOrderSurvivesTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "set-session-order", "charlie", "alpha", "bravo"); err != nil {
		t.Fatalf("set-session-order: %v\n%s", err, out)
	}
	wantLsOrder(t, base, "before a restart", "charlie", "alpha", "bravo")

	// kill-server is synchronous and saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	// Starting any session starts a daemon, and a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v: %s", err, out)
	}
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if s := waitForSessionInfo(t, base, name); !s.Restored {
			t.Fatalf("%s is listed but not as restored, so the restart did not happen", name)
		}
	}
	wantLsOrder(t, base, "after a restart", "charlie", "alpha", "bravo")
}
