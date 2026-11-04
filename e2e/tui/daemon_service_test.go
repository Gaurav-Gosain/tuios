package tuie2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDaemonRunsAsAService installs the daemon as a systemd user unit and
// drives it through the cases the unit has to get right:
//
//   - install-service starts the daemon under the unit, with the lines that
//     matter (KillMode=process, Restart=on-failure, the display unset).
//   - kill-server stops the daemon with status 0. The unit stays stopped,
//     and a program a pane started with setsid survives the stop. With
//     KillMode=control-group systemd kills it, which is the negative control.
//   - a client start (start-server) goes through the unit, so the new daemon
//     is the unit's main process.
//   - a SIGKILL is a crash, and systemd starts the daemon again.
//   - a live upgrade keeps the main pid, and the unit stays active.
//   - uninstall-service removes the unit file and leaves the daemon running.
//
// The unit is a scratch one: a named server of its own (tuios-daemon-svcXXXX)
// with every XDG folder and HOME under the test's root, carried in the unit.
// It never touches the person's tuios-daemon unit or daemon. Each step is
// written to transcript.txt in the artifact directory.
func TestDaemonRunsAsAService(t *testing.T) {
	runtimeDir := fmt.Sprintf("/run/user/%d", os.Getuid())
	systemctl := func(args ...string) (string, error) {
		cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
		cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("no systemctl on this machine")
	}
	if _, err := systemctl("show-environment"); err != nil {
		t.Skip("no systemd user manager answers")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid on this machine")
	}

	base := t.TempDir()
	var suffix [3]byte
	_, _ = rand.Read(suffix[:])
	server := "svc" + hex.EncodeToString(suffix[:])
	unit := "tuios-daemon-" + server + ".service"
	home, _ := exec.Command("sh", "-c", "getent passwd $(id -u) | cut -d: -f6").Output()
	unitPath := filepath.Join(strings.TrimSpace(string(home)), ".config", "systemd", "user", unit)

	var transcript strings.Builder
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		transcript.WriteString(line)
		transcript.WriteString("\n")
		t.Log(line)
	}
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, append([]string{"--server", server}, args...)...)
		logf("$ tuios --server %s %s\n%s", server, strings.Join(args, " "), strings.TrimRight(out, "\n"))
		if err != nil {
			t.Fatalf("tuios %s: %v", strings.Join(args, " "), err)
		}
		return out
	}
	t.Cleanup(func() {
		// The daemon first, with its saved state, then the unit and its file.
		_, _ = tuiosCLI(t, base, "--server", server, "kill-server")
		_, _ = systemctl("stop", unit)
		_, _ = systemctl("disable", unit)
		_ = os.Remove(unitPath)
		_, _ = systemctl("daemon-reload")
		_, _ = systemctl("reset-failed", unit)
		if _, err := os.Stat(unitPath); err == nil {
			t.Errorf("the scratch unit %s is still there", unitPath)
		}
		_ = os.WriteFile(filepath.Join(artifactDir(t), "transcript.txt"), []byte(transcript.String()), 0o644)
	})

	type status struct {
		Installed    bool   `json:"installed"`
		Enabled      bool   `json:"enabled"`
		Running      bool   `json:"running"`
		Detail       string `json:"detail"`
		PID          int    `json:"pid"`
		DaemonPID    int    `json:"daemon_pid"`
		UnderService bool   `json:"under_service"`
		File         string `json:"file"`
	}
	read := func() status {
		t.Helper()
		var st status
		out := cli("daemon", "service-status", "--json")
		if err := json.Unmarshal([]byte(out), &st); err != nil {
			t.Fatalf("service-status --json: %v: %s", err, out)
		}
		return st
	}
	waitFor := func(what string, ok func(status) bool) status {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			st := read()
			if ok(st) {
				return st
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the service did not get there: %+v", what, st)
			}
			time.Sleep(300 * time.Millisecond)
		}
	}

	// The unit as it will be written.
	dry := cli("daemon", "install-service", "--dry-run")
	for _, want := range []string{
		"KillMode=process", "Restart=on-failure", "UnsetEnvironment=DISPLAY WAYLAND_DISPLAY XAUTHORITY",
		"daemon --service", "TUIOS_SERVER=" + server, "WantedBy=default.target",
	} {
		if !strings.Contains(dry, want) {
			t.Fatalf("the unit lacks %q:\n%s", want, dry)
		}
	}
	if _, err := os.Stat(unitPath); err == nil {
		t.Fatalf("--dry-run wrote %s", unitPath)
	}
	// systemd's own parser reads the unit, so a setting it refuses fails here
	// with its message, not later as "bad unit file setting".
	if analyze, err := exec.LookPath("systemd-analyze"); err == nil {
		_, text, _ := strings.Cut(dry, "\n")
		copyPath := filepath.Join(t.TempDir(), unit)
		if err := os.WriteFile(copyPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(analyze, "--user", "verify", copyPath)
		cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("systemd-analyze verify refused the unit: %v\n%s", err, out)
		}
	}

	// Install: no daemon runs, so the unit starts one.
	if out := cli("daemon", "install-service"); !strings.Contains(out, "The service started the daemon.") {
		t.Fatalf("install-service did not start the daemon:\n%s", out)
	}
	first := waitFor("after install", func(st status) bool { return st.Running && st.UnderService })
	logf("installed: main pid %d, daemon pid %d", first.PID, first.DaemonPID)
	if first.File != unitPath {
		t.Fatalf("the unit is at %s, want %s", first.File, unitPath)
	}

	// A pane starts a program that leaves the pane's session on purpose.
	pidFile := filepath.Join(base, "survivor.pid")
	cli("new", "-d", "svc")
	cli("new-window", "-s", "svc", "survivor", "--no-focus", "--", "sh", "-c",
		"setsid sleep 600 </dev/null >/dev/null 2>&1 & echo $! > "+pidFile+"; exec sleep 600")
	var survivor int
	for deadline := time.Now().Add(10 * time.Second); survivor == 0; {
		if data, err := os.ReadFile(pidFile); err == nil {
			survivor, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
		if survivor == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the pane did not write the pid of its program")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	t.Cleanup(func() { _ = syscall.Kill(survivor, syscall.SIGKILL) })
	// $! is written right after the fork, before the child has run setsid.
	// Until it has, it is still in the pane's session, and the hangup when the
	// daemon stops the pane kills it. Wait for it to lead a session of its own.
	for deadline := time.Now().Add(10 * time.Second); sessionOf(survivor) != survivor; {
		if time.Now().After(deadline) {
			t.Fatalf("the pane program %d did not leave the pane's session (session %d)", survivor, sessionOf(survivor))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cg, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", survivor)); !strings.Contains(string(cg), unit) {
		t.Fatalf("the pane program is not in the unit's cgroup: %s", cg)
	}
	logf("pane program %d runs in %s", survivor, unit)

	// kill-server: status 0, so the unit stays stopped, and KillMode=process
	// leaves the program that left its pane.
	cli("kill-server")
	time.Sleep(4 * time.Second) // twice RestartSec
	if state, _ := systemctl("is-active", unit); state != "inactive" {
		t.Fatalf("after kill-server the unit is %q, want inactive (a clean stop must not restart it)", state)
	}
	if !processAlive(survivor) {
		t.Fatalf("the pane program %d died with the daemon: the unit killed what outlived the daemon", survivor)
	}
	logf("after kill-server: unit inactive, pane program %d alive", survivor)

	// A client start goes through the unit.
	cli("start-server")
	second := waitFor("after start-server", func(st status) bool { return st.Running && st.UnderService })
	logf("start-server: main pid %d", second.PID)

	// A crash: systemd starts the daemon again.
	if err := syscall.Kill(second.DaemonPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	third := waitFor("after SIGKILL", func(st status) bool {
		return st.Running && st.UnderService && st.PID != second.PID
	})
	logf("SIGKILL %d: systemd started %d", second.DaemonPID, third.PID)

	// A live upgrade keeps the pid, and the unit sees no change.
	cli("daemon", "upgrade", "--binary", tuiosBin)
	fourth := waitFor("after the upgrade", func(st status) bool { return st.Running && st.UnderService })
	if fourth.PID != third.PID {
		t.Fatalf("the live upgrade changed the main pid from %d to %d", third.PID, fourth.PID)
	}
	logf("live upgrade: main pid %d kept, unit %s", fourth.PID, fourth.Detail)

	// Uninstall removes the file and leaves the daemon.
	cli("daemon", "uninstall-service")
	if _, err := os.Stat(unitPath); err == nil {
		t.Fatalf("uninstall-service left %s", unitPath)
	}
	if !processAlive(fourth.DaemonPID) {
		t.Fatal("uninstall-service stopped the daemon")
	}
	logf("uninstall: file gone, daemon %d runs on", fourth.DaemonPID)
}

// sessionOf returns the session id of pid from /proc, or 0 when it cannot be
// read. The command name in the stat line can hold spaces and parentheses, so
// the fields are counted from the last ')'.
func sessionOf(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	i := strings.LastIndexByte(string(data), ')')
	if i < 0 {
		return 0
	}
	// After the name: state, ppid, pgrp, session.
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 4 {
		return 0
	}
	sid, _ := strconv.Atoi(fields[3])
	return sid
}
