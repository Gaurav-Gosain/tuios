package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

const (
	hostOpen = "▾"
	hostShut = "▸"
)

const hostPollActive = 5 * time.Second

func writeFakeSSH(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-ssh")
	script := "#!/bin/sh\n" +
		"while [ $# -gt 0 ]; do\n" +
		"  case \"$1\" in\n" +
		"    -o) shift 2 ;;\n" +
		"    -T) shift ;;\n" +
		"    -t) shift ;;\n" +
		"    --) shift; break ;;\n" +
		"    *) break ;;\n" +
		"  esac\n" +
		"done\n" +
		"shift\n" +
		"case \"$*\" in\n" +
		"  *stdio-proxy*) TUIOS_E2E_RELAY_CMD=\"$*\" exec env " + relaySockEnv + "=" + startSSHRelay(t) + " " + testBinary(t) + " -test.run='^$' ;;\n" +
		"esac\n" +
		"exec /bin/sh -c \"$*\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write the ssh stand-in: %v", err)
	}
	return path
}

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
	requireReal(t, "real ssh or sshd")
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-e2e"}, env: []string{"TUIOS_SSH=" + ssh}})
	waitBoot(t, term)

	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), hostOpen+" build")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never showed the host group: %v\n%s", err, term.Snapshot())
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, hostOpen+" offline") && strings.Contains(text, "offline")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never showed the unreachable host: %v\n%s", err, term.Snapshot())
	}

	t.Logf("rail with host groups:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-host-groups")
}

// TestDraggingAMachineHeaderReordersTheRail is the on-screen proof for the
// second half of "the rail should stay fixed and the user should be able to
// reorder": the machines start in the daemon's sorted order, a drag on a
// header puts them in the user's, and a poll later they are still in it.
//
// What would pass a weaker test and fail this one: a drag that reorders the
// rail for one frame and is then overwritten by the next host poll, which
// rebuilds the section from the daemon's own order.
func TestDraggingAMachineHeaderReordersTheRail(t *testing.T) {
	requireReal(t, "real ssh or sshd")
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	writeHostsConfig(t, base, tuiosBin)

	term := startIn(t, base, startOpts{args: []string{"new", "fed-drag"}, env: []string{"TUIOS_SSH=" + ssh}})
	waitBoot(t, term)
	toggleSidebarViaPalette(t, term)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, o := railRowOf(s, hostOpen+" build"), railRowOf(s, hostOpen+" offline")
		return b >= 0 && o >= 0 && b < o
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the rail never listed build above offline: %v\n%s", err, term.Snapshot())
	}
	t.Logf("before the drag:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-machine-order-before")

	s := term.Screen()
	from, to := railRowOf(s, hostOpen+" offline"), railRowOf(s, hostOpen+" build")
	mouseDrag(t, term, 3, from, 3, to, tuitest.MouseLeft, 0)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		b, o := railRowOf(s, hostOpen+" build"), railRowOf(s, hostOpen+" offline")
		return b >= 0 && o >= 0 && o < b
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the drag did not put offline above build: %v\n%s", err, term.Snapshot())
	}
	t.Logf("after the drag:\n%s", term.Snapshot())
	saveFrame(t, term, "rail-machine-order-after")

	if l, o := railRowOf(term.Screen(), hostOpen+" local"), railRowOf(term.Screen(), hostOpen+" offline"); l < 0 || l > o {
		t.Errorf("ASSERTION: the drag moved this machine out of the first slot (local=%d offline=%d):\n%s", l, o, term.Snapshot())
	}

	time.Sleep(2 * hostPollActive)
	if b, o := railRowOf(term.Screen(), hostOpen+" build"), railRowOf(term.Screen(), hostOpen+" offline"); o > b {
		t.Errorf("ASSERTION: a host poll put the machines back in the daemon's order (build=%d offline=%d):\n%s", b, o, term.Snapshot())
	}
	alive(t, term, "after dragging a machine header")
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
	requireReal(t, "real ssh or sshd")
	base := t.TempDir()
	ssh := writeFakeSSH(t, base)
	env := []string{"TUIOS_SSH=" + ssh}
	writeConfig(t, base, "[appearance.sidebar]\nenabled = true\n")

	term := startIn(t, base, startOpts{args: []string{"new", "fed-live"}, env: env})
	waitBoot(t, term)
	railShows(t, term, "sessions")
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
	requireReal(t, "real ssh or sshd")
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
	if !strings.Contains(out, "tuios attach --host") {
		t.Errorf("the listing does not say how to attach a session on a host:\n%s", out)
	}
	if !strings.Contains(out, "LOAD") || !strings.Contains(out, " cores") {
		t.Errorf("ASSERTION: the listing does not show the host's load:\n%s", out)
	}
	out, err = tuiosCLI(t, base, "hosts", "--json")
	if err != nil {
		t.Fatalf("tuios hosts --json: %v\n%s", err, out)
	}
	var res struct {
		Hosts []struct {
			Host      string   `json:"host"`
			Cores     int      `json:"cores"`
			Load1     *float64 `json:"load1"`
			RTTMillis int64    `json:"rtt_ms"`
			LoadAt    int64    `json:"load_at"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("tuios hosts --json did not print JSON: %v\n%s", err, out)
	}
	for _, h := range res.Hosts {
		switch h.Host {
		case "build":
			if h.Cores != runtime.NumCPU() || h.RTTMillis <= 0 || h.LoadAt == 0 {
				t.Errorf("ASSERTION: build reports cores %d (want %d), rtt_ms %d, load_at %d:\n%s", h.Cores, runtime.NumCPU(), h.RTTMillis, h.LoadAt, out)
			}
		case "offline":
			if h.Cores != 0 || h.Load1 != nil {
				t.Errorf("ASSERTION: the unreachable host reports a load:\n%s", out)
			}
		}
	}
}
