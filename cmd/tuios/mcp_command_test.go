package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/mcp"
)

// runMCPChild runs tuios mcp in this process, for a test that started the test
// binary as its child. Its arguments come from TUIOS_TEST_MCP_ARGS.
func runMCPChild() int {
	cmd := newMCPCommand()
	cmd.SetArgs(strings.Fields(os.Getenv("TUIOS_TEST_MCP_ARGS")))
	if err := cmd.Execute(); err != nil {
		return 1
	}
	return 0
}

// mcpChild is tuios mcp running as a child process, driven over its stdio by a
// scripted client.
type mcpChild struct {
	t     *testing.T
	in    io.WriteCloser
	lines chan map[string]any
	next  int
}

func startMCPChild(t *testing.T, args string, env ...string) *mcpChild {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TUIOS_TEST_MCP_CHILD=1", "TUIOS_TEST_MCP_ARGS="+args, "TUIOS_SOCKET=")
	cmd.Env = append(cmd.Env, env...)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &mcpChild{t: t, in: in, lines: make(chan map[string]any, 64)}
	go func() {
		r := bufio.NewReader(out)
		for {
			line, err := r.ReadBytes('\n')
			if err != nil {
				close(c.lines)
				return
			}
			var m map[string]any
			if json.Unmarshal(line, &m) != nil {
				m = map[string]any{"not_json": string(line)}
			}
			c.lines <- m
		}
	}()
	t.Cleanup(func() {
		_ = in.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("tuios mcp exited with %v at end of input, want 0", err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("tuios mcp did not exit at end of input")
		}
	})
	return c
}

func (c *mcpChild) rpc(method string, params any) map[string]any {
	c.t.Helper()
	c.next++
	req := map[string]any{"jsonrpc": "2.0", "id": c.next, "method": method, "params": params}
	raw, _ := json.Marshal(req)
	if _, err := c.in.Write(append(raw, '\n')); err != nil {
		c.t.Fatalf("write: %v", err)
	}
	select {
	case m, ok := <-c.lines:
		if !ok {
			c.t.Fatal("tuios mcp closed its output")
		}
		if m["id"] != float64(c.next) {
			c.t.Fatalf("answer %v, want id %d", m, c.next)
		}
		return m
	case <-time.After(20 * time.Second):
		c.t.Fatalf("no answer to %s", method)
	}
	return nil
}

func (c *mcpChild) tool(name string, args map[string]any) (map[string]any, string) {
	c.t.Helper()
	res := c.rpc("tools/call", map[string]any{"name": name, "arguments": args})["result"].(map[string]any)
	content := res["content"].([]any)
	return res, content[len(content)-1].(map[string]any)["text"].(string)
}

var paneTokenRe = regexp.MustCompile(`TOK=([0-9a-f]{32})`)

// TestMCPServerOverStdioIsHeldToItsPane drives the real tuios mcp command over
// its stdio against a real daemon. The server runs as a child of the test
// process, which is the daemon, so the kernel counts it inside a pane without
// being able to say which. It proves its pane with the TUIOS_PANE_TOKEN the
// pane's shell was started with, read off the pane's own screen. From there it
// reaches its own session and nothing else, cannot type, and its self report
// lands on its own pane.
func TestMCPServerOverStdioIsHeldToItsPane(t *testing.T) {
	c := startSubscribeDaemon(t)
	for _, name := range []string{"mine", "theirs"} {
		if _, err := c.Call("new-session", map[string]any{"name": name, "window": false}); err != nil {
			t.Fatalf("new-session %s: %v", name, err)
		}
	}
	raw, err := c.Call("new-window", map[string]any{
		"session": "mine", "name": "agent",
		"command": []string{"/bin/sh", "-c", `printf 'TOK=%s\n' "$TUIOS_PANE_TOKEN"; exec sleep 600`},
	})
	if err != nil {
		t.Fatalf("new-window: %v", err)
	}
	var win struct {
		WindowID string `json:"window_id"`
	}
	_ = json.Unmarshal(raw, &win)
	if _, err := c.CallWithTimeout("wait-for", map[string]any{"session": "mine", "window": win.WindowID, "condition": "window-output", "pattern": "TOK=[0-9a-f]{32}", "timeout": 10000}, 15*time.Second); err != nil {
		t.Fatalf("the pane never printed its token: %v", err)
	}
	cap, err := c.Call("capture-pane", map[string]any{"session": "mine", "window": win.WindowID})
	if err != nil {
		t.Fatal(err)
	}
	m := paneTokenRe.FindStringSubmatch(string(cap))
	if m == nil {
		t.Fatalf("no token on the pane: %s", cap)
	}

	srv := startMCPChild(t, "", "TUIOS_PANE_ID="+win.WindowID, "TUIOS_PANE_TOKEN="+m[1])
	init := srv.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test"}})
	if init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize = %v", init)
	}
	tools := srv.rpc("tools/list", map[string]any{})["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	if strings.Contains(strings.Join(names, ","), "tuios_send_text") {
		t.Errorf("the default server lists send_text: %v", names)
	}

	res, text := srv.tool("tuios_list_windows", nil)
	if res["isError"] == true || !strings.Contains(text, win.WindowID) {
		t.Errorf("list_windows of its own session = %s", text)
	}
	res, text = srv.tool("tuios_list_windows", map[string]any{"session": "theirs"})
	if res["isError"] != true || !strings.Contains(text, "forbidden") {
		t.Errorf("list_windows of another session = %s, want forbidden", text)
	}
	res, text = srv.tool("tuios_capture_pane", map[string]any{"window": win.WindowID})
	if res["isError"] == true || !strings.Contains(text, "TOK=") || !strings.Contains(text, `"untrusted":true`) {
		t.Errorf("capture_pane of its own pane = %s", text)
	}

	res, text = srv.tool("tuios_set_agent_state", map[string]any{"state": "working", "message": "from mcp"})
	if res["isError"] == true {
		t.Fatalf("set_agent_state = %s", text)
	}
	st, err := c.Call("get-agent-state", map[string]any{"session": "mine", "window": win.WindowID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(st), `"working"`) {
		t.Errorf("the pane's state after its own report = %s, want working", st)
	}

	// The event stream carries its own session and not the other one.
	if _, err := c.Call("new-window", map[string]any{"session": "theirs", "name": "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Call("set-agent-state", map[string]any{"session": "mine", "window": win.WindowID, "state": "idle"}); err != nil {
		t.Fatal(err)
	}
	_, text = srv.tool("tuios_events", map[string]any{"after_seq": 0, "wait_ms": 2000})
	var events struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(text), &events); err != nil {
		t.Fatalf("events = %s: %v", text, err)
	}
	sawMine := false
	for _, ev := range events.Events {
		switch ev["session"] {
		case "theirs":
			t.Errorf("an event of another session reached the stream: %v", ev)
		case "mine":
			sawMine = true
		}
	}
	if !sawMine {
		t.Errorf("no event of its own session: %s", text)
	}

	// Its own session goes quiet while the other one is busy. The replay
	// after last_seq is empty for this caller, and the next call must still
	// resume from where the daemon is, not hand the same seq back forever.
	var point struct {
		LastSeq float64 `json:"last_seq"`
		BootID  string  `json:"boot_id"`
	}
	_ = json.Unmarshal([]byte(text), &point)
	raw, err = c.Call("new-window", map[string]any{"session": "theirs", "name": "busy"})
	if err != nil {
		t.Fatal(err)
	}
	var busy struct {
		WindowID string `json:"window_id"`
	}
	_ = json.Unmarshal(raw, &busy)
	for _, state := range []string{"working", "idle", "working"} {
		if _, err := c.Call("set-agent-state", map[string]any{"session": "theirs", "window": busy.WindowID, "state": state}); err != nil {
			t.Fatal(err)
		}
	}
	att, err := c.Call("list-attention", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var now struct {
		Seq float64 `json:"seq"`
	}
	_ = json.Unmarshal(att, &now)
	if now.Seq <= point.LastSeq {
		t.Fatalf("the daemon's seq %v did not move past %v", now.Seq, point.LastSeq)
	}
	_, text = srv.tool("tuios_events", map[string]any{"after_seq": point.LastSeq, "boot_id": point.BootID, "wait_ms": 300})
	var resumed struct {
		LastSeq float64          `json:"last_seq"`
		Events  []map[string]any `json:"events"`
	}
	if err := json.Unmarshal([]byte(text), &resumed); err != nil {
		t.Fatalf("events = %s: %v", text, err)
	}
	if len(resumed.Events) != 0 {
		t.Errorf("a quiet session's resume returned %v", resumed.Events)
	}
	if resumed.LastSeq < now.Seq {
		t.Errorf("resumed last_seq = %v, want at least %v, the seq the daemon had reached", resumed.LastSeq, now.Seq)
	}
}

// TestMCPServerWithoutAPaneReachesNothing: a server restricted to its own
// session that cannot prove a pane is refused everything that reads a session.
func TestMCPServerWithoutAPaneReachesNothing(t *testing.T) {
	c := startSubscribeDaemon(t)
	if _, err := c.Call("new-session", map[string]any{"name": "any"}); err != nil {
		t.Fatal(err)
	}
	srv := startMCPChild(t, "--write")
	res, text := srv.tool("tuios_list_windows", map[string]any{"session": "any"})
	if res["isError"] != true || !strings.Contains(text, "no pane") {
		t.Errorf("list_windows from no pane = %s, want forbidden", text)
	}
	res, text = srv.tool("tuios_send_text", map[string]any{"text": "x"})
	if res["isError"] != true || !strings.Contains(text, "forbidden") {
		t.Errorf("send_text from no pane = %s, want forbidden", text)
	}
	// --scope all lifts the session restriction, and read-only still holds.
	all := startMCPChild(t, "--scope all")
	res, text = all.tool("tuios_list_windows", map[string]any{"session": "any"})
	if res["isError"] == true {
		t.Errorf("list_windows under --scope all = %s", text)
	}
	res, text = all.tool("tuios_send_agent_message", map[string]any{"session": "any", "text": "hello"})
	if res["isError"] == true {
		t.Errorf("mail under --scope all, read-only = %s", text)
	}
}

// TestSkillNamesEveryMCPTool holds the skill and the CLI reference to the tools
// the server built from this binary's verb table offers, so a tool added to
// the catalog, or one whose verb went away, shows up here.
func TestSkillNamesEveryMCPTool(t *testing.T) {
	srv := mcp.New(mcp.Options{Write: true, Verbs: mcpVerbDocs()})
	names := srv.ToolNames()
	if len(names) < 15 {
		t.Fatalf("the server offers %d tools from this verb table, want the whole catalog: %v", len(names), names)
	}
	ref, err := os.ReadFile("../../docs/CLI_REFERENCE.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !strings.Contains(skillText(t, "mcp"), name) {
			t.Errorf("the skill does not name the MCP tool %s", name)
		}
		if !strings.Contains(string(ref), name) {
			t.Errorf("docs/CLI_REFERENCE.md does not name the MCP tool %s", name)
		}
	}
}

// TestIntegrationInstallMCPRegistersTheServer runs the command a person runs:
// install --mcp writes the server into Claude Code's user config, status
// reports it, and uninstall takes it out again with the hooks.
func TestIntegrationInstallMCPRegistersTheServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		root := newRootCommand()
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("tuios %s: %v", strings.Join(args, " "), err)
		}
	}
	run("integration", "install", "claude-code", "--mcp-write")
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if e := doc.Servers["tuios"]; e.Command != "tuios" || strings.Join(e.Args, " ") != "mcp --write --integration 1" {
		t.Errorf("registered server = %+v", e)
	}
	// The registered arguments parse: the hidden --integration flag exists.
	if err := newMCPCommand().ParseFlags(doc.Servers["tuios"].Args[1:]); err != nil {
		t.Errorf("tuios mcp does not accept the arguments install registers: %v", err)
	}

	run("integration", "uninstall", "claude-code")
	data, _ = os.ReadFile(filepath.Join(home, ".claude.json"))
	if strings.Contains(string(data), `"tuios"`) {
		t.Errorf("uninstall left the server: %s", data)
	}

	// --mcp for a harness without a registration is refused before anything
	// is written.
	root := newRootCommand()
	root.SetArgs([]string{"integration", "install", "amp", "--mcp"})
	root.SilenceErrors, root.SilenceUsage = true, true
	if err := root.Execute(); err == nil {
		t.Error("install amp --mcp did not fail")
	}
}
