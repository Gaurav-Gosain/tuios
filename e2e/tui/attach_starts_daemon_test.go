package tuie2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// This file covers the state a user reaches by closing their laptop lid: every
// session saved on disk, no daemon listening. Attach used to refuse there and
// name 'tuios new', which makes a different session than the ones being asked
// for. The daemon restores everything it finds when it starts, so the sessions
// were one process away the whole time.

// lsRow is a row of 'tuios ls --json', including the flag that only appears
// when no daemon is holding the session.
type lsRow struct {
	Name     string `json:"name"`
	Attached bool   `json:"attached"`
	Saved    bool   `json:"saved"`
}

// listSessionRows runs 'tuios ls --json' and returns the rows with the exit
// status, which is the whole point of the command for a script: 0 means a
// daemon answered, noDaemonExit means the rows came off the disk instead.
func listSessionRows(t *testing.T, base string) ([]lsRow, int) {
	t.Helper()
	out, err := tuiosCLI(t, base, "ls", "--json")
	var rows []lsRow
	if jsonErr := json.Unmarshal([]byte(out), &rows); jsonErr != nil {
		t.Fatalf("ls --json is not JSON: %v\n%s", jsonErr, out)
	}
	return rows, exitCode(t, err)
}

// waitForRows polls the listing until it satisfies want, and returns the last
// one it read either way, so a failure prints what was actually there.
func waitForRows(t *testing.T, base string, want func([]lsRow) bool) []lsRow {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		rows, _ := listSessionRows(t, base)
		if want(rows) || !time.Now().Before(deadline) {
			return rows
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// noDaemonExit is the status 'tuios ls' reports when no daemon answered.
const noDaemonExit = 3

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("command failed without an exit status: %v", err)
	}
	return exit.ExitCode()
}

// TestAttachStartsTheDaemonAndBringsSessionsBack is the reported bug end to
// end: sessions saved, no daemon, and a bare 'tuios attach'.
func TestAttachStartsTheDaemonAndBringsSessionsBack(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	if out, err := tuiosCLI(t, base, "new", "e2e-autostart", "--detach"); err != nil {
		t.Fatalf("create detached session: %v: %s", err, out)
	}
	// kill-server is synchronous and saves every session on the way out.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}

	// The listing has to show what is actually there, or attach opening a
	// session the user was just told did not exist is incomprehensible.
	rows, code := listSessionRows(t, base)
	if code != noDaemonExit {
		t.Errorf("'ls' exited %d with no daemon, want %d so a script can tell that from an empty daemon", code, noDaemonExit)
	}
	if len(rows) != 1 || rows[0].Name != "e2e-autostart" || !rows[0].Saved {
		t.Fatalf("'ls' did not report the saved session: %+v", rows)
	}

	client, logPath := startInLogged(t, base, startOpts{args: []string{"attach"}})
	if err := client.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 1 }, bootTimeout); err != nil {
		t.Fatalf("attach never brought the saved session up: %v\n%s", err, client.Snapshot())
	}

	// It said what it was bringing back, before the TUI took the screen.
	logBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBytes), "e2e-autostart") {
		t.Errorf("attach did not name the session it restored; PTY log:\n%s", string(logBytes))
	}

	// And it opened that session rather than starting a fresh one beside it.
	// Polled, because the daemon records the client a moment after the first
	// frame is on screen.
	rows = waitForRows(t, base, func(rows []lsRow) bool {
		return len(rows) == 1 && rows[0].Name == "e2e-autostart" && rows[0].Attached
	})
	if len(rows) != 1 || rows[0].Name != "e2e-autostart" || !rows[0].Attached {
		t.Fatalf("attach did not land on the saved session with a client on it: %+v", rows)
	}
	alive(t, client, "after attaching to the restored session")
}

// TestAttachWithNothingSavedOpensANewSession is the deliberate other half. With
// no daemon and nothing on disk, the only thing that gets the user to a terminal
// is a new session, so attach opens one; what it must not do is appear without
// saying so.
func TestAttachWithNothingSavedOpensANewSession(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	rows, code := listSessionRows(t, base)
	if code != noDaemonExit || len(rows) != 0 {
		t.Fatalf("the test started with something already there: exit %d, rows %+v", code, rows)
	}

	client, logPath := startInLogged(t, base, startOpts{args: []string{"attach"}})
	// A new session has no windows yet, so the welcome screen is what "it
	// opened one" looks like.
	waitBoot(t, client)

	logBytes, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logBytes), "No saved sessions to restore") {
		t.Errorf("a session appeared with nothing said about where it came from; PTY log:\n%s", string(logBytes))
	}

	rows = waitForRows(t, base, func(rows []lsRow) bool { return len(rows) == 1 })
	if len(rows) != 1 {
		t.Fatalf("want exactly one new session, got %+v", rows)
	}
	alive(t, client, "after opening a session from nothing")
}

// TestClientsStartingAtOnceProduceOneDaemon covers the race auto-start creates:
// several clients can all find no daemon at the same moment and all start one.
//
// One listing holding every session is the proof. A second daemon that took the
// socket would be holding some of them where nothing can reach them, and it
// would still be listening after kill-server stopped the one that answered.
func TestClientsStartingAtOnceProduceOneDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	const starters = 4
	outs := make([]string, starters)
	errs := make([]error, starters)

	var wg sync.WaitGroup
	begin := make(chan struct{})
	for i := range starters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			outs[i], errs[i] = tuiosCLI(t, base, "new", fmt.Sprintf("e2e-race-%d", i), "--detach")
		}()
	}
	close(begin)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("starter %d failed: %v: %s", i, err, outs[i])
		}
	}

	rows, code := listSessionRows(t, base)
	if code != 0 {
		t.Fatalf("no daemon answered after the race: 'ls' exited %d", code)
	}
	if len(rows) != starters {
		t.Fatalf("one daemon should hold all %d sessions, the listing has %+v", starters, rows)
	}

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v: %s", err, out)
	}
	if _, code := listSessionRows(t, base); code != noDaemonExit {
		t.Errorf("something is still listening after kill-server: 'ls' exited %d, so the race left a second daemon", code)
	}
}
