package tuie2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadinessCheckEndsWithTheDaemon starts the New agent sheet's check, the
// agent-readiness verb, against a claude whose `auth status` hangs, and stops
// the daemon while the check runs. The check's process must be gone when the
// daemon is: left running, a real Claude Code went on to write its own files
// under HOME after the daemon had stopped, which raced the test's cleanup
// (TestInboxOnTheShippedLooks, "directory not empty").
//
// How it could pass wrongly, written down first:
//   - The check might never start, and then nothing outlives the daemon. The
//     stand-in writes its pid when it starts, and the test waits for it.
//   - The stand-in might end by itself before the daemon stops. It sleeps 30
//     s, and the test looks for it within 3 s of kill-server.
//
// Negative control: on main the check runs under context.Background, and the
// stand-in is still alive after kill-server.
func TestReadinessCheckEndsWithTheDaemon(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fakes := filepath.Join(t.TempDir(), "fakes")
	if err := os.MkdirAll(fakes, 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(fakes, "claude.pid")
	stub := "#!/bin/sh\n" +
		"if [ \"$1\" = auth ]; then echo $$ > '" + pidFile + "'; exec sleep 30; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(fakes, "claude"), []byte(stub), 0o755); err != nil { //nolint:gosec // a stand-in this test runs
		t.Fatal(err)
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))

	if out, err := tuiosCLI(t, base, "new", "probe", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	go func() { _, _ = tuiosCLI(t, base, "call", "agent-readiness", `{"harnesses":["claude-code"]}`) }()

	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 0 {
				pid = n
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatalf("the readiness check never ran claude auth status")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	gone := time.Now().Add(3 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(gone) {
			t.Fatalf("ASSERTION: claude auth status (pid %d) is still running 3 s after kill-server: the check outlives its daemon", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
