package session

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// readArgv waits for the fake pane program to have written its argv.
func readArgv(t *testing.T, path string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second * testDeadlineScale)
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 && strings.HasSuffix(string(data), "\n") {
			return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane program never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestStartAgentProtocolRunsThePaneProgram checks what a protocol pane runs:
// this daemon's binary as agent-proto with the protocol and the harness, and
// the agent's argv after --, with app-server put between codex and the
// caller's args. The pane is ready on the pane's own report, the result names
// the protocol and the agent's command, and list-agents names the protocol.
func TestStartAgentProtocolRunsThePaneProgram(t *testing.T) {
	d, sp := startTestDaemon(t)
	argvFile := fakeProtoExe(t, d)
	fakeProgramOnPath(t, "codex")
	c := dialVerb(t, sp)

	c.send(t, `{"id":1,"verb":"start-agent","params":`+jsonParams(map[string]any{
		"session": "headless", "agent": "codex", "protocol": "codex", "args": []string{"--listen", "stdio://"},
	})+`}`)
	sess, windowID := waitOnlyPane(t, d, "headless", 1)
	got := readArgv(t, argvFile)
	want := []string{"agent-proto", "--protocol", "codex", "--harness", "codex", "--", "codex", "app-server", "--listen", "stdio://"}
	if !slices.Equal(got, want) {
		t.Fatalf("the pane runs %q, want %q", got, want)
	}
	if err := sess.SetDaemonWindowAgentState(windowID, AgentStateIdle, ""); err != nil {
		t.Fatal(err)
	}
	res := result(t, c.readResp(t))
	if res["ready"] != true || res["protocol"] != "codex" || res["command"] != "codex app-server --listen stdio://" {
		t.Fatalf("result = %v, want a ready codex protocol pane", res)
	}

	res = result(t, c.call(t, `{"id":2,"verb":"list-agents","params":{"session":"headless","all":true}}`))
	rows, _ := res["agents"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["protocol"] != "codex" {
		t.Errorf("list-agents = %v, want the pane with protocol codex", rows)
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

// TestProtocolPaneHoldsWithoutTheApprovalsTable: with no [agents.approvals]
// at all, a protocol pane's permission is held for the Inbox and answered by
// the person, while an ordinary pane's is refused as disabled. Nothing else
// about the hold changes: it still needs the pane on needs_input.
func TestProtocolPaneHoldsWithoutTheApprovalsTable(t *testing.T) {
	d, sp := startTestDaemon(t)
	_, a, b := twoWindowSession(t, d, "work")
	makeSessionWithWindow(t, d, "other")
	c := dialVerb(t, sp)
	tui := attachTUI(t, sp, "other")
	d.markProtocolPane(a, "acp")

	params := map[string]any{"session": "work", "window": a, "harness": "acp", "summary": "approve execute: go test ./...", "options": []string{"once", "deny"}}

	// Not on needs_input: nothing is held, protocol pane or not.
	res := result(t, c.call(t, `{"id":1,"verb":"request-approval","params":`+jsonParams(params)+`}`))
	if res["reason"] != approvalEndNotBlocked {
		t.Fatalf("a pane not blocked answered %v", res)
	}

	setAgentState(t, c, "work", a, "needs_input", "approval", "approve execute: go test ./...")
	setAgentState(t, c, "work", b, "needs_input", "approval", "approve execute: go test ./...")

	params["window"] = b
	res = result(t, c.call(t, `{"id":2,"verb":"request-approval","params":`+jsonParams(params)+`}`))
	if res["reason"] != approvalEndDisabled {
		t.Fatalf("the ordinary pane answered %v, want disabled", res)
	}

	params["window"] = a
	pending, _ := requestApprovalWith(t, sp, params)
	it := heldItem(t, c, a)
	id, _ := it["request_id"].(string)
	if id == "" {
		t.Fatalf("the protocol pane's approval was not held: %v", it)
	}
	if got := result(t, reply(c, t, id, ApprovalOnce, tui.HumanNonce())); got["applied"] != true {
		t.Fatalf("the reply answered %v", got)
	}
	if got := awaitResult(t, pending); got["decision"] != ApprovalOnce {
		t.Fatalf("the pane program got %v, want once", got)
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
