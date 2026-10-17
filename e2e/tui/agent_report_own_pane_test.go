//go:build !windows

package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// buildFakeHarness builds the stand-in harness in testdata/fakeharness.
func buildFakeHarness(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeharness")
	build := exec.Command("go", "build", "-o", bin, "./testdata/fakeharness")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeharness: %v\n%s", err, out)
	}
	return bin
}

// hookExplained is the part of agent-hook --explain's line this test reads.
type hookExplained struct {
	Window     string `json:"window"`
	PaneBy     string `json:"pane_by"`
	HarnessPID int    `json:"harness_pid"`
	Applied    *bool  `json:"applied"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	Error      string `json:"error"`
}

func parseExplained(t *testing.T, line string) hookExplained {
	t.Helper()
	var got hookExplained
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &got); err != nil {
		t.Fatalf("the hook's --explain line is not JSON: %v\n%s", err, line)
	}
	return got
}

// TestAgentReportOnlyFromTheHarnessInThePane runs the real Claude Code hook
// under a stand-in harness twice against a real daemon, with the same pane
// environment both times.
//
// Outside the pane: the harness is a process the test starts in a session of
// its own, with no terminal, carrying the pane's TUIOS_PANE_ID and
// TUIOS_SESSION, the way Claude Code's daemon starts a background session
// with the environment of the pane that asked for it. Its prompt, its subagent
// and its conversation id are each refused with foreign_pane, the pane stays
// none and list-agents does not list it.
//
// Inside the pane: the same harness and hook run from the pane's shell, and
// the prompt takes the pane to working, which list-agents lists.
//
// The artifact is agent-report-own-pane.log in the test's artifact directory:
// each hook's --explain line and the pane's state after it.
//
// Negative control: with the harnessOutsidePane call taken out of
// verbSetAgentState, the outside prompt is applied and the first check fails.
func TestAgentReportOnlyFromTheHarnessInThePane(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-pane"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	harness := buildFakeHarness(t)
	artifact := &strings.Builder{}
	defer func() {
		path := filepath.Join(artifactDir(t), "agent-report-own-pane.log")
		if err := os.WriteFile(path, []byte(artifact.String()), 0o644); err != nil {
			t.Errorf("save the artifact: %v", err)
		}
		t.Logf("artifact: %s", path)
	}()
	note := func(format string, args ...any) {
		artifact.WriteString(strings.TrimRight(fmt.Sprintf(format, args...), "\n") + "\n")
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "printf 'PANE=%s=%s\\n' \"$TUIOS_PANE_ID\" DONE\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`PANE=([0-9a-f-]{8,})=DONE`).FindStringSubmatch(waitCapture(t, base, session, "0", "=DONE"))
	if m == nil {
		t.Fatalf("the pane did not print its TUIOS_PANE_ID:\n%s", capture(t, base, session, "0"))
	}
	win := m[1]
	note("pane %s of session %s", win, session)

	// outside runs the hook under the stand-in harness in a session of its
	// own, with no terminal. paneID is the TUIOS_PANE_ID the harness carries,
	// and args are agent-hook's flags beyond the ones every run takes.
	outside := func(paneID, harnessID, payload string, args ...string) hookExplained {
		t.Helper()
		argv := append([]string{tuiosBin, "agent-hook", harnessID, "--timeout", "10s", "--explain"}, args...)
		cmd := exec.Command(harness, argv...)
		cmd.Dir = workDirIn(t, base)
		cmd.Env = append(os.Environ(), "SHELL=/bin/sh", "TUIOS_PANE_ID="+paneID, "TUIOS_SESSION="+session)
		for _, key := range xdgKeys {
			cmd.Env = append(cmd.Env, key+"="+xdgDir(base, key))
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdin = strings.NewReader(payload)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("the hook outside the pane failed: %v\n%s", err, stderr.String())
		}
		note("outside %-10s %s", harnessID, strings.TrimSpace(stderr.String()))
		return parseExplained(t, stderr.String())
	}
	checkUntouched := func(step string) {
		t.Helper()
		st := readAgentState(t, base, session, win)
		note("  pane after %s: state=%s harness=%s agent_session_id=%s", step, st.State, st.Harness, st.AgentSession)
		if st.State != "none" || st.AgentSession != "" {
			t.Errorf("%s: the pane took the report of a harness outside it: %+v", step, st)
		}
		if _, listed := paneHarness(t, base, session)[win]; listed {
			t.Errorf("%s: list-agents lists the pane for a harness outside it", step)
		}
	}

	got := outside(win, "claude-code", `{"hook_event_name":"UserPromptSubmit","session_id":"phantom-1","prompt":"forked conversation"}`)
	if got.PaneBy != "env" || got.Window != win || got.HarnessPID <= 1 {
		t.Fatalf("the hook did not report for the pane by its environment, with a harness pid: %+v", got)
	}
	if got.Applied == nil || *got.Applied || got.Reason != "foreign_pane" {
		t.Errorf("a prompt from a harness outside the pane: applied=%s reason=%q, want refused with foreign_pane", appliedText(got.Applied), got.Reason)
	}
	checkUntouched("the outside prompt")

	got = outside(win, "claude-code", `{"hook_event_name":"SubagentStart","session_id":"phantom-1","agent_id":"a1b2c3","agent_type":"Explore"}`)
	if got.Reason != "foreign_pane" {
		t.Errorf("a subagent from a harness outside the pane: reason=%q, want foreign_pane", got.Reason)
	}
	checkUntouched("the outside subagent")

	got = outside(win, "droid", `{"hook_event_name":"SessionStart","session_id":"phantom-droid","source":"startup"}`)
	if got.Applied == nil || *got.Applied || got.Reason != "foreign_pane" {
		t.Errorf("a conversation id from a harness outside the pane: applied=%s reason=%q, want refused with foreign_pane", appliedText(got.Applied), got.Reason)
	}
	checkUntouched("the outside conversation id")

	// The positive half: the same harness and hook, run from the pane's shell.
	explain := filepath.Join(base, "inside-explain.json")
	line := "printf '%s' '" + `{"hook_event_name":"UserPromptSubmit","session_id":"own-1","prompt":"in the pane"}` + "' | " +
		harness + " " + tuiosBin + " agent-hook claude-code --timeout 10s --explain 2>" + explain + "; printf '%s-%s\\n' INSIDE DONE\n"
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, line); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitCapture(t, base, session, "0", "INSIDE-DONE")
	data, err := os.ReadFile(explain)
	if err != nil {
		t.Fatalf("read the inside hook's --explain: %v", err)
	}
	note("inside  %-10s %s", "claude-code", strings.TrimSpace(string(data)))
	got = parseExplained(t, string(data))
	if got.PaneBy != "env" || got.Window != win || got.HarnessPID <= 1 {
		t.Fatalf("the inside hook did not report for the pane by its environment, with a harness pid: %+v", got)
	}
	if got.Applied == nil || !*got.Applied || got.Reason != "" {
		t.Errorf("a prompt from the harness in the pane: applied=%s reason=%q, want applied", appliedText(got.Applied), got.Reason)
	}
	st := readAgentState(t, base, session, win)
	note("  pane after the inside prompt: state=%s harness=%s agent_session_id=%s", st.State, st.Harness, st.AgentSession)
	if st.State != "working" || st.AgentSession != "own-1" {
		t.Errorf("the harness in the pane did not set its state: %+v", st)
	}
	if _, listed := paneHarness(t, base, session)[win]; !listed {
		t.Error("list-agents does not list the pane its own harness reported")
	}

	// A pane chosen with --window by a harness whose environment names no pane
	// is the caller's choice, as the suite's other hook tests make it, and is
	// taken although the harness runs outside the pane.
	got = outside("", "claude-code", `{"hook_event_name":"Stop","session_id":"own-1"}`, "--session", session, "--window", win)
	if got.PaneBy != "flag" || got.Applied == nil || !*got.Applied || got.State != "done" {
		t.Errorf("a report for a pane chosen with --window: pane_by=%q applied=%s state=%q reason=%q, want applied, done", got.PaneBy, appliedText(got.Applied), got.State, got.Reason)
	}
}

// appliedText prints an --explain line's applied field.
func appliedText(applied *bool) string {
	if applied == nil {
		return "absent"
	}
	return fmt.Sprint(*applied)
}
