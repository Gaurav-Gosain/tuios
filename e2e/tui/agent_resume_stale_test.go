package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestResumeOfferClosesWhenAPaneRunsTheConversation: a restart offers three
// conversations back. The person resumes one of them in a new pane by hand,
// as claude --resume would, and another one in a pane of another session, by
// a hook that names the conversation alone (set-agent-session). Both offers
// are stale then: y on them would start the conversation a second time. They
// leave the Inbox. The third offer, whose conversation runs nowhere, stays.
//
// Negative control: see NEGATIVE_CONTROLS.md, "A stale resume offer".
func TestResumeOfferClosesWhenAPaneRunsTheConversation(t *testing.T) {
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
	convs := map[string]string{"e2e-stale-a": "a1a1-0001", "e2e-stale-b": "b2b2-0002", "e2e-stale-c": "c3c3-0003"}
	for name, conv := range convs {
		mustCLI(t, base, "new", name, "--detach")
		mustCLI(t, base, "set-agent-state", "-s", name, "working", "--harness", "echoer", "--agent-session-id", conv)
	}
	mustCLI(t, base, "restart-server", "--yes")
	mustCLI(t, base, "new", "e2e-stale-other", "--detach")
	for name := range convs {
		waitForSessionInfo(t, base, name)
	}
	waitAttention := func(want func(string) bool, what string) string {
		t.Helper()
		deadline := time.Now().Add(uiTimeout)
		for {
			out, _ := tuiosCLI(t, base, "list-attention")
			if want(out) {
				return out
			}
			if time.Now().After(deadline) {
				t.Fatalf("ASSERTION: %s:\n%s", what, out)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	offered := func(conv string) func(string) bool {
		return func(out string) bool { return strings.Contains(out, "echo resumed-"+conv) }
	}
	for _, conv := range convs {
		waitAttention(offered(conv), "the restore did not offer "+conv)
	}

	// a: resumed by hand in a new pane of its own session.
	mustCLI(t, base, "new-window", "-s", "e2e-stale-a", "again")
	mustCLI(t, base, "set-agent-state", "-s", "e2e-stale-a", "-w", "again", "working", "--harness", "echoer", "--agent-session-id", convs["e2e-stale-a"])
	waitAttention(func(out string) bool { return !offered(convs["e2e-stale-a"])(out) }, "the offer for a conversation a pane runs stayed open")

	// b: resumed in a pane of another session, named by set-agent-session.
	mustCLI(t, base, "new-window", "-s", "e2e-stale-other", "elsewhere")
	mustCLI(t, base, "set-agent-session", "-s", "e2e-stale-other", "-w", "elsewhere", "--harness", "echoer", convs["e2e-stale-b"])
	out := waitAttention(func(out string) bool { return !offered(convs["e2e-stale-b"])(out) }, "the offer for a conversation another session runs stayed open")
	if !offered(convs["e2e-stale-c"])(out) {
		t.Fatalf("ASSERTION: the offer for a conversation that runs nowhere closed too:\n%s", out)
	}

	term := startIn(t, base, startOpts{args: []string{"attach", "e2e-stale-other"}})
	if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "╰──") }, bootTimeout); err != nil {
		t.Fatalf("the client never drew the session: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "i"); err != nil {
		t.Fatalf("open the Inbox: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "echo resumed-"+convs["e2e-stale-c"]) &&
			!strings.Contains(text, "echo resumed-"+convs["e2e-stale-a"]) && !strings.Contains(text, "echo resumed-"+convs["e2e-stale-b"])
	}, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the Inbox does not show the one offer left: %v\n%s", err, term.Snapshot())
	}
	saveFrame(t, term, "resume-stale-inbox")
}
