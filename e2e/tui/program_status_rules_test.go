package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestProgramStatusOutlivesTheShellCheck: a working record stays while the
// program that reported it still runs, though the pane's shell, or the pane's
// own process, holds the foreground. The rule that ends a record when the
// shell takes the foreground back must only follow a report made from the
// foreground. Step 6 of TestProgramStatusDrivesTheRailAndInbox is its
// positive half: a script that reported from the foreground and exited loses
// its working record.
//
// How this could pass wrongly: the record could be read before the detector
// looked. Each case waits more than two detector readings (2 seconds each)
// with the program still asleep.
func TestProgramStatusOutlivesTheShellCheck(t *testing.T) {
	const session = "bg"
	base := t.TempDir()
	killDaemon(t, base)
	work := workDirIn(t, base)
	job := "printf '\\033]7501;state=working:app=job\\033\\\\'\necho JOB\"\"-ON\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(work, "job.sh"), []byte(job), 0o644); err != nil {
		t.Fatalf("write job.sh: %v", err)
	}
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "--name", "shell"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	t.Run("the pane's own process", func(t *testing.T) {
		if out, err := tuiosCLI(t, base, "new-window", "cmd2", "-s", session, "--no-focus", "--", "sh", "job.sh"); err != nil {
			t.Fatalf("open the pane: %v\n%s", err, out)
		}
		waitCapture(t, base, session, "cmd2", "JOB-ON")
		waitPSState(t, base, session, "cmd2", "the job's report", func(s psState) bool { return s.records() == "-=working" })
		time.Sleep(5 * time.Second)
		if st := readPSState(t, base, session, "cmd2"); st.records() != "-=working" || st.State != "working" {
			t.Errorf("ASSERTION: the running job lost its record: %+v", st)
		}
	})
	t.Run("a foreground program beside a background job", func(t *testing.T) {
		// Each record keeps the group it came from: the foreground program
		// exiting ends its own record and leaves the job's.
		// The job reports first, while the shell holds the foreground, and the
		// program starts after: which process wrote a report is not something
		// the terminal can see, only who held the foreground when it came.
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "shell",
			`(printf '\033]7501;state=working:id=job\033\\'; sleep 40) &`+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
		waitPSState(t, base, session, "shell", "the job's report", func(s psState) bool { return strings.Contains(s.records(), "job=working") })
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "shell",
			`sh -c "printf '\033]7501;state=working:id=fg\033\\\\'; sleep 3"; echo FG""-DONE`+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
		waitPSState(t, base, session, "shell", "both reports", func(s psState) bool {
			return strings.Contains(s.records(), "fg=working") && strings.Contains(s.records(), "job=working")
		})
		waitCapture(t, base, session, "shell", "FG-DONE")
		st := waitPSState(t, base, session, "shell", "the foreground program's exit", func(s psState) bool { return !strings.Contains(s.records(), "fg=working") })
		if !strings.Contains(st.records(), "job=working") {
			t.Errorf("ASSERTION: the foreground program's exit ended the background job's record: %s", st.records())
		}
	})
	t.Run("a program that reports and exits between two readings", func(t *testing.T) {
		// The group is read as the report comes off the terminal, so a
		// program that lives a fifth of a second, far less than the 2 seconds
		// between two detector readings, still has its exit seen. A program
		// that exits in the same instant it reports can beat that read; the
		// docs say so, and this test does not cover it.
		for i := range 10 {
			id := "fast" + string(rune('a'+i))
			if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "shell",
				`sh -c "printf '\033]7501;state=working:id=`+id+`\033\\\\'; sleep 0.2; exit 1"`+"\n"); err != nil {
				t.Fatalf("send-text: %v\n%s", err, out)
			}
		}
		seen := false
		waitPSState(t, base, session, "shell", "the fast programs' exits", func(s psState) bool {
			if strings.Contains(s.records(), "fast") {
				seen = true
				return false
			}
			return seen
		})
	})
	t.Run("a background job", func(t *testing.T) {
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "shell",
			`(printf '\033]7501;state=working:id=bg\033\\'; sleep 30) & echo BG""-ON`+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
		waitCapture(t, base, session, "shell", "BG-ON")
		waitPSState(t, base, session, "shell", "the job's report", func(s psState) bool { return strings.Contains(s.records(), "bg=working") })
		time.Sleep(5 * time.Second)
		if st := readPSState(t, base, session, "shell"); !strings.Contains(st.records(), "bg=working") {
			t.Errorf("ASSERTION: the background job lost its record: %+v", st)
		}
	})
}

// TestProgramStatusAuthIsAnsweredInThePane: a program blocked on kind=auth
// waits for a login, and the Inbox must not offer to type one. peek-prompt,
// which the Inbox's detail reads, says the prompt is answered in the pane and
// offers no answer.
//
// How this could pass wrongly: a pane no rule can read is unanswerable
// anyway. The reason has to be the login, not that tuios knows no rule.
func TestProgramStatusAuthIsAnsweredInThePane(t *testing.T) {
	const session = "au"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", session,
		`printf '\033]7501;state=blocked:kind=auth:app=brew\033\\'; echo `+splitMarker("AUTHSENT")+"\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, session, "0", "AUTHSENT")
	st := waitPSState(t, base, session, "0", "the auth block", func(s psState) bool { return s.State == "needs_input" })
	if st.BlockedBy != "auth" {
		t.Errorf("ASSERTION: blocked_by = %q, want auth", st.BlockedBy)
	}
	var peek struct {
		Answerable bool     `json:"answerable"`
		Reason     string   `json:"reason"`
		Actions    []string `json:"actions"`
	}
	out, err := tuiosCLI(t, base, "peek-prompt", "-s", session, "-w", "0", "--json")
	if err != nil {
		t.Fatalf("peek-prompt: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(out), &peek); err != nil {
		t.Fatalf("peek-prompt json: %v\n%s", err, out)
	}
	if peek.Answerable || len(peek.Actions) != 0 || !strings.Contains(peek.Reason, "login") {
		t.Errorf("ASSERTION: an auth block must be answered in the pane, peek-prompt said %+v", peek)
	}
}

// TestProgramStatusAlertAfterTheGap: a pane whose state comes from OSC 7501
// sends at most one alert outside tuios in 30 seconds, and a change of state
// inside that gap is held, not dropped. The pane blocks on a login, which
// alerts at once, and is done three seconds later; the done alert has to
// reach the terminal once the gap ends.
//
// How this could pass wrongly: the done alert could go out at once, with no
// limit at all. It must not arrive before the gap ends, and it must arrive
// after.
func TestProgramStatusAlertAfterTheGap(t *testing.T) {
	const session = "al"
	host := buildHostTerm(t)
	base := t.TempDir()
	killDaemon(t, base)
	hostLog := filepath.Join(t.TempDir(), "host.log")
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "--name", "job"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	// Another pane takes the focus, since the focused pane raises no alert.
	if out, err := tuiosCLI(t, base, "new-window", "other", "-s", session); err != nil {
		t.Fatalf("open another pane: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", session}, wrap: []string{host, "run", "-log", hostLog, "--"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) == 2 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	notices := func() []string {
		data, _ := os.ReadFile(hostLog)
		var out []string
		for _, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, "notify ") {
				out = append(out, l)
			}
		}
		return out
	}
	waitNotice := func(what, word string, within time.Duration) time.Time {
		t.Helper()
		deadline := time.Now().Add(within)
		for {
			for _, l := range notices() {
				if strings.Contains(l, word) {
					return time.Now()
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: no notification with %q reached the terminal; notifications: %q", what, word, notices())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	report := func(body string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", "job",
			`printf '\033]7501;`+body+`\033\\'`+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}

	report("state=blocked:kind=auth:msg=" + b64e("Sign in to the registry"))
	first := waitNotice("the login", "Sign in to the registry", uiTimeout)
	time.Sleep(3 * time.Second)
	report("state=done:msg=" + b64e("Published the crate"))
	time.Sleep(10 * time.Second)
	for _, l := range notices() {
		if strings.Contains(l, "Published the crate") {
			t.Fatalf("ASSERTION: the done alert went out %s after the login, inside the 30-second gap", time.Since(first).Round(time.Second))
		}
	}
	got := waitNotice("the done alert after the gap", "Published the crate", 30*time.Second)
	if gap := got.Sub(first); gap < 29*time.Second {
		t.Errorf("ASSERTION: the done alert came %s after the login, want at least 30 seconds", gap.Round(time.Second))
	}
}

// TestProgramStatusYieldsToAHook: a harness hook and an OSC 7501 report on
// the same pane. The hook's report keeps the pane, and the program's records
// ending leave the hook's state as it was. A weaker source the program took
// the pane from does not get its old state replayed when the records end: it
// may be stale, so the pane is looked at again, and here, where no rule reads
// the pane, it is left with no state.
//
// How this could pass wrongly: the program's report could never arrive. The
// second half, with a weaker source, shows the same report taking the pane.
func TestProgramStatusYieldsToAHook(t *testing.T) {
	const session = "hk"
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-window", "-s", session, "--name", "agent"); err != nil {
		t.Fatalf("name the pane: %v\n%s", err, out)
	}
	pane := "agent"
	report := func(body, marker string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", pane,
			`printf '\033]7501;`+body+`\033\\'; echo `+splitMarker(marker)+"\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
		waitCapture(t, base, session, pane, marker)
	}
	setState := func(args ...string) {
		t.Helper()
		if out, err := tuiosCLI(t, base, append([]string{"set-agent-state", "-s", session, "-w", pane}, args...)...); err != nil {
			t.Fatalf("set-agent-state %v: %v\n%s", args, err, out)
		}
	}

	setState("needs_input", "--kind", "approval", "-m", "hook says")
	report("state=working:app=prog", "REPORTED1")
	st := waitPSState(t, base, session, "agent", "the record", func(s psState) bool { return s.records() == "-=working" })
	time.Sleep(time.Second)
	if st = readPSState(t, base, session, "agent"); st.State != "needs_input" || st.Source != "report" || st.Message != "hook says" {
		t.Errorf("ASSERTION: the program's report took the pane from the hook: %+v", st)
	}
	report("state=clear", "CLEARED1")
	waitPSState(t, base, session, "agent", "the clear", func(s psState) bool { return len(s.Program) == 0 })
	time.Sleep(time.Second)
	if st = readPSState(t, base, session, "agent"); st.State != "needs_input" || st.Message != "hook says" {
		t.Errorf("ASSERTION: the records ending erased the hook's state: %+v", st)
	}

	// A weaker source, in a pane of its own: the screen tier. The program
	// takes the pane from it, and lets go of it.
	if out, err := tuiosCLI(t, base, "new-window", "weak", "-s", session); err != nil {
		t.Fatalf("open the second pane: %v\n%s", err, out)
	}
	pane = "weak"
	setState("needs_input", "--source", "screen", "-m", "screen says")
	report("state=working:app=prog", "REPORTED2")
	st = waitPSState(t, base, session, pane, "the program taking the pane", func(s psState) bool { return s.Source == "program" })
	if st.State != "working" {
		t.Errorf("ASSERTION: the program's report did not take the pane from the screen tier: %+v", st)
	}
	report("state=clear", "CLEARED2")
	st = waitPSState(t, base, session, pane, "the program letting go", func(s psState) bool { return s.Source != "program" })
	time.Sleep(time.Second)
	if st = readPSState(t, base, session, pane); st.State != "none" || st.Message == "screen says" {
		t.Errorf("ASSERTION: the screen tier's old state was replayed: %+v", st)
	}
}
