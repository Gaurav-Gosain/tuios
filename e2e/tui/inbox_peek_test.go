package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		return strings.Contains(s.Text(), "Approvals 1")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the approval: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(" "); err != nil {
		t.Fatalf("peek: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Do you want to proceed?") && strings.Contains(text, "rm -rf build") &&
			strings.Contains(text, "approve") && strings.Contains(text, "deny") && strings.Contains(text, "Yes, and don")
	}, uiTimeout); err != nil {
		t.Fatalf("the peek never showed the menu and its answers: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "inbox-peek")

	if err := term.SendKeys("a"); err != nil {
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
