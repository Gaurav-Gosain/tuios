package herdrplugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The ways stopping a plugin can fail, which these tests hold it to:
//
//   - StopPlugin kills the command and not the process it started, so a
//     plugin's helper outlives it (Windows has no process group);
//   - a process a finished command left behind is never reached again;
//   - the daemon ends without running StopAll, as kill-server's
//     TerminateProcess makes it, and every plugin process outlives it.
//
// The helper modes run this test binary as the plugin's processes.

const helperEnv = "TUIOS_HERDRPLUGIN_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "parent", "parent-exits":
		// Start a child that sleeps, write its pid, then sleep or exit.
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperEnv+"=child")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		_ = os.WriteFile(os.Getenv("TUIOS_HERDRPLUGIN_PIDFILE"), []byte(strconv.Itoa(child.Process.Pid)), 0o600)
		if os.Getenv(helperEnv) == "parent-exits" {
			os.Exit(0)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "host":
		// A daemon: a runner with one plugin command, which never stops it.
		r := NewRunner()
		self, _ := os.Executable()
		_, perr := r.Start(Job{
			Plugin:  &Plugin{PluginID: "test.host", PluginRoot: filepath.Dir(self)},
			Command: []string{self},
			Env:     []string{helperEnv + "=parent", "TUIOS_HERDRPLUGIN_PIDFILE=" + os.Getenv("TUIOS_HERDRPLUGIN_PIDFILE")},
		})
		if perr != nil {
			os.Exit(4)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the helper never wrote its child's pid")
	return 0
}

func waitGone(t *testing.T, pid int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	killPid(pid)
	t.Fatalf("%s: process %d still runs", what, pid)
}

func startHelper(t *testing.T, r *Runner, mode string) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(t.TempDir(), "pid")
	if _, perr := r.Start(Job{
		Plugin:  &Plugin{PluginID: "test.plugin", PluginRoot: filepath.Dir(self)},
		Command: []string{self},
		Env:     []string{helperEnv + "=" + mode, "TUIOS_HERDRPLUGIN_PIDFILE=" + pidfile},
	}); perr != nil {
		t.Fatalf("Start: %s", perr.Msg)
	}
	return readPid(t, pidfile)
}

func TestStopPluginKillsWhatTheCommandStarted(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	r := NewRunner()
	child := startHelper(t, r, "parent")
	r.StopAll(5 * time.Second)
	waitGone(t, child, "the child of a running command")
}

func TestStopPluginKillsWhatAFinishedCommandLeft(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("on Unix a finished command's group is not kept: its id may name another group")
	}
	if testing.Short() {
		t.Skip("spawns processes")
	}
	r := NewRunner()
	child := startHelper(t, r, "parent-exits")
	// The command itself has exited once its log entry is finished.
	deadline := time.Now().Add(10 * time.Second)
	for {
		logs := r.Logs("test.plugin", 1)
		if len(logs) == 1 && logs[0].Status != StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the command never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !alive(child) {
		t.Fatal("setup: the child died with its parent")
	}
	r.StopPlugin("test.plugin")
	waitGone(t, child, "the child a finished command left")
}

func TestPluginProcessesEndWithTheDaemon(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows ties a plugin's processes to the daemon's life")
	}
	if testing.Short() {
		t.Skip("spawns processes")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(t.TempDir(), "pid")
	host := exec.Command(self)
	host.Env = append(os.Environ(), helperEnv+"=host", "TUIOS_HERDRPLUGIN_PIDFILE="+pidfile)
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	child := readPid(t, pidfile)
	// TerminateProcess, as kill-server did: no StopAll runs.
	_ = host.Process.Kill()
	_ = host.Wait()
	waitGone(t, child, "a plugin's process after the daemon was terminated")
}
