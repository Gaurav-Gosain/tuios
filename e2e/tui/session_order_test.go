package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// TestSessionOrderTakesQuickMoves: moves made before the daemon has answered
// the one before them all stick. The rail draws the order a move made until
// the daemon answers it, and the next move is built on that. The daemon keeps
// an order from the start, since with none the client draws its own anyway.
//
//  1. ctrl+b e, then shift+down three times in one burst, moves alpha from
//     the top to the bottom: bravo, charlie, delta, alpha.
//  2. A mouse drag of delta onto the top row, then at once a second drag of
//     alpha from the bottom row onto the one above it: delta, bravo, alpha,
//     charlie.
//
// Each order must be on the rail and in tuios ls.
//
// Negative control: with sidebarSessionOrderFor not drawing the order of a
// move on its way, only the first of the three presses sticks, and the second
// drag is built on the order before the first. The run is in NEGATIVE_CONTROLS.md.
func TestSessionOrderTakesQuickMoves(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	names := []string{"alpha", "bravo", "charlie", "delta"}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "alpha")
	toggleSidebarViaPalette(t, term)
	waitForAll(t, term, uiTimeout, "the rail with every session", append([]string{sidebarHeader}, names...)...)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, names...) }, uiTimeout); err != nil {
		t.Fatalf("setup: the rail does not list the creation order: %v\n%s", err, term.Snapshot())
	}
	// The bug is in the order the daemon keeps, so the daemon keeps one from
	// the start: the creation order, set.
	if out, err := tuiosCLI(t, base, append([]string{"set-session-order"}, names...)...); err != nil {
		t.Fatalf("set-session-order: %v\n%s", err, out)
	}
	want := func(what string, order ...string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, order...) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the rail does not list %v: %v\n%s", what, order, err, term.Snapshot())
		}
		wantLsOrder(t, base, what, order...)
	}

	// 1. Three presses in one burst.
	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatalf("ctrl+b e: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys("\x1b[1;2B", "\x1b[1;2B", "\x1b[1;2B"); err != nil {
		t.Fatalf("shift+down three times: %v", err)
	}
	want("three quick shift+down", "bravo", "charlie", "delta", "alpha")

	// 2. Two drags, the second before the daemon has answered the first. The
	// rows are read once: the first drag puts delta on the top row, so the
	// second drags alpha, still on the bottom row, onto the row above it.
	s := term.Screen()
	col, _ := findOnScreen(t, term, "delta")
	top, third, bottom := screenRowOf(s, "bravo"), screenRowOf(s, "delta"), screenRowOf(s, "alpha")
	mouseDrag(t, term, col, third, col, top, tuitest.MouseLeft, 0)
	mouseDrag(t, term, col, bottom, col, third, tuitest.MouseLeft, 0)
	want("two drags, the second at once", "delta", "bravo", "alpha", "charlie")
}

// TestSessionOrderRestoredSessionDoesNotTie: a saved session that was not
// running while the order was set again comes back after the placed sessions,
// not on the rank it was saved with, which a live session holds now.
//
// alpha and bravo are ordered alpha, bravo and saved by kill-server. A daemon
// started with --no-restore holds neither; charlie and delta are made and
// ordered charlie, delta, so charlie holds alpha's old rank 1. tuios resurrect
// alpha must list charlie, delta, alpha.
//
// Then a full restart, which restores every saved session in name order:
// bravo, saved with rank 2 and in no order set since, must not take delta's
// place.
//
// Negative control: with restoredRank keeping the saved rank, alpha ties with
// charlie and lists between charlie and delta. With the ranks left in the
// saved files, bravo lists second after the restart. The runs are in
// NEGATIVE_CONTROLS.md.
func TestSessionOrderRestoredSessionDoesNotTie(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "set-session-order", "alpha", "bravo"); err != nil {
		t.Fatalf("set-session-order: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	daemon := exec.Command(tuiosBin, "daemon", "--no-restore")
	daemon.Dir = workDirIn(t, base)
	daemon.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		daemon.Env = append(daemon.Env, key+"="+xdgDir(base, key))
	}
	if err := daemon.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	})
	deadline := time.Now().Add(shellTimeout)
	for {
		out, err := tuiosCLI(t, base, "ls")
		if err == nil && strings.Contains(out, "No sessions") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("setup: the --no-restore daemon never answered with no sessions: %v\n%s", err, out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, name := range []string{"charlie", "delta"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	if out, err := tuiosCLI(t, base, "set-session-order", "charlie", "delta"); err != nil {
		t.Fatalf("set-session-order: %v\n%s", err, out)
	}
	// resurrect takes no --no-animations, so the harness is told to keep them.
	term := startIn(t, base, startOpts{cols: 120, rows: 40, animations: true, args: []string{"resurrect", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("setup: tuios resurrect alpha never attached: %v\n%s", err, term.Snapshot())
	}
	// The restored mark comes off when a client attaches, and resurrect
	// attaches, so being listed again is the proof here.
	waitForSessionInfo(t, base, "alpha")
	wantLsOrder(t, base, "after alpha is restored", "charlie", "delta", "alpha")

	// A full restart restores every saved session in name order. bravo was
	// never in the order set since, so it must not take delta's place.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	_ = daemon.Wait()
	if out, err := tuiosCLI(t, base, "new", "trigger", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	waitForSessionInfo(t, base, "bravo")
	wantLsOrder(t, base, "after a full restart", "charlie", "delta")
}

// TestSessionOrderBuildsOnAnotherClientsMove: a move made after another
// client moved the order builds on the daemon's order, not on the one this
// client sent before. A presses shift+down on alpha, another client puts
// alpha back on top, and A presses shift+down twice in one burst: alpha must
// end third, below bravo and charlie.
//
// Then the client must keep no order of its own in the sidebar state file.
//
// Negative control: with the reply comparing its order against the drawn
// one, the client keeps its copy in the state file. The run is in
// NEGATIVE_CONTROLS.md.
func TestSessionOrderBuildsOnAnotherClientsMove(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeConfig(t, base, "[startup]\nopen_default_window = true\n")
	names := []string{"alpha", "bravo", "charlie", "delta"}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, append([]string{"set-session-order"}, names...)...); err != nil {
		t.Fatalf("set-session-order: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{cols: 140, rows: 40, args: []string{"attach", "alpha"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "alpha")
	toggleSidebarViaPalette(t, term)
	want := func(what string, order ...string) {
		t.Helper()
		if err := term.WaitFor(func(s tuitest.Screen) bool { return railOrder(s, order...) }, uiTimeout); err != nil {
			t.Fatalf("ASSERTION: %s: the rail does not list %v: %v\n%s", what, order, err, term.Snapshot())
		}
		wantLsOrder(t, base, what, order...)
	}
	want("setup", names...)
	if err := term.SendKeys(tuitest.Ctrl('b'), "e"); err != nil {
		t.Fatalf("ctrl+b e: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := term.SendKeys("\x1b[1;2B"); err != nil {
		t.Fatalf("shift+down: %v", err)
	}
	want("one shift+down", "bravo", "alpha", "charlie", "delta")
	if out, err := tuiosCLI(t, base, append([]string{"set-session-order"}, names...)...); err != nil {
		t.Fatalf("the other client's move: %v\n%s", err, out)
	}
	want("another client puts alpha back on top", names...)
	if err := term.SendKeys("\x1b[1;2B", "\x1b[1;2B"); err != nil {
		t.Fatalf("shift+down twice: %v", err)
	}
	want("two quick shift+down after the other client's move", "bravo", "charlie", "alpha", "delta")

	// Once the daemon has answered, this client keeps no order of its own:
	// the one it drew is the daemon's now. A copy left in the sidebar state
	// file would be drawn again for this machine's group from another one.
	path := filepath.Join(xdgDir(base, "XDG_STATE_HOME"), "tuios", "sidebar.json")
	deadline := time.Now().Add(uiTimeout)
	var data []byte
	for time.Now().Before(deadline) {
		data, _ = os.ReadFile(path)
		var st struct {
			Order []string `json:"order"`
		}
		if json.Unmarshal(data, &st) == nil && len(st.Order) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the client kept its own order after the daemon answered: %s", data)
}

// TestSessionOrderSkipsAnEndedSession: the rail sends the names it drew, and
// another client may have ended one of them since. With skip_missing the
// order is set without it, and the missing name is reported.
func TestSessionOrderSkipsAnEndedSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v\n%s", name, err, out)
		}
	}
	out, err := tuiosCLI(t, base, "call", "set-session-order", `{"order":["charlie","gone","alpha"]}`)
	if err == nil {
		t.Fatalf("ASSERTION: an order with a gone name was taken without skip_missing\n%s", out)
	}
	out, err = tuiosCLI(t, base, "call", "set-session-order", `{"order":["charlie","gone","alpha"],"skip_missing":true}`)
	if err != nil || !strings.Contains(out, "gone") {
		t.Fatalf("ASSERTION: skip_missing did not set the order and report the gone name: %v\n%s", err, out)
	}
	wantLsOrder(t, base, "after skip_missing", "charlie", "alpha", "bravo")
}
