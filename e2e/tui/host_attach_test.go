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
	b.WriteString("exec \"$@\"\n")
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
	railShows(t, term, "@ build")
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

// TestRailAttachesARemoteSessionInThisClient is the on-screen proof for the
// rail: enter on a session under a host switches this client onto it, with no
// pane opened and no nested client, and this machine's sessions stay
// reachable under a host named local.
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
	railShows(t, term, "@ build")
	railShows(t, term, "far-shell")
	panesBefore := -1
	if wl, err := daemonWindows(base, "home"); err == nil {
		panesBefore = len(wl.Windows)
	}

	// The keyboard path: the cursor to the remote row, then enter.
	rowOf := func(want string) int {
		s := term.Screen()
		_, rows := s.Size()
		for r := 0; r < rows; r++ {
			if c := strings.Index(s.Line(r), want); c >= 0 && c < sidebarBand {
				return r
			}
		}
		return -1
	}
	time.Sleep(insertGuard)
	row := rowOf("far-shell")
	if row < 0 {
		t.Fatalf("no remote session row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, 3, row, tuitest.MouseLeft, 0)

	// The proof: the main group is now build's, and this machine's sessions
	// are the host group named local.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "sessions") && strings.Contains(s.Text(), "@ build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail did not switch onto build: %v\n%s", err, term.Snapshot())
	}
	railShows(t, term, "@ local")
	railShows(t, term, "home")
	t.Logf("after the rail switched onto build:\n%s", term.Snapshot())

	// No pane was opened for it on this machine, and no nested client runs.
	if wl, err := daemonWindows(base, "home"); err == nil && len(wl.Windows) != panesBefore {
		t.Fatalf("ASSERTION: the switch opened a pane in the local session: %d windows, had %d", len(wl.Windows), panesBefore)
	}
	noNestedClient(t, "far-shell")

	// And back: the local session row under @ local.
	time.Sleep(insertGuard)
	row = rowOf("home")
	if row < 0 {
		t.Fatalf("no local session row under @ local:\n%s", term.Snapshot())
	}
	mouseClick(t, term, 3, row, tuitest.MouseLeft, 0)
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "sessions") && !strings.Contains(s.Text(), "@ local")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail did not come back to this machine: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after coming back from build")
}

// TestALinkThatDropsBringsTheClientHome is the failure model on screen: the
// ssh child dies under an attached session, the client comes back to the
// session it left here and says so, and the far session is still there.
func TestALinkThatDropsBringsTheClientHome(t *testing.T) {
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
	railShows(t, term, "far-shell")
	time.Sleep(insertGuard)
	s := term.Screen()
	_, rows := s.Size()
	row := -1
	for r := 0; r < rows; r++ {
		if c := strings.Index(s.Line(r), "far-shell"); c >= 0 && c < sidebarBand {
			row = r
			break
		}
	}
	if row < 0 {
		t.Fatalf("no remote session row:\n%s", term.Snapshot())
	}
	mouseClick(t, term, 3, row, tuitest.MouseLeft, 0)
	railShows(t, term, "@ local")

	// The link dies: the proxy on the far side is the ssh child's process.
	out, err := exec.Command("pkill", "-f", tuiosBin+" stdio-proxy").CombinedOutput()
	if err != nil {
		t.Fatalf("kill the link's proxy: %v\n%s", err, out)
	}

	if err := term.WaitForText("The link to build closed", uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not say the link closed: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "sessions") && !strings.Contains(s.Text(), "@ local")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the client did not come back to this machine: %v\n%s", err, term.Snapshot())
	}
	t.Logf("after the link dropped:\n%s", term.Snapshot())

	if out := remoteSessionsListed(t, remote); !strings.Contains(out, "far-shell") {
		t.Fatalf("ASSERTION: the far session did not survive the link dropping:\n%s", out)
	}
	alive(t, term, "after the link dropped")
}
