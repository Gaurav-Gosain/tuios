package tuie2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// One user on one machine reaches one daemon, whether XDG_RUNTIME_DIR is set
// or not. A desktop session sets it; the command a host link runs over ssh
// often does not. Before the fix the desktop used $XDG_RUNTIME_DIR/tuios and
// the link used /tmp/tuios-<uid>: two daemons, each blind to the other's
// sessions.
//
// The real /run/user and /tmp hold the daemons of whoever runs the suite, so
// both roots are moved into the test with TUIOS_E2E_RUN_USER and
// TUIOS_E2E_TMP. /run/user/<uid> becomes <run>/<uid>, made 0700 the way
// pam_systemd makes it.
//
// Negative control: with sockpath.RuntimeDir going straight to "" when
// XDG_RUNTIME_DIR is unset, and pickLiveSocket returning primary, both tests
// fail. See NEGATIVE_CONTROLS.md.

// oneMachine is one machine of one user: the isolation root, the moved
// roots, and the folder that stands in for /run/user/<uid>.
type oneMachine struct {
	base     string
	run, tmp string
	// empty is a /run/user with no folder for this user, for a command that
	// must behave like a tuios that never looks there.
	empty string
	sys   string
}

func newOneMachine(t *testing.T) *oneMachine {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS has no /run/user")
	}
	mustMkdir(shortRuntimeRoot)
	short := func() string {
		dir, err := os.MkdirTemp(shortRuntimeRoot, "u")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}
	m := &oneMachine{base: t.TempDir(), run: short(), tmp: short(), empty: short()}
	m.sys = filepath.Join(m.run, strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(m.sys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(m.sys, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range xdgKeys {
		xdgDir(m.base, key)
	}
	// Both daemons, by the environment that can only reach each one.
	t.Cleanup(func() {
		for _, env := range [][]string{m.desktopEnv(), m.oldLinkEnv()} {
			_, _ = m.cli(t, env, "kill-server")
		}
	})
	return m
}

// env is the environment of a tuios on this machine, with XDG_RUNTIME_DIR
// set to runtimeDir ("" is unset) and /run/user moved to runUser.
func (m *oneMachine) env(runtimeDir, runUser string) []string {
	env := []string{"SHELL=/bin/sh", "HOME=" + m.base}
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(m.base, key))
	}
	return append(env,
		"XDG_RUNTIME_DIR="+runtimeDir,
		"TUIOS_E2E=1",
		"TUIOS_E2E_RUN_USER="+runUser,
		"TUIOS_E2E_TMP="+m.tmp)
}

// desktopEnv is a desktop session: XDG_RUNTIME_DIR=/run/user/<uid>. /run/user
// is empty for it, so it never looks in /tmp and only ever reaches the daemon
// in the runtime folder.
func (m *oneMachine) desktopEnv() []string { return m.env(m.sys, m.empty) }

// oldLinkEnv is what an older tuios run over ssh did: no runtime folder, so
// /tmp/tuios-<uid>.
func (m *oneMachine) oldLinkEnv() []string { return m.env("", m.empty) }

// sshEnv is a command over ssh on a systemd machine: no XDG_RUNTIME_DIR, and a
// /run/user/<uid> pam_systemd made.
func (m *oneMachine) sshEnv() []string { return m.env("", m.run) }

func (m *oneMachine) cli(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	pinPreV080Looks(t, m.base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, m.base)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (m *oneMachine) mustCLI(t *testing.T, env []string, args ...string) string {
	t.Helper()
	out, err := m.cli(t, env, args...)
	if err != nil {
		t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (m *oneMachine) legacySocket() string {
	return filepath.Join(m.tmp, "tuios-"+strconv.Itoa(os.Getuid()), "tuios.sock")
}

// writeLinkSSH is the ssh stand-in for a link to m: it runs the command the
// way sshd does on a systemd machine, with no XDG_RUNTIME_DIR.
func writeLinkSSH(t *testing.T, dir string, m *oneMachine) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("shift\n")
	for _, kv := range m.sshEnv() {
		if strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteString("='")
		b.WriteString(v)
		b.WriteString("'\n")
	}
	b.WriteString("unset XDG_RUNTIME_DIR\n")
	b.WriteString("exec /bin/sh -c \"$*\"\n")
	path := filepath.Join(dir, "fake-ssh-no-runtime")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestOneDaemonWithAndWithoutXDGRuntimeDir is the reported bug. The desktop
// daemon runs with XDG_RUNTIME_DIR set. A command over ssh without it, and a
// host link's stdio-proxy, must reach that same daemon and see its sessions.
func TestOneDaemonWithAndWithoutXDGRuntimeDir(t *testing.T) {
	m := newOneMachine(t)
	m.mustCLI(t, m.desktopEnv(), "new", "desk", "--detach")

	// A command over ssh sees the desktop's session, and what it makes the
	// desktop sees. Errorf, so a broken build also reports the link below.
	if out, _ := m.cli(t, m.sshEnv(), "ls"); !strings.Contains(out, "desk") {
		t.Errorf("ASSERTION: without XDG_RUNTIME_DIR, ls does not list the desktop session:\n%s", out)
	}
	m.mustCLI(t, m.sshEnv(), "new", "oversh", "--detach")
	if out, _ := m.cli(t, m.desktopEnv(), "ls"); !strings.Contains(out, "oversh") {
		t.Errorf("ASSERTION: the session made without XDG_RUNTIME_DIR is not on the desktop daemon:\n%s", out)
	}
	if _, err := os.Stat(m.legacySocket()); err == nil {
		t.Errorf("ASSERTION: a second daemon socket exists at %s", m.legacySocket())
	}

	// A host link, through the real stdio-proxy over the ssh stand-in.
	hub := t.TempDir()
	writeOneHostConfig(t, hub, tuiosBin)
	env := []string{"TUIOS_SSH=" + writeLinkSSH(t, hub, m)}
	killDaemon(t, hub)
	if out, err := tuiosCLIEnv(t, hub, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, hub, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the hub never reported build up: the link found no daemon")
	for _, name := range []string{"desk", "oversh"} {
		if out, err := tuiosCLIEnv(t, hub, env, "list-windows", "-s", "build:"+name); err != nil {
			t.Fatalf("ASSERTION: the link does not reach session %s on build: %v\n%s", name, err, out)
		}
	}
	if _, err := os.Stat(m.legacySocket()); err == nil {
		t.Fatalf("ASSERTION: the link left a second daemon socket at %s", m.legacySocket())
	}
}

// TestADaemonAnOlderLinkStartedIsFoundNotSplit is the upgrade. An older tuios
// started a daemon over ssh in /tmp/tuios-<uid>. A new client without
// XDG_RUNTIME_DIR uses that daemon rather than starting a second one in
// /run/user. When both daemons run, the one in /run/user wins, and a person
// at a terminal is told once about the other.
func TestADaemonAnOlderLinkStartedIsFoundNotSplit(t *testing.T) {
	m := newOneMachine(t)
	m.mustCLI(t, m.oldLinkEnv(), "new", "old", "--detach")
	if _, err := os.Stat(m.legacySocket()); err != nil {
		t.Fatalf("the old daemon is not at %s: %v", m.legacySocket(), err)
	}

	if out, _ := m.cli(t, m.sshEnv(), "ls"); !strings.Contains(out, "old") {
		t.Fatalf("ASSERTION: the daemon in /tmp is not found:\n%s", out)
	}
	m.mustCLI(t, m.sshEnv(), "new", "joined", "--detach")
	if out := m.mustCLI(t, m.oldLinkEnv(), "ls"); !strings.Contains(out, "joined") {
		t.Fatalf("ASSERTION: a new session went to a second daemon, not the running one:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(m.sys, "tuios", "tuios.sock")); err == nil {
		t.Fatalf("ASSERTION: a second daemon started in the runtime folder")
	}

	// The desktop daemon, as an older tuios left both running.
	m.mustCLI(t, m.desktopEnv(), "new", "desk", "--detach")

	ls := func() string {
		t.Helper()
		term := tuitest.StartT(t, []string{tuiosBin, "ls"},
			tuitest.WithSize(400, 10),
			tuitest.WithTerm("xterm-256color"),
			tuitest.WithEnv(append(m.sshEnv(), "PATH="+os.Getenv("PATH"), "GOCOVERDIR="+os.Getenv("GOCOVERDIR"))...),
			tuitest.WithDir(workDirIn(t, m.base)))
		if _, err := term.WaitExit(uiTimeout); err != nil {
			t.Fatalf("tuios ls in a terminal did not exit: %v\n%s", err, term.Snapshot())
		}
		return term.Snapshot()
	}
	first := ls()
	t.Logf("first ls:\n%s", first)
	if !strings.Contains(first, "desk") || strings.Contains(first, "joined") {
		t.Fatalf("ASSERTION: with both daemons running, ls did not use the one in the runtime folder:\n%s", first)
	}
	if !strings.Contains(first, "second tuios daemon") || !strings.Contains(first, m.legacySocket()) {
		t.Fatalf("ASSERTION: the warning about the other daemon is missing:\n%s", first)
	}
	if second := ls(); strings.Contains(second, "second tuios daemon") {
		t.Fatalf("ASSERTION: the warning showed a second time:\n%s", second)
	}

	// What the warning says to do: stop the other daemon, then resurrect.
	data, err := os.ReadFile(m.legacySocket() + ".pid")
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for processAlive(pid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	out := m.mustCLI(t, m.sshEnv(), "resurrect")
	if !strings.Contains(out, "old") || !strings.Contains(out, "joined") {
		t.Fatalf("ASSERTION: the stopped daemon's sessions are not offered by resurrect:\n%s", out)
	}
}

// TestAPaneOfTheTmpDaemonStaysWithIt: an older tuios started a daemon in
// /tmp/tuios-<uid> before /run/user/<uid> existed, and later the desktop
// daemon started in /run/user. A command typed in a pane of the /tmp daemon
// carries that daemon's TUIOS_SOCKET and must reach it, not the desktop one.
// Then the /tmp daemon stops, with /run/user/<uid> present now, and must clean
// its own folder and stop at once.
func TestAPaneOfTheTmpDaemonStaysWithIt(t *testing.T) {
	m := newOneMachine(t)
	if err := os.Remove(m.sys); err != nil {
		t.Fatal(err)
	}
	// No runtime folder yet: this daemon and its panes resolve to /tmp.
	m.mustCLI(t, m.sshEnv(), "new", "old", "--detach")
	if _, err := os.Stat(m.legacySocket()); err != nil {
		t.Fatalf("the old daemon is not at %s: %v", m.legacySocket(), err)
	}
	if err := os.Mkdir(m.sys, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(m.sys, 0o700); err != nil {
		t.Fatal(err)
	}
	m.mustCLI(t, m.desktopEnv(), "new", "desk", "--detach")

	// Typed into the old daemon's pane. oldLinkEnv reaches only that daemon,
	// so the send itself does not depend on the code under test.
	out := filepath.Join(m.base, "pane-out")
	line := tuiosBin + " list-windows -s old > " + out + ".tmp 2>&1; echo \"exit=$?\" >> " + out + ".tmp; mv " + out + ".tmp " + out + "\n"
	m.mustCLI(t, m.oldLinkEnv(), "send-text", "-s", "old", line)
	deadline := time.Now().Add(15 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(out); err == nil {
			got = data
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("list-windows in a pane of the /tmp daemon:\n%s", got)
	if got == nil {
		t.Fatalf("the command in the pane never finished")
	}
	if !strings.Contains(string(got), "exit=0") {
		t.Errorf("ASSERTION: list-windows -s old in a pane of its own daemon failed: it reached the other daemon:\n%s", got)
	}

	// The /tmp daemon stops cleanly though /run/user/<uid> now exists.
	m.mustCLI(t, m.desktopEnv(), "kill-server")
	start := time.Now()
	if o, err := m.cli(t, m.sshEnv(), "kill-server"); err != nil {
		t.Errorf("ASSERTION: kill-server of the /tmp daemon failed after %v: %v\n%s", time.Since(start), err, o)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("ASSERTION: kill-server of the /tmp daemon took %v: it waited on a socket that was never removed", took)
	}
	for _, suffix := range []string{"", ".pid", ".link", ".link-human"} {
		if _, err := os.Lstat(m.legacySocket() + suffix); err == nil {
			t.Errorf("ASSERTION: %s is left after the /tmp daemon stopped", m.legacySocket()+suffix)
		}
	}
}
