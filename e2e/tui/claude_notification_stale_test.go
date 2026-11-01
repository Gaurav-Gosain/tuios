package tuie2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeStage is one pane in a throwaway daemon that draws Claude Code's
// screens, with Claude Code's real hook run against it. The pane never runs
// Claude Code: each frame is printed with the shell held in a read so its
// prompt does not come back under it, and Esc is modelled by the frame Esc
// leaves.
type claudeStage struct {
	t               *testing.T
	base, sess, win string
	log             *stateLog
	title           string
	// playing says a frame's read holds the shell.
	playing bool
}

// Claude Code's frames, as tuios's screen rules read them.
var (
	claudeRule      = strings.Repeat("─", 60)
	claudeMenu      = []string{"Bash command", "  go test ./...", "Do you want to proceed?", "❯ 1. Yes", "  2. Yes, and don't ask again for go test commands", "  3. No, and tell Claude what to do differently (esc)"}
	claudePromptBox = []string{"⏺ Stopped at your request.", "", claudeRule, "❯ ", claudeRule, "  ? for shortcuts"}
)

const (
	claudePermissionRequest = `{"hook_event_name":"PermissionRequest","session_id":"cl-e2e","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`
	claudePermissionNotice  = `{"hook_event_name":"Notification","session_id":"cl-e2e","message":"Claude needs your permission to use Bash","notification_type":"permission_prompt"}`
	claudePromptSubmit      = `{"hook_event_name":"UserPromptSubmit","session_id":"cl-e2e","prompt":"run the tests"}`
)

func newClaudeStage(t *testing.T, logName string) *claudeStage {
	t.Helper()
	base := t.TempDir()
	killDaemon(t, base)
	const session = "e2e-agent"
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "new-window", "claude", "-s", session, "--no-focus"); err != nil {
		t.Fatalf("new-window: %v\n%s", err, out)
	}
	c := &claudeStage{t: t, base: base, sess: session, win: windowID(t, base, session, "claude"), log: &stateLog{name: logName}}
	t.Cleanup(func() { c.log.save(t) })
	return c
}

// hook runs Claude Code's hook with payload and waits for the pane to be on
// state.
func (c *claudeStage) hook(step, payload, state string) {
	c.t.Helper()
	if out := runHook(c.t, c.base, "claude-code", c.sess, c.win, payload); out != "" {
		c.t.Fatalf("%s: the hook printed %q, want nothing", step, out)
	}
	waitAgentState(c.t, c.base, c.sess, c.win, state, "claude-code", c.log, step)
}

// draw stops the frame on the pane and draws lines in its place, under the
// stage's title when it has one.
func (c *claudeStage) draw(lines ...string) {
	c.t.Helper()
	c.play(c.frame(lines))
}

// drawUntil draws lines, and the frame next once the file flag exists, with
// nothing written to the pane in between.
func (c *claudeStage) drawUntil(lines []string, flag string, next ...string) {
	c.t.Helper()
	c.play(c.frame(lines) + fmt.Sprintf("until [ -e '%s' ]; do sleep 0.1; done; ", flag) + c.frame(next))
}

// frame is the shell command that clears the pane and prints one frame.
func (c *claudeStage) frame(lines []string) string {
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = "'" + strings.ReplaceAll(l, "'", `'\''`) + "'"
	}
	cmd := "clear; "
	if c.title != "" {
		cmd += `printf '\033]0;%s\007' '` + c.title + `'; `
	}
	if len(lines) > 0 {
		cmd += "printf '%s\\n' " + strings.Join(quoted, " ") + "; "
	}
	return cmd
}

// play ends the frame the pane holds, if any, and types cmd, which holds its
// frame with a read so the shell's prompt does not come back under it. A
// newline ends the frame. A Ctrl-C to a sleep did too, but on macOS the next
// write to the pane failed with EIO in four runs of five on a loaded machine.
func (c *claudeStage) play(cmd string) {
	c.t.Helper()
	if c.playing {
		if out, err := tuiosCLI(c.t, c.base, "send-text", "-s", c.sess, "-w", c.win, "\n"); err != nil {
			c.t.Fatalf("send-text: %v\n%s", err, out)
		}
	}
	c.playing = true
	if out, err := tuiosCLI(c.t, c.base, "send-text", "-s", c.sess, "-w", c.win, cmd+"read x\n"); err != nil {
		c.t.Fatalf("send-text: %v\n%s", err, out)
	}
}

func (c *claudeStage) note(step string, st agentStateJSON) {
	c.log.add("%-48s state=%s source=%s blocked_by=%s message=%q", step, st.State, st.Source, st.BlockedBy, st.Message)
}

// holds checks the pane stays on state for d, and returns what it says then.
func (c *claudeStage) holds(state, step string, d time.Duration) agentStateJSON {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if st := readAgentState(c.t, c.base, c.sess, c.win); st.State != state {
			c.note(step, st)
			c.t.Fatalf("%s: the pane is %s, want it to stay %s", step, st.State, state)
		}
		time.Sleep(100 * time.Millisecond)
	}
	st := readAgentState(c.t, c.base, c.sess, c.win)
	c.note(step, st)
	return st
}

// leaves waits for the pane to leave needs_input.
func (c *claudeStage) leaves(step string) {
	c.t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		st := readAgentState(c.t, c.base, c.sess, c.win)
		if st.State != "needs_input" {
			c.note(step, st)
			return
		}
		if time.Now().After(deadline) {
			c.note(step, st)
			c.t.Fatalf("%s: the pane is still needs_input (%q) with the prompt gone from its screen", step, st.Message)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitSource waits for the pane's state to be read from source.
func (c *claudeStage) waitSource(source, step string) {
	c.t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		st := readAgentState(c.t, c.base, c.sess, c.win)
		if st.Source == source {
			c.note(step, st)
			return
		}
		if time.Now().After(deadline) {
			c.note(step, st)
			c.t.Fatalf("%s: the pane's state is from %q, want %q", step, st.Source, source)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitFrame waits for the pane to show the frame lines and nothing else: the
// command that printed it cleared the screen first, so the command itself is
// gone once the frame is up.
func (c *claudeStage) waitFrame(lines ...string) {
	c.t.Helper()
	deadline := time.Now().Add(uiTimeout)
	for {
		out, err := tuiosCLI(c.t, c.base, "capture-pane", "-s", c.sess, "-w", c.win)
		shown := err == nil && !strings.Contains(out, "printf")
		if len(lines) == 0 {
			shown = err == nil && strings.TrimSpace(out) == ""
		}
		for _, l := range lines {
			shown = shown && strings.Contains(out, strings.TrimSpace(l))
		}
		if shown {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("the pane never showed the frame %q:\n%s", lines, out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// blockItems lists the ids of the open Inbox items about the pane's prompt.
func (c *claudeStage) blockItems() []string {
	c.t.Helper()
	out, err := tuiosCLI(c.t, c.base, "list-attention", "--json")
	if err != nil {
		c.t.Fatalf("list-attention: %v\n%s", err, out)
	}
	var res struct {
		Items []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Window string `json:"window"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		c.t.Fatalf("list-attention: %v\n%s", err, out)
	}
	var ids []string
	for _, it := range res.Items {
		if it.Window == c.win && (it.Kind == "approval" || it.Kind == "question") {
			ids = append(ids, it.ID+":"+it.Kind)
		}
	}
	return ids
}

// TestClaudeNotificationPromptClearsWhenThePromptGoes runs Claude Code's real
// hook against a pane that draws Claude Code's screens, and checks that a
// prompt the hooks reported ends once the prompt is gone from the screen.
// Claude Code sends nothing when a prompt goes without a tool running: Esc at
// a permission menu, or a background agent's question answered in that
// agent's own view. The pane stayed on needs_input, with its alert and Inbox
// row, until the next prompt.
//
// The hooks come in the order Claude Code sends them: PermissionRequest, the
// menu, then the permission_prompt notification, which the daemon refuses
// since the pane is on needs_input already. So the claim the screen has to
// take back is PermissionRequest's. While the menu stays on the screen the
// pane stays on needs_input, through a redraw of the same menu. The menu is
// then replaced by the empty prompt box, as Esc leaves it, and the pane leaves
// needs_input. Then the same for a background agent's agent_needs_input.
//
// The artifact is claude-notification-stale.log in the test's artifact
// directory: each step and the pane's state after it.
//
// Negative controls: with the hook sending notification on neither event, or
// on the Notification alone, the pane stays on needs_input after the menu is
// gone.
func TestClaudeNotificationPromptClearsWhenThePromptGoes(t *testing.T) {
	c := newClaudeStage(t, "claude-notification-stale")

	c.hook("UserPromptSubmit", claudePromptSubmit, "working")
	c.hook("PermissionRequest", claudePermissionRequest, "needs_input")
	c.draw(claudeMenu...)
	c.hook("Notification permission_prompt (refused)", claudePermissionNotice, "needs_input")
	c.draw(claudeMenu...)
	c.holds("needs_input", "the menu drawn again", 3*time.Second)
	c.draw(claudePromptBox...)
	c.leaves("Esc: the menu gone, the prompt box empty")

	c.hook("UserPromptSubmit again", `{"hook_event_name":"UserPromptSubmit","session_id":"cl-e2e","prompt":"start the deploy agent"}`, "working")
	c.hook("Notification agent_needs_input", `{"hook_event_name":"Notification","session_id":"cl-e2e","message":"production-setup needs your input","notification_type":"agent_needs_input"}`, "needs_input")
	c.draw(claudePromptBox...)
	c.leaves("the background agent's question answered")
}

// TestClaudePromptKeepsItsWordsThroughARedraw checks a prompt the screen took
// over while its menu was drawn comes back whole when a frame no rule reads
// stands in the menu's place: still needs_input, still an approval, with the
// words the hook reported, and still able to end once the prompt box shows.
// A redraw draws such a frame, and one that stood half a second used to hand
// the pane back as a question with no words, which nothing could end.
//
// The artifact is claude-prompt-redraw.log in the test's artifact directory.
//
// Negative control: with the screen's loan keeping no message, kind or event
// mark of the claim it displaced, the pane comes back with neither words nor
// kind, and stays on needs_input under the empty prompt box.
func TestClaudePromptKeepsItsWordsThroughARedraw(t *testing.T) {
	c := newClaudeStage(t, "claude-prompt-redraw")

	c.hook("UserPromptSubmit", claudePromptSubmit, "working")
	c.hook("PermissionRequest", claudePermissionRequest, "needs_input")
	reported := readAgentState(t, c.base, c.sess, c.win)
	if reported.BlockedBy != "approval" || reported.Message == "" {
		t.Fatalf("PermissionRequest left %+v, want an approval with its words", reported)
	}
	c.draw(claudeMenu...)
	c.waitSource("screen", "the menu drawn: the screen reads it")
	// A frame of the redraw that no rule reads, standing long enough for the
	// screen to give the prompt back.
	partial := []string{"Bash command", "  go test ./..."}
	c.draw(partial...)
	c.waitFrame(partial...)
	c.waitSource("report", "a frame no rule reads: the screen gives it back")
	st := c.holds("needs_input", "the frame standing 1s", time.Second)
	if st.Source != "report" || st.BlockedBy != "approval" || st.Message != reported.Message {
		t.Fatalf("after the frame the pane says %+v, want the hook's approval back: blocked_by approval, message %q", st, reported.Message)
	}
	c.draw(claudeMenu...)
	c.holds("needs_input", "the menu back", 2*time.Second)
	c.draw(claudePromptBox...)
	c.leaves("Esc: the menu gone, the prompt box empty")
}

// TestClaudePromptHoldsThroughABlankFrame checks a prompt that is still
// waiting is not ended by Claude Code's rest glyph in the title. Claude Code
// shows ✳ in its title at a menu as well as at its prompt box, so the title
// says nothing about the menu. A blank frame of a redraw, standing 1.5s, used
// to take the pane to idle and close its Inbox item, and the menu coming back
// opened a second one. The pane must hold through it with one Inbox item, and
// still leave needs_input once the prompt box shows.
//
// The blank frame is the first thing the pane writes after the notification,
// so it meets the hook's own claim and not a screen rule's loan of it. Then
// the same with the loan: the menu drawn again, a blank frame, the menu back.
//
// The artifact is claude-prompt-blank.log in the test's artifact directory.
//
// Negative control: with an idle look able to replace the prompt on the
// title's word, at once, the pane goes idle during the blank frame.
func TestClaudePromptHoldsThroughABlankFrame(t *testing.T) {
	c := newClaudeStage(t, "claude-prompt-blank")
	c.title = "✳ Claude Code"

	c.hook("UserPromptSubmit", claudePromptSubmit, "working")
	flag := filepath.Join(t.TempDir(), "blank")
	c.drawUntil(claudeMenu, flag)
	c.waitFrame(claudeMenu...)
	c.hook("Notification permission_prompt", claudePermissionNotice, "needs_input")
	first := c.blockItems()
	for deadline := time.Now().Add(uiTimeout); len(first) == 0; first = c.blockItems() {
		if time.Now().After(deadline) {
			t.Fatal("the prompt opened no Inbox item")
		}
		time.Sleep(100 * time.Millisecond)
	}
	c.log.add("Inbox items: %v", first)
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c.waitFrame()
	// Longer than agentStartupGrace, which holds every idle look for a
	// harness the daemon has only just seen in the pane.
	c.holds("needs_input", "the menu, then a blank frame for 4s", 4*time.Second)
	c.draw(claudeMenu...)
	c.holds("needs_input", "the menu back", 2*time.Second)
	c.draw()
	c.waitFrame()
	c.holds("needs_input", "a blank frame again, for 1.5s", 1500*time.Millisecond)
	c.draw(claudeMenu...)
	c.holds("needs_input", "the menu back again", 2*time.Second)
	if got := c.blockItems(); len(got) != 1 || got[0] != first[0] {
		c.log.add("Inbox items: %v", got)
		t.Fatalf("the Inbox holds %v after the blank frames, want the one item %v", got, first)
	}
	c.draw(claudePromptBox...)
	c.leaves("Esc: the menu gone, the prompt box empty")
}

// TestClaudeURLFormHoldsWithoutARule checks a question no screen rule reads,
// Claude Code's elicitation_url_dialog, stays on needs_input after the pane
// writes, under Claude Code's ✳ title. Nothing on the screen could tell the
// form gone from the form still up, so only a hook may end it.
//
// The artifact is claude-url-form.log in the test's artifact directory.
//
// Negative control: with an idle look able to replace the prompt on the
// title's word, the pane goes idle after the form is drawn again.
func TestClaudeURLFormHoldsWithoutARule(t *testing.T) {
	c := newClaudeStage(t, "claude-url-form")
	c.title = "✳ Claude Code"
	form := []string{"The db server asks you to sign in", "  https://example.com/login", "Press Enter to open the link"}

	c.hook("UserPromptSubmit", claudePromptSubmit, "working")
	c.draw(form...)
	c.hook("Notification elicitation_url_dialog", `{"hook_event_name":"Notification","session_id":"cl-e2e","message":"The db server asks you to sign in","notification_type":"elicitation_url_dialog"}`, "needs_input")
	c.draw(form...)
	c.holds("needs_input", "the form drawn again, for 6s", 6*time.Second)
	c.hook("ElicitationResult", `{"hook_event_name":"ElicitationResult","session_id":"cl-e2e"}`, "working")
}
