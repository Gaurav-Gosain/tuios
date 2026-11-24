package tuie2e

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// barLine is one line of tuios bar-status, the fields the test reads.
type barLine struct {
	Text     string `json:"text"`
	Alt      string `json:"alt"`
	Tooltip  string `json:"tooltip"`
	Class    string `json:"class"`
	NeedsYou int    `json:"needs_you"`
	Working  int    `json:"working"`
	Rows     []struct {
		Kind    string `json:"kind"`
		Session string `json:"session"`
		Summary string `json:"summary"`
	} `json:"rows"`
}

// TestBarStatusFollowsTheAgents: tuios bar-status prints the line a status
// bar shows, and --follow prints a new line as an agent asks for approval,
// goes back to work, and as the daemon stops and starts again.
//
// How it could pass wrongly, written down first:
//   - --follow could print the first line and never another: each step
//     waits for a line with the new class.
//   - It could poll and so print the same line again and again: no two
//     lines in a row may be the same.
//   - An approval could count as work: the needs-you line must carry the
//     approval as a row, with its words, and text 1.
//   - The tooltip could pass the agent's markup to Waybar: a summary with
//     <b> must reach the tooltip escaped.
//   - A stopped daemon could end the command, or leave the bar on its last
//     state: kill-server must give an off line, and a new daemon a live line
//     again, from the same process.
//
// NEGATIVE CONTROL: with the refresh after an event cut from
// followBarStatusOnce, the wait for the needs-you line fails.
func TestBarStatusFollowsTheAgents(t *testing.T) {
	base := t.TempDir()
	if out, err := tuiosCLI(t, base, "new", "work", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}

	out, err := tuiosCLI(t, base, "bar-status")
	if err != nil {
		t.Fatalf("bar-status: %v\n%s", err, out)
	}
	var one barLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &one); err != nil {
		t.Fatalf("ASSERTION: bar-status did not print one JSON line: %v\n%s", err, out)
	}
	if one.Class != "idle" || one.Alt != "idle" || one.Text != "" {
		t.Errorf("ASSERTION: a session with no agent gives %+v, want an idle line with no text", one)
	}

	cmd := exec.Command(tuiosBin, "bar-status", "--follow")
	cmd.Dir = workDirIn(t, base)
	cmd.Env = append(os.Environ(), "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
	}
	ownTuiosEnvGuard(t, cmd.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			lines <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			t.Logf("reading bar-status --follow: %v", err)
		}
		close(lines)
	}()
	var seen []string
	t.Cleanup(func() { saveText(t, "bar-status-lines", strings.Join(seen, "\n")+"\n") })
	waitLine := func(what string, ok func(barLine) bool) barLine {
		t.Helper()
		deadline := time.NewTimer(uiTimeout * 3)
		defer deadline.Stop()
		for {
			select {
			case raw, open := <-lines:
				if !open {
					t.Fatalf("ASSERTION: bar-status --follow ended before %s:\n%s", what, strings.Join(seen, "\n"))
				}
				if len(seen) > 0 && seen[len(seen)-1] == raw {
					t.Errorf("ASSERTION: --follow printed the same line twice in a row: %s", raw)
				}
				seen = append(seen, raw)
				var l barLine
				if err := json.Unmarshal([]byte(raw), &l); err != nil {
					t.Fatalf("ASSERTION: --follow printed a line that is not JSON: %v\n%s", err, raw)
				}
				if ok(l) {
					return l
				}
			case <-deadline.C:
				t.Fatalf("ASSERTION: no line %s:\n%s", what, strings.Join(seen, "\n"))
			}
		}
	}

	waitLine("for the idle start", func(l barLine) bool { return l.Class == "idle" })

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "work", "needs_input", "--kind", "approval",
		"-m", "Allow Bash: make <b>deploy</b>"); err != nil {
		t.Fatalf("set-agent-state needs_input: %v\n%s", err, out)
	}
	need := waitLine("for the approval", func(l barLine) bool { return l.Class == "needs-you" })
	if need.Text != "1" || need.NeedsYou != 1 || need.Alt != "needs-you" {
		t.Errorf("ASSERTION: the approval line is %+v, want text 1 and needs_you 1", need)
	}
	if len(need.Rows) != 1 || need.Rows[0].Kind != "approval" || need.Rows[0].Session != "work" ||
		!strings.Contains(need.Rows[0].Summary, "make <b>deploy</b>") {
		t.Errorf("ASSERTION: the approval is not the row of the line: %+v", need.Rows)
	}
	if !strings.Contains(need.Tooltip, "1 item needs you.") || !strings.Contains(need.Tooltip, "Approval  work") ||
		!strings.Contains(need.Tooltip, "make &lt;b&gt;deploy&lt;/b&gt;") {
		t.Errorf("ASSERTION: the tooltip does not list the approval, escaped:\n%s", need.Tooltip)
	}

	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "work", "working"); err != nil {
		t.Fatalf("set-agent-state working: %v\n%s", err, out)
	}
	work := waitLine("for the agent at work", func(l barLine) bool { return l.Class == "working" })
	if work.Text != "1" || work.Working != 1 || work.NeedsYou != 0 || len(work.Rows) != 0 {
		t.Errorf("ASSERTION: the working line is %+v, want text 1, working 1 and no rows", work)
	}

	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}
	off := waitLine("for the stopped daemon", func(l barLine) bool { return l.Class == "off" })
	if off.Text != "" || off.Tooltip != "tuios is not running." {
		t.Errorf("ASSERTION: the off line is %+v", off)
	}

	if out, err := tuiosCLI(t, base, "new", "again", "--detach"); err != nil {
		t.Fatalf("start a new daemon: %v\n%s", err, out)
	}
	waitLine("from the new daemon", func(l barLine) bool { return l.Class == "idle" })
}
