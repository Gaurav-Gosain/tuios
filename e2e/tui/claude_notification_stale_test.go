package tuie2e

import (
	"strings"
	"testing"
	"time"
)

// TestClaudeNotificationPromptClearsWhenThePromptGoes runs Claude Code's real
// hook against a pane that draws Claude Code's screens, and checks that a
// needs_input read from a Notification ends once the prompt is gone from the
// screen. Claude Code sends nothing when a prompt goes without a tool
// running: Esc at a permission menu, or a background agent's question
// answered in that agent's own view. The pane stayed on needs_input, with its
// alert and Inbox row, until the next prompt.
//
// The steps: a prompt takes the pane to working. A permission_prompt
// notification takes it to needs_input, and while the menu stays on the
// screen it stays there, through a redraw of the same menu. The menu is then
// replaced by the empty prompt box, as Esc leaves it, and the pane leaves
// needs_input. Then the same for a background agent's agent_needs_input.
//
// The artifact is claude-notification-stale.log in the test's artifact
// directory: each step and the pane's state after it.
//
// Negative control: with the hook not sending notification, the pane stays
// on needs_input after the menu is gone.
func TestClaudeNotificationPromptClearsWhenThePromptGoes(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-agent"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "claude", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	win := windowID(t, base, session, "claude")
	log := &stateLog{name: "claude-notification-stale"}
	defer log.save(t)

	hook := func(step, payload, state string) {
		t.Helper()
		if out := runHook(t, base, "claude-code", session, win, payload); out != "" {
			t.Fatalf("%s: the hook printed %q, want nothing", step, out)
		}
		waitAgentState(t, base, session, win, state, "claude-code", log, step)
	}
	// draw clears the pane and leaves lines on it, with the shell held in a
	// sleep so its prompt does not come back under them.
	draw := func(lines ...string) {
		t.Helper()
		quoted := make([]string, len(lines))
		for i, l := range lines {
			quoted[i] = "'" + strings.ReplaceAll(l, "'", `'\''`) + "'"
		}
		if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", win, "clear; printf '%s\\n' "+strings.Join(quoted, " ")+"; sleep 600\n"); err != nil {
			t.Fatalf("send-text: %v\n%s", err, out)
		}
	}
	interrupt := func() {
		t.Helper()
		if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-w", win, "C-c"); err != nil {
			t.Fatalf("send-keys: %v\n%s", err, out)
		}
	}
	rule := strings.Repeat("─", 60)
	menu := []string{"Bash command", "  go test ./...", "Do you want to proceed?", "❯ 1. Yes", "  2. Yes, and don't ask again for go test commands", "  3. No, and tell Claude what to do differently (esc)"}
	promptBox := []string{"⏺ Stopped at your request.", "", rule, "❯ ", rule, "  ? for shortcuts"}
	// holds checks the pane stays on state for a while after a redraw.
	holds := func(state, step string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if st := readAgentState(t, base, session, win); st.State != state {
				log.add("%-44s state=%s source=%s message=%q", step, st.State, st.Source, st.Message)
				t.Fatalf("%s: the pane is %s, want it to stay %s", step, st.State, state)
			}
			time.Sleep(200 * time.Millisecond)
		}
		st := readAgentState(t, base, session, win)
		log.add("%-44s state=%s source=%s message=%q", step, st.State, st.Source, st.Message)
	}
	// leaves waits for the pane to leave needs_input.
	leaves := func(step string) {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			st := readAgentState(t, base, session, win)
			if st.State != "needs_input" {
				log.add("%-44s state=%s source=%s message=%q", step, st.State, st.Source, st.Message)
				return
			}
			if time.Now().After(deadline) {
				log.add("%-44s state=%s source=%s message=%q", step, st.State, st.Source, st.Message)
				t.Fatalf("%s: the pane is still needs_input (%q) with the prompt gone from its screen", step, st.Message)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	hook("UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","session_id":"cl-e2e","prompt":"run the tests"}`, "working")
	draw(menu...)
	hook("Notification permission_prompt", `{"hook_event_name":"Notification","session_id":"cl-e2e","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`, "needs_input")
	interrupt()
	draw(menu...)
	holds("needs_input", "the menu drawn again")
	interrupt()
	draw(promptBox...)
	leaves("Esc: the menu gone, the prompt box empty")

	hook("UserPromptSubmit again", `{"hook_event_name":"UserPromptSubmit","session_id":"cl-e2e","prompt":"start the deploy agent"}`, "working")
	hook("Notification agent_needs_input", `{"hook_event_name":"Notification","session_id":"cl-e2e","message":"production-setup needs your input","notification_type":"agent_needs_input"}`, "needs_input")
	interrupt()
	draw(promptBox...)
	leaves("the background agent's question answered")
}
