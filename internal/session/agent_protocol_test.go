package session

import (
	"os"
	"path/filepath"
	"slices"
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

func TestStartAgentRefusesAnUnknownProtocol(t *testing.T) {
	d, sp := startTestDaemon(t)
	fakeProtoExe(t, d)
	c := dialVerb(t, sp)
	resp := c.call(t, `{"id":1,"verb":"start-agent","params":{"session":"s","agent":"sh","protocol":"mcp"}}`)
	if code := errCode(t, resp); code != ErrVerbInvalidParams {
		t.Fatalf("code = %q, want %q", code, ErrVerbInvalidParams)
	}
	if d.manager.GetSession("s") != nil {
		t.Error("a refused call made the session")
	}
}

// TestProtocolPaneMarkGoesWithTheWindow: a pane whose program exits is no
// longer a protocol pane.
func TestProtocolPaneMarkGoesWithTheWindow(t *testing.T) {
	d, sp := startTestDaemon(t)
	fakeProtoExe(t, d)
	fakeProgramOnPath(t, "someagent")
	c := dialVerb(t, sp)
	res := result(t, c.call(t, `{"id":1,"verb":"start-agent","params":{"session":"s","agent":"someagent","protocol":"acp","ready_timeout":200}}`))
	id, _ := res["window_id"].(string)
	if d.paneProtocol(id) != "acp" {
		t.Fatal("the pane was not marked")
	}
	result(t, c.call(t, `{"id":2,"verb":"close-window","params":{"session":"s","window":"`+id+`"}}`))
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for d.paneProtocol(id) != "" {
		if time.Now().After(deadline) {
			t.Fatal("the mark outlived the window")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProtocolAgentArgv(t *testing.T) {
	cases := []struct {
		protocol    string
		base, extra []string
		want        []string
	}{
		{"codex", []string{"codex"}, nil, []string{"codex", "app-server"}},
		{"codex", []string{"/bin/codex", "-c", "x=1"}, []string{"--listen", "stdio://"}, []string{"/bin/codex", "-c", "x=1", "app-server", "--listen", "stdio://"}},
		{"codex", []string{"codex", "app-server"}, []string{"--x"}, []string{"codex", "app-server", "--x"}},
		{"codex", []string{"codex"}, []string{"app-server"}, []string{"codex", "app-server"}},
		{"acp", []string{"opencode", "acp"}, []string{"--port", "0"}, []string{"opencode", "acp", "--port", "0"}},
	}
	for _, tc := range cases {
		if got := protocolAgentArgv(tc.protocol, tc.base, tc.extra); !slices.Equal(got, tc.want) {
			t.Errorf("protocolAgentArgv(%s, %q, %q) = %q, want %q", tc.protocol, tc.base, tc.extra, got, tc.want)
		}
	}
}
