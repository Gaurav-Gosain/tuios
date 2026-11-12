package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// The ACP version 2 tests: the real daemon, the real pane program and the
// stand-in agent in testdata/fakeacp, which speaks version 1, version 2,
// both or neither. Version 2 is a draft of the spec, so a pane asks for it
// only when [agents] acp_version = 2 says so; these tests set it in the
// config file the daemon reads. Each saves its frames (TUIOS_E2E_FRAMES) and
// a log of what it checked.

// writeACPVersion writes [agents] acp_version into the config the daemon of
// base reads.
func writeACPVersion(t *testing.T, base string, v int) {
	t.Helper()
	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios")
	mustMkdir(dir)
	body := "[agents]\nacp_version = " + string(rune('0'+v)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}
}

// protoState is the state list-agents reports for the one agent in session.
func protoState(t *testing.T, base, session string) string {
	t.Helper()
	raw, err := tuiosCLI(t, base, "list-agents", "-s", session, "--json")
	if err != nil {
		return ""
	}
	var listing struct {
		Agents []struct {
			State string `json:"state"`
		} `json:"agents"`
	}
	if json.Unmarshal([]byte(raw), &listing) != nil || len(listing.Agents) != 1 {
		return ""
	}
	return listing.Agents[0].State
}

// waitProtoState waits until the agent in session reads state.
func waitProtoState(t *testing.T, base, session, state string) {
	t.Helper()
	waitTextOf(t, "the agent never read "+state, uiTimeout, func() string { return protoState(t, base, session) },
		func(got string) bool { return got == state })
}

// waitProtoPane is waitCapture on the pane's lines unwrapped, so a long line
// the pane wrapped still holds its words in order.
func waitProtoPane(t *testing.T, base, session, window string, markers ...string) string {
	t.Helper()
	return waitTextOf(t, "the pane never showed "+strings.Join(markers, " | "), uiTimeout, func() string {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", session, "-w", window, "--unwrap")
		return out
	}, func(out string) bool { return containsAll(out, markers...) })
}

// attachAgentSession attaches a client to session, so the frames show the
// protocol pane.
func attachAgentSession(t *testing.T, base, session string) *tuitest.Terminal {
	t.Helper()
	term := startIn(t, base, startOpts{args: []string{"attach", session}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return countWindows(s) >= 1 }, bootTimeout); err != nil {
		t.Fatalf("client never attached: %v\n%s", err, term.Snapshot())
	}
	pause(t, "tuios drops plain keys for a moment after it enters terminal mode", insertGuard)
	return term
}

// TestProtocolPaneACPv2TurnAndPermission runs an agent that speaks only ACP
// version 2, with acp_version = 2:
//
//   - the pane says it speaks version 2, names the model from the session's
//     config options, and the turn ends on the agent's idle state, after the
//     reply, with the OSC sequence in the reply made inert;
//   - a command permission shows its title, its cwd and the command, reads
//     needs_input, and 1 in the pane allows it;
//   - a whole message that replaces a streamed one is shown again, said to
//     replace it;
//   - a notice, a compaction, the slash commands and an update of a kind no
//     client knows each show as one plain line, and the plan as a plan.
func TestProtocolPaneACPv2TurnAndPermission(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeACPVersion(t, base, 2)
	fake := buildFakeACP(t)

	out, err := tuiosCLI(t, base, "start-agent", fake+" -versions=2", "--protocol", "acp", "-s", "e2e-agent", "--name", "helper", "--prompt", "hello there")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is ready: it reads idle") || !strings.Contains(out, "The prompt was typed and taken.") {
		t.Fatalf("start-agent printed:\n%s", out)
	}
	term := attachAgentSession(t, base, "e2e-agent")
	pane := waitProtoPane(t, base, "e2e-agent", "helper", "connected to fakeacp 2.0 (Fake Model Two), ACP version 2", "you  hello there", "ECHO: hello there]0;pwned", "turn finished")
	if strings.Contains(pane, "TTYLEAK") {
		t.Errorf("the agent wrote to the pane past the transcript:\n%s", pane)
	}
	// The reply comes before the end of the turn: the turn ended on the
	// idle state, not on the prompt's answer, which came first.
	if strings.Index(pane, "ECHO: hello there") > strings.Index(pane, "turn finished") {
		t.Errorf("the turn ended before its reply:\n%s", pane)
	}
	waitProtoState(t, base, "e2e-agent", "done")
	saveFrame(t, term, "acp-v2-turn")

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "please run the tests\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitProtoPane(t, base, "e2e-agent", "helper", "permission execute: go test ./...", "Run the tests?", "in /fake/work", "reason: The agent wants to run the test suite.", "1 Allow")
	waitProtoState(t, base, "e2e-agent", "needs_input")
	saveFrame(t, term, "acp-v2-permission")
	// The pane is in front of the person, so it is answered there: the
	// Inbox holds a question only for a pane the person is not looking at,
	// the same for both versions (TestProtocolPaneTurnAndInboxApproval).
	pause(t, "a held approval takes an answer key only after it has been on screen for a moment", answerSettle)
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "1"); err != nil {
		t.Fatalf("answer: %v\n%s", err, out)
	}
	waitProtoPane(t, base, "e2e-agent", "helper", "answered: Allow (in the pane)", "PERMISSION: allow", "[done] execute: go test ./...")
	waitProtoState(t, base, "e2e-agent", "done")

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "fix it\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	pane = waitProtoPane(t, base, "e2e-agent", "helper", "Draft ans", "the agent replaced its message:", "FINAL ANSWER: the bug is fixed")
	if strings.Count(pane, "FINAL ANSWER") != 1 {
		t.Errorf("the replacing message was shown %d times:\n%s", strings.Count(pane, "FINAL ANSWER"), pane)
	}

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "show usage\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	pane = waitProtoPane(t, base, "e2e-agent", "helper", "[x] read", "[>] fix", "note: Rate limited: retrying in 5s",
		"compacting the context", "context compacted", "commands: /init /review",
		"the agent sent an update tuios does not show: _fake.example/widget")
	saveFrame(t, term, "acp-v2-updates")
	alive(t, term, "after an ACP version 2 conversation")
}

// TestProtocolPaneACPv2CancelAndWait: a cancel ends a version 2 turn when the
// agent says it stopped, after its last words; requires_action with no
// question on screen reads needs_input; a question the agent takes back
// leaves the pane.
func TestProtocolPaneACPv2CancelAndWait(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeACPVersion(t, base, 2)
	fake := buildFakeACP(t)
	out, err := tuiosCLI(t, base, "start-agent", fake+" -versions=1,2", "--protocol", "acp", "-s", "e2e-agent", "--name", "helper", "--prompt", "a slow job")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	term := attachAgentSession(t, base, "e2e-agent")
	waitProtoPane(t, base, "e2e-agent", "helper", "ACP version 2", "[running] execute: sleep 600")
	waitProtoState(t, base, "e2e-agent", "working")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "\x03"); err != nil {
		t.Fatalf("send Ctrl+C: %v\n%s", err, out)
	}
	pane := waitProtoPane(t, base, "e2e-agent", "helper", "cancelling the turn", "STOPPED AFTER CANCEL", "turn cancelled")
	if strings.Index(pane, "STOPPED AFTER CANCEL") > strings.Index(pane, "turn cancelled") {
		t.Errorf("the turn ended before the agent's last words:\n%s", pane)
	}
	waitProtoState(t, base, "e2e-agent", "idle")
	saveFrame(t, term, "acp-v2-cancel")

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "wait for me\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitProtoPane(t, base, "e2e-agent", "helper", "ECHO: wait for me")
	waitProtoState(t, base, "e2e-agent", "needs_input")
	saveFrame(t, term, "acp-v2-requires-action")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "\x03"); err != nil {
		t.Fatalf("send Ctrl+C: %v\n%s", err, out)
	}
	waitProtoPane(t, base, "e2e-agent", "helper", "ECHO: wait for me", "turn cancelled")
	waitProtoState(t, base, "e2e-agent", "idle")

	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-agent", "-w", "helper", "withdraw it\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
	waitProtoPane(t, base, "e2e-agent", "helper", "permission execute: rm -rf build", "Delete the build folder?")
	pane = waitProtoPane(t, base, "e2e-agent", "helper", "answered: cancelled (the agent withdrew the question)", "WITHDRAWN: cancelled")
	if strings.Contains(pane, "answer> press a number") {
		t.Errorf("the withdrawn question still holds the prompt line:\n%s", pane)
	}
	waitProtoState(t, base, "e2e-agent", "done")
	saveFrame(t, term, "acp-v2-withdrawn")
	alive(t, term, "after cancelling ACP version 2 turns")
}

// TestProtocolPaneACPVersionNegotiation: which version a pane speaks, by what
// the agent speaks and what the config asks for.
//
//   - an agent of version 1 only, asked for 2, answers 1 on the same
//     connection, and the turn works as version 1;
//   - an agent of version 2 only, with the default, says which versions each
//     side speaks and how to turn on version 2;
//   - an agent that refuses both is started a second time for version 1, and
//     the pane names both refusals.
func TestProtocolPaneACPVersionNegotiation(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	writeACPVersion(t, base, 2)
	fake := buildFakeACP(t)
	logs := t.TempDir()

	v1log := filepath.Join(logs, "v1.log")
	out, err := tuiosCLI(t, base, "start-agent", fake+" -versions=1 -log="+v1log, "--protocol", "acp", "-s", "e2e-v1", "--name", "old", "--prompt", "hello there")
	if err != nil {
		t.Fatalf("start-agent: %v\n%s", err, out)
	}
	pane := waitProtoPane(t, base, "e2e-v1", "old", "connected to fakeacp 1.0 (Fake Model), ACP version 1", "ECHO: hello there", "turn finished")
	if got, _ := os.ReadFile(v1log); string(got) != "initialize 2\n" {
		t.Errorf("the version 1 agent read %q, want one initialize asking for 2", got)
	}
	termV1 := attachAgentSession(t, base, "e2e-v1")
	saveFrame(t, termV1, "acp-negotiation-v1-only")
	_ = pane

	refuselog := filepath.Join(logs, "refuse.log")
	_, _ = tuiosCLI(t, base, "start-agent", fake+" -versions=none -log="+refuselog, "--protocol", "acp", "-s", "e2e-none", "--name", "refuses", "--no-wait")
	waitProtoPane(t, base, "e2e-none", "refuses", "the agent did not start", "the agent refused ACP version 2", "and version 1", "tuios speaks ACP versions 2 and 1")
	if got, _ := os.ReadFile(refuselog); string(got) != "initialize 2\ninitialize 1\n" {
		t.Errorf("the refusing agent read %q, want 2 and then 1 on a fresh start", got)
	}
	termNone := attachAgentSession(t, base, "e2e-none")
	saveFrame(t, termNone, "acp-negotiation-refuses-both")
	alive(t, termNone, "after the version negotiation panes")
}

// TestProtocolPaneACPv2OnlyAgentByDefault: with no acp_version, a pane asks
// for version 1, so an agent of version 2 only cannot be spoken to; the pane
// says which versions each side speaks and how to turn version 2 on.
func TestProtocolPaneACPv2OnlyAgentByDefault(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	fake := buildFakeACP(t)
	v2log := filepath.Join(t.TempDir(), "v2.log")
	_, _ = tuiosCLI(t, base, "start-agent", fake+" -versions=2 -log="+v2log, "--protocol", "acp", "-s", "e2e-v2", "--name", "new", "--no-wait")
	waitProtoPane(t, base, "e2e-v2", "new", "the agent did not start", "the agent speaks ACP version 2, and tuios speaks ACP version 1", "agents.acp_version = 2")
	if got, _ := os.ReadFile(v2log); string(got) != "initialize 1\n" {
		t.Errorf("the agent read %q, want one initialize asking for 1", got)
	}
	term := attachAgentSession(t, base, "e2e-v2")
	saveFrame(t, term, "acp-negotiation-v2-only-default")
	alive(t, term, "after a version 2 only agent with the default")
}
