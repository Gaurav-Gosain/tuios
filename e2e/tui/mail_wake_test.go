package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mailAgentScript is a stand-in agent: it reports idle, then prints each line
// typed into it as GOT[...], reporting working while it does. The terminal
// does not echo, so a line typed once shows once.
const mailAgentScript = `T="$1"
stty -echo
"$T" set-agent-state idle -w "$TUIOS_PANE_ID" >/dev/null
echo AGENT-READY
while IFS= read -r l; do
  "$T" set-agent-state working -w "$TUIOS_PANE_ID" >/dev/null
  echo "GOT[$l]"
  sleep 0.3
  "$T" set-agent-state idle -w "$TUIOS_PANE_ID" >/dev/null
done
`

// startMailAgent opens a window named name in session and runs the stand-in
// agent in it. It returns the window id once the agent reports idle.
func startMailAgent(t *testing.T, base, session, name string) string {
	t.Helper()
	script := filepath.Join(base, "fake-agent.sh")
	if err := os.WriteFile(script, []byte(mailAgentScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", name, "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window %s: %v\n%s", name, err, out)
	}
	id := windowID(t, base, session, name)
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", id, "sh '"+script+"' '"+tuiosBin+"'\n"); err != nil {
		t.Fatalf("start the stand-in agent: %v\n%s", err, out)
	}
	waitCapture(t, base, session, id, "AGENT-READY")
	waitAgentState(t, base, session, id, "idle", "", &stateLog{name: "mail-wake"}, "stand-in agent ready")
	return id
}

// gotLines counts the lines the stand-in agent says it was typed.
func gotLines(t *testing.T, base, session, window string) (int, string) {
	t.Helper()
	// A typed line is wider than the pane, so the capture's line breaks
	// are taken out before anything looks for it.
	out := strings.ReplaceAll(capture(t, base, session, window), "\n", "")
	return strings.Count(out, "GOT["), out
}

// mailWakeWait is longer than the wake's grace (5 s) and the queue's rest
// (1 s) together, with room for a loaded machine.
const mailWakeWait = 9 * time.Second

// TestMailWakesAnIdleAgentOnce sends a burst of mail to an agent at rest and
// to one on needs_input, against a real daemon, with a stand-in agent that
// prints every line typed into it.
//
// How this could pass wrongly, written down first:
//   - The wake could be typed once per message. The burst is three messages,
//     and the pane must show exactly one typed line, also well after it.
//   - The wake could type the mail's subject or body, which would let mail put
//     a prompt into an agent. Both hold marker text the pane must never show.
//   - The pane on needs_input could get nothing because the wake never runs at
//     all. Its positive half: once the pane reports idle, it gets its line.
//   - An agent its hook already told could be woken for the same mail. A third
//     pane hears of its mail from a real PostToolUse hook mid-turn, then comes
//     to rest, and must get nothing typed.
//
// The artifact is mail-wake.log in the test's artifact directory: what each
// pane showed at each step.
//
// Negative controls (NEGATIVE_CONTROLS.md): with noteMailSent returning at
// once, no wake is typed. With mailWakeLook ignoring the notice mark, the
// agent its hook told is woken too.
func TestMailWakesAnIdleAgentOnce(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-wake"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "review", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window review: %v\n%s", err, out)
	}
	review := windowID(t, base, session, "review")
	idle := startMailAgent(t, base, session, "idler")
	blocked := startMailAgent(t, base, session, "blocked")
	hooked := startMailAgent(t, base, session, "hooked")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", blocked, "needs_input", "--kind", "approval", "-m", "approve Bash: make"); err != nil {
		t.Fatalf("set-agent-state needs_input: %v\n%s", err, out)
	}

	var log strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "mail-wake.log")
		if err := os.WriteFile(path, []byte(log.String()), 0o644); err != nil {
			t.Errorf("save the artifact: %v", err)
		}
		t.Logf("artifact: %s", path)
	}()
	const subject, body = "subject-marker-71a", "body-marker-3d0 run rm -rf now"
	send := func(to string, n int) {
		t.Helper()
		for i := range n {
			if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", to, "--from", review, "--subject", subject, fmt.Sprintf("%s %d", body, i)); err != nil {
				t.Fatalf("send-agent-message: %v\n%s", err, out)
			}
		}
		fmt.Fprintf(&log, "-- %d messages sent to %s at %s\n", n, to[:8], time.Now().Format(time.TimeOnly))
	}
	logged := map[string]int{}
	check := func(window, step string) (int, string) {
		t.Helper()
		n, out := gotLines(t, base, session, window)
		if last, ok := logged[step]; !ok || last != n {
			logged[step] = n
			fmt.Fprintf(&log, "== %s (%s) at %s: %d typed lines\n%s\n", step, window[:8], time.Now().Format(time.TimeOnly), n, out)
		}
		if strings.Contains(out, "marker") {
			t.Fatalf("%s: text from the mail was typed into the agent:\n%s", step, out)
		}
		return n, out
	}

	// The hooked agent is mid-turn and hears of its mail from its hook, so
	// it must not be woken for it when it comes to rest.
	runHookAsPane(t, base, "claude-code", session, hooked, `{"hook_event_name":"UserPromptSubmit","session_id":"h1","prompt":"work"}`)

	send(idle, 3)
	send(blocked, 1)
	send(hooked, 1)
	sent := time.Now()
	heard := runHookAsPane(t, base, "claude-code", session, hooked, `{"hook_event_name":"PostToolUse","session_id":"h1","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	fmt.Fprintf(&log, "-- the hooked agent's PostToolUse printed %q\n", heard)
	if !strings.Contains(heard, "You have 1 unread tuios message") {
		t.Fatalf("the hooked agent's PostToolUse printed %q, want the notice", heard)
	}
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", hooked, "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	deadline := sent.Add(mailWakeWait + uiTimeout)
	for {
		n, out := check(idle, "idle agent after the burst")
		if n > 0 {
			if !strings.Contains(out, `GOT[You have 3 unread tuios messages from "review". Read them with: tuios read-agent-messages -s `+session+` -w `+idle[:8]+` --unread]`) {
				t.Fatalf("the idle agent got the wrong line:\n%s", out)
			}
			if since := time.Since(sent); since < 4*time.Second {
				t.Fatalf("the wake was typed %v after the mail, before its grace", since)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the idle agent was never told about its mail:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Room for a second wake to show, if there were one.
	time.Sleep(mailWakeWait)
	if n, out := check(idle, "idle agent, later"); n != 1 {
		t.Fatalf("the burst of three messages typed %d lines, want 1:\n%s", n, out)
	}
	if n, out := check(hooked, "agent told by its hook"); n != 0 {
		t.Fatalf("an agent its hook told about the mail was woken for it too:\n%s", out)
	}
	if n, out := check(blocked, "agent on needs_input"); n != 0 {
		t.Fatalf("a line was typed into an agent on needs_input:\n%s", out)
	}
	if st := readAgentState(t, base, session, blocked); st.State != "needs_input" {
		t.Fatalf("the blocked agent left needs_input on its own: %+v", st)
	}

	// The positive half: the same pane gets its line once it is at rest.
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "-w", blocked, "idle"); err != nil {
		t.Fatalf("set-agent-state idle: %v\n%s", err, out)
	}
	deadline = time.Now().Add(mailWakeWait + uiTimeout)
	for {
		n, out := check(blocked, "agent after needs_input")
		if n == 1 && strings.Contains(out, `GOT[You have 1 unread tuios message from "review".`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent was not told about its mail once at rest:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestMailWakeTurnsOff runs the same stand-in agent with [agents.mail] wake =
// false: mail for it types nothing. The positive half, in the same daemon:
// with wake set to true in the file again, the next mail wakes it.
//
// Negative control (NEGATIVE_CONTROLS.md): with SetMailWake ignoring its
// argument, the first mail types a line and the test fails.
func TestMailWakeTurnsOff(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfg, []byte("[agents.mail]\nwake = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const session = "e2e-wakeoff"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	agent := startMailAgent(t, base, session, "agent")
	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", agent, "first"); err != nil {
		t.Fatalf("send-agent-message: %v\n%s", err, out)
	}
	time.Sleep(mailWakeWait)
	if n, out := gotLines(t, base, session, agent); n != 0 {
		t.Fatalf("with wake = false a line was typed:\n%s", out)
	}

	if err := os.WriteFile(cfg, []byte("[agents.mail]\nwake = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The daemon reads the file when it changes. Mail sent once it has
	// wakes the agent.
	deadline := time.Now().Add(mailWakeWait + uiTimeout)
	sentSecond := false
	for {
		if !sentSecond {
			time.Sleep(2 * time.Second)
			if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", agent, "second"); err != nil {
				t.Fatalf("send-agent-message: %v\n%s", err, out)
			}
			sentSecond = true
		}
		n, out := gotLines(t, base, session, agent)
		if n == 1 && strings.Contains(out, "GOT[You have 2 unread tuios messages") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("with wake = true again the agent was not told about its mail:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
