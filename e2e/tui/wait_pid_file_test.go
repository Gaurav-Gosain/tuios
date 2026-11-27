package tuie2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestWaitForPidAndFile: wait-for pid blocks while a process that is not
// the daemon's child runs, and returns when it exits. wait-for file blocks
// until a file appears in a folder that does not exist yet, and with
// --pattern until a line matches, while the writer keeps the file open.
//
// How it could pass wrongly: a wait that returned at once would pass every
// "it returned" check, so each wait must still be blocked a while after it
// started. A file wait that matched on any write would pass the pattern
// step, so a write that does not match comes first and must not end it.
//
// Negative controls: with waitPidExit answering at once, the pid wait is not
// blocked while the process runs. With WatchFile watching the file's folder
// rather than the file on kqueue, a write to the open log never wakes the
// wait. With the folder watch not moved down as folders appear, the file in
// the new folder is never seen.
func TestWaitForPidAndFile(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const sess = "e2e-wait-pid-file"
	mustCLI(t, base, "new", sess, "--detach")
	waitForSessionInfo(t, base, sess)
	var report strings.Builder
	blocked := func(what string, done <-chan askRun) {
		t.Helper()
		stayTrue(t, what, 2*time.Second, func() (bool, string) {
			return len(done) == 0, "the wait ended early"
		})
	}
	ended := func(what string, done <-chan askRun) askRun {
		t.Helper()
		select {
		case r := <-done:
			fmt.Fprintf(&report, "%s:\n%s(err %v)\n", what, r.out, r.err)
			return r
		case <-time.After(scaled(uiTimeout)):
			t.Fatalf("ASSERTION: %s: the wait did not end", what)
			return askRun{}
		}
	}

	// A process the test started, so the daemon is not its parent.
	proc := exec.Command("sleep", "60")
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	pid := strconv.Itoa(proc.Process.Pid)
	done := startCLIBackground(t, base, "wait-for", "pid", pid, "--timeout", "60000")
	blocked("the pid wait stays blocked while the process runs", done)
	_ = proc.Process.Kill()
	_ = proc.Wait()
	if r := ended("pid wait", done); r.err != nil || !strings.Contains(r.out, "Process "+pid+" exited.") {
		t.Errorf("ASSERTION: the pid wait did not report the exit: %v\n%s", r.err, r.out)
	}
	if out := mustCLI(t, base, "wait-for", "pid", pid, "--timeout", "5000"); !strings.Contains(out, "No process "+pid+" runs.") {
		t.Errorf("ASSERTION: a wait on a process that is gone did not end at once:\n%s", out)
	}

	// A file in a folder that does not exist yet.
	ready := filepath.Join(base, "out", "sub", "ready")
	done = startCLIBackground(t, base, "wait-for", "file", ready, "--timeout", "60000")
	blocked("the file wait stays blocked while there is no file", done)
	if err := os.MkdirAll(filepath.Dir(ready), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ready, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if r := ended("file wait", done); r.err != nil || !strings.Contains(r.out, "exists.") {
		t.Errorf("ASSERTION: the file wait did not see the file: %v\n%s", r.err, r.out)
	}

	// A log that a running program appends to and keeps open.
	logPath := filepath.Join(base, "build.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	write := func(s string) {
		t.Helper()
		if _, err := f.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	write("starting\n")
	done = startCLIBackground(t, base, "wait-for", "file", logPath, "--pattern", "^BUILD (OK|FAILED)$", "--timeout", "60000")
	write("compiling\n")
	blocked("the pattern wait stays blocked on lines that do not match", done)
	write("BUILD OK\n")
	if r := ended("pattern wait", done); r.err != nil || !strings.Contains(r.out, "matched: BUILD OK") {
		t.Errorf("ASSERTION: the pattern wait did not see the line in the open log: %v\n%s", r.err, r.out)
	}
	saveText(t, "wait-pid-file", report.String())
}
