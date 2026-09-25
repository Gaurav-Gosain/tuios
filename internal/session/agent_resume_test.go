package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// echoHarness installs a user manifest whose resume command is an echo, so a
// test can see the command land in a real shell without a real agent. It has
// to be in place before the daemon loads its registry.
func echoHarness(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
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
	t.Setenv("TUIOS_HARNESS_DIR", dir)
}

// savedAgentSession is the state a previous daemon wrote for a session whose
// panes ran agents: one resumable pane with its agent running, one whose
// harness has no resume command, one that ran on another machine, one with no
// conversation, and one whose agent had already exited, which keeps its id and
// has nothing live to resume.
func savedAgentSession(name string) *SessionState {
	return &SessionState{
		Name:             name,
		CurrentWorkspace: 1,
		Width:            120,
		Height:           40,
		Windows: []WindowState{
			{ID: "win-agent", Title: "agent", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-1",
				AgentState: AgentStateWorking, AgentHarness: "echoer",
				AgentSessionID: "5f1c-9a3d", AgentSessionHarness: "echoer"},
			{ID: "win-aider", Title: "aider", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-2",
				AgentState: AgentStateIdle, AgentHarness: "aider",
				AgentSessionID: "a1", AgentSessionHarness: "aider"},
			{ID: "win-remote", Title: "remote", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-3",
				Host: "build", AgentState: AgentStateWorking, AgentHarness: "echoer",
				AgentSessionID: "r1", AgentSessionHarness: "echoer"},
			{ID: "win-plain", Title: "plain", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-4"},
			{ID: "win-ended", Title: "ended", Width: 60, Height: 40, Workspace: 1, PTYID: "dead-5",
				AgentSessionID: "e1-ended", AgentSessionHarness: "echoer"},
		},
	}
}

// capturePane reads what a pane shows.
func capturePane(t *testing.T, c *verbConn, session, window string) string {
	t.Helper()
	res := result(t, c.call(t, `{"id":1,"verb":"capture-pane","params":{"session":"`+session+`","window":"`+window+`"}}`))
	content, _ := res["content"].(string)
	return content
}

// TestResumeAgentRefusesWhatItCannotDo covers each refusal: nothing
// recorded, a harness with no resume command, an id a shell would read as
// more than one argument, and a pane running a program.
func TestResumeAgentRefusesWhatItCannotDo(t *testing.T) {
	echoHarness(t)
	d, sp := startTestDaemon(t)
	c := dialVerb(t, sp)
	d.resumeAgents = resumeModeOff
	if _, err := d.restoreSession(savedAgentSession("refuse")); err != nil {
		t.Fatalf("restore: %v", err)
	}

	for _, tc := range []struct{ window, code string }{
		{"win-plain", ErrVerbNotResumable},
		{"win-aider", ErrVerbNotResumable},
		{"win-remote", ErrVerbNotResumable},
	} {
		resp := c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"refuse","window":"`+tc.window+`"}}`)
		if code := errCode(t, resp); code != tc.code {
			t.Errorf("%s: code %q, want %q", tc.window, code, tc.code)
		}
	}

	// An id reported by a pane that a shell would split is never typed.
	sess := d.manager.GetSession("refuse")
	result(t, c.call(t, `{"id":1,"verb":"set-agent-session","params":{"session":"refuse","window":"win-plain","harness":"echoer","agent_session_id":"x; touch /tmp/pwned"}}`))
	resp := c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"refuse","window":"win-plain"}}`)
	if code := errCode(t, resp); code != ErrVerbNotResumable {
		t.Errorf("an unsafe id: code %q, want %q", code, ErrVerbNotResumable)
	}

	// A pane running a program does not get the command typed into it.
	pty, err := d.resolvePTYForTarget(sess, "win-agent")
	if err != nil {
		t.Fatal(err)
	}
	if !waitShellPrompt(t.Context(), pty, 5*time.Second, 200*time.Millisecond) {
		t.Fatal("the restored shell never drew its prompt")
	}
	if _, err := pty.Write([]byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if pgid, ok := readForegroundPGID(pty.ShellPID()); !ok || pgid != pty.ShellPID() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sleep never took the foreground")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, ok := readForegroundPGID(pty.ShellPID()); !ok {
		t.Skip("this platform does not say which process group holds a terminal")
	}
	resp = c.call(t, `{"id":1,"verb":"resume-agent","params":{"session":"refuse","window":"win-agent"}}`)
	if code := errCode(t, resp); code != ErrVerbNotReady {
		t.Errorf("a pane running sleep: code %q, want %q", code, ErrVerbNotReady)
	}
	if text := capturePane(t, c, "refuse", "win-agent"); strings.Contains(text, "resumed-") {
		t.Errorf("the command was typed into a running program:\n%s", text)
	}
}
