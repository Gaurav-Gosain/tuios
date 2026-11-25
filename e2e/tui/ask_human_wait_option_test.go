package tuie2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestAskHumanWaitsAsLongAsTheOptionSays: an ask-human call that names no
// timeout waits as long as agents.ask_wait_seconds says, and then ends
// pending (exit 2, the "No answer yet" line with a request id), never with a
// transport error of its own. Nobody answers in any row:
//
//   - S: the option at 3, no --timeout: pending after about 3 s.
//   - T: the option at 138, --timeout 3000: pending after about 3 s, the
//     caller's timeout wins over the option.
//   - L: the option at 138, no --timeout: pending no sooner than 138 s.
//
// L is 138 s because the cut it guards is at 135 s (the command's own budget
// of 120 s and 15 s): three seconds past it tell the two apart, and every
// second more is paid on every run. The test takes about 150 s. The bounds
// are against product timers, the daemon's wait and the command's socket
// budget, which TUIOS_E2E_TIME_SCALE does not move, and the test's own
// deadlines are plain durations, not waitUntil, so no scale changes them.
//
// How this could pass wrongly, written down first:
//   - A command that never asked would end at once. Every row requires the
//     "No answer yet" line with a request id.
//   - An option that is not read would hold S for 120 s. S's 30 s deadline
//     ends it with "still running after 30s".
//   - A daemon that drops the caller's timeout would hold T for 138 s. The
//     command's own budget of 3 s and 15 s ends T first, with exit 1 and a
//     read timeout, which T's exit code check reports.
//   - A fix that makes the command send its own long timeout would ignore
//     the option. S and T must end at about 3 s.
//   - L could pass by ending pending early. Its lower bound is 138 s.
//   - A hung command would hang the test. Each row has a deadline, and the
//     process is killed by its own handle when it passes.
//
// Negative controls, recorded in NEGATIVE_CONTROLS.md: main fails row L (exit
// 1 at 135 s with "i/o timeout"); so do the command's wait at one minute and
// the daemon's cap at two minutes. With the command sending the cap as its
// own timeout, S fails with "still running after 30s" and L runs to its
// deadline.
func TestAskHumanWaitsAsLongAsTheOptionSays(t *testing.T) {
	type askEnd struct {
		elapsed        time.Duration
		code           int
		stdout, stderr string
	}
	pendingLine := regexp.MustCompile(`No answer yet; the question stays in the Inbox\. Come back with: tuios ask-human --request-id \S+`)

	record := func(t *testing.T, name string, end askEnd) {
		t.Helper()
		dir := os.Getenv("TUIOS_E2E_FRAMES")
		if dir == "" {
			return
		}
		text := fmt.Sprintf("exit=%d elapsed=%s\nstderr:\n%s", end.code, end.elapsed.Round(time.Millisecond), end.stderr)
		if err := os.WriteFile(filepath.Join(dir, name+".txt"), []byte(text), 0o644); err != nil {
			t.Logf("could not save %s: %v", name, err)
		}
	}

	// ask runs the real ask-human against the daemon under base, waits for it
	// to exit within deadline, and kills it by its own handle when it does not.
	ask := func(t *testing.T, base, name string, deadline time.Duration, extra ...string) askEnd {
		t.Helper()
		pinPreV080Looks(t, base)
		args := append([]string{"ask-human", "-s", "e2e-agent", "-w", "0"}, extra...)
		args = append(args, "Merge the branch?", "-o", "merge", "-o", "wait")
		cmd := exec.Command(tuiosBin, args...)
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		ownTuiosEnvGuard(t, cmd.Env)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr

		type exit struct {
			err error
			at  time.Time
		}
		start := time.Now()
		if err := cmd.Start(); err != nil {
			t.Fatalf("start %v: %v", args, err)
		}
		done := make(chan exit, 1)
		finished := make(chan struct{})
		go func() {
			err := cmd.Wait()
			done <- exit{err: err, at: time.Now()}
			close(finished)
		}()
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			<-finished
		})

		timer := time.NewTimer(deadline)
		defer timer.Stop()
		var e exit
		killed := false
		select {
		case e = <-done:
		case <-timer.C:
			_ = cmd.Process.Kill()
			e = <-done
			killed = true
		}
		end := askEnd{elapsed: e.at.Sub(start), stdout: stdout.String(), stderr: stderr.String()}
		var exitErr *exec.ExitError
		switch {
		case e.err == nil:
		case errors.As(e.err, &exitErr):
			end.code = exitErr.ExitCode()
		default:
			t.Fatalf("ask-human %v: %v", args, e.err)
		}
		record(t, name, end)
		if killed {
			t.Fatalf("ASSERTION: ask-human (%s) still running after %s, killed\nstdout: %q\nstderr: %q", name, deadline, end.stdout, end.stderr)
		}
		return end
	}

	pending := func(t *testing.T, end askEnd, least, most time.Duration) {
		t.Helper()
		if end.code != 2 {
			t.Errorf("ASSERTION: ask-human exited %d after %s, want 2 for no answer yet\nstderr: %q", end.code, end.elapsed, end.stderr)
		}
		if !pendingLine.MatchString(end.stderr) {
			t.Errorf("ASSERTION: ask-human printed no \"No answer yet\" line with a request id (exit %d after %s)\nstderr: %q", end.code, end.elapsed, end.stderr)
		}
		if end.stdout != "" {
			t.Errorf("ASSERTION: ask-human printed %q on stdout with no answer given (exit %d after %s)\nstderr: %q", end.stdout, end.code, end.elapsed, end.stderr)
		}
		if end.elapsed < least {
			t.Errorf("ASSERTION: ask-human ended after %s, sooner than %s (exit %d)\nstderr: %q", end.elapsed, least, end.code, end.stderr)
		}
		if end.elapsed >= most {
			t.Errorf("ASSERTION: ask-human ended after %s, not under %s (exit %d)\nstderr: %q", end.elapsed, most, end.code, end.stderr)
		}
	}

	row := func(t *testing.T, option string) (string, func(name string)) {
		t.Helper()
		base := t.TempDir()
		writeConfig(t, base, "[agents]\nask_wait_seconds = "+option+"\n")
		term := attachClientIn(t, base, nil)
		if out, err := tuiosCLI(t, base, "new", "e2e-agent", "--detach"); err != nil {
			t.Fatalf("create the agent's session: %v\n%s", err, out)
		}
		return base, func(name string) {
			t.Helper()
			saveFrame(t, term, name)
			alive(t, term, "after an unanswered question")
		}
	}

	t.Run("S option 3", func(t *testing.T) {
		base, finish := row(t, "3")
		end := ask(t, base, "ask-human-wait-option-s", 30*time.Second)
		pending(t, end, 3*time.Second, 20*time.Second)
		finish("ask-human-wait-option-s-client")
	})

	t.Run("T option 138 timeout 3000", func(t *testing.T) {
		base, finish := row(t, "138")
		end := ask(t, base, "ask-human-wait-option-t", 30*time.Second, "--timeout", "3000")
		pending(t, end, 3*time.Second, 20*time.Second)
		finish("ask-human-wait-option-t-client")
	})

	t.Run("L option 138", func(t *testing.T) {
		base, finish := row(t, "138")
		end := ask(t, base, "ask-human-wait-option-l", 138*time.Second+40*time.Second)
		pending(t, end, 138*time.Second, 138*time.Second+30*time.Second)
		for _, cut := range []string{"i/o timeout", "failed to read response"} {
			if strings.Contains(end.stderr, cut) {
				t.Errorf("ASSERTION: ask-human ended on its own transport error %q (exit %d after %s)\nstderr: %q", cut, end.code, end.elapsed, end.stderr)
			}
		}
		finish("ask-human-wait-option-l-client")
	})
}
