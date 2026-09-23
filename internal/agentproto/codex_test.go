package agentproto

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// startCodex runs the app-server handshake against the peer. It checks that
// no message carries a jsonrpc member, and that initialized follows
// initialize before anything else.
func startCodex(t *testing.T) (*Codex, *peer, *events) {
	t.Helper()
	r, w, p := newPeer(t)
	ev := newEvents()
	c := NewCodex(r, w, "1.2.3", ev.emit)
	done := make(chan error, 1)
	go func() {
		_, err := c.Start(context.Background(), "/src/api")
		done <- err
	}()
	raw, init := p.nextRaw()
	if init["method"] != "initialize" || strings.Contains(raw, "jsonrpc") {
		t.Fatalf("first message %s, want initialize without jsonrpc", raw)
	}
	params, _ := json.Marshal(init["params"])
	if !strings.Contains(string(params), `"experimentalApi":false`) || !strings.Contains(string(params), `"name":"tuios"`) {
		t.Errorf("initialize params = %s", params)
	}
	p.send(map[string]any{"id": init["id"], "result": map[string]any{"userAgent": "codex/0.99"}})
	if m := p.expect("initialized"); m["id"] != nil {
		t.Errorf("initialized carried an id: %v", m)
	}
	start := p.expect("thread/start")
	if sp := start["params"].(map[string]any); sp["cwd"] != "/src/api" {
		t.Errorf("thread/start params = %v", sp)
	}
	p.send(map[string]any{"id": start["id"], "result": map[string]any{"thread": map[string]any{"id": "th-1"}, "model": "o5"}})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return c, p, ev
}

func notify(p *peer, method string, params map[string]any) {
	p.send(map[string]any{"method": method, "params": params})
}

// TestCodexTurn is a turn: turn/start, the reply streamed, a command and a file
// change as items, and turn/completed with its status.
func TestCodexTurn(t *testing.T) {
	c, p, ev := startCodex(t)
	done := make(chan TurnResult, 1)
	go func() {
		res, _ := c.Prompt(context.Background(), "fix it")
		done <- res
	}()
	ts := p.expect("turn/start")
	input, _ := json.Marshal(ts["params"])
	if string(input) != `{"input":[{"text":"fix it","text_elements":[],"type":"text"}],"threadId":"th-1"}` {
		t.Errorf("turn/start params = %s", input)
	}
	p.send(map[string]any{"id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "tu-1", "status": "inProgress"}}})
	notify(p, "item/agentMessage/delta", map[string]any{"threadId": "th-1", "turnId": "tu-1", "itemId": "m1", "delta": "Hel"})
	notify(p, "item/agentMessage/delta", map[string]any{"threadId": "th-1", "turnId": "tu-1", "itemId": "m1", "delta": "lo"})
	notify(p, "item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m1", "text": "Hello"}})
	notify(p, "item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "id": "m2", "text": "Unstreamed"}})
	notify(p, "item/started", map[string]any{"item": map[string]any{"type": "commandExecution", "id": "c1", "command": "go test ./...", "cwd": "/src/api", "status": "inProgress"}})
	notify(p, "item/completed", map[string]any{"item": map[string]any{"type": "commandExecution", "id": "c1", "command": "go test ./...", "status": "failed", "exitCode": 1}})
	notify(p, "item/started", map[string]any{"item": map[string]any{"type": "fileChange", "id": "f1", "status": "inProgress", "changes": []any{
		map[string]any{"path": "a.go", "kind": map[string]any{"type": "update"}, "diff": "@@ -1 +1 @@\n-a\n+b\n"},
	}}})
	notify(p, "turn/completed", map[string]any{"threadId": "th-1", "turn": map[string]any{"id": "tu-1", "status": "completed"}})

	var texts []string
	for range 3 {
		texts = append(texts, ev.next(t).(Text).Text)
	}
	// A streamed message is shown as it streams and not again whole; one
	// that did not stream is shown when it completes.
	if strings.Join(texts, "|") != "Hel|lo|Unstreamed" {
		t.Errorf("texts = %q", texts)
	}
	if tool := ev.next(t).(Tool); tool.Title != "go test ./..." || tool.Status != ToolRunning || tool.Kind != "execute" {
		t.Errorf("command started = %+v", tool)
	}
	if tool := ev.next(t).(Tool); tool.Status != ToolFailed || tool.Text != "exit code 1" {
		t.Errorf("command completed = %+v", tool)
	}
	if tool := ev.next(t).(Tool); tool.Title != "edit a.go" || len(tool.Diffs) != 1 || !strings.Contains(tool.Diffs[0].Unified, "+b") {
		t.Errorf("file change = %+v", tool)
	}
	if res := <-done; res.Stop != StopFinished {
		t.Errorf("turn = %+v", res)
	}
}

// TestCodexTurnEndsBeforeItsAnswerIsRead: turn/completed can arrive right
// behind the answer to turn/start, before Prompt has read that answer.
func TestCodexTurnEndsBeforeItsAnswerIsRead(t *testing.T) {
	c, p, _ := startCodex(t)
	done := make(chan TurnResult, 1)
	go func() {
		res, _ := c.Prompt(context.Background(), "x")
		done <- res
	}()
	ts := p.expect("turn/start")
	// Both lines in one write, so the read goroutine dispatches both before
	// Prompt runs again.
	a, _ := json.Marshal(map[string]any{"id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "tu-9"}}})
	b, _ := json.Marshal(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "tu-9", "status": "failed", "error": map[string]any{"message": "quota"}}}})
	p.line(string(a) + "\n" + string(b))
	if res := <-done; res.Stop != StopFailed || res.Detail != "quota" {
		t.Errorf("turn = %+v, want failed with the error", res)
	}
}

func TestCodexInterrupt(t *testing.T) {
	c, p, _ := startCodex(t)
	done := make(chan TurnResult, 1)
	go func() {
		res, _ := c.Prompt(context.Background(), "x")
		done <- res
	}()
	ts := p.expect("turn/start")
	p.send(map[string]any{"id": ts["id"], "result": map[string]any{"turn": map[string]any{"id": "tu-2"}}})
	// Cancel is sent once the turn is known.
	for {
		c.mu.Lock()
		known := c.turn == "tu-2"
		c.mu.Unlock()
		if known {
			break
		}
	}
	c.Cancel()
	m := p.expect("turn/interrupt")
	if params, _ := json.Marshal(m["params"]); string(params) != `{"threadId":"th-1","turnId":"tu-2"}` {
		t.Errorf("turn/interrupt params = %s", params)
	}
	p.send(map[string]any{"id": m["id"], "result": map[string]any{}})
	notify(p, "turn/completed", map[string]any{"turn": map[string]any{"id": "tu-2", "status": "interrupted"}})
	if res := <-done; res.Stop != StopCancelled {
		t.Errorf("turn = %+v, want cancelled", res)
	}
}

// TestCodexApprovals: a command approval is the command line, answered with
// accept, decline or cancel; a command that needs the network or is input to
// a running command is not the Inbox's; a file change carries the item's
// diff; anything else is method not found.
func TestCodexApprovals(t *testing.T) {
	_, p, ev := startCodex(t)
	p.send(map[string]any{"id": 5, "method": "item/commandExecution/requestApproval", "params": map[string]any{
		"threadId": "th-1", "turnId": "tu-1", "itemId": "c1", "startedAtMs": 1, "environmentId": nil,
		"command": "go test ./...", "cwd": "/src/api", "reason": "run the tests",
	}})
	perm := ev.permission(t)
	if line := InboxLine(perm); line != "approve execute: go test ./..." {
		t.Errorf("InboxLine = %q", line)
	}
	if perm.Detail != "in /src/api" || perm.Reason != "run the tests" {
		t.Errorf("detail %q reason %q", perm.Detail, perm.Reason)
	}
	perm.Choose(perm.Pick(DecisionOnce))
	if m := p.next(); m["id"] != float64(5) || m["result"].(map[string]any)["decision"] != "accept" {
		t.Errorf("answered %v, want accept", m)
	}

	for i, extra := range []map[string]any{
		{"networkApprovalContext": map[string]any{"host": "example.com", "protocol": "https"}},
		{"kind": "writeStdin"},
	} {
		params := map[string]any{"threadId": "th-1", "turnId": "tu-1", "itemId": "c2", "command": "curl example.com"}
		for k, v := range extra {
			params[k] = v
		}
		p.send(map[string]any{"id": 10 + i, "method": "item/commandExecution/requestApproval", "params": params})
		perm := ev.permission(t)
		if line := InboxLine(perm); line != "" {
			t.Errorf("%v: InboxLine = %q, want the pane only", extra, line)
		}
		perm.Cancel()
		if m := p.next(); m["result"].(map[string]any)["decision"] != "cancel" {
			t.Errorf("cancel answered %v", m)
		}
	}

	notify(p, "item/started", map[string]any{"item": map[string]any{"type": "fileChange", "id": "f1", "status": "inProgress", "changes": []any{
		map[string]any{"path": "a.go", "diff": "@@ -1 +1 @@\n-a\n+b\n"},
	}}})
	ev.next(t)
	p.send(map[string]any{"id": 20, "method": "item/fileChange/requestApproval", "params": map[string]any{"threadId": "th-1", "turnId": "tu-1", "itemId": "f1"}})
	perm = ev.permission(t)
	if len(perm.Tool.Diffs) != 1 || InboxLine(perm) != "" {
		t.Errorf("file change permission = %+v, want its diff and no Inbox line", perm.Tool)
	}
	perm.Choose(perm.Pick(DecisionDeny))
	if m := p.next(); m["result"].(map[string]any)["decision"] != "decline" {
		t.Errorf("deny answered %v, want decline", m)
	}

	for i, method := range []string{"item/permissions/requestApproval", "mcpServer/elicitation/request", "item/tool/call", "item/tool/requestUserInput"} {
		p.send(map[string]any{"id": 30 + i, "method": method, "params": map[string]any{}})
		m := p.next()
		errObj, _ := m["error"].(map[string]any)
		if errObj["code"] != float64(codeMethodNotFound) {
			t.Errorf("%s answered %v, want method not found", method, m)
		}
	}
}

func TestCodexErrorNotice(t *testing.T) {
	_, p, ev := startCodex(t)
	notify(p, "error", map[string]any{"error": map[string]any{"message": "rate limited"}, "willRetry": true})
	if n := ev.next(t).(Notice); n.Text != "rate limited (retrying)" || n.Error {
		t.Errorf("notice = %+v", n)
	}
}
