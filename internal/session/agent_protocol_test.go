package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeProtoExe points the daemon's protocol pane program at a script that
// writes its argv, one word a line, to a file and then waits on stdin, the way
// the real one waits for prompts. It returns the file.
func fakeProtoExe(t *testing.T, d *Daemon) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "argv")
	script := filepath.Join(dir, "tuios")
	body := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + out + "\nwhile IFS= read -r line; do :; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	d.agentProtoExe = func() (string, error) { return script, nil }
	return out
}

// fakeProgramOnPath puts an executable named name on PATH that waits on stdin.
func fakeProgramOnPath(t *testing.T, name string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nwhile IFS= read -r line; do :; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// waitOnlyPane waits for the session name to exist with n windows, and
// returns the session and its newest window. start-agent holds its reply
// until the agent is ready, so a test finds the pane from the daemon's side
// and makes it ready.
func waitOnlyPane(t *testing.T, d *Daemon, name string, n int) (*Session, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sess := d.manager.GetSession(name); sess != nil {
			if ws := sess.GetState().Windows; len(ws) == n {
				return sess, ws[n-1].ID
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never had %d windows", name, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStartAgentProtocolWaitsForTheReport is the readiness rule with its
// negative control: a plain pane of a harness that cannot show idle is ready
// on unknown, which is all its screen can show, and a protocol pane of the
// same harness is not, because its own report is the only evidence it has.
func TestStartAgentProtocolWaitsForTheReport(t *testing.T) {
	d, sp := startTestDaemon(t)
	if d.agentMatcher.registry == nil || d.agentMatcher.registry.CanProveIdle("aider") {
		t.Skip("aider now has an idle rule; pick a harness without one")
	}
	fakeProtoExe(t, d)
	fakeProgramOnPath(t, "aider")
	c := dialVerb(t, sp)

	start := func(id int, extra string, windows int) map[string]any {
		c.send(t, `{"id":`+string(rune('0'+id))+`,"verb":"start-agent","params":{"session":"s","agent":"aider","ready_timeout":600`+extra+`}}`)
		sess, windowID := waitOnlyPane(t, d, "s", windows)
		reportAs(t, sess, windowID, AgentStateUnknown, "aider")
		return result(t, c.readResp(t))
	}
	plain := start(1, "", 1)
	if plain["ready"] != true || plain["ready_by"] != "quiet" || plain["protocol"] != nil {
		t.Fatalf("the plain pane = %v, want ready by quiet with no protocol", plain)
	}
	proto := start(2, `,"protocol":"acp"`, 2)
	if proto["ready"] != false || proto["outcome"] != string(agentStartTimeout) {
		t.Fatalf("the protocol pane = %v, want not ready until it reports", proto)
	}
	id, _ := proto["window_id"].(string)
	if d.paneProtocol(id) != "acp" {
		t.Errorf("the pane is not marked as a protocol pane")
	}
	if d.paneProtocol(plain["window_id"].(string)) != "" {
		t.Errorf("the plain pane is marked as a protocol pane")
	}
}
