package agentproto

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConnMatchesAnswersToCalls(t *testing.T) {
	r, w, p := newPeer(t)
	notes := make(chan string, 4)
	c := NewConn(r, w, ConnOptions{OnNotify: func(m string, _ json.RawMessage) { notes <- m }})

	type answer struct {
		out map[string]any
		err error
	}
	first, second := make(chan answer, 1), make(chan answer, 1)
	go func() {
		var out map[string]any
		err := c.Call(context.Background(), "a", map[string]int{"n": 1}, &out)
		first <- answer{out, err}
	}()
	ma := p.expect("a")
	go func() {
		var out map[string]any
		err := c.Call(context.Background(), "b", nil, &out)
		second <- answer{out, err}
	}()
	mb := p.expect("b")
	if ma["jsonrpc"] != "2.0" {
		t.Errorf("a request left out jsonrpc: %v", ma)
	}
	// Answered out of order, one with the id as a string, and a line that is
	// not JSON between them: the answers still find their calls.
	p.send(map[string]any{"jsonrpc": "2.0", "id": mb["id"], "error": map[string]any{"code": -1, "message": "no"}})
	p.line("some banner the agent printed")
	p.send(map[string]any{"jsonrpc": "2.0", "method": "note"})
	id, _ := json.Marshal(ma["id"])
	p.send(map[string]any{"jsonrpc": "2.0", "id": string(id), "result": map[string]any{"ok": true}})

	if got := <-first; got.err != nil || got.out["ok"] != true {
		t.Errorf("a = %+v", got)
	}
	var rpcErr *RPCError
	if got := <-second; !errors.As(got.err, &rpcErr) || rpcErr.Message != "no" {
		t.Errorf("b = %+v, want the RPC error", got)
	}
	if m := <-notes; m != "note" {
		t.Errorf("notification = %q", m)
	}
}

func TestConnAnswersRequestsItHasNoHandlerFor(t *testing.T) {
	r, w, p := newPeer(t)
	NewConn(r, w, ConnOptions{})
	p.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "fs/read_text_file", "params": map[string]any{"path": "/etc/passwd"}})
	m := p.next()
	errObj, _ := m["error"].(map[string]any)
	if m["id"] != float64(3) || errObj["code"] != float64(codeMethodNotFound) {
		t.Errorf("answered %v", m)
	}
}

func TestConnOmitsVersionForCodex(t *testing.T) {
	r, w, p := newPeer(t)
	c := NewConn(r, w, ConnOptions{OmitVersion: true})
	_ = c.Notify("initialized", nil)
	raw, _ := p.nextRaw()
	if raw != `{"method":"initialized"}` {
		t.Errorf("sent %s", raw)
	}
}

// TestConnFailsCallsWhenTheAgentGoes: a call waiting when the agent's output
// ends returns ErrClosed rather than waiting for ever.
func TestConnFailsCallsWhenTheAgentGoes(t *testing.T) {
	r, w, p := newPeer(t)
	c := NewConn(r, w, ConnOptions{})
	done := make(chan error, 1)
	go func() { done <- c.Call(context.Background(), "x", nil, nil) }()
	p.expect("x")
	p.close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("err = %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call never returned")
	}
	if err := c.Call(context.Background(), "y", nil, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("a call after the end = %v", err)
	}
}

func TestReplyWithNothingSendsNull(t *testing.T) {
	r, w, p := newPeer(t)
	NewConn(r, w, ConnOptions{OnRequest: func(req *Request) { req.Reply(nil) }})
	p.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "m"})
	raw, _ := p.nextRaw()
	if !strings.Contains(raw, `"result":null`) {
		t.Errorf("sent %s, want a null result", raw)
	}
}
