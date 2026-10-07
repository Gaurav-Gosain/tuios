package tuie2e

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// remoteBase that forwards the agent the way ssh -A does. With -A, the far
// command runs with SSH_AUTH_SOCK naming relay, a socket that sshd would
// make, and every connection to relay reaches the socket the stand-in's own
// SSH_AUTH_SOCK names at that moment. The stand-in writes that path to
// agentPath, and the relay the test runs reads it per connection, as ssh
// opens its SSH_AUTH_SOCK per request. Without -A, the far command has no
// SSH_AUTH_SOCK.
func forwardingSSH(t *testing.T, dir, remoteBase, relay, agentPath string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh-agent")
	var b strings.Builder
	b.WriteString("#!/bin/sh\nfwd=\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T|-t) shift ;;\n    -A) fwd=1; shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("shift\n")
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
		t.Fatalf("the far pane did not reach the hub client's agent (the link's ssh had SSH_AUTH_SOCK %q): %v\n%s", raw, err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "ssh-agent-through-host")
}
