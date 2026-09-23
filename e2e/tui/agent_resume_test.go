package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestRestartOffersToResumeTheConversation is the resume offer end to end,
// against the real binary: a pane records a conversation, the daemon is
// killed and started again, and the restored pane is offered back. The dock
// says so, the Inbox lists it under Resume with the exact command, y types
// that command into the restored shell, and the item goes.
//
// The harness is a user manifest whose resume command is an echo, so the test
// sees the command run in a real shell without a real agent.
//
// Negative control: with the restore's conversation ids not put back after
// its state push (daemon_resurrect.go), the Inbox still lists the offer but
// resume-agent finds no conversation on the pane, and the dry run fails.
func TestRestartOffersToResumeTheConversation(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)

	dir := filepath.Join(xdgDir(base, "XDG_CONFIG_HOME"), "tuios", "harnesses")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `schema_version = 1
id             = "echoer"
display_name   = "Echoer"

[detect]
comm = ["echoer-agent"]

[resume]
argv = ["echo", "resumed-{session_id}"]
`
	if err := os.WriteFile(filepath.Join(dir, "echoer.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := tuiosCLI(t, base, "new", "e2e-resume", "--detach"); err != nil {
		t.Fatalf("create the session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "set-agent-session", "-s", "e2e-resume", "--harness", "echoer", "5f1c-9a3d"); err != nil {
		t.Fatalf("record the conversation: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "kill-server"); err != nil {
		t.Fatalf("kill-server: %v\n%s", err, out)
	}

	// Any command that starts a daemon restores on start.
	if out, err := tuiosCLI(t, base, "new", "e2e-other", "--detach"); err != nil {
		t.Fatalf("start a fresh daemon: %v\n%s", err, out)
	}
	waitForSessionInfo(t, base, "e2e-resume")

	deadline := time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Resume") && strings.Contains(out, "echo resumed-5f1c-9a3d") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the restore made no resume offer:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if out, err := tuiosCLI(t, base, "resume-agent", "-s", "e2e-resume", "--dry-run"); err != nil || strings.TrimSpace(out) != "echo resumed-5f1c-9a3d" {
		t.Fatalf("resume-agent --dry-run: %v\n%s", err, out)
	}

	// A client attached elsewhere hears about it once, with the key.
	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-other"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return strings.Contains(s.Text(), "can resume its conversation")
	}, bootTimeout); err != nil {
		t.Fatalf("the dock never announced the offer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-dock")

	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "Resume 1") && strings.Contains(text, "echo resumed-5f1c-9a3d") &&
			strings.Contains(text, "resume")
	}, uiTimeout); err != nil {
		t.Fatalf("the Inbox never listed the offer: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-inbox")

	// y goes to the pane and types the command there.
	if err := term.SendKeys("y"); err != nil {
		t.Fatalf("answer the offer: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "Resume 1")
	}, uiTimeout); err != nil {
		t.Fatalf("y did not close the Inbox: %v\n%s", err, term.Snapshot())
	}
	// The echo ran: its output is a line of its own, apart from the command.
	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "capture-pane", "-s", "e2e-resume")
		ran := false
		for _, line := range strings.Split(out, "\n") {
			ran = ran || strings.TrimSpace(line) == "resumed-5f1c-9a3d"
		}
		if ran {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("y did not type the resume command into the restored shell:\n%s\n%s", out, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveFrame(t, term, "resume-typed")

	deadline = time.Now().Add(uiTimeout)
	for {
		out, _ := tuiosCLI(t, base, "list-attention")
		if strings.Contains(out, "Nothing is waiting for you.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the offer stayed open after the resume:\n%s", out)
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive(t, term, "after resuming from the Inbox")
}
