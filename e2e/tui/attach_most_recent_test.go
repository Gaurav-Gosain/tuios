package tuie2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Issue #486: the help says a bare `tuios attach` attaches to the most recent
// session. The daemon took the first session a range over its session map
// gave, and Go randomises map order, so with several sessions the client
// landed on any of them.
//
// "Most recent" is the session the person used last: the one they typed in.
// A window an agent opens, or a command a script routes to a client, is not
// the person using a session, so neither may pull the attach away.

// TestBareAttachLandsOnTheSessionTypedInLast makes four sessions. Each round
// attaches a client to one of them by name, uses it as the person would, and
// detaches. Odd rounds type a command into its pane, which reaches the daemon
// as pane input. Even rounds press Alt and a number in window mode, which
// reaches it only as the client's state push. A bare attach must then land on
// that session. The rounds
// visit the oldest session, the newest and the ones between, out of creation
// order, so neither map order nor creation order passes them all: a random
// pick passes five rounds about once in a thousand runs.
//
// How this could pass wrongly: the listing could show a client left over from
// the round before. Each round waits for no session to have a client first.
func TestBareAttachLandsOnTheSessionTypedInLast(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	names := []string{"recent-a", "recent-b", "recent-c", "recent-d"}
	for _, name := range names {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}

	for i, target := range []string{"recent-c", "recent-a", "recent-d", "recent-b", "recent-c"} {
		waitNoClient(t, base, len(names))
		if i%2 == 0 {
			typeInSession(t, base, target, i)
		} else {
			switchWorkspaceIn(t, base, target, i+2)
		}
		got := bareAttachLandsOn(t, base)
		if got != target {
			t.Fatalf("ASSERTION: round %d: a bare attach landed on %q, want %q, the session typed in last", i+1, got, target)
		}
	}
}

// TestBareAttachIgnoresAgentWindowsAndRoutedCommands is the review's case:
// the person types in "person" while an agent opens windows in "agent", and a
// script runs a command in "watched", which has a client of its own that
// nobody types in. Each of those bumps the session's activity, and each is
// later than the typing. A bare attach must still land on "person".
//
// The positive half: before the typing, the activity alone decides, and the
// agent's session wins. That proves the agent's windows did bump the
// activity the pick now ignores.
func TestBareAttachIgnoresAgentWindowsAndRoutedCommands(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"person", "agent", "watched"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	// A client that sits on watched and is never typed in. attachIn would
	// press Alt+Esc in it, which is the person using watched.
	startIn(t, base, startOpts{args: []string{"attach", "watched"}})
	clientShows(t, base, "watched")

	// The agent: windows opened from outside every client.
	agentWindows := func() {
		t.Helper()
		for range 3 {
			if out, err := tuiosCLI(t, base, "new-window", "-s", "agent"); err != nil {
				t.Fatalf("new-window in agent: %v: %s", err, out)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Nobody has typed yet, so the activity decides, and the agent's
	// windows are the latest.
	agentWindows()
	if got := bareAttachLandsOn(t, base); got != "agent" {
		t.Fatalf("before any typing a bare attach landed on %q, want \"agent\", the latest activity: the fixture does not hold", got)
	}
	waitNoClientOn(t, base, "agent")

	typeInSession(t, base, "person", 0)
	agentWindows()
	// The script: a command routed to the client on watched, which pushes
	// the state it changes.
	if out, err := tuiosCLI(t, base, "select-workspace", "-s", "watched", "2"); err != nil {
		t.Fatalf("select-workspace in watched: %v: %s", err, out)
	}
	waitWorkspace(t, base, "watched", 2)

	if got := bareAttachLandsOn(t, base); got != "person" {
		t.Fatalf("ASSERTION: a bare attach landed on %q, want \"person\", the session typed in last. An agent's windows or a routed command took the pick", got)
	}
}

// TestBareAttachIgnoresKeysSentToATerminalModeClient is the second review's
// case. A client sits on "watched" in terminal mode, so keys a script sends
// to it reach its pane as typed input, the same message the person's typing
// is. The person types in "person", then tuios send-keys types into
// "watched" through that client. A bare attach must land on "person".
//
// The positive half: the keys did reach the pane in "watched", so they were
// typed there after the person's typing.
func TestBareAttachIgnoresKeysSentToATerminalModeClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"person", "watched"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	// A client boots in terminal mode. No key is pressed in it: attachIn
	// would press Alt+Esc, which is the person using watched.
	watcher := startIn(t, base, startOpts{args: []string{"attach", "watched"}})
	clientShows(t, base, "watched")

	typeInSession(t, base, "person", 0)

	if out, err := tuiosCLI(t, base, "send-keys", "-s", "watched", "--raw", "echo sent-by-script-done"); err != nil {
		t.Fatalf("send-keys to watched: %v: %s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-keys", "-s", "watched", "Enter"); err != nil {
		t.Fatalf("send-keys Enter to watched: %v: %s", err, out)
	}
	deadline := time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosOut(base, "capture-pane", "-s", "watched")
		if strings.Contains(out, "\nsent-by-script-done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the keys sent to watched never ran in its pane: the fixture does not hold\n%s\n%s", out, watcher.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}

	if got := bareAttachLandsOn(t, base); got != "person" {
		t.Fatalf("ASSERTION: a bare attach landed on %q, want \"person\", the session typed in last. Keys a script sent through a client took the pick", got)
	}
}

// TestBareAttachFollowsASwitchFromTheSessionBrowser is the third review's
// case. The client reports the person's input before it handles it, so the
// Enter that switches sessions is reported for the session left. The person
// attaches to "first", switches to "second" in the session browser, reads
// it without a key, and detaches. A bare attach must land on "second".
//
// The positive half: every key the person pressed was used in "first", so
// without the report for the session moved to, "first" is the pick.
func TestBareAttachFollowsASwitchFromTheSessionBrowser(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"first", "second"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	term := attachIn(t, base, "first", startOpts{})
	clientShows(t, base, "first")
	if err := term.SendKeys(tuitest.Ctrl('b'), "S"); err != nil {
		t.Fatalf("open the session browser: %v", err)
	}
	if err := term.WaitForText("Sessions", uiTimeout); err != nil {
		t.Fatalf("the session browser did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("second"); err != nil {
		t.Fatalf("type the filter: %v", err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if err := term.WaitForText("Session: second", uiTimeout); err != nil {
		t.Fatalf("the browser did not switch to second: %v\n%s", err, term.Snapshot())
	}
	clientShows(t, base, "second")
	// Reading: the screen redraws, and no key is pressed.
	time.Sleep(500 * time.Millisecond)
	if err := term.Close(); err != nil {
		t.Logf("close the client: %v", err)
	}
	waitNoClient(t, base, 2)

	if got := bareAttachLandsOn(t, base); got != "second" {
		t.Fatalf("ASSERTION: a bare attach landed on %q, want \"second\", the session the person switched to and read", got)
	}
}

// TestBareAttachAfterARestartLandsOnTheSessionTypedInLast types in "alpha",
// the first of three sessions by name, then restarts the daemon. The restore
// starts every session again in name order, so a pick by activity alone goes
// to "gamma", the last one restored. A bare attach must land on "alpha".
func TestBareAttachAfterARestartLandsOnTheSessionTypedInLast(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create %s: %v: %s", name, err, out)
		}
	}
	typeInSession(t, base, "alpha", 0)
	waitNoClient(t, base, 3)
	// kill-server saves every session on the way out, and the bare attach
	// starts the daemon again, which restores them.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	if got := bareAttachLandsOn(t, base); got != "alpha" {
		t.Fatalf("ASSERTION: after a restart a bare attach landed on %q, want \"alpha\", the session typed in last", got)
	}
}

// typeInSession attaches a client to session by name, types a command into its pane
// as the person would, waits for the shell to run it, and detaches.
func typeInSession(t *testing.T, base, session string, round int) {
	t.Helper()
	term := attachIn(t, base, session, startOpts{})
	clientShows(t, base, session)
	enterTerminalMode(t, term)
	mark := "typed-" + session + "-" + string(rune('a'+round))
	typeLine(t, term, "echo "+mark+"-done")
	deadline := time.Now().Add(shellTimeout)
	for {
		out, _ := tuiosOut(base, "capture-pane", "-s", session)
		if strings.Contains(out, "\n"+mark+"-done") || strings.HasPrefix(out, mark+"-done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the shell in %s never ran the typed command:\n%s\n%s", session, out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.Close(); err != nil {
		t.Logf("close the client on %s: %v", session, err)
	}
	waitNoClientOn(t, base, session)
	// Two uses in the same instant would tie.
	time.Sleep(20 * time.Millisecond)
}

// switchWorkspaceIn attaches a client to session by name, presses Alt and ws
// in window mode as the person would, waits for the session to show that
// workspace, and detaches. Nothing reaches a pane: the use is the client's
// state push alone.
func switchWorkspaceIn(t *testing.T, base, session string, ws int) {
	t.Helper()
	term := attachIn(t, base, session, startOpts{})
	clientShows(t, base, session)
	time.Sleep(insertGuard)
	if err := term.SendKeys(tuitest.Alt(rune('0' + ws))); err != nil {
		t.Fatalf("press alt+%d: %v", ws, err)
	}
	waitWorkspace(t, base, session, ws)
	if err := term.Close(); err != nil {
		t.Logf("close the client on %s: %v", session, err)
	}
	waitNoClientOn(t, base, session)
	time.Sleep(20 * time.Millisecond)
}

// bareAttachLandsOn runs a bare attach, returns the session it landed on, and
// detaches it. It counts clients per session, so it also works while another
// session already has a client.
func bareAttachLandsOn(t *testing.T, base string) string {
	t.Helper()
	before := clientCounts(base)
	client := startIn(t, base, startOpts{args: []string{"attach"}})
	var got string
	var now map[string]int
	deadline := time.Now().Add(uiTimeout)
	for time.Now().Before(deadline) {
		now = clientCounts(base)
		if got = newClientSession(before, now); got != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == "" {
		t.Fatalf("the bare attach never landed on a session: clients before %v, after %v\n%s", before, now, client.Snapshot())
	}
	if err := client.Close(); err != nil {
		t.Logf("close the bare client: %v", err)
	}
	// Wait for the client to go, so the next count starts from where it was.
	for time.Now().Before(deadline.Add(uiTimeout)) {
		if clientCounts(base)[got] == before[got] {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return got
}

// clientCounts is the number of attached clients on each session.
func clientCounts(base string) map[string]int {
	counts := map[string]int{}
	out, err := tuiosOut(base, "list-clients", "--json")
	if err != nil {
		return counts
	}
	var rows []struct {
		Session string `json:"session"`
	}
	if json.Unmarshal([]byte(out), &rows) != nil {
		return counts
	}
	for _, r := range rows {
		if r.Session != "" {
			counts[r.Session]++
		}
	}
	return counts
}

// newClientSession is the one session that has one more client in now than
// in before, or "" when none or more than one does.
func newClientSession(before, now map[string]int) string {
	name := ""
	for session, n := range now {
		if n != before[session]+1 {
			continue
		}
		if name != "" {
			return ""
		}
		name = session
	}
	return name
}

// waitNoClient waits until all n sessions are listed and none has a client.
func waitNoClient(t *testing.T, base string, n int) {
	t.Helper()
	rows := waitForRows(t, base, func(rows []lsRow) bool {
		for _, r := range rows {
			if r.Attached {
				return false
			}
		}
		return len(rows) == n
	})
	for _, r := range rows {
		if r.Attached {
			t.Fatalf("%s still has a client: %+v", r.Name, rows)
		}
	}
}

// waitNoClientOn waits until session has no client.
func waitNoClientOn(t *testing.T, base, session string) {
	t.Helper()
	waitForRows(t, base, func(rows []lsRow) bool {
		for _, r := range rows {
			if r.Name == session && r.Attached {
				return false
			}
		}
		return true
	})
}
