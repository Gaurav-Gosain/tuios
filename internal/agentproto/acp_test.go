package agentproto

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// startACP runs the ACP handshake against the peer and returns the client.
func startACP(t *testing.T) (*ACP, *peer, *events) {
	t.Helper()
	r, w, p := newPeer(t)
	ev := newEvents()
	a := NewACP(r, w, "1.2.3", ev.emit)
	done := make(chan error, 1)
	var info Info
	go func() {
		var err error
		info, err = a.Start(context.Background(), "/src/api")
		done <- err
	}()
	init := p.expect("initialize")
	params := init["params"].(map[string]any)
	if params["protocolVersion"] != float64(1) {
		t.Errorf("protocolVersion = %v, want 1", params["protocolVersion"])
	}
	// The client offers the agent nothing: no file system, no terminal.
	caps, _ := json.Marshal(params["clientCapabilities"])
	if string(caps) != `{"fs":{"readTextFile":false,"writeTextFile":false},"terminal":false}` {
		t.Errorf("clientCapabilities = %s, want nothing offered", caps)
	}
	p.respond(init, map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "fake", "version": "0.1"}})
	sess := p.expect("session/new")
	if sp := sess["params"].(map[string]any); sp["cwd"] != "/src/api" {
		t.Errorf("session/new params = %v, want the cwd", sp)
	}
	p.respond(sess, map[string]any{"sessionId": "s-1"})
	if err := <-done; err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.Session != "s-1" || info.Agent != "fake 0.1" {
		t.Errorf("info = %+v", info)
	}
	return a, p, ev
}

// TestACPTurn is a whole turn: the prompt, streamed text and reasoning, a
// tool call and its update merged, a diff and a plan, and the stop reason.
func TestACPTurn(t *testing.T) {
	a, p, ev := startACP(t)
	type turn struct {
		res TurnResult
		err error
	}
	done := make(chan turn, 1)
	go func() {
		res, err := a.Prompt(context.Background(), "fix it")
		done <- turn{res, err}
	}()
	prompt := p.expect("session/prompt")
	pp, _ := json.Marshal(prompt["params"])
	if string(pp) != `{"prompt":[{"text":"fix it","type":"text"}],"sessionId":"s-1"}` {
		t.Errorf("session/prompt params = %s", pp)
	}
	update := func(u map[string]any) {
		p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s-1", "update": u}})
	}
	update(map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking"}})
	update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Hello"}})
	update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Edit main.go", "kind": "edit", "status": "pending"})
	update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed", "content": []any{
		map[string]any{"type": "diff", "path": "main.go", "oldText": "a\n", "newText": "b\n"},
	}})
	update(map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "step", "priority": "high", "status": "in_progress"}}})
	update(map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "replayed"}})
	p.respond(prompt, map[string]any{"stopReason": "end_turn"})

	if e := ev.next(t).(Text); e.Text != "thinking" || !e.Thought {
		t.Errorf("first event = %+v, want the thought", e)
	}
	if e := ev.next(t).(Text); e.Text != "Hello" || e.Thought {
		t.Errorf("second event = %+v, want the reply", e)
	}
	if e := ev.next(t).(Tool); e.ID != "t1" || e.Title != "Edit main.go" || e.Status != ToolPending {
		t.Errorf("tool_call = %+v", e)
	}
	// The update keeps the title and kind it did not repeat.
	tool := ev.next(t).(Tool)
	if tool.Title != "Edit main.go" || tool.Kind != "edit" || tool.Status != ToolDone || len(tool.Diffs) != 1 || tool.Diffs[0].New != "b\n" || *tool.Diffs[0].Old != "a\n" {
		t.Errorf("tool_call_update = %+v", tool)
	}
	if e := ev.next(t).(Plan); len(e.Entries) != 1 || e.Entries[0].Status != "in_progress" {
		t.Errorf("plan = %+v", e)
	}
	got := <-done
	if got.err != nil || got.res.Stop != StopFinished {
		t.Fatalf("turn = %+v", got)
	}
	select {
	case e := <-ev.ch:
		t.Errorf("a replayed user message was shown: %+v", e)
	default:
	}
}

func TestACPStopReasons(t *testing.T) {
	for reason, want := range map[string]string{
		"end_turn": StopFinished, "cancelled": StopCancelled, "refusal": StopRefused,
		"max_tokens": StopLimit, "max_turn_requests": StopLimit,
	} {
		a, p, _ := startACP(t)
		done := make(chan TurnResult, 1)
		go func() {
			res, _ := a.Prompt(context.Background(), "x")
			done <- res
		}()
		p.respond(p.expect("session/prompt"), map[string]any{"stopReason": reason})
		if got := <-done; got.Stop != want {
			t.Errorf("stopReason %s = %s, want %s", reason, got.Stop, want)
		}
	}
}

// TestACPPermission covers session/request_permission: the options map to
// Inbox decisions only where the decision means exactly that option, and the
// answer goes back as the chosen optionId, or as cancelled.
func TestACPPermission(t *testing.T) {
	a, p, ev := startACP(t)
	_ = a
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s-1", "update": map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": "t9", "title": "go test ./...", "kind": "execute", "rawInput": map[string]any{"command": "go test ./..."},
	}}})
	ev.next(t)
	p.send(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s-1",
		"toolCall":  map[string]any{"toolCallId": "t9"},
		"options": []any{
			map[string]any{"optionId": "yes", "name": "Allow", "kind": "allow_once"},
			map[string]any{"optionId": "yes-all", "name": "Always", "kind": "allow_always"},
			map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"},
			map[string]any{"optionId": "never", "name": "Never", "kind": "reject_always"},
		},
	}})
	perm := ev.permission(t)
	if perm.Tool.Title != "go test ./..." || perm.Tool.Kind != "execute" {
		t.Errorf("the permission's tool = %+v, want the call it is about", perm.Tool)
	}
	var decisions []string
	for _, o := range perm.Options {
		decisions = append(decisions, o.Decision)
	}
	if strings.Join(decisions, ",") != "once,,deny," {
		t.Errorf("decisions = %q, want once for allow_once and deny for reject_once only", decisions)
	}
	if line := InboxLine(perm); line != "approve execute: go test ./..." {
		t.Errorf("InboxLine = %q", line)
	}
	perm.Choose(perm.Pick(DecisionDeny))
	resp := p.next()
	if resp["id"] != float64(77) {
		t.Fatalf("answered %v", resp)
	}
	if out, _ := json.Marshal(resp["result"]); string(out) != `{"outcome":{"optionId":"no","outcome":"selected"}}` {
		t.Errorf("result = %s", out)
	}
	// A second answer is dropped.
	perm.Choose(0)
	perm.Cancel()
	p.quiet()

	p.send(map[string]any{"jsonrpc": "2.0", "id": "p2", "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s-1", "toolCall": map[string]any{"toolCallId": "t10", "title": "x"},
		"options": []any{map[string]any{"optionId": "yes", "name": "Allow", "kind": "allow_once"}},
	}})
	ev.permission(t).Cancel()
	resp = p.next()
	if out, _ := json.Marshal(resp["result"]); resp["id"] != "p2" || string(out) != `{"outcome":{"outcome":"cancelled"}}` {
		t.Errorf("cancel answered %v", resp)
	}
}

// TestACPRefusesWhatItDoesNotOffer: fs and terminal requests, which the client
// did not advertise, get method not found and never touch anything.
func TestACPRefusesWhatItDoesNotOffer(t *testing.T) {
	_, p, _ := startACP(t)
	for i, method := range []string{"fs/read_text_file", "fs/write_text_file", "terminal/create"} {
		p.send(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": method, "params": map[string]any{"sessionId": "s-1", "path": "/etc/passwd", "content": "x"}})
		resp := p.next()
		errObj, _ := resp["error"].(map[string]any)
		if resp["id"] != float64(i+1) || errObj["code"] != float64(codeMethodNotFound) {
			t.Errorf("%s answered %v, want method not found", method, resp)
		}
	}
}

func TestACPVersionMismatch(t *testing.T) {
	r, w, p := newPeer(t)
	a := NewACP(r, w, "1", func(Event) {})
	done := make(chan error, 1)
	go func() {
		_, err := a.Start(context.Background(), "/")
		done <- err
	}()
	p.respond(p.expect("initialize"), map[string]any{"protocolVersion": 2})
	if err := <-done; err == nil || !strings.Contains(err.Error(), "version 2") {
		t.Fatalf("Start = %v, want a version error", err)
	}
}

func TestACPLoginHint(t *testing.T) {
	r, w, p := newPeer(t)
	a := NewACP(r, w, "1", func(Event) {})
	done := make(chan error, 1)
	go func() {
		_, err := a.Start(context.Background(), "/")
		done <- err
	}()
	p.respond(p.expect("initialize"), map[string]any{"protocolVersion": 1, "authMethods": []any{map[string]any{"id": "oauth", "name": "Log in with the browser"}}})
	m := p.expect("session/new")
	p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "error": map[string]any{"code": -32000, "message": "Authentication required"}})
	err := <-done
	if err == nil || !strings.Contains(err.Error(), "Log in with the browser") || !strings.Contains(err.Error(), "Authentication required") {
		t.Fatalf("Start = %v, want the error and the login methods", err)
	}
	// authenticate is never sent.
	p.quiet()
}

func TestACPCancel(t *testing.T) {
	a, p, _ := startACP(t)
	a.Cancel()
	m := p.expect("session/cancel")
	if _, ok := m["id"]; ok {
		t.Error("session/cancel was sent as a request, want a notification")
	}
	if m["params"].(map[string]any)["sessionId"] != "s-1" {
		t.Errorf("session/cancel params = %v", m["params"])
	}
}
