package tuie2e

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Discussion #549: with [daemon] ssh_agent = "follow", each session has a
// link to the ssh agent socket of the client that attached or used it last,
// and new panes get SSH_AUTH_SOCK naming the link.

// fakeAgent listens on a Unix socket named agent.sock in a new folder with
// the given mode, and returns the socket's path. Nothing speaks the agent
// protocol on it: the daemon only checks what the path is.
func fakeAgent(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	// The daemon links to the resolved path, so the test compares with it.
	resolved, err := filepath.EvalSymlinks(sock)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// fakeAgentUnder is fakeAgent in a private folder whose parent anyone can
// write to, which is not sticky. Another user could rename the private folder
// away and put their own in its place.
func fakeAgentUnder(t *testing.T) string {
	t.Helper()
	parent, err := os.MkdirTemp("", "agp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	dir := filepath.Join(parent, "s")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	if err := os.Chmod(parent, 0o777); err != nil {
		t.Fatal(err)
	}
	return sock
}

// agentLink is tuios ssh-agent-path --json for session.
type agentLink struct {
	Path   string `json:"path"`
	Follow bool   `json:"follow"`
	Target string `json:"target"`
}

func readAgentLink(t *testing.T, base, session string) agentLink {
	t.Helper()
	out, err := tuiosCLI(t, base, "ssh-agent-path", "-s", session, "--json")
	if err != nil {
		t.Fatalf("ssh-agent-path: %v\n%s", err, out)
	}
	var l agentLink
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		t.Fatalf("decode ssh-agent-path: %v\n%s", err, out)
	}
	return l
}

// listClientSessions is the session of every attached client.
func listClientSessions(t *testing.T, base string) []string {
	t.Helper()
	out, err := tuiosCLI(t, base, "list-clients", "--json")
	if err != nil {
		t.Fatalf("list-clients: %v\n%s", err, out)
	}
	var rows []struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("decode list-clients: %v\n%s", err, out)
	}
	var sessions []string
	for _, r := range rows {
		if r.Session != "" {
			sessions = append(sessions, r.Session)
		}
	}
	return sessions
}

// waitLinkTo waits for the link at path to point at want, or to be gone when
// want is "". It reads the link on disk, not the daemon's report.
func waitLinkTo(t *testing.T, path, want, what string) {
	t.Helper()
	deadline := time.Now().Add(uiTimeout)
	got := ""
	for time.Now().Before(deadline) {
		target, err := os.Readlink(path)
		got = target
		if err != nil {
			got = "(no link: " + err.Error() + ")"
		}
		if (want == "" && err != nil && os.IsNotExist(err)) || (want != "" && target == want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if want == "" {
		want = "(no link)"
	}
	t.Fatalf("%s: the agent link points at %s, want %s", what, got, want)
}

// agentClient attaches a client to session with SSH_AUTH_SOCK set to sock.
func agentClient(t *testing.T, base, session, sock string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"attach", session}, env: []string{"SSH_AUTH_SOCK=" + sock}})
	// Read from the daemon, not the screen: a client inside a pane can
	// shrink the session until the dock no longer shows a window count.
	deadline := time.Now().Add(bootTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-clients", "--json")
		var rows []struct {
			PID     int    `json:"pid"`
			Session string `json:"session"`
		}
		if json.Unmarshal([]byte(out), &rows) == nil && slices.ContainsFunc(rows, func(r struct {
			PID     int    `json:"pid"`
			Session string `json:"session"`
		}) bool {
			return r.PID == term.Pid() && r.Session == session
		}) {
			return term
		}
		if time.Now().After(deadline) {
			t.Fatalf("client with agent %s never attached %s\n%s", sock, session, term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSSHAgentLinkFollowsTheNewestClient attaches two clients with different
// fake agent sockets. The link follows the attach order. Clients whose socket
// is in a folder anyone can write to, or is a symlink, do not move it. A new
// pane gets SSH_AUTH_SOCK naming the link and finds a socket there. When the
// newest client detaches the link goes back to the first, and when that one
// drops too the link is removed.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The ssh agent link".
func TestSSHAgentLinkFollowsTheNewestClient(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	// No socket of the daemon's own, so the link is removed, not moved to a
	// fallback, when the last client leaves. See the fallback test.
	t.Setenv("SSH_AUTH_SOCK", "")
	writeConfig(t, base, "[daemon]\nssh_agent = \"follow\"\n")
	const sess = "e2e-agent"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	sockA := fakeAgent(t, 0o700)
	sockB := fakeAgent(t, 0o700)
	open := fakeAgent(t, 0o777)
	// ~/.ssh/agent.sock and the 1Password agent are links to the socket.
	linked := filepath.Join(filepath.Dir(sockA), "linked.sock")
	if err := os.Symlink(sockA, linked); err != nil {
		t.Fatal(err)
	}
	linkedOpen := filepath.Join(filepath.Dir(sockA), "linked-open.sock")
	if err := os.Symlink(open, linkedOpen); err != nil {
		t.Fatal(err)
	}

	l := readAgentLink(t, base, sess)
	if !l.Follow || l.Path == "" || l.Target != "" {
		t.Fatalf("before any client: ssh-agent-path = %+v, want follow, a path and no target", l)
	}
	link := l.Path

	first := agentClient(t, base, sess, sockA)
	waitLinkTo(t, link, sockA, "after the first client attached")
	second := agentClient(t, base, sess, sockB)
	waitLinkTo(t, link, sockB, "after the second client attached")

	// A symlink is resolved once: the link points at the socket it names.
	viaLink := agentClient(t, base, sess, linked)
	waitLinkTo(t, link, sockA, "after a client with a symlink to the first socket attached")
	if err := viaLink.Close(); err != nil {
		t.Logf("close the client with a symlink: %v", err)
	}
	waitLinkTo(t, link, sockB, "after the client with a symlink left")

	// Refused sockets: the link stays on the second client.
	for _, bad := range []struct{ name, sock string }{
		{"a socket in a folder anyone can write to", open},
		{"a symlink to a socket in a folder anyone can write to", linkedOpen},
		{"a socket under a folder anyone can write to", fakeAgentUnder(t)},
		{"a path that is not there", filepath.Join(filepath.Dir(sockA), "missing.sock")},
	} {
		c := agentClient(t, base, sess, bad.sock)
		time.Sleep(300 * time.Millisecond)
		if got, _ := os.Readlink(link); got != sockB {
			t.Fatalf("a client with %s moved the link to %s", bad.name, got)
		}
		if err := c.Close(); err != nil {
			t.Logf("close the client with %s: %v", bad.name, err)
		}
	}
	waitLinkTo(t, link, sockB, "after the refused clients left")

	// A new pane is started with the link.
	if out, err := tuiosCLI(t, base, "new-window", "-s", sess, "agentprobe"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	// The pane prints the last part of the path, which fits on one line, and
	// whether a socket answers at the full path.
	probe := "echo AGENT=${SSH_AUTH_SOCK##*/}; test -S \"$SSH_AUTH_SOCK\" && echo SOCK_\"\"OK\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, probe); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := second.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "SOCK_OK") && strings.Contains(text, "AGENT="+filepath.Base(link))
	}, uiTimeout); err != nil {
		t.Fatalf("the new pane did not get the agent link %s: %v\n%s", link, err, second.Snapshot())
	}
	saveArtifact(t, second, artifactDir(t), "ssh-agent-new-pane")

	// A client that runs inside a pane is not the person: its socket does not
	// count. It attaches a second session from the probe pane. The positive
	// half: the same socket from a client outside every pane makes the link.
	const other = "e2e-agent-other"
	if out, err := tuiosCLI(t, base, "new", other, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	sockC := fakeAgent(t, 0o700)
	otherLink := readAgentLink(t, base, other).Path
	nested := "SSH_AUTH_SOCK=" + sockC + " " + tuiosBin + " attach " + other + "\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", sess, nested); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	deadline := time.Now().Add(uiTimeout)
	for !slices.ContainsFunc(listClientSessions(t, base), func(s string) bool { return s == other }) {
		if time.Now().After(deadline) {
			t.Fatalf("the client in the pane never attached %s\n%s", other, second.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	if target, err := os.Readlink(otherLink); err == nil {
		t.Fatalf("a client inside a pane made the agent link of %s point at %s", other, target)
	}
	outside := agentClient(t, base, other, sockC)
	waitLinkTo(t, otherLink, sockC, "after a client outside every pane attached "+other)
	if err := outside.Close(); err != nil {
		t.Logf("close the client outside: %v", err)
	}
	waitLinkTo(t, otherLink, "", "after the only client with an agent left "+other)

	// The second client detaches: the link goes back to the first.
	if err := second.SendKeys(tuitest.Ctrl('b'), "d"); err != nil {
		t.Fatalf("detach the second client: %v", err)
	}
	waitExit(t, second, "the second client's detach")
	waitLinkTo(t, link, sockA, "after the second client detached")
	if l := readAgentLink(t, base, sess); l.Target != sockA {
		t.Fatalf("ssh-agent-path target = %q after the fallback, want %s", l.Target, sockA)
	}

	// The first client's connection drops: no client with an agent is left.
	if err := first.Close(); err != nil {
		t.Logf("close the first client: %v", err)
	}
	waitLinkTo(t, link, "", "after the last client left")
}

// TestSSHAgentLinkFallsBackToTheDaemonsSocket starts the daemon with an
// SSH_AUTH_SOCK of its own. A new session's link points at it before any
// client attaches, moves to a client's socket, and comes back to the
// daemon's when the client leaves, so a pane never has a dead path. A stale
// link a killed daemon left is swept at start, and kill-server removes the
// daemon's links.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The ssh agent link".
func TestSSHAgentLinkFallsBackToTheDaemonsSocket(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	own := fakeAgent(t, 0o700)
	t.Setenv("SSH_AUTH_SOCK", own)
	writeConfig(t, base, "[daemon]\nssh_agent = \"follow\"\n")
	const sess = "e2e-agent-own"
	if out, err := tuiosCLI(t, base, "new", sess, "--detach"); err != nil {
		t.Fatalf("create session: %v: %s", err, out)
	}
	link := readAgentLink(t, base, sess).Path
	waitLinkTo(t, link, own, "before any client attached")

	sockA := fakeAgent(t, 0o700)
	a := agentClient(t, base, sess, sockA)
	waitLinkTo(t, link, sockA, "after a client attached")
	if err := a.Close(); err != nil {
		t.Logf("close the client: %v", err)
	}
	waitLinkTo(t, link, own, "after the client left")

	// Stop: the daemon's links go with it.
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	waitLinkTo(t, link, "", "after kill-server")

	// Start: links left by a daemon that was killed are swept, a session's
	// and a host's. The links of a daemon on another socket in the same
	// folder are not, even one whose socket is called agent.sock.
	const id = "0c0ffee0-0000-4000-8000-000000000549"
	dir := filepath.Dir(link)
	stale := []string{filepath.Join(dir, "agent-"+id+".sock"), filepath.Join(dir, "agent-link-build.sock")}
	others := []string{filepath.Join(dir, "other-agent-"+id+".sock"), filepath.Join(dir, "agent-agent-"+id+".sock")}
	for _, p := range append(append([]string{}, stale...), others...) {
		if err := os.Symlink(own, p); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := tuiosCLI(t, base, "new", sess+"-2", "--detach"); err != nil {
		t.Fatalf("start the daemon again: %v: %s", err, out)
	}
	for _, p := range stale {
		waitLinkTo(t, p, "", "after the daemon started again")
	}
	for _, p := range others {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("the sweep removed %s, a link of another socket's daemon: %v", filepath.Base(p), err)
		}
	}
	waitLinkTo(t, readAgentLink(t, base, sess+"-2").Path, own, "a session made after the restart")
}
