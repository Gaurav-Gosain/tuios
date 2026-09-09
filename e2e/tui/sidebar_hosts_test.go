package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// hostOpen and hostShut are the fold marks a machine's header wears on the
// rail, open and folded, in the glyph set the harness's terminal draws.
const (
	hostOpen = "▾"
	hostShut = "▸"
)

// This is federation stage 1 driven the way a user reaches it: a real config
// file with a [hosts] table, a real daemon, a real ssh subprocess, the real
// stdio proxy, and the real rail.
//
// The ssh stand-in below is why this can run at all. It drops ssh's options and
// the address and runs the command locally, so the link goes out through the
// same subprocess transport, the same framing and the same proxy that a real
// ssh would carry, and reaches this test's own daemon instead of a machine
// somewhere. Nothing here reads the developer's ssh config, known_hosts or
// agent, and no network connection is made.
//
// The command runs through `sh -c` on the joined words, which is what sshd
// does on the far side: the command reaches a shell as one string and the
// shell re-parses it. That is what lets the link's own probe for the tuios
// binary, which is a quoted shell script, run here the way it runs over ssh.

// writeFakeSSH puts an ssh stand-in in dir and returns its path.
func writeFakeSSH(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\n" +
		"while [ $# -gt 0 ]; do\n" +
		"  case \"$1\" in\n" +
		"    -o) shift 2 ;;\n" +
		"    -T) shift ;;\n" +
		"    -t) shift ;;\n" +
		"    *) break ;;\n" +
		"  esac\n" +
		"done\n" +
		"shift\n" + // the address
		"exec /bin/sh -c \"$*\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

// writeHostsConfig writes a config file naming one reachable host and one that
// cannot be reached.
func writeHostsConfig(t *testing.T, base, tuiosPath string) {
	t.Helper()
	dir := filepath.Join(base, "XDG_CONFIG_HOME", "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config: %v", err)
	}
	body := "[hosts.build]\n" +
		"addr = \"someone@buildbox\"\n" +
		"command = \"" + tuiosPath + "\"\n" +
		"connect_timeout = 5\n\n" +
		"[hosts.offline]\n" +
		"addr = \"someone@poweredoff\"\n" +
		"command = \"/nonexistent/tuios\"\n" +
		"connect_timeout = 2\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestSidebarGroupsSessionsByHost is the on-screen proof for stage 1. The rail
// shows a group for the host that answers, its sessions under it, and a row for
// the host that does not answer marked offline.
func TestSidebarGroupsSessionsByHost(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-e2e"}, env: []string{"TUIOS_SSH=" + ssh}})
	waitBoot(t, term)

	toggleSidebarViaPalette(t, term)

	// The host that answers gets a group header. The wait is what makes this a
	// test of the link rather than of the config: the row only appears after
	// the daemon has run ssh, spoken the framing to the proxy, and had a
	// listing come back.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), hostOpen+" build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never showed the host group: %v\n%s", err, term.Snapshot())
	}

	// The host that cannot be reached keeps its row and says so.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, hostOpen+" offline") && strings.Contains(text, "offline")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never showed the unreachable host: %v\n%s", err, term.Snapshot())
	}

	t.Logf("rail with host groups:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-host-groups")
}

// TestRailShowsAHostAddedWhileAttached is the refresh proof. The client is
// attached to a daemon with no hosts, which is the default install and the
// state in which the rail stops asking about hosts. A host is then added from
// the command line, and the rail shows it without the client reattaching.
//
// What would pass a weaker test and fail this one: a client whose only way to
// learn about the first host is its own poll, since that poll stopped for
// good on the daemon's first answer. The daemon has to tell it, and the wait
// below is what proves it did.
func TestRailShowsAHostAddedWhileAttached(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}

	term := startIn(t, base, startOpts{args: []string{"new", "fed-live"}, env: env})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)
	railShows(t, term, "sessions")
	// The first poll is one local verb call. It has answered, and stopped the
	// polling, long before this returns; the wait is what keeps the add from
	// racing it, since an add that lands before the first answer would be
	// found by that answer and prove nothing.
	time.Sleep(2 * time.Second)
	if strings.Contains(term.Screen().Text(), hostOpen+" local") {
		t.Fatalf("the rail shows a machine group with no hosts configured:\n%s", term.Snapshot())
	}
	saveFrame(t, term, "rail-host-add-before")

	out, err := tuiosCLIEnv(t, base, env, "hosts", "add", "build", "someone@buildbox",
		"--command", tuiosBin, "--connect-timeout", "5")
	if err != nil {
		t.Fatalf("tuios hosts add: %v\n%s", err, out)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, hostOpen+" build") && strings.Contains(text, hostOpen+" local")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail did not show the host added while the client was attached: %v\n%s", err, term.Snapshot())
	}
	t.Logf("the rail after a host was added on the command line:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-host-add-after")
	alive(t, term, "after a host was added while attached")
}

// TestHostsCommandReportsALinkEndToEnd drives `tuios hosts` against the same
// daemon, so the CLI half is proved on the same link.
func TestHostsCommandReportsALinkEndToEnd(t *testing.T) {
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-e2e"}, env: []string{"TUIOS_SSH=" + ssh}})
	waitBoot(t, term)

	out, err := tuiosCLI(t, base, "hosts")
	if err != nil {
		t.Fatalf("tuios hosts: %v\n%s", err, out)
	}
	t.Logf("tuios hosts:\n%s", out)

	if !strings.Contains(out, "build") || !strings.Contains(out, "up") {
		t.Errorf("the reachable host is not reported as up:\n%s", out)
	}
	if !strings.Contains(out, "unreachable") {
		t.Errorf("the unreachable host is not reported:\n%s", out)
	}
	// The listing says what a person does next with an up host.
	if !strings.Contains(out, "tuios attach --host") {
		t.Errorf("the listing does not say how to attach a session on a host:\n%s", out)
	}
}
