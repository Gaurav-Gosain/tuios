package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
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

// writeFakeSSH puts an ssh stand-in in dir and returns its path.
func writeFakeSSH(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\n" +
		"while [ $# -gt 0 ]; do\n" +
		"  case \"$1\" in\n" +
		"    -o) shift 2 ;;\n" +
		"    -T) shift ;;\n" +
		"    *) break ;;\n" +
		"  esac\n" +
		"done\n" +
		"shift\n" + // the address
		"exec \"$@\"\n"
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
		return strings.Contains(s.Text(), "@ build")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed the host group: %v\n%s", err, term.Snapshot())
	}

	// The host that cannot be reached keeps its row and says so.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "@ offline") && strings.Contains(text, "offline")
	}, uiTimeout); err != nil {
		t.Fatalf("the rail never showed the unreachable host: %v\n%s", err, term.Snapshot())
	}

	t.Logf("rail with host groups:\n%s", term.Snapshot())
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
	// The whole promise of stage 1 in one line of output.
	if !strings.Contains(out, "read only") {
		t.Errorf("the listing does not say it is read only:\n%s", out)
	}
}
