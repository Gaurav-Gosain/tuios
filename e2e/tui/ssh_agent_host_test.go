package tuie2e

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Discussion #549 also asks for the agent to follow through a host: a person
// attaches a session on another machine from this one, and ssh in that
// session's panes uses the person's agent. The link's ssh forwards the agent
// when the person asks for it per host (-A in ssh_options). This machine's
// daemon starts that ssh with SSH_AUTH_SOCK naming its own agent link, which
// points at the agent of the client attached here. On the other machine,
// tuios stdio-proxy reports the forwarded socket, and that daemon follows it
// for a client attached through the link.

// testAgent starts a real ssh-agent on a socket in a private folder, holding
// one key whose comment is comment, and returns the socket. It skips the test
// when the OpenSSH tools are not installed.
func testAgent(t *testing.T, comment string) string {
	t.Helper()
	for _, tool := range []string{"ssh-agent", "ssh-add", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	dir, err := os.MkdirTemp("", "ta")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.sock")
	agent := exec.Command("ssh-agent", "-D", "-a", sock)
	if err := agent.Start(); err != nil {
		t.Fatalf("start ssh-agent: %v", err)
	}
	t.Cleanup(func() {
		_ = agent.Process.Kill()
		_ = agent.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ssh-agent never made its socket")
		}
		time.Sleep(20 * time.Millisecond)
	}
	key := filepath.Join(dir, "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", comment, "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	add := exec.Command("ssh-add", key)
	add.Env = append(os.Environ(), "SSH_AUTH_SOCK="+sock)
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("ssh-add: %v\n%s", err, out)
	}
	resolved, err := filepath.EvalSymlinks(sock)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// forwardingSSH writes an ssh stand-in for a link to the daemon rooted at
// remoteBase that forwards the agent the way ssh -A does. It forwards with -A,
// and for an address that starts with fwd@, which stands for ForwardAgent yes
// in ~/.ssh/config: ssh -G says forwardagent yes for such an address and no
// for any other. When it forwards, the far command runs with SSH_AUTH_SOCK
// naming relay, a socket that sshd would make, and every connection to relay
// reaches the socket the stand-in's own SSH_AUTH_SOCK names at that moment:
// the stand-in writes that path to agentPath, and the relay reads it per
// connection, as ssh opens its SSH_AUTH_SOCK per request. Without forwarding
// the far command has no SSH_AUTH_SOCK.
//
// Every run also writes the SSH_AUTH_SOCK it got to env-ADDR in dir, with
// ADDR's punctuation as underscores, so a test can read what each link's ssh
// was started with.
func forwardingSSH(t *testing.T, dir, remoteBase, relay, agentPath string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh-agent")
	var b strings.Builder
	b.WriteString(`#!/bin/sh
if [ "$1" = -G ]; then
  for a; do last=$a; done
  case "$last" in fwd@*) echo 'forwardagent yes' ;; *) echo 'forwardagent no' ;; esac
  exit 0
fi
fwd=
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    -T|-t) shift ;;
    -A) fwd=1; shift ;;
    --) shift; break ;;
    *) break ;;
  esac
done
addr=$1
shift
case "$addr" in fwd@*) fwd=1 ;; esac
`)
	b.WriteString("printf '%s' \"$SSH_AUTH_SOCK\" > " + dir + "/env-$(printf '%s' \"$addr\" | tr -c 'A-Za-z0-9' _)\n")
	b.WriteString("if [ -n \"$fwd\" ]; then\n  printf '%s' \"$SSH_AUTH_SOCK\" > " + agentPath + "\n  export SSH_AUTH_SOCK=" + relay + "\nelse\n  unset SSH_AUTH_SOCK\nfi\n")
	for _, key := range xdgKeys {
		b.WriteString("export " + key + "=" + xdgDir(remoteBase, key) + "\n")
	}
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

// linkEnv waits for the link ssh to addr to have run, and returns the
// SSH_AUTH_SOCK it was started with.
func linkEnv(t *testing.T, dir, addr string) string {
	t.Helper()
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, addr)
	deadline := time.Now().Add(bootTimeout)
	for {
		if raw, err := os.ReadFile(filepath.Join(dir, "env-"+name)); err == nil {
			return string(raw)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the link ssh to %s never ran", addr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// agentRelay listens on a socket in a private folder and joins every
// connection to the socket named in agentPath, read when the connection
// comes. It returns the relay's socket.
func agentRelay(t *testing.T, agentPath string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "agent.relay")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				target, err := os.ReadFile(agentPath)
				if err != nil {
					return
				}
				up, err := net.Dial("unix", string(target))
				if err != nil {
					return
				}
				defer func() { _ = up.Close() }()
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	resolved, err := filepath.EvalSymlinks(sock)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// TestSSHAgentFollowsThroughAHost runs a hub and a remote daemon on one
// machine, both with ssh_agent = "follow", joined by a link with -A. The hub
// daemon starts with no agent of its own, and so does the remote one. A
// person attaches the remote session from the hub with a real ssh-agent, and
// ssh-add -l in the remote pane lists that agent's key.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The ssh agent link".
func TestSSHAgentFollowsThroughAHost(t *testing.T) {
	agent := testAgent(t, "hk549")
	base := t.TempDir()
	killDaemon(t, base)
	remote := remoteMachine(t)

	agentPath := filepath.Join(base, "forwarded-agent")
	relay := agentRelay(t, agentPath)
	ssh := forwardingSSH(t, base, remote, relay, agentPath)

	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("TUIOS_SSH", ssh)
	writeConfig(t, remote, "[daemon]\nssh_agent = \"follow\"\n")
	writeConfig(t, base, "[daemon]\nssh_agent = \"follow\"\n\n"+
		"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\nssh_options = [\"-A\"]\n")
	if out, err := tuiosCLI(t, remote, "new", "far-agent", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}

	term := startIn(t, base, startOpts{
		args: []string{"attach", "--host", "build", "far-agent"},
		env:  []string{"SSH_AUTH_SOCK=" + agent, "TUIOS_SSH=" + ssh},
	})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰──") }, bootTimeout); err != nil {
		t.Fatalf("the client never drew the far session: %v\n%s", err, term.Snapshot())
	}

	// The pane was started with the far session's link, which now points
	// at the forwarded socket. ssh-add asks the person's agent through it.
	// The comment is printed on a line of its own, so the pane's width
	// cannot split it.
	line := "ssh-add -l >/dev/null; echo ADD_EXIT=$?; ssh-add -L | awk '{print \"KEY=\" $3}'\n"
	if out, err := tuiosCLI(t, remote, "send-text", "-s", "far-agent", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "KEY=hk549") && strings.Contains(text, "ADD_EXIT=0")
	}, uiTimeout); err != nil {
		raw, _ := os.ReadFile(agentPath)
		hubTarget, _ := os.Readlink(string(raw))
		far := readAgentLink(t, remote, "far-agent")
		t.Fatalf("the far pane did not reach the hub client's agent (the link's ssh had SSH_AUTH_SOCK %q, pointing at %q; the far link %+v): %v\n%s", raw, hubTarget, far, err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "ssh-agent-through-host")
}

// hubWithHost sets up a hub and a remote daemon on one machine, both with
// ssh_agent = "follow", and a host build whose link forwards with -A. The far
// session is far-agent, and the hub holds a local session home. Neither
// daemon has an agent of its own. It returns the hub's root, the remote's,
// the ssh stand-in and the folder the stand-in records in.
func hubWithHost(t *testing.T, extraHosts string) (base, remote, ssh string) {
	t.Helper()
	base = t.TempDir()
	killDaemon(t, base)
	remote = remoteMachine(t)
	agentPath := filepath.Join(base, "forwarded-agent")
	relay := agentRelay(t, agentPath)
	ssh = forwardingSSH(t, base, remote, relay, agentPath)
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv("TUIOS_SSH", ssh)
	writeConfig(t, remote, "[daemon]\nssh_agent = \"follow\"\n")
	writeConfig(t, base, "[daemon]\nssh_agent = \"follow\"\n\n"+
		"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\nssh_options = [\"-A\"]\n"+extraHosts)
	if out, err := tuiosCLI(t, remote, "new", "far-agent", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	return base, remote, ssh
}

// farKeyRuns numbers the farKey calls, so each one waits for its own line and
// never for one an earlier call left on the screen.
var farKeyRuns atomic.Int64

// farKey runs ssh-add in the far pane, typed by the remote daemon and not by
// any client, and waits for term to show the comment of the key the agent
// holds.
func farKey(t *testing.T, remote string, term *tuitest.Terminal, want, what string) {
	t.Helper()
	tag := fmt.Sprintf("R%d", farKeyRuns.Add(1))
	// The tag is split in the typed line, so only the output carries it whole.
	line := "clear; ssh-add -L | awk '{print \"" + tag[:1] + "\" \"" + tag[1:] + "KEY=\" $3}'\n"
	if out, err := tuiosCLI(t, remote, "send-text", "-s", "far-agent", line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), tag+"KEY=")
	}, uiTimeout); err != nil {
		t.Fatalf("%s: the far pane never answered: %v\n%s", what, err, term.Snapshot())
	}
	if !strings.Contains(term.Snapshot(), tag+"KEY="+want) {
		t.Fatalf("%s: the far pane reaches another agent than the one holding %s\n%s", what, want, term.Snapshot())
	}
}

// TestSSHAgentHostLinkFollowsOnlyThatHostsClients puts two clients on the
// hub: A attaches the far session through the link with agent A, and B
// attaches the hub's own session with agent B. B attached last, and the far
// pane must still reach A: a local session on the hub does not move the
// link of a host. Then C attaches the far session with agent C, and the far
// pane reaches C. Then A types, and the far pane reaches A again: typing on
// the host counts as using it.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The ssh agent link".
func TestSSHAgentHostLinkFollowsOnlyThatHostsClients(t *testing.T) {
	agentA := testAgent(t, "ka549")
	agentB := testAgent(t, "kb549")
	agentC := testAgent(t, "kc549")
	base, remote, ssh := hubWithHost(t, "")

	onHost := func(agent string) *tuitest.Terminal {
		term := startIn(t, base, startOpts{
			args: []string{"attach", "--host", "build", "far-agent"},
			env:  []string{"SSH_AUTH_SOCK=" + agent, "TUIOS_SSH=" + ssh},
		})
		if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰──") }, bootTimeout); err != nil {
			t.Fatalf("a client never drew the far session: %v\n%s", err, term.Snapshot())
		}
		return term
	}
	a := onHost(agentA)
	farKey(t, remote, a, "ka549", "with A attached through the link")

	b := startIn(t, base, startOpts{args: []string{"attach", "home"}, env: []string{"SSH_AUTH_SOCK=" + agentB, "TUIOS_SSH=" + ssh}})
	if err := b.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("B never attached home: %v\n%s", err, b.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	farKey(t, remote, a, "ka549", "after B attached a session of the hub")

	c := onHost(agentC)
	farKey(t, remote, c, "kc549", "after C attached through the link")

	// A types in the far pane. Its keys cross the hub's relay.
	if err := a.SendKeys("echo TYPED-$((5*7))\r"); err != nil {
		t.Fatalf("type in A: %v", err)
	}
	if err := a.WaitForText("TYPED-35", uiTimeout); err != nil {
		t.Fatalf("A's keys never reached the far pane: %v\n%s", err, a.Snapshot())
	}
	// Input counts at most once a second, so the hub's link for build can
	// move up to a second after the keys.
	hubLink := filepath.Join(filepath.Dir(readAgentLink(t, base, "home").Path), "agent-link-build.sock")
	waitLinkTo(t, hubLink, agentA, "the hub's link for build after A typed")
	farKey(t, remote, a, "ka549", "after A typed")
	saveArtifact(t, a, artifactDir(t), "ssh-agent-host-typing")
	alive(t, b, "B on the hub's own session")
}

// TestSSHAgentLinkLeavesANonForwardingHostAlone starts the hub daemon with an
// agent of its own and three hosts: build forwards with -A, cfg forwards
// because ssh -G says so for its address, and plain does not forward. The
// links of build and cfg start with SSH_AUTH_SOCK naming their own host link.
// The link of plain starts with the daemon's own SSH_AUTH_SOCK, unchanged.
//
// Negative control: with hostForwardsAgent answering yes for every host,
// plain's ssh starts with its host link.
func TestSSHAgentLinkLeavesANonForwardingHostAlone(t *testing.T) {
	own := fakeAgent(t, 0o700)
	hosts := "\n[hosts.cfg]\naddr = \"fwd@cfgbox\"\ncommand = \"" + tuiosBin + "\"\nconnect_timeout = 5\n" +
		"\n[hosts.plain]\naddr = \"someone@plainbox\"\ncommand = \"" + tuiosBin + "\"\nconnect_timeout = 5\n"
	base := t.TempDir()
	killDaemon(t, base)
	remote := remoteMachine(t)
	agentPath := filepath.Join(base, "forwarded-agent")
	ssh := forwardingSSH(t, base, remote, agentRelay(t, agentPath), agentPath)
	t.Setenv("SSH_AUTH_SOCK", own)
	t.Setenv("TUIOS_SSH", ssh)
	writeConfig(t, base, "[daemon]\nssh_agent = \"follow\"\n\n"+
		"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\nssh_options = [\"-A\"]\n"+hosts)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	dir := filepath.Dir(readAgentLink(t, base, "home").Path)
	for _, h := range []struct{ name, addr string }{{"build", "someone@buildbox"}, {"cfg", "fwd@cfgbox"}} {
		if got, want := linkEnv(t, base, h.addr), filepath.Join(dir, "agent-link-"+h.name+".sock"); got != want {
			t.Fatalf("the link ssh to %s, which forwards, started with SSH_AUTH_SOCK %q, want its host link %q", h.name, got, want)
		}
	}
	if got := linkEnv(t, base, "someone@plainbox"); got != own {
		t.Fatalf("the link ssh to plain, which does not forward, started with SSH_AUTH_SOCK %q, want the daemon's own %q", got, own)
	}
}

// TestSSHAgentFollowWithAnOlderDaemon runs the host link against daemons from
// a build before ssh_agent follow, named by TUIOS_E2E_OLD_BIN (the suite does
// not build it). Such a daemon refuses the ssh_auth_sock parameter it does not
// know, and the newer side must ask again without it rather than fail:
//
//   - far: the far daemon is the old build and the proxy is this one, as on a
//     host whose tuios was upgraded while its daemon ran. The proxy's
//     link-peer carries the forwarded socket, the old daemon refuses it, and
//     the proxy sends link-peer again.
//   - hub: the hub daemon is the old build and the client is this one. The
//     client's open-host-connection carries its socket, the old daemon
//     refuses it, and the client asks again.
//
// In both the client must draw the far session.
//
// Negative controls: see NEGATIVE_CONTROLS.md, "The ssh agent link".
func TestSSHAgentFollowWithAnOlderDaemon(t *testing.T) {
	old := os.Getenv("TUIOS_E2E_OLD_BIN")
	if old == "" {
		t.Skip("TUIOS_E2E_OLD_BIN is not set")
	}
	agent := testAgent(t, "ko549")
	withOld := func(f func()) {
		prev := tuiosBin
		tuiosBin = old
		defer func() { tuiosBin = prev }()
		f()
	}
	setup := func(t *testing.T, oldFar, oldHub bool) (base, ssh string) {
		base = t.TempDir()
		killDaemon(t, base)
		remote := remoteMachine(t)
		agentPath := filepath.Join(base, "forwarded-agent")
		ssh = forwardingSSH(t, base, remote, agentRelay(t, agentPath), agentPath)
		t.Setenv("SSH_AUTH_SOCK", "")
		t.Setenv("TUIOS_SSH", ssh)
		writeConfig(t, remote, "[daemon]\nssh_agent = \"follow\"\n")
		writeConfig(t, base, "[daemon]\nssh_agent = \"follow\"\n\n"+
			"[hosts.build]\naddr = \"someone@buildbox\"\ncommand = \""+tuiosBin+"\"\nconnect_timeout = 5\nssh_options = [\"-A\"]\n")
		far := func() {
			if out, err := tuiosCLI(t, remote, "new", "far-agent", "--detach"); err != nil {
				t.Fatalf("create the far session: %v\n%s", err, out)
			}
		}
		hub := func() {
			if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
				t.Fatalf("start the hub daemon: %v\n%s", err, out)
			}
		}
		if oldFar {
			withOld(far)
		} else {
			far()
		}
		if oldHub {
			withOld(hub)
		} else {
			hub()
		}
		return base, ssh
	}
	for _, c := range []struct {
		name           string
		oldFar, oldHub bool
	}{{"far", true, false}, {"hub", false, true}} {
		t.Run(c.name, func(t *testing.T) {
			base, ssh := setup(t, c.oldFar, c.oldHub)
			term := startIn(t, base, startOpts{
				args: []string{"attach", "--host", "build", "far-agent"},
				env:  []string{"SSH_AUTH_SOCK=" + agent, "TUIOS_SSH=" + ssh},
			})
			if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰──") }, bootTimeout); err != nil {
				t.Fatalf("with an older %s daemon the client never drew the far session: %v\n%s", c.name, err, term.Snapshot())
			}
			alive(t, term, "attached through a link to an older daemon")
		})
	}
}
