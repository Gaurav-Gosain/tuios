package tuie2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runSplit runs tuios with the isolation of base and extra env, and returns
// its stdout and stderr apart, and its exit status.
func runSplit(t *testing.T, base string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	cmd.Env = append(cmd.Env, env...)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	err := cmd.Run()
	return out.String(), errs.String(), exitCodeOf(err)
}

// pidAlive reports whether the process whose id is in path still runs.
func pidAlive(t *testing.T, path string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("%s holds %q, not a pid", path, data)
	}
	return syscall.Kill(pid, 0) == nil
}

// waitForPidFile waits until path exists.
func waitForPidFile(t *testing.T, path string) {
	t.Helper()
	waitFileExists(t, path, "the pid file never appeared", 15*time.Second)
}

// TestRunOnAHostPrintsItsOutputAndExitsWithItsStatus runs one command on a
// linked machine with tuios run --host, over the real link: the ssh stand-in
// runs the real stdio-proxy against a second real daemon. Before, the only
// way was a window on the host, a sentinel echoed after the command, a loop
// of capture-pane and a close, which mixed stdout with stderr and lost the
// exit status.
//
// How this could fail, written down first, and what each subtest checks:
//   - streams: stdout and stderr come back mixed, or one is lost. The command
//     writes one word to each and exits 3; tuios must print each on its own
//     stream and exit 3.
//   - where: the command runs on this machine. It prints the daemon's
//     XDG_STATE_HOME, which must be build's.
//   - timeout: the command never ends, or a child it started outlives it. A
//     child sleeps in the background and writes its pid; at the timeout tuios
//     exits 124 at once and the child is gone.
//   - caller gone: the person stops tuios. The command on build must stop
//     with it, children included.
//   - output: a huge output fills the far daemon. 3 MB of stdout comes back
//     as its last 1 MiB, with a note on stderr.
//   - flags: -w with --host would read as a pane on the host; it is refused.
//   - policy: the link's policy has no open. The run is refused and the error
//     names open, the one capability it needs.
//
// Negative control: on origin/main run has no --host flag, so every subtest
// fails at the first call.
func TestRunOnAHostPrintsItsOutputAndExitsWithItsStatus(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, remote)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start this machine's daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool { return containsAll(s, "build", "up") }, "the daemon never reported build up")

	t.Run("streams", func(t *testing.T) {
		out, errs, code := runSplit(t, base, env, "run", "--host", "build", "--",
			"printf", "to-stdout;", "printf", "to-stderr", ">&2;", "exit", "3")
		if out != "to-stdout" || errs != "to-stderr" || code != 3 {
			dumpLinkLogs(t, base, remote)
			t.Fatalf("ASSERTION: run --host printed stdout %q and stderr %q and exited %d; want to-stdout, to-stderr and 3", out, errs, code)
		}
	})

	t.Run("where", func(t *testing.T) {
		out, errs, code := runSplit(t, base, env, "run", "--host", "build", "--", "printf", "%s", "\"$XDG_STATE_HOME\"")
		if code != 0 || out != xdgDir(remote, "XDG_STATE_HOME") {
			t.Fatalf("ASSERTION: the command did not run on build: it printed %q (stderr %q, exit %d), want %q",
				out, errs, code, xdgDir(remote, "XDG_STATE_HOME"))
		}
	})

	t.Run("timeout", func(t *testing.T) {
		pidFile := filepath.Join(remote, "child.pid")
		start := time.Now()
		_, errs, code := runSplit(t, base, env, "run", "--host", "build", "--timeout", "1500", "--",
			"sleep 60 & echo $! > "+pidFile+"; wait")
		took := time.Since(start)
		if code != 124 || !strings.Contains(errs, "was stopped") {
			t.Fatalf("ASSERTION: a command past its timeout exited %d with stderr %q; want 124 and a note", code, errs)
		}
		if took > 10*time.Second {
			t.Fatalf("ASSERTION: run --host took %v to give up on a 1.5s timeout", took)
		}
		deadline := time.Now().Add(5 * time.Second)
		for pidAlive(t, pidFile) {
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: the child the command started still runs on build after the timeout")
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	t.Run("caller gone", func(t *testing.T) {
		pidFile := filepath.Join(remote, "left.pid")
		cmd := exec.Command(tuiosBin, "run", "--host", "build", "--timeout", "120000", "--",
			"sleep 120 & echo $! > "+pidFile+"; wait")
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TUIOS_SSH="+ssh)
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waitForPidFile(t, pidFile)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		deadline := time.Now().Add(10 * time.Second)
		for pidAlive(t, pidFile) {
			if time.Now().After(deadline) {
				dumpLinkLogs(t, base, remote)
				t.Fatalf("ASSERTION: the command still runs on build after the caller went away")
			}
			time.Sleep(100 * time.Millisecond)
		}
	})

	t.Run("output", func(t *testing.T) {
		out, errs, code := runSplit(t, base, env, "run", "--host", "build", "--",
			"head -c 3000000 /dev/zero | tr '\\0' a; printf END")
		if code != 0 || len(out) != 1<<20 || !strings.HasSuffix(out, "aEND") || !strings.Contains(errs, "longer than 1 MiB") {
			t.Fatalf("ASSERTION: 3 MB of output came back as %d bytes (ends %q), exit %d, stderr %q; want the last 1 MiB and a note",
				len(out), out[max(len(out)-8, 0):], code, errs)
		}
	})

	t.Run("flags", func(t *testing.T) {
		_, errs, code := runSplit(t, base, env, "run", "--host", "build", "-w", "deploy", "--", "true")
		if code == 0 || !strings.Contains(errs, "no -s or -w") {
			t.Fatalf("ASSERTION: run --host with -w exited %d with stderr %q; want a refusal", code, errs)
		}
	})

	t.Run("policy", func(t *testing.T) {
		writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\"]\n")
		if out, err := tuiosCLI(t, remote, "config", "apply"); err != nil {
			t.Fatalf("config apply on build: %v\n%s", err, out)
		}
		out, errs, code := runSplit(t, base, env, "run", "--host", "build", "--", "echo", "SHOULD-NOT-RUN")
		if code == 0 || strings.Contains(out, "SHOULD-NOT-RUN") || !strings.Contains(errs, "open") {
			t.Fatalf("ASSERTION: a link without open ran the command: exit %d, stdout %q, stderr %q", code, out, errs)
		}
	})
}
