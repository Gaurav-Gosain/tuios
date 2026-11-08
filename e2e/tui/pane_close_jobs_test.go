//go:build unix

package tuie2e

import (
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

// Closing a pane hangs up its jobs, as a terminal that goes away does, on
// each of the three paths that close one: close-window, kill-session and
// kill-server. Before, the daemon sent SIGKILL to the shell alone, and the
// kernel's hangup on the pane's terminal reaches only its foreground process
// group, so every background job of the pane ran on with no pane (M13 in the
// state machine audit). As in tmux, a job that ignores SIGHUP (nohup) or that
// left the pane's session (setsid) is the person's choice, and runs on.
//
// How this could pass wrongly, written down first:
//   - A job that never started reads as "ended". Each job writes its pid to a
//     file, and the test checks each one runs, with its marker in its command
//     line, before the pane closes.
//   - A job that ends on its own reads as hung up. Every job sleeps for hours.
//   - A pid reused by another program after the job died reads as "still
//     running". A pid counts as the job only while its command line holds
//     the job's marker.
//   - A fix that kills too much passes every "ended" check. The nohup job and
//     the setsid job must still run after the close, and so must a process the
//     test starts itself, outside every pane.
//   - Only the easy case covered. The jobs that must end are a plain
//     background job, a stopped job, and an orphan whose parent exited but
//     which stays in the pane's session.
//   - The shell's own exit, not the close, ends the jobs. The jobs run under
//     an interactive shell with job control, which puts each in a process
//     group of its own, and the shell gets no chance to hang them up.
func TestPaneCloseEndsItsJobs(t *testing.T) {
	if os.Getenv("TUIOS_E2E") == "" {
		t.Skip("set TUIOS_E2E=1 to run")
	}
	base := t.TempDir()
	killDaemon(t, base)
	dir := t.TempDir()
	cli := func(args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	var log strings.Builder
	// Each job is a sleep of its own length, which is its marker.
	seed := 600000 + os.Getpid()%100000*10
	marker := func(n int) string { return strconv.Itoa(seed + n) }

	perl, _ := exec.LookPath("perl")

	type job struct {
		line, marker string
		survives     bool
	}
	// Each line takes the marker and the pid file, in that order.
	jobsFrom := func(first int) map[string]job {
		jobs := map[string]job{
			"background": {"sleep %s & echo $! > %s", marker(first), false},
			"stopped":    {"sleep %s & echo $! > %s", marker(first + 1), false},
			// Its parent, a subshell, exits at once, so only the session it
			// stays in ties it to the pane.
			"orphan in the session": {"(sleep %s & echo $! > %s)", marker(first + 2), false},
			"nohup":                 {"nohup sleep %s > /dev/null 2>&1 & echo $! > %s", marker(first + 3), true},
		}
		if perl != "" {
			// setsid fails in a process group leader, which a job is, so the
			// job forks and the child leaves. The parent stays, waiting.
			jobs["left the session"] = job{perl + " -MPOSIX -e 'if (my $c = fork) { waitpid($c, 0); exit }" +
				" POSIX::setsid() or die; open(F, \">\", $ARGV[1]); print F $$; close F; sleep $ARGV[0]' %s %s &",
				marker(first + 4), true}
		}
		return jobs
	}

	// startJobs starts the jobs in window win of session sess and returns
	// their pids by name, once each one runs.
	startJobs := func(sess, win string, jobs map[string]job) map[string]int {
		t.Helper()
		var script strings.Builder
		files := make(map[string]string, len(jobs))
		for name, j := range jobs {
			file := filepath.Join(dir, sess+"-"+j.marker+".pid")
			files[name] = file
			fmt.Fprintf(&script, j.line+"\n", j.marker, file)
		}
		src := filepath.Join(dir, sess+"-"+jobs["background"].marker+".sh")
		if err := os.WriteFile(src, []byte(script.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		// Sourced, so the interactive shell itself starts the jobs.
		cli("send-keys", "-s", sess, "-w", win, "--raw", ". "+src)
		cli("send-keys", "-s", sess, "-w", win, "Enter")
		pids := map[string]int{}
		deadline := time.Now().Add(uiTimeout)
		for name, file := range files {
			for {
				data, err := os.ReadFile(file)
				pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				if err == nil && pid > 0 && runsWith(pid, jobs[name].marker) {
					pids[name] = pid
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("job %q in session %s never ran (pid file %q)", name, sess, data)
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		// No job may outlive the test, the ones that survive on purpose
		// included.
		for _, pid := range pids {
			cmd := commandOf(pid)
			t.Cleanup(func() {
				if runsWith(pid, cmd) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			})
		}
		// Stopped from here once it runs: a SIGSTOP sent by the shell right
		// after the fork can land before the job runs sleep.
		if err := syscall.Kill(pids["stopped"], syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&log, "%s: started %v\n", sess, pids)
		return pids
	}

	// check waits for the jobs that must end to end, then checks that the
	// others still run.
	check := func(what string, jobs map[string]job, pids map[string]int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for name, j := range jobs {
			if j.survives {
				continue
			}
			for runsWith(pids[name], j.marker) && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if runsWith(pids[name], j.marker) {
				fmt.Fprintf(&log, "%s: %q (pid %d) STILL RUNS, want it hung up\n", what, name, pids[name])
				t.Errorf("%s: job %q (pid %d) still runs, want it hung up", what, name, pids[name])
			} else {
				fmt.Fprintf(&log, "%s: %q (pid %d) ended\n", what, name, pids[name])
			}
		}
		// The hangup has had its effect on the jobs that take it; a moment
		// more for any late signal.
		time.Sleep(time.Second)
		for name, j := range jobs {
			if !j.survives {
				continue
			}
			if runsWith(pids[name], j.marker) {
				fmt.Fprintf(&log, "%s: %q (pid %d) still runs, as it should\n", what, name, pids[name])
			} else {
				fmt.Fprintf(&log, "%s: %q (pid %d) ENDED, want it running\n", what, name, pids[name])
				t.Errorf("%s: job %q (pid %d) ended, want it running", what, name, pids[name])
			}
		}
	}

	// A process outside every pane. The fix must not reach it.
	outsider := exec.Command("sleep", marker(9))
	if err := outsider.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outsider.Process.Kill(); _ = outsider.Wait() })

	// close-window.
	cli("new", "jobs", "--detach")
	first := focusedWindowID(t, base, "jobs")
	cli("new-window", "doomed", "-s", "jobs")
	doomed := windowIDByName(t, base, "jobs", "doomed")
	jobs := jobsFrom(10)
	pids := startJobs("jobs", doomed, jobs)
	cli("close-window", doomed, "-s", "jobs")
	check("close-window", jobs, pids)

	// kill-session.
	cli("new", "victim", "--detach")
	jobs = jobsFrom(20)
	pids = startJobs("victim", focusedWindowID(t, base, "victim"), jobs)
	cli("kill-session", "victim")
	check("kill-session", jobs, pids)

	// kill-server, which closes every pane as the daemon stops.
	jobs = jobsFrom(30)
	pids = startJobs("jobs", first, jobs)
	cli("kill-server")
	check("kill-server", jobs, pids)

	if runsWith(outsider.Process.Pid, marker(9)) {
		fmt.Fprintf(&log, "outside every pane: pid %d still runs, as it should\n", outsider.Process.Pid)
	} else {
		fmt.Fprintf(&log, "outside every pane: pid %d ENDED\n", outsider.Process.Pid)
		t.Errorf("the process outside every pane (pid %d) ended", outsider.Process.Pid)
	}

	path := filepath.Join(artifactDir(t), "pane-close-jobs.txt")
	if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("steps:\n%s(saved to %s)", log.String(), path)
}

// commandOf is the command line of pid, as ps prints it, or "" when no such
// process runs. A zombie has no command line worth the name and reads as "".
func commandOf(pid int) string {
	if pid <= 0 || syscall.Kill(pid, 0) != nil {
		return ""
	}
	out, err := exec.Command("ps", "-o", "stat=,command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	stat, cmd, _ := strings.Cut(line, " ")
	if strings.HasPrefix(stat, "Z") {
		return ""
	}
	return strings.TrimSpace(cmd)
}

// runsWith reports whether pid runs and its command line holds marker.
func runsWith(pid int, marker string) bool {
	return marker != "" && strings.Contains(commandOf(pid), marker)
}
