package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuitest"
)

// peekAgentScript paints Claude Code's permission menu, reads one key, writes
// it to the file named by its argument, and prints that it was answered.
const peekAgentScript = `#!/bin/sh
stty raw -echo
printf '\033[2J\033[H Bash command\r\n   rm -rf build\r\n Do you want to proceed?\r\n \342\235\257 1. Yes\r\n   2. Yes, and don'\''t ask again for rm commands\r\n   3. No, and tell Claude what to do differently (esc)\r\n'
k=$(dd bs=1 count=1 2>/dev/null)
printf '%s' "$k" > "$1"
stty sane
printf '\033[2J\033[Hanswered %s\r\n' "$k"
sleep 600
`

// TestInboxPeekAnswersAnApproval is the peek end to end against a real daemon
// and a real client: an agent in a session nobody is attached to shows Claude
// Code's permission menu. The Inbox lists it; space reads the menu into a
// panel with its options and the keys that answer it; a presses the key the
// manifest declares for approve, 1, into that pane, and the client says it was
// sent. Nobody attached to the agent's pane.
//
// Negative control: with the space binding removed from handleInboxInput, the
// panel never shows the menu and the second wait fails.
func TestInboxPeekAnswersAnApproval(t *testing.T) {
	term, base := attachClientBase(t)

	if out, err := tuiosCLI(t, base, "new", "e2e-ask", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	script := filepath.Join(base, "agent.sh")
	if err := os.WriteFile(script, []byte(peekAgentScript), 0o700); err != nil {
		t.Fatal(err)
	}
	got := filepath.Join(base, "got")
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ask", "sh "+script+" "+got+"\n"); err != nil {
		t.Fatalf("start the agent: %v\n%s", err, out)
	}
	waitForCapture(t, base, nil, []string{"-s", "e2e-ask"}, "Do you want to proceed?")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-ask", "needs_input",
		"--kind", "approval", "--harness", "claude-code", "-m", "Bash: rm -rf build"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "1 needs you") && strings.Contains(s.Text(), "Allow")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the approval: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		// A numbered menu is answered by its digits, which is the one way the
		// footer offers; a still approves, as pressed below.
		return strings.Contains(text, "Do you want to proceed?") && strings.Contains(text, "rm -rf build") &&
			strings.Contains(text, "choose") && strings.Contains(text, "Yes, and don")
	}, uiTimeout); err != nil {
		t.Fatalf("the peek never showed the menu and its answers: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-peek")
	// Issue #2065: the active row keeps its cursor glyph, and its number sits
	// in the same column as the other rows'. The title filter dropped the
	// glyph and left the space after it, a column to the left.
	assertPeekRowsAligned(t, term.Snapshot(), "1. Yes", "2. Yes, and")

	// esc closes the peek. Nothing in the frame cache's key moves with the
	// peek, so the next frame once matched the peek's and drew it again.
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatalf("close the peek: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "1 needs you") && !strings.Contains(s.Text(), "Yes, and don")
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the peek stayed on the screen after esc: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("peek again: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "Yes, and don") }, uiTimeout); err != nil {
		t.Fatalf("the peek did not open again: %v\n%s", err, term.Snapshot())
	}

	// a asks, and enter answers.
	if err := term.SendKeys("a", tuitest.Enter); err != nil {
		t.Fatalf("approve: %v", err)
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		data, _ := os.ReadFile(got)
		if string(data) == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the agent read %q, want 1\n%s", data, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), `Sent "1"`)
	}, uiTimeout); err != nil {
		t.Fatalf("the client never said the answer was sent: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-peek-answered")
	alive(t, term, "after answering from the peek")
}

// assertPeekRowsAligned fails unless the screen has a row with the cursor glyph
// before active, and active starts in the same column as other.
func assertPeekRowsAligned(t *testing.T, screen, active, other string) {
	t.Helper()
	col := func(want string) (int, string) {
		for line := range strings.SplitSeq(screen, "\n") {
			if i := strings.Index(line, want); i >= 0 {
				return utf8.RuneCountInString(line[:i]), line
			}
		}
		return -1, ""
	}
	a, aLine := col(active)
	o, oLine := col(other)
	if a < 0 || o < 0 {
		t.Fatalf("ASSERTION: the peek lacks %q or %q\n%s", active, other, screen)
	}
	if !strings.Contains(aLine, "\u276f "+active) {
		t.Errorf("ASSERTION: the active row lost its cursor glyph: %q", aLine)
	}
	if a != o {
		t.Errorf("ASSERTION: the active row starts at column %d, the next at %d:\n%q\n%q", a, o, aLine, oLine)
	}
}

// gateAgentManifest is a harness whose one needs_input rule takes typed text
// and esc, and no number, on a screen that still shows a numbered list.
const gateAgentManifest = `schema_version = 1
id = "tuiosgateagent"
[detect]
comm  = ["tuiosgateagent"]
argv0 = ["tuiosgateagent"]
[screen]
enabled = true
[[screen.rule]]
state    = "needs_input"
priority = 30
message  = "Waits for typed text."
all      = ["Type your answer"]
[screen.rule.answers]
text = true
deny = { keys = ["esc"] }
`

// gateAgentScript paints a numbered list above a text field and waits.
const gateAgentScript = `#!/bin/sh
stty raw -echo
printf '\033[2J\033[H Which change?\r\n \342\235\257 1. Bug fix\r\n   2. New feature\r\n   3. Refactor\r\n Type your answer:\r\n'
sleep 600
`

// TestInboxPeekListsOptionsOnlyWhenTheRuleChooses is issue #2065's second
// half: the matched rule takes text and esc but no number, so the peek shows
// the dialog lines and no numbered option list, the same as its hint row.
//
// Negative control: with the Offers(harness.ActionChoose) test removed from
// the option list in render_inbox.go, the panel lists "1  Bug fix" and the
// second wait fails.
func TestInboxPeekListsOptionsOnlyWhenTheRuleChooses(t *testing.T) {
	base := t.TempDir()
	manifests := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "harnesses")
	if err := os.MkdirAll(manifests, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifests, "tuiosgateagent.toml"), []byte(gateAgentManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	term := attachClientIn(t, base, nil)
	if out, err := tuiosCLI(t, base, "new", "e2e-gate", "--detach"); err != nil {
		t.Fatalf("create the agent's session: %v\n%s", err, out)
	}
	script := filepath.Join(base, "gate.sh")
	if err := os.WriteFile(script, []byte(gateAgentScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-gate", "sh "+script+"\n"); err != nil {
		t.Fatalf("start the agent: %v\n%s", err, out)
	}
	waitForCapture(t, base, nil, []string{"-s", "e2e-gate"}, "Type your answer")
	if out, err := tuiosCLI(t, base, "set-agent-state", "-s", "e2e-gate", "needs_input",
		"--kind", "question", "--harness", "tuiosgateagent", "-m", "Which change?"); err != nil {
		t.Fatalf("set-agent-state: %v\n%s", err, out)
	}
	out, err := tuiosCLI(t, base, "peek-prompt", "--json", "-s", "e2e-gate", "-w", "0")
	if err != nil {
		t.Fatalf("peek-prompt: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"text"`) || strings.Contains(out, `"choose"`) || !strings.Contains(out, `"Bug fix"`) {
		t.Fatalf("the fixture is not the case: want the text action, no choose, and parsed options\n%s", out)
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "1 needs you")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the question: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "3. Refactor") && strings.Contains(s.Text(), "type")
	}, uiTimeout); err != nil {
		t.Fatalf("the peek never showed the dialog: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-peek-no-choose")
	snap := term.Snapshot()
	for _, n := range []int{1, 2, 3} {
		for _, label := range []string{"Bug fix", "New feature", "Refactor"} {
			if strings.Contains(snap, fmt.Sprintf("%d  %s", n, label)) {
				t.Errorf("ASSERTION: the peek lists option %d %q, which the rule cannot choose\n%s", n, label, snap)
			}
		}
	}
	assertPeekRowsAligned(t, snap, "1. Bug fix", "2. New feature")
	alive(t, term, "after the peek")
}
