package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestCockpitPeekClosesOnAClick: the peek reads one agent's prompt and takes
// the keys of its answers. A click while it is open closes it and does
// nothing else. Two agents ask: e2e-agent has a held risky call, e2e-ask a
// prompt in its pane. With the peek open on e2e-ask, a click on e2e-agent's
// row holds:
//
//   - the peek is gone;
//   - the cursor stays on e2e-ask (the click did nothing else);
//   - 1 then answers nothing: the prompt in e2e-ask's pane gets no key.
//
// How this could pass wrongly, written down first:
//   - The peek could close and the row click still move the cursor. The test
//     reads the cursor row.
//   - 1 could reach the pane late. The test waits two seconds and reads the
//     file the pane's program writes the key it gets into.
//
// Negative control, recorded in NEGATIVE_CONTROLS.md: on fix/sm-n1 without
// the peek case the click moves the cursor to e2e-agent and the peek of
// e2e-ask stays, so its digits still answer e2e-ask. (There 1 only asks
// first, as the prompt reads as risky, so the last check is a guard.)
func TestCockpitPeekClosesOnAClick(t *testing.T) {
	base, term := approvalsClient(t)
	startHeldHook(t, base, `{"hook_event_name":"PermissionRequest","session_id":"e2e-risk","tool_name":"Bash","tool_input":{"command":"rm -rf build/"}}`)
	if out, err := tuiosCLI(t, base, "new", "e2e-ask", "--detach"); err != nil {
		t.Fatalf("create e2e-ask: %v\n%s", err, out)
	}
	script, got := filepath.Join(base, "agent.sh"), filepath.Join(base, "got")
	if err := os.WriteFile(script, []byte(peekAgentScript), 0o700); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, base, "send-text", "-s", "e2e-ask", "sh "+script+" "+got+"\n")
	waitForCapture(t, base, nil, []string{"-s", "e2e-ask"}, "Do you want to proceed?")
	mustCLI(t, base, "set-agent-state", "-s", "e2e-ask", "needs_input", "--kind", "approval", "--harness", "claude-code", "-m", "Bash: ls")

	sendKeys(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "two agents asking", "2 needs you")
	time.Sleep(answerSettle)
	const peek = "1-3 choose" // the peek's key line
	sendKeys(t, term, "j")
	_ = term.WaitStable(2 * time.Second)
	sendKeys(t, term, " ")
	waitText(t, term, "the peek on e2e-ask", peek, "Do you want to proceed?")
	if !strings.Contains(ckCursorRow(term.Screen()), "e2e-ask") {
		t.Fatalf("the peek is not on e2e-ask's row: cursor %q\n%s", ckCursorRow(term.Screen()), term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "peek")

	clickAgentsRow(t, term, "Allow Bash")
	_ = term.WaitStable(2 * time.Second)
	saveArtifact(t, term, dir, "after-click")
	if strings.Contains(term.Screen().Text(), peek) {
		t.Errorf("ASSERTION: the peek is still open after a click\n%s", term.Snapshot())
	}
	if cur := ckCursorRow(term.Screen()); !strings.Contains(cur, "e2e-ask") {
		t.Errorf("ASSERTION: the click that closed the peek also moved the cursor (now %q)\n%s", cur, term.Snapshot())
	}
	sendKeys(t, term, "1")
	time.Sleep(2 * time.Second)
	if b, _ := os.ReadFile(got); len(b) > 0 {
		t.Errorf("ASSERTION: 1 after the click answered e2e-ask's prompt with %q", b)
	}
}
