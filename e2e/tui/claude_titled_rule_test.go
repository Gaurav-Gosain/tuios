package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// claudeFormScript paints the screen it is given, the way Claude Code draws a
// form, and waits. The screen is a file so that no shell quoting gets between
// the test and what the pane shows.
const claudeFormScript = `#!/bin/sh
stty raw -echo
printf '\033[2J\033[H'
sed 's/$/\r/' "$1"
sleep 600
`

// titledRule is the rule Claude Code 2.1.311 draws under a form, with the
// session's name in it.
const titledRule = "───────────────────────────────── Color test ─"

// formScreen is an AskUserQuestion form as Claude Code 2.1.311 draws it,
// with footer as its footer line or lines.
func formScreen(footer ...string) []string {
	screen := []string{
		"❯ Ask me which color to use.",
		"──────────────────────────────────────────────",
		" ☐ Color",
		"",
		"Which color for the test?",
		"",
		"❯ 1. Red",
		"  2. Green",
		"  3. Blue",
		"  4. Type something.",
		"──────────────────────────────────────────────",
		"  5. Chat about this",
		"",
	}
	return append(append(screen, footer...), titledRule)
}

type peekReply struct {
	Answerable bool     `json:"answerable"`
	Actions    []string `json:"actions"`
	Options    []struct {
		Label string `json:"label"`
	} `json:"options"`
}

// peekClaude starts an agent in a session of its own that shows screen,
// reports it as Claude Code on needs_input, and returns what peek-prompt
// reads off the pane once it has had time to look.
func peekClaude(t *testing.T, base, session string, screen []string) (peekReply, string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("create %s: %v\n%s", session, err, out)
	}
	script := filepath.Join(base, session+".sh")
	if err := os.WriteFile(script, []byte(claudeFormScript), 0o700); err != nil {
		t.Fatal(err)
	}
	text := filepath.Join(base, session+".txt")
	data := ""
	for _, line := range screen {
		data += line + "\n"
	}
	if err := os.WriteFile(text, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "sh "+script+" "+text+"\n"); err != nil {
		t.Fatalf("start the agent: %v\n%s", err, out)
	}
	waitForCapture(t, base, nil, []string{"-s", session}, screen[len(screen)-2])
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", session, "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "approve AskUserQuestion"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	var reply peekReply
	var out string
	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ = tuiosCLI(t, base, "peek-prompt", "--json", "-s", session, "-w", "0")
		reply = peekReply{}
		_ = json.Unmarshal([]byte(out), &reply)
		if reply.Answerable || time.Now().After(deadline) {
			return reply, out
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func optionLabels(r peekReply) []string {
	var out []string
	for _, o := range r.Options {
		out = append(out, o.Label)
	}
	return out
}

// TestClaudeFormUnderATitledRuleIsAPrompt: Claude Code 2.1.311 draws a rule
// with the session's name under an AskUserQuestion form. The form is still a
// prompt peek-prompt reads, with its options and choose. The same form in a
// narrow pane, where Claude Code wraps the footer at a space, is read too.
//
// Negative control: on main before the fix (after_last_horizontal_rule took
// the titled rule as the last rule, and substrings read one line at a time),
// both forms are not answerable. See NEGATIVE_CONTROLS.md.
func TestClaudeFormUnderATitledRuleIsAPrompt(t *testing.T) {
	_, base := attachClientBase(t)
	for _, tc := range []struct {
		session string
		footer  []string
	}{
		{"e2e-form-wide", []string{"Enter to select · ↑/↓ to navigate · Esc to cancel"}},
		// What a pane 39 columns wide shows: Claude Code wraps the footer
		// itself, at a space.
		{"e2e-form-narrow", []string{"Enter to select · ↑/↓ to navigate · Esc", "to cancel"}},
	} {
		t.Run(tc.session, func(t *testing.T) {
			reply, out := peekClaude(t, base, tc.session, formScreen(tc.footer...))
			if !reply.Answerable || !slices.Contains(reply.Actions, "choose") {
				t.Fatalf("ASSERTION: the form under the titled rule is not answerable by choose:\n%s", out)
			}
			if got := optionLabels(reply); !slices.Contains(got, "Green") || !slices.Contains(got, "Chat about this") {
				t.Fatalf("ASSERTION: options %q, want Red, Green, Blue and the rest:\n%s", got, out)
			}
		})
	}
}

// TestClaudePermissionUnderATitledRuleStillApproves guards the permission
// menu, which the claude-code rules also read by its "1. Yes" line: with the
// titled rule under it, approve is still offered.
//
// It passes before and after the fix, since rule 0 reads the menu from the
// whole tail. It is here so that a change to the region cannot take the
// permission menu with it unnoticed.
func TestClaudePermissionUnderATitledRuleStillApproves(t *testing.T) {
	_, base := attachClientBase(t)
	reply, out := peekClaude(t, base, "e2e-proceed", []string{
		" Bash command",
		"",
		"   rm -rf build",
		"",
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. Yes, and don't ask again for rm commands",
		"   3. No, and tell Claude what to do differently (esc)",
		"",
		" Esc to cancel · Tab to amend · ctrl+e to explain",
		titledRule,
	})
	if !reply.Answerable || !slices.Contains(reply.Actions, "approve") {
		t.Fatalf("ASSERTION: the permission menu under the titled rule is not approvable:\n%s", out)
	}
}

// TestClaudeTitledRuleWithALineUnderItIsNotAForm: the titled rule is skipped
// only at the very bottom. A form's footer above a titled rule with a line
// under it is settled output, and peek-prompt does not offer it.
//
// Negative control: with blankFrom dropped from the skip in regionBounds,
// the form is answerable by choose.
func TestClaudeTitledRuleWithALineUnderItIsNotAForm(t *testing.T) {
	_, base := attachClientBase(t)
	screen := formScreen("Enter to select · ↑/↓ to navigate · Esc to cancel")
	screen = append(screen, "  ⏵⏵ auto mode on (shift+tab to cycle)")
	reply, out := peekClaude(t, base, "e2e-form-settled", screen)
	if reply.Answerable {
		t.Fatalf("ASSERTION: a form above a titled rule with a line under it is answerable:\n%s", out)
	}
}
