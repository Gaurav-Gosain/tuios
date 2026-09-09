package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A session on another machine, drawn by this client.
//
// The other machine is real in every way that matters: a second daemon with
// its own runtime directory, its own sessions and its own shells, reached only
// over the hub daemon's link. The ssh stand-in below switches the XDG
// directories to that machine's before it runs the command, so `ssh build
// tuios stdio-proxy` reaches the second daemon's socket and nothing else. No
// network is used and nothing reads the developer's ssh configuration.
//
// What would pass a weaker test and fail these: a --host flag that still runs
// ssh in a pane (the session would be drawn by a nested client and the hub's
// daemon would hold a second window), a rail row that opens a pane instead of
// switching (same), or a relay that carries the attach and not the keystrokes
// (the echo would never come back).

// writeFakeSSHTo puts an ssh stand-in in dir whose far side is the daemon
// rooted at remoteBase. Every XDG directory is switched before the command
// runs, so the proxy it starts dials that daemon's socket.
func writeFakeSSHTo(t *testing.T, dir, remoteBase string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh-remote")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("shift\n") // the address
	for _, key := range xdgKeys {
		b.WriteString("export " + key + "=" + filepath.Join(remoteBase, key) + "\n")
	}
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

// remoteMachine is the second daemon: its isolation root, with the XDG
// directories made and a cleanup that kills it.
func remoteMachine(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, key := range xdgKeys {
		if err := os.MkdirAll(filepath.Join(base, key), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", key, err)
		}
	}
	killDaemon(t, base)
	return base
}

// writeOneHostConfig names one host, build, that the stand-in routes to the
// remote machine.
func writeOneHostConfig(t *testing.T, base, tuiosPath string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[hosts.build]\n" +
		"addr = \"someone@buildbox\"\n" +
		"command = \"" + tuiosPath + "\"\n" +
		"connect_timeout = 5\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// remoteSessionsListed is what the remote daemon says its sessions are.
func remoteSessionsListed(t *testing.T, remoteBase string) string {
	t.Helper()
	out, _ := tuiosCLI(t, remoteBase, "ls")
	return out
}

// noNestedClient fails the test if a tuios client is running on the far side
// for the session, which is what the old ssh-in-a-pane path leaves behind.
func noNestedClient(t *testing.T, session string) {
	t.Helper()
	out, _ := exec.Command("pgrep", "-af", tuiosBin).CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, " attach "+session) && !strings.Contains(line, "--host") {
			t.Fatalf("ASSERTION: a nested client is running for %s: %s", session, line)
		}
	}
}

// TestAttachOnAHostIsDrawnByThisClient is the frame the whole feature is for:
// a session that exists only on the other machine, on screen in this client.
func TestAttachOnAHostIsDrawnByThisClient(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	// The session lives on the other machine and nowhere else.
	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	// The command a person types, in a plain terminal.
	term := startIn(t, base, startOpts{args: []string{"attach", "--host", "build", "far-shell"}, env: env})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "╰──")
	}, bootTimeout); err != nil {
		t.Fatalf("ASSERTION: the client never drew the far session: %v\n%s", err, term.Snapshot())
	}
	// Drawn by this client, not by one on the far side.
	noNestedClient(t, "far-shell")

	// Keystrokes cross to the far shell and its output crosses back.
	if err := term.SendKeys("echo REMOTE-$((6*7))\r"); err != nil {
		t.Fatalf("type into the far shell: %v", err)
	}
	if err := term.WaitForText("REMOTE-42", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the far shell's output never reached this client: %v\n%s", err, term.Snapshot())
	}

	// The rail names the machine.
	toggleSidebarViaPalette(t, term)
	railShows(t, term, hostOpen+" build")
	t.Logf("a session on build, drawn by this client:\n%s", term.Snapshot())

	// It is the far daemon's session: this machine's daemon does not hold
	// it, and the far daemon sees a client attached.
	if out, _ := tuiosCLI(t, base, "ls"); strings.Contains(out, "far-shell") {
		t.Fatalf("ASSERTION: this machine's daemon holds far-shell, so the session was not attached across the link:\n%s", out)
	}
	if out := remoteSessionsListed(t, remote); !strings.Contains(out, "far-shell") {
		t.Fatalf("the far daemon lost its session:\n%s", out)
	}
	alive(t, term, "after attaching a session on a host")
}

// railCurrentIs reports whether the rail marks the session named want as the
// one this client is on: its row wears the focus mark in the rail's gutter,
// which is the screen's first column for a left rail.
func railCurrentIs(s tuitest.Screen, want string) bool {
	r := railRowOf(s, want)
	if r < 0 {
		return false
	}
	return strings.HasPrefix(s.Line(r), "▎")
}

// waitRailCurrent blocks until the rail marks want as the attached session.
func waitRailCurrent(t *testing.T, term *tuitest.Terminal, want string) {
	t.Helper()
	if err := term.WaitFor(func(s tuitest.Screen) bool { return railCurrentIs(s, want) }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never marked %q as the attached session: %v\n%s", want, err, term.Snapshot())
	}
}

// railRows is where each of the named rows is on screen right now.
func railRows(s tuitest.Screen, wants ...string) map[string]int {
	out := make(map[string]int, len(wants))
	for _, w := range wants {
		out[w] = railRowOf(s, w)
	}
	return out
}

// TestRailAttachesARemoteSessionInThisClient is the on-screen proof for the
// rail: a click on a session under a host switches this client onto it, with
// no pane opened and no nested client, and not one row of the rail moves. The
// machine groups keep their order, this machine first, and the attached
// session is marked current in place under the machine that holds it.
//
// What would pass a weaker test and fail this one: the rail that used to be
// drawn, with build hoisted to the top of the section and this machine's
// sessions pushed down into a group of their own, so the clicked row moved out
// from under the pointer.
func TestRailAttachesARemoteSessionInThisClient(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	if out, err := tuiosCLI(t, remote, "new", "far-shell", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	rows := []string{hostOpen + " local", "home", hostOpen + " build", "far-shell"}
	for _, want := range rows {
		railShows(t, term, want)
	}
	waitRailCurrent(t, term, "home")
	panesBefore := -1
	if wl, err := daemonWindows(base, "home"); err == nil {
		panesBefore = len(wl.Windows)
	}

	time.Sleep(insertGuard)
	before := railRows(term.Screen(), rows...)
	if !(before[rows[0]] < before[rows[1]] && before[rows[1]] < before[rows[2]] && before[rows[2]] < before[rows[3]]) {
		t.Fatalf("ASSERTION: the rail is not laid out local first (%v):\n%s", before, term.Snapshot())
	}
	t.Logf("before the click:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-switch-before")

	// The click on the remote row.
	mouseClick(t, term, 3, before["far-shell"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "far-shell")
	t.Logf("after the click:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-switch-after")

	// The proof: every row is where it was, and only the mark moved.
	after := railRows(term.Screen(), rows...)
	for _, want := range rows {
		if after[want] != before[want] {
			t.Errorf("ASSERTION: the row %q moved from screen row %d to %d on the switch:\n%s",
				want, before[want], after[want], term.Snapshot())
		}
	}
	if railCurrentIs(term.Screen(), "home") {
		t.Errorf("ASSERTION: this machine's session is still marked current after the switch:\n%s", term.Snapshot())
	}
	// The rail band only: the dock still says "far-shell @ build", which is
	// the one place the qualifier belongs.
	if railRowOf(term.Screen(), "@ ") >= 0 {
		t.Errorf("ASSERTION: the rail still marks the attached machine with @:\n%s", term.Snapshot())
	}

	// No pane was opened for it on this machine, and no nested client runs.
	if wl, err := daemonWindows(base, "home"); err == nil && len(wl.Windows) != panesBefore {
		t.Fatalf("ASSERTION: the switch opened a pane in the local session: %d windows, had %d", len(wl.Windows), panesBefore)
	}
	noNestedClient(t, "far-shell")

	// A click on the machine's header folds its group: far-shell leaves the
	// rail, the header wears the shut mark and says how many it holds.
	time.Sleep(insertGuard)
	mouseClick(t, term, 3, after[hostOpen+" build"], tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := railRowOf(s, hostShut+" build")
		return r >= 0 && railRowOf(s, "far-shell") < 0 && strings.Contains(s.Line(r), "1")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the click on the header did not fold the group: %v\n%s", err, term.Snapshot())
	}
	if railRowOf(term.Screen(), "home") != before["home"] {
		t.Errorf("ASSERTION: folding build moved this machine's rows:\n%s", term.Snapshot())
	}
	t.Logf("with build folded:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-host-folded")

	// And open again.
	mouseClick(t, term, 3, after[hostOpen+" build"], tuitest.MouseLeft, 0)
	railShows(t, term, "far-shell")

	// And back: the local session row under this machine's header.
	time.Sleep(insertGuard)
	mouseClick(t, term, 3, before["home"], tuitest.MouseLeft, 0)
	waitRailCurrent(t, term, "home")
	back := railRows(term.Screen(), rows...)
	for _, want := range rows {
		if back[want] != before[want] {
			t.Errorf("ASSERTION: the row %q is on screen row %d after coming back, was %d:\n%s",
				want, back[want], before[want], term.Snapshot())
		}
	}
	alive(t, term, "after coming back from build")
}
