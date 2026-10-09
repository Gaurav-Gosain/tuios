//go:build unix || windows

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
//   - a program a finished command opened for the person, such as an editor
//     or a browser, is killed when the plugin stops or the daemon ends;
//   - the daemon ends without running StopAll, as kill-server's
//     TerminateProcess makes it, and a running command outlives it;
//   - without a job, the command never starts, or StopPlugin misses it.

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

func TestAFinishedCommandLeavesItsProgramRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	r := NewRunner()
	child := startHelper(t, r, "parent-exits")
	defer killPid(child)
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
	r.StopPlugin("test.plugin")
	r.StopAll(time.Second)
	time.Sleep(500 * time.Millisecond)
	if !alive(child) {
		t.Fatal("a program the finished command opened was killed")
	}
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
