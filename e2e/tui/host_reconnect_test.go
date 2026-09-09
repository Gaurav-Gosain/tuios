package tuie2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A link that drops under an attached session, on screen.
//
// The report this file exists for, in the maintainer's words: "the ssh
// connection is not very reliable btw, i connected to oci and it keeps dropping
// and it very rudely just switches off from the terminal i was working on".
// Two failures. The link dropped, and the client answered by closing every pane
// and putting him back on his own machine.
//
// The second is what these tests hold. The daemon on the host owns the session
// and is still running it, so a dropped pipe is this client's problem to solve,
// not the person's. What must happen: the pane stays on screen, the dock says
// the link is being dialed again, and the session comes back with its content
// and with the place in the history the person was reading.
//
// Everything here goes through the real transport. The ssh stand-in drops
// ssh's options and runs the command locally with the far machine's XDG
// directories, so `ssh build tuios stdio-proxy` reaches a second real daemon
// over a real pipe. No network is used and nothing reads the developer's ssh
// configuration.
//
// NEGATIVE CONTROLS are recorded in NEGATIVE_CONTROLS.md and in the report for
// this change. Each was run by mutating the shipped code and watching a named
// assertion fail.

// attachOnBuild starts a client on this machine, opens the rail and switches
// onto the named session on build. It returns once the client is drawing it.
func attachOnBuild(t *testing.T, base string, env []string, farSession string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	railShows(t, term, farSession)
	time.Sleep(insertGuard)
	s := term.Screen()
	_, rows := s.Size()
	row := -1
	for r := range rows {
		if c := strings.Index(s.Line(r), farSession); c >= 0 && c < sidebarBand {
			row = r
			break
		}
	}
	if row < 0 {
		t.Fatalf("no remote session row for %s:\n%s", farSession, term.Snapshot())
	}
	mouseClick(t, term, 3, row, tuitest.MouseLeft, 0)
	// The rail marks the far session as the one this client is on. It used to
	// name this machine "@ local" in a group of its own; the section is laid
	// out by machine now and the mark moves in place. See sidebar_hosts.go.
	waitRailCurrent(t, term, farSession)
	// The switch lands with the pane focused and the client in terminal mode,
	// so a caller types straight into the far session's shell.
	time.Sleep(insertGuard)
	return term
}

// breakTheLink kills the far side of every open link, which is what an ssh
// child dying looks like from this machine.
func breakTheLink(t *testing.T) {
	t.Helper()
	out, err := exec.Command("pkill", "-f", tuiosBin+" stdio-proxy").CombinedOutput()
	if err != nil {
		t.Fatalf("kill the link's proxy: %v\n%s", err, out)
	}
}

// TestAHostSessionStaysAttached is the drop itself, and it is the first thing
// to fix because it needed no network to happen.
//
// The daemon answered open-host-connection under a ten second write deadline
// and then handed the connection to the relay, which cleared the read deadline
// and left the write one armed. Eleven seconds into every attached session the
// far pane printed something, that write failed with "i/o timeout", and the
// client was told it had lost the link. Nothing else had to go wrong.
//
// The wait is longer than that window on purpose. A session that survives
// twenty-five seconds of a pane printing has passed the deadline twice over.
func TestAHostSessionStaysAttached(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-steady", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := attachOnBuild(t, base, env, "far-steady")
	toggleSidebarViaPalette(t, term)

	// A pane that prints every second, so every second is a chance for the
	// relay's write to fail.
	runInShell(t, term, "{ for i in $(seq 1 40); do echo TICK-$i; sleep 1; done; } & echo TICKER-UP", "TICKER-UP", shellTimeout)
	if err := term.WaitForText("TICK-3", shellTimeout); err != nil {
		t.Fatalf("the far pane never printed: %v\n%s", err, term.Snapshot())
	}

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(term.Screen().Text(), "Reconnecting to build") {
			t.Fatalf("ASSERTION: the session on build dropped on its own, with no network between the two machines. %v after the attach.\n%s",
				25*time.Second-time.Until(deadline), term.Snapshot())
		}
		time.Sleep(500 * time.Millisecond)
	}
	if out, _ := tuiosCLI(t, base, "hosts"); strings.Contains(out, "the link dropped") {
		t.Fatalf("ASSERTION: the link dropped while nothing was wrong with it:\n%s", out)
	}
	t.Logf("a session on build, still attached after 25 seconds:\n%s", term.Snapshot())
	alive(t, term, "after a long attached session on a host")
}

// TestALinkThatDropsIsDialedAgainAndThePaneComesBack is the whole fix in one
// frame.
//
// The pane is left where it is while the link is dialed again, the dock says so
// in one line, and when the link is back the pane is live: a command typed
// after the reconnect runs on the far machine and its output is drawn here.
func TestALinkThatDropsIsDialedAgainAndThePaneComesBack(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := attachOnBuild(t, base, env, "far-shell")

	// Something on screen that only the far session could have produced, so a
	// pane that came back empty is a failure rather than a pass.
	runInShell(t, term, "echo BEFORE-DROP-42", "BEFORE-DROP-42", shellTimeout)

	breakTheLink(t)

	// The pane stays. What used to happen instead: every pane closed and the
	// client went back to the session on this machine.
	if err := term.WaitForText("Reconnecting to build", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not say it was dialing the link again: %v\n%s", err, term.Snapshot())
	}
	t.Logf("a pane while the link is being dialed again:\n%s", term.Snapshot())
	if s := term.Screen().Text(); !strings.Contains(s, "BEFORE-DROP-42") {
		t.Fatalf("ASSERTION: the last frame of the far session was thrown away while the link was down. A dropped pipe is not a session that ended.\n%s", term.Snapshot())
	}
	if railCurrentIs(term.Screen(), "home") {
		t.Fatalf("ASSERTION: the client switched away from the pane while the link was being dialed again\n%s", term.Snapshot())
	}

	// The link comes back on its own, and so does the session.
	if err := term.WaitForText("The link to build is back", 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: the client never got the link back: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Reconnecting to build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock still says the link is being dialed after it came back\n%s", term.Snapshot())
	}
	t.Logf("the same pane after the link came back:\n%s", term.Snapshot())

	// The content it had is still there, and it is live again.
	if s := term.Screen().Text(); !strings.Contains(s, "BEFORE-DROP-42") {
		t.Fatalf("ASSERTION: the pane came back without the history it had before the drop\n%s", term.Snapshot())
	}
	runInShell(t, term, "echo AFTER-RECONNECT-99", "AFTER-RECONNECT-99", shellTimeout)

	// It is still the far session, drawn by this client, and not a new one
	// here.
	if out := remoteSessionsListed(t, remote); !strings.Contains(out, "far-shell") {
		t.Fatalf("ASSERTION: the far session did not survive:\n%s", out)
	}
	if out, _ := tuiosCLI(t, base, "ls"); strings.Contains(out, "far-shell") {
		t.Fatalf("ASSERTION: the reconnect made a session on this machine instead of going back to the one on build:\n%s", out)
	}
	noNestedClient(t, "far-shell")
	alive(t, term, "after the link came back")
}

// TestAScrolledPaneIsStillScrolledAfterAReconnect is the part that decides
// whether coming back is worth anything.
//
// A person reading something forty lines back has a place in the history, not a
// distance from the bottom, and the session printed more while the link was
// down. Coming back on a rebuilt pane would put them at the end of a longer
// history, which is the same as losing their place. Coming back on the pane
// they already had, with only the missing rows asked for, keeps it.
func TestAScrolledPaneIsStillScrolledAfterAReconnect(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-scroll", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := attachOnBuild(t, base, env, "far-scroll")
	toggleSidebarViaPalette(t, term)

	const filled = 400
	fillScrollback(t, term, "FAR", filled)

	col, row := paneCell(t, term)
	wheelAt(t, term, col, row, tuitest.MouseWheelUp, 25)
	waitScrolledTo(t, term, "FAR", "the wheel did not scroll back through the far session's history",
		func(n int) bool { return n > 0 && n <= filled-40 })
	before := settledScroll(t, term, "FAR")
	t.Logf("the newest FAR line on screen before the drop is %d", before)

	breakTheLink(t)
	if err := term.WaitForText("The link to build is back", 60*time.Second); err != nil {
		t.Fatalf("ASSERTION: the client never got the link back: %v\n%s", err, term.Snapshot())
	}
	// The reconnect asks the daemon for the rows this pane does not hold, so
	// the history grows underneath a view that must not move with it.
	time.Sleep(2 * time.Second)

	after := newestVisible(term.Screen(), "FAR")
	t.Logf("the newest FAR line on screen after the link came back is %d", after)
	if after != before {
		t.Fatalf("ASSERTION: the view moved across a reconnect: the newest line on screen went from "+
			"FAR-%d-END to FAR-%d-END. A person who was scrolled back must be on the same line when the link comes back.\n%s",
			before, after, term.Snapshot())
	}
	alive(t, term, "after a reconnect under a scrolled pane")
}

// TestAHostThatNeverComesBackGivesUpAndSaysWhy is the "do not paper over a real
// failure with infinite retries" rule on screen.
//
// The machine is made permanently unreachable, so no number of dials can help.
// The client must stop, say what happened and what to do, and put the person
// back on the session they left here. The budget is shortened by the
// environment so the test watches the real give-up rather than a shorter code
// path.
func TestAHostThatNeverComesBackGivesUpAndSaysWhy(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh, "TUIOS_HOST_RECONNECT_BUDGET=8s"}

	if out, err := tuiosCLI(t, remote, "new", "far-gone", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := attachOnBuild(t, base, env, "far-gone")

	// The machine goes away for good: the stand-in now fails the way ssh does
	// when it cannot reach a host at all.
	dead := "#!/bin/sh\necho 'ssh: connect to host buildbox port 22: No route to host' >&2\nexit 255\n"
	if err := os.WriteFile(ssh, []byte(dead), 0o700); err != nil {
		t.Fatalf("replace the ssh stand-in: %v", err)
	}
	breakTheLink(t)

	if err := term.WaitForText("Reconnecting to build", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not try to get the link back at all: %v\n%s", err, term.Snapshot())
	}
	// It stops on its own, and the sentence names the machine and says the
	// session is still there.
	if err := term.WaitForText("did not come back", 40*time.Second); err != nil {
		t.Fatalf("ASSERTION: the client is still dialing a machine that cannot be reached. Retrying a real failure forever hides it: %v\n%s", err, term.Snapshot())
	}
	t.Logf("after giving up on the host:\n%s", term.Snapshot())
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Reconnecting to build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the dock still says the link is being dialed after the client gave up\n%s", term.Snapshot())
	}
	// The person is back on the session they left on this machine, which is
	// the fallback that always existed.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return railCurrentIs(s, "home")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not come back to this machine after giving up: %v\n%s", err, term.Snapshot())
	}
	if out := remoteSessionsListed(t, remote); !strings.Contains(out, "far-gone") {
		t.Fatalf("ASSERTION: the far session did not survive:\n%s", out)
	}
	alive(t, term, "after giving up on a host")
}

// TestTheDaemonSaysWhyALinkDropped is the instrumentation.
//
// A keepalive that lapsed, a stream nobody was reading and a dead sshd all used
// to end a link with the same sentence, so nobody could tell which of the three
// fixes to reach for. The listing now carries the count and the reason.
func TestTheDaemonSaysWhyALinkDropped(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-witness", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	term := attachOnBuild(t, base, env, "far-witness")

	breakTheLink(t)
	if err := term.WaitForText("The link to build is back", 60*time.Second); err != nil {
		t.Fatalf("the link never came back: %v\n%s", err, term.Snapshot())
	}

	deadline := time.Now().Add(30 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, _ := tuiosCLI(t, base, "hosts")
		last = out
		if strings.Contains(out, "the link dropped") {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !strings.Contains(last, "the link dropped") {
		t.Fatalf("ASSERTION: the listing does not say the link dropped, so a link that keeps dropping reads as healthy every time it is asked:\n%s", last)
	}
	if !strings.Contains(last, "connecting again") {
		t.Errorf("ASSERTION: the listing does not say what happened to the link:\n%s", last)
	}
	t.Logf("tuios hosts after a drop:\n%s", last)
}
