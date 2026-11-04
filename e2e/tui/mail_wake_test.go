package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
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
//     Nor may it type the sender's name, which is a pane title a sender sets.
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
// agent its hook told is woken too. With the wake typing the hook's line,
// which names senders, the sender's name reaches the pane.
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
		// The sender's name is text a sender chose too: a pane's title.
		if strings.Contains(out, `"review"`) {
			t.Fatalf("%s: the sender's name was typed into the agent:\n%s", step, out)
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
			if !strings.Contains(out, `GOT[You have 3 unread tuios messages. Read them with: tuios read-agent-messages -s `+session+` -w `+idle[:8]+` --unread]`) {
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
		if n == 1 && strings.Contains(out, `GOT[You have 1 unread tuios message. Read it with:`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the agent was not told about its mail once at rest:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestMailWakeTurnsOff runs the same stand-in agent with [agents.mail] wake =
// false: mail for it types nothing. wake = true written to the file, as a
// process in a pane could write it, still types nothing. The positive half,
// in the same daemon: once the person runs tuios config apply, the next mail
// wakes it.
//
// Negative controls (NEGATIVE_CONTROLS.md): with SetMailWake ignoring its
// argument, the first mail types a line. With a file change allowed to turn
// the wake on, the mail after the file change types a line.
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

	// A process in a pane can write the file, so the file alone does not
	// turn the wake back on: mail sent after the change still types nothing.
	if err := os.WriteFile(cfg, []byte("[agents.mail]\nwake = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", agent, "second"); err != nil {
		t.Fatalf("send-agent-message: %v\n%s", err, out)
	}
	time.Sleep(mailWakeWait)
	if n, out := gotLines(t, base, session, agent); n != 0 {
		t.Fatalf("wake = true written to the file turned the wake on without the person:\n%s", out)
	}

	// The person applies the file from outside tuios, and the next mail
	// wakes the agent.
	if out, err := tuiosCLI(t, base, "config", "apply"); err != nil {
		t.Fatalf("tuios config apply: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", agent, "third"); err != nil {
		t.Fatalf("send-agent-message: %v\n%s", err, out)
	}
	deadline := time.Now().Add(mailWakeWait + uiTimeout)
	for {
		n, out := gotLines(t, base, session, agent)
		if n == 1 && strings.Contains(out, "GOT[You have 3 unread tuios messages") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("with wake = true again the agent was not told about its mail:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestMailWakeTakesNoAuthorityFromAReadOnlyCaller sends mail to an agent at
// rest from tuios mcp, whose connection is read_only: it may type into no
// pane, so its mail must not get a line typed either. The positive half, in
// the same daemon: mail from the person's shell wakes the agent.
//
// Negative control (NEGATIVE_CONTROLS.md): with the scope check cut from
// mailOriginOf, the read_only connection's mail types a line.
func TestMailWakeTakesNoAuthorityFromAReadOnlyCaller(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-wakero"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	agent := startMailAgent(t, base, session, "agent")
	reqs := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tuios_send_agent_message","arguments":{"session":"` + session + `","to":"` + agent + `","text":"from a read-only caller"}}}`,
	}, "\n") + "\n"
	out, err := tuiosCLIStdin(t, base, reqs, "mcp", "--scope", "all")
	if err != nil || !strings.Contains(out, `agent_message_sent`) {
		t.Fatalf("tuios mcp did not send the mail: %v\n%s", err, out)
	}
	time.Sleep(mailWakeWait)
	if n, out := gotLines(t, base, session, agent); n != 0 {
		t.Fatalf("mail from a read_only connection typed a line into the agent:\n%s", out)
	}

	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", session, "-w", agent, "from the shell"); err != nil {
		t.Fatalf("send-agent-message: %v\n%s", err, out)
	}
	deadline := time.Now().Add(mailWakeWait + uiTimeout)
	for {
		n, out := gotLines(t, base, session, agent)
		if n == 1 && strings.Contains(out, "GOT[You have 2 unread tuios messages") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mail from the shell did not wake the agent:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// TestMailWakeTakesNoAuthorityFromAPaneOnAnotherMachine sends mail to an
// agent at rest from a pane of this session whose process runs on build. Its
// call reaches this daemon forwarded as the pane, which queue-prompt refuses,
// so its mail must not get a line typed either. The positive half, in the
// same daemon: mail from the person's shell wakes the agent.
//
// Negative control (NEGATIVE_CONTROLS.md): with the forwarded-pane check cut
// from mailOriginOf, the hosted pane's mail types a line.
func TestMailWakeTakesNoAuthorityFromAPaneOnAnotherMachine(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("create the far session: %v\n%s", err, out)
	}
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}

	term := startIn(t, base, startOpts{args: []string{"new", "home"}, env: env})
	waitBoot(t, term)
	waitForHostListing(t, base, func(s string) bool {
		return containsAll(s, "build", "up")
	}, "the daemon never reported build up")
	agent := startMailAgent(t, base, "home", "agent")

	script := tuiosBin + ` send-agent-message -s home -w ` + agent + ` --from "$TUIOS_PANE_ID" 'from build'; echo "SENT $?"; sleep 600`
	if out, err := tuiosCLIEnv(t, base, env, "new-window", "hosted", "-s", "home",
		"--host", "build", "--", "/bin/sh", "-c", script); err != nil {
		t.Fatalf("create a window on build: %v\n%s", err, out)
	}
	hosted := windowID(t, base, "home", "hosted")
	sent := waitCapture(t, base, "home", hosted, "SENT")
	if !strings.Contains(sent, "SENT 0") {
		t.Fatalf("the hosted pane could not send its mail:\n%s", sent)
	}
	time.Sleep(mailWakeWait)
	if n, out := gotLines(t, base, "home", agent); n != 0 {
		t.Fatalf("mail from a pane on another machine typed a line into the agent:\n%s", out)
	}

	if out, err := tuiosCLI(t, base, "send-agent-message", "-s", "home", "-w", agent, "from the shell"); err != nil {
		t.Fatalf("send-agent-message: %v\n%s", err, out)
	}
	deadline := time.Now().Add(mailWakeWait + uiTimeout)
	for {
		n, out := gotLines(t, base, "home", agent)
		if n == 1 && strings.Contains(out, "GOT[You have 2 unread tuios messages") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mail from the shell did not wake the agent:\n%s", out)
		}
		time.Sleep(250 * time.Millisecond)
	}
	alive(t, term, "after mail from a hosted pane")
}

// delegateChildScript is the child agent of the delegate tests. It reports
// idle, then for each line typed into it reports working and, after DELAY
// seconds (default 3), prints ANSWER-7f3[line] and reports done. A line with
// ASK in it is a question instead: it prints the question and reports
// needs_input of kind question.
const delegateChildScript = `T="$1"
stty -echo
echo CHILD-READY
"$T" set-agent-state idle -w "$TUIOS_PANE_ID" >/dev/null
while IFS= read -r l; do
  "$T" set-agent-state working -w "$TUIOS_PANE_ID" >/dev/null
  case "$l" in
  *ASK*)
    echo "QUESTION-5c1: which file should I read?"
    "$T" set-agent-state needs_input --kind question -m "Which file should I read?" -w "$TUIOS_PANE_ID" >/dev/null
    continue ;;
  esac
  sleep "${DELAY:-3}"
  echo "ANSWER-7f3[$l]"
  "$T" set-agent-state done -w "$TUIOS_PANE_ID" >/dev/null
done
`

// delegateRow is the part of a list-agents row the delegate tests read.
type delegateRow struct {
	Window     string `json:"window_id"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	Delegation *struct {
		Role       string `json:"role"`
		Isolation  string `json:"isolation"`
		Mode       string `json:"mode"`
		State      string `json:"state"`
		ResultMail uint64 `json:"result_mail"`
		AskedAt    int64  `json:"asked_at"`
	} `json:"delegation"`
}

// delegateRows lists the agent panes of every session, by window id.
func delegateRows(t *testing.T, base string) map[string]delegateRow {
	t.Helper()
	res := mustCall(t, base, "list-agents", map[string]any{"all_sessions": true})
	raw, _ := json.Marshal(res["agents"])
	var rows []delegateRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("list-agents rows: %v: %s", err, raw)
	}
	out := map[string]delegateRow{}
	for _, r := range rows {
		out[r.Window] = r
	}
	return out
}

// delegateMail is one message of the parent's inbox, as the delegate tests
// read it.
type delegateMail struct {
	From    string `json:"from"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// parentMail reads every message to window in session, marking nothing.
func parentMail(t *testing.T, base, session, window string) []delegateMail {
	t.Helper()
	res := mustCall(t, base, "read-agent-messages", map[string]any{"session": session, "to": window, "peek": true, "limit": 100})
	raw, _ := json.Marshal(res["messages"])
	var msgs []delegateMail
	if err := json.Unmarshal(raw, &msgs); err != nil {
		t.Fatalf("read-agent-messages: %v: %s", err, raw)
	}
	return msgs
}

// delegateResult is the part of a delegate --json answer the tests read.
type delegateResult struct {
	Window    string `json:"window_id"`
	Session   string `json:"session"`
	Delegated bool   `json:"delegated"`
	Mode      string `json:"mode"`
	Next      string `json:"next"`
	Outcome   string `json:"outcome"`
	Answer    string `json:"answer"`
	Untrusted bool   `json:"untrusted"`
	WaitNote  string `json:"wait_note"`
	Worktree  string `json:"worktree"`
	Base      string `json:"base"`
	Note      string `json:"worktree_note"`
	Question  *struct {
		Kind    string   `json:"kind"`
		Message string   `json:"message"`
		Lines   []string `json:"lines"`
	} `json:"question"`
}

// runDelegate runs tuios delegate --json and decodes its answer. It fails the
// test when the command fails.
func runDelegate(t *testing.T, base string, args ...string) (delegateResult, string) {
	t.Helper()
	out, err := tuiosCLI(t, base, append([]string{"delegate", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("delegate %v: %v\n%s", args, err, out)
	}
	var res delegateResult
	start := max(strings.Index(out, "{"), 0)
	if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
		t.Fatalf("delegate output: %v\n%s", err, out)
	}
	return res, out
}

// TestDelegateMailsTheAnswerAndWakesTheParentOnce delegates a task to a
// stand-in child agent, for a stand-in parent agent at rest, against a real
// daemon.
//
// How this could pass wrongly, written down first:
//   - The call could wait for the child. The child takes 3 s after its prompt,
//     and the call must return with the parent's inbox still empty.
//   - The answer could be mailed twice, or the parent woken once per state
//     event. The parent must hold one message and show one typed line, also
//     well after.
//   - The wake could type the child's text into the parent. The answer holds
//     marker text the parent's pane must never show.
//   - The answer could be something the child printed before its prompt. The
//     message must hold the answer line with the prompt's own marker.
//
// It also checks the record: the role on list-agents, done with the message
// id, and a research child given the read grant only.
//
// The artifact is delegate-async.log in the test's artifact directory.
//
// Negative control (NEGATIVE_CONTROLS.md): with delegateMail sending nothing,
// the parent never gets the answer.
func TestDelegateMailsTheAnswerAndWakesTheParentOnce(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-delegate"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	parent := startMailAgent(t, base, session, "parent")
	child := filepath.Join(base, "child.sh")
	if err := os.WriteFile(child, []byte(delegateChildScript), 0o755); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "delegate-async.log")
		_ = os.WriteFile(path, []byte(log.String()), 0o644)
		t.Logf("artifact: %s", path)
	}()

	started := time.Now()
	res, out := runDelegate(t, base, "--parent", parent, "--role", "research", "--agent", "sh "+child+" "+tuiosBin, "find marker-q41")
	took := time.Since(started)
	fmt.Fprintf(&log, "-- delegate returned after %v:\n%s\n", took.Round(time.Millisecond), out)
	if !res.Delegated || res.Mode != "async" || !strings.Contains(res.Next, "End your turn") {
		t.Fatalf("ASSERTION: delegate did not hand the task over async: %+v", res)
	}
	if msgs := parentMail(t, base, session, parent); len(msgs) != 0 {
		t.Fatalf("ASSERTION: the parent had mail when delegate returned, so the call waited for the child: %+v", msgs)
	}
	waitCapture(t, base, session, res.Window, "ANSWER-7f3[find marker-q41]")

	// The answer is mailed once, from the child.
	deadline := time.Now().Add(uiTimeout)
	var msgs []delegateMail
	for {
		msgs = parentMail(t, base, session, parent)
		if len(msgs) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(&log, "-- the parent's mail: %+v\n", msgs)
	if len(msgs) != 1 {
		t.Fatalf("ASSERTION: the parent has %d messages, want the one answer: %+v", len(msgs), msgs)
	}
	if m := msgs[0]; m.From != res.Window || m.Subject != "Delegated task done: research" || !strings.Contains(m.Text, "ANSWER-7f3[find marker-q41]") {
		t.Fatalf("ASSERTION: the answer mail is wrong: %+v", m)
	}

	// The parent is woken once, with no text of the child.
	deadline = time.Now().Add(mailWakeWait + uiTimeout)
	for {
		n, got := gotLines(t, base, session, parent)
		if n > 0 {
			fmt.Fprintf(&log, "-- the parent's pane:\n%s\n", got)
			if !strings.Contains(got, "GOT[You have 1 unread tuios message. Read it with:") {
				t.Fatalf("ASSERTION: the parent got the wrong line:\n%s", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the parent was never woken for the answer:\n%s", got)
		}
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(mailWakeWait)
	n, got := gotLines(t, base, session, parent)
	if n != 1 {
		t.Fatalf("ASSERTION: the parent was typed %d lines, want 1:\n%s", n, got)
	}
	if strings.Contains(got, "ANSWER-7f3") || strings.Contains(got, "marker-q41") {
		t.Fatalf("ASSERTION: the child's text was typed into the parent:\n%s", got)
	}
	if msgs := parentMail(t, base, session, parent); len(msgs) != 1 {
		t.Fatalf("ASSERTION: the parent has %d messages later, want 1: %+v", len(msgs), msgs)
	}

	row := delegateRows(t, base)[res.Window]
	fmt.Fprintf(&log, "-- the child's row: %+v %+v\n", row, row.Delegation)
	if row.Role != "research" || row.Delegation == nil || row.Delegation.State != "done" || row.Delegation.ResultMail == 0 || row.Delegation.Mode != "async" {
		t.Fatalf("ASSERTION: the child's record is wrong: %+v %+v", row, row.Delegation)
	}
	grants, err := tuiosCLI(t, base, "list-agents", "-s", session, "--select", "role:research")
	fmt.Fprintf(&log, "-- list-agents --select role:research:\n%s\n", grants)
	if err != nil || !strings.Contains(grants, res.Window[:8]) || strings.Contains(grants, "\n│ "+parent[:8]) || !strings.Contains(grants, "ROLE") {
		t.Fatalf("ASSERTION: the role selector or column is wrong: %v\n%s", err, grants)
	}
	held, _ := tuiosCLI(t, base, "list-windows", "--json", "-s", session)
	var lw struct {
		Windows []struct {
			ID     string   `json:"window_id"`
			Grants []string `json:"grants"`
		} `json:"windows"`
	}
	_ = json.Unmarshal([]byte(held), &lw)
	for _, w := range lw.Windows {
		if w.ID == res.Window && strings.Join(w.Grants, ",") != "read" {
			t.Fatalf("ASSERTION: a research child holds %v, want read", w.Grants)
		}
	}
}

// TestDelegateWaitReturnsTheOutcome delegates with --wait from outside every
// pane, with no parent, and with a parent for the timeout.
//
// How this could pass wrongly: a wait could return at its timeout every time,
// so the done and question cases check the outcome and its text, and finish
// well inside the wait. A timeout could stop the child, so the child must
// still finish afterwards, and its answer must then reach the parent by mail.
// A wait could also mail the answer it returned, so the parent's inbox holds
// only the timed-out task's answer.
//
// Negative control (NEGATIVE_CONTROLS.md): with deliver handing nothing to a
// waiting call, every wait ends at its timeout.
func TestDelegateWaitReturnsTheOutcome(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-delegate-wait"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	child := filepath.Join(base, "child.sh")
	if err := os.WriteFile(child, []byte(delegateChildScript), 0o755); err != nil {
		t.Fatal(err)
	}
	agent := "sh " + child + " " + tuiosBin
	var log strings.Builder
	defer func() {
		path := filepath.Join(artifactDir(t), "delegate-wait.log")
		_ = os.WriteFile(path, []byte(log.String()), 0o644)
		t.Logf("artifact: %s", path)
	}()

	// No parent and no wait: refused, nothing starts.
	out, err := tuiosCLI(t, base, "delegate", "-s", session, "--agent", agent, "nobody gets this")
	fmt.Fprintf(&log, "-- no parent, no wait:\n%s\n", out)
	if err == nil || !strings.Contains(out, "needs an agent to send the answer to") {
		t.Fatalf("ASSERTION: a delegate with no one to mail was not refused: %v\n%s", err, out)
	}

	// Done.
	started := time.Now()
	res, out := runDelegate(t, base, "-s", session, "--agent", agent, "--wait", "60s", "--name", "waiter", "quick marker-w9")
	fmt.Fprintf(&log, "-- wait for done, %v:\n%s\n", time.Since(started).Round(time.Millisecond), out)
	if res.Outcome != "done" || !strings.Contains(res.Answer, "ANSWER-7f3[quick marker-w9]") || !res.Untrusted {
		t.Fatalf("ASSERTION: --wait did not return the answer: %+v", res)
	}
	if took := time.Since(started); took > 40*time.Second {
		t.Fatalf("ASSERTION: the wait for done took %v, near its timeout", took)
	}
	text, _ := tuiosCLI(t, base, "delegate", "-s", session, "--agent", agent, "--wait", "60s", "--name", "waiter2", "quick marker-w10")
	fmt.Fprintf(&log, "-- wait for done, as text:\n%s\n", text)
	if !strings.Contains(text, "The child finished") || !strings.Contains(text, "ANSWER-7f3[quick marker-w10]") {
		t.Fatalf("ASSERTION: the text output does not show the answer:\n%s", text)
	}

	// A question.
	res, out = runDelegate(t, base, "-s", session, "--agent", agent, "--wait", "60s", "--name", "asker", "ASK me")
	fmt.Fprintf(&log, "-- wait for a question:\n%s\n", out)
	if res.Outcome != "question" || res.Question == nil || res.Question.Kind != "question" || !strings.Contains(res.Question.Message+strings.Join(res.Question.Lines, "\n"), "Which file should I read?") {
		t.Fatalf("ASSERTION: --wait did not return the question: %+v %+v", res, res.Question)
	}
	if row := delegateRows(t, base)[res.Window]; row.Delegation == nil || row.Delegation.State != "asking" || row.Delegation.AskedAt == 0 {
		t.Fatalf("ASSERTION: the asking child's record is wrong: %+v", row.Delegation)
	}

	// A timeout, for a parent: the child goes on, and its answer is mailed.
	parent := startMailAgent(t, base, session, "parent")
	res, out = runDelegate(t, base, "--parent", parent, "--agent", agent, "--env", "DELAY=6", "--wait", "1s", "--name", "slow", "slow marker-s3")
	fmt.Fprintf(&log, "-- wait that times out:\n%s\n", out)
	if res.Outcome != "timeout" || !strings.Contains(res.WaitNote, "still works") {
		t.Fatalf("ASSERTION: a 1 s wait on a 6 s task did not time out: %+v", res)
	}
	waitCapture(t, base, session, res.Window, "ANSWER-7f3[slow marker-s3]")
	deadline := time.Now().Add(uiTimeout)
	var msgs []delegateMail
	for {
		msgs = parentMail(t, base, session, parent)
		if len(msgs) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(&log, "-- the parent's mail after the timeout: %+v\n", msgs)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "ANSWER-7f3[slow marker-s3]") {
		t.Fatalf("ASSERTION: the timed-out child's answer did not reach the parent once: %+v", msgs)
	}
}

// TestDelegateWorktreeAndClamp delegates into a worktree, and from a parent in
// plan mode asks for a wider child.
//
// The worktree must be at the commit of the parent's checkout and must not
// hold its uncommitted file, and the answer must say so. The wide child must
// be refused with no window, as start-agent refuses it (child_clamp.go).
func TestDelegateWorktreeAndClamp(t *testing.T) {
	base, repo := fanFixture(t)
	const session = "e2e-delegate-wt"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "uncommitted-9b2.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(testutil.Git(t, repo, "rev-parse", "HEAD"))
	child := filepath.Join(base, "child.sh")
	if err := os.WriteFile(child, []byte(delegateChildScript), 0o755); err != nil {
		t.Fatal(err)
	}
	parent := startMailAgent(t, base, session, "parent")
	res, out := runDelegate(t, base, "--parent", parent, "--isolation", "worktree", "--cwd", repo, "--role", "test", "--agent", "sh "+child+" "+tuiosBin, "wt marker-t5")
	if !res.Delegated || res.Worktree == "" || res.Base != head || !strings.Contains(res.Note, "uncommitted") {
		t.Fatalf("ASSERTION: the worktree delegate is wrong (HEAD %s):\n%s", head, out)
	}
	got, err := exec.Command("git", "-C", res.Worktree, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(got)) != head {
		t.Fatalf("ASSERTION: the worktree is at %q, want %s: %v", got, head, err)
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "uncommitted-9b2.txt")); err == nil {
		t.Fatalf("ASSERTION: the worktree holds the parent's uncommitted file")
	}
	if res.Session == session {
		t.Fatalf("ASSERTION: the worktree child opened in the parent's session %s", res.Session)
	}
	waitCapture(t, base, res.Session, res.Window, "ANSWER-7f3[wt marker-t5]")

	// A parent in plan mode asks for a child in bypass mode.
	outDir := t.TempDir()
	parentBin := filepath.Join(t.TempDir(), "parent-bin")
	if err := os.MkdirAll(parentBin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"'" + tuiosBin + "' set-agent-state idle -w \"$TUIOS_PANE_ID\" >/dev/null\n" +
		"'" + tuiosBin + "' delegate --agent 'claude --dangerously-skip-permissions' --name wide 'do it' >'" + outDir + "/wide.txt' 2>&1\n" +
		"echo \"WIDE_EXIT=$?\" >>'" + outDir + "/wide.txt'\n" +
		"touch '" + outDir + "/done'\n" +
		"exec sleep 600\n"
	planned := filepath.Join(parentBin, "claude")
	if err := os.WriteFile(planned, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "new-window", "-s", session, "planner", "--no-focus", "--", planned, "--permission-mode", "plan"); err != nil {
		t.Fatalf("open the parent agent's pane: %v: %s", err, out)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(outDir, "done")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the parent agent never finished its call")
		}
		time.Sleep(200 * time.Millisecond)
	}
	wide, _ := os.ReadFile(filepath.Join(outDir, "wide.txt"))
	if strings.Contains(string(wide), "WIDE_EXIT=0") || !strings.Contains(string(wide), "permission mode") {
		t.Fatalf("ASSERTION: a wider child from a parent in plan mode was not refused:\n%s", wide)
	}
	if windows, _ := tuiosCLI(t, base, "list-windows", "-s", session); strings.Contains(windows, "wide") {
		t.Fatalf("ASSERTION: the refused child has a window:\n%s", windows)
	}
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "delegate-worktree-clamp.txt"), []byte("worktree:\n"+out+"\nwide child:\n"+string(wide)), 0o644)
	}
}
