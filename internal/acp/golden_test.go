package acp

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The wire compatibility tests. Each one drives the client through an
// exchange with a scripted agent, in one protocol version, and compares what
// the client sent, the updates it delivered and what its calls returned with
// a golden file under testdata/golden. The golden files are the record of
// the wire shapes each version uses: a change that alters one fails here and
// shows the diff. go test -run TestGolden -update writes them again.

var update = flag.Bool("update", false, "write the golden files again")

// rec records one exchange.
type rec struct {
	mu      sync.Mutex
	sent    []string
	updates []string
	results []string
}

func (r *rec) update(u Update) {
	data, err := json.Marshal(u)
	if err != nil {
		data = []byte(err.Error())
	}
	r.mu.Lock()
	r.updates = append(r.updates, fmt.Sprintf("%T session=%s kind=%s %s", u, u.Session(), u.UpdateKind(), data))
	r.mu.Unlock()
}

func (r *rec) result(format string, a ...any) {
	r.mu.Lock()
	r.results = append(r.results, fmt.Sprintf(format, a...))
	r.mu.Unlock()
}

// expect reads the next message the client sent on p, records it and checks
// its method.
func (r *rec) expect(p *peer, method string) map[string]any {
	p.t.Helper()
	raw, m := p.nextRaw()
	r.mu.Lock()
	r.sent = append(r.sent, raw)
	r.mu.Unlock()
	if m["method"] != method {
		p.t.Fatalf("the client sent %s, want %s", raw, method)
	}
	return m
}

func (r *rec) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, part := range []struct {
		name  string
		lines []string
	}{{"sent", r.sent}, {"updates", r.updates}, {"results", r.results}} {
		fmt.Fprintf(&b, "## %s\n", part.name)
		for _, l := range part.lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// golden compares r with testdata/golden/name.txt.
func golden(t *testing.T, name string, r *rec) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	got := r.text()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the wire record.\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// dialer is a transport whose every Dial is a fresh scripted agent.
type dialer struct {
	t     *testing.T
	peers chan *peer
}

func newDialer(t *testing.T) *dialer { return &dialer{t: t, peers: make(chan *peer, 4)} }

type peerConn struct {
	io.Reader
	io.Writer
	p *peer
}

func (c peerConn) Close() error { c.p.close(); return nil }

func (d *dialer) Dial(context.Context) (io.ReadWriteCloser, error) {
	r, w, p := newPeer(d.t)
	d.peers <- p
	return peerConn{r, w, p}, nil
}

// next is the agent of the next Dial.
func (d *dialer) next() *peer {
	d.t.Helper()
	select {
	case p := <-d.peers:
		return p
	case <-time.After(5 * time.Second):
		d.t.Fatal("the client never dialled")
		return nil
	}
}

func note(p *peer, session string, u map[string]any) {
	p.send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": u}})
}

// connect runs Connect in the background and returns its result channel.
type connected struct {
	c   *Client
	err error
}

func connectAsync(d *dialer, r *rec, max int) chan connected {
	ch := make(chan connected, 1)
	go func() {
		c, err := Connect(context.Background(), d, ClientOptions{
			Info:       Implementation{Name: "tuios", Title: "tuios", Version: "test"},
			MaxVersion: max,
			OnUpdate:   r.update,
			OnPermission: func(p *PermissionRequest) {
				r.result("permission title=%q description=%q subject=%s tool=%v command=%+v options=%+v",
					p.Title, p.Description, p.SubjectType, p.ToolCall != nil, p.Command, p.Options)
				go func() {
					select {
					case <-p.Withdrawn():
						r.result("permission withdrawn")
						p.Cancel()
					case <-time.After(200 * time.Millisecond):
						p.Choose(p.Options[0].ID)
					}
				}()
			},
		})
		ch <- connected{c, err}
	}()
	return ch
}

func wait[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

type prompted struct {
	end TurnEnd
	err error
}

func promptAsync(s *Session, text string) chan prompted {
	ch := make(chan prompted, 1)
	go func() {
		end, err := s.Prompt(context.Background(), Text(text))
		ch <- prompted{end, err}
	}()
	return ch
}

// opencodeInit is what opencode 2.2.7 answers to an initialize asking for
// version 2, as it was read on 2026-11-13: version 1, in the version 1 shape.
var opencodeInit = map[string]any{
	"protocolVersion": 1,
	"agentCapabilities": map[string]any{
		"loadSession":        true,
		"promptCapabilities": map[string]any{"image": true, "embeddedContext": true},
		"sessionCapabilities": map[string]any{
			"list": map[string]any{}, "resume": map[string]any{}, "close": map[string]any{},
		},
	},
	"authMethods": []any{map[string]any{"description": "Run `opencode auth login` in the terminal", "name": "Login with opencode", "id": "opencode-login"}},
	"agentInfo":   map[string]any{"name": "OpenCode", "version": "2.2.7"},
}

// TestGoldenV1Turn: a client that asks for 2, an agent that answers 1, and a
// version 1 turn on the same connection with every update version 1 has and
// a permission.
func TestGoldenV1Turn(t *testing.T) {
	r := &rec{}
	d := newDialer(t)
	conn := connectAsync(d, r, 2)
	p := d.next()
	p.respond(r.expect(p, "initialize"), opencodeInit)
	cn := wait(t, conn)
	if cn.err != nil {
		t.Fatal(cn.err)
	}
	c := cn.c
	r.result("version=%d agent=%+v", c.Version(), c.Agent().Implementation)
	r.result("auth=%+v", c.Agent().AuthMethods)

	news := make(chan *Session, 1)
	go func() {
		s, err := c.NewSession(context.Background(), "/work")
		if err != nil {
			t.Error(err)
		}
		news <- s
	}()
	p.respond(r.expect(p, "session/new"), map[string]any{"sessionId": "s1", "models": map[string]any{"currentModelId": "m"},
		"configOptions": []any{map[string]any{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "m",
			"options": []any{map[string]any{"value": "m", "name": "Model M"}}}}})
	s := wait(t, news)
	r.result("session=%s options=%+v", s.ID, s.Setup.Options)

	turn := promptAsync(s, "hello")
	pm := r.expect(p, "session/prompt")
	note(p, "s1", map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "hello"}})
	note(p, "s1", map[string]any{"sessionUpdate": "agent_thought_chunk", "content": map[string]any{"type": "text", "text": "thinking"}})
	note(p, "s1", map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "a1", "content": map[string]any{"type": "text", "text": "Hi"}})
	note(p, "s1", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "go test", "kind": "execute", "status": "pending", "rawInput": map[string]any{"command": "go test"}})
	note(p, "s1", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "title": nil,
		"content": []any{map[string]any{"type": "diff", "path": "/work/a.go", "oldText": "a", "newText": "b"}}})
	note(p, "s1", map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "read", "priority": "high", "status": "completed"}}})
	note(p, "s1", map[string]any{"sessionUpdate": "usage_update", "used": 10, "size": 100, "cost": nil})
	note(p, "s1", map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "code"})
	note(p, "s1", map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{map[string]any{"name": "init", "description": "Set up", "input": map[string]any{"hint": "path"}}}})
	// A version 2 kind on a version 1 connection is not a version 1 update.
	note(p, "s1", map[string]any{"sessionUpdate": "state_update", "state": "idle", "stopReason": "end_turn"})
	p.send(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s1", "toolCall": map[string]any{"toolCallId": "c2", "title": "rm -rf build", "kind": "execute"},
		"options": []any{map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"}, map[string]any{"optionId": "no", "name": "No", "kind": "reject_once"}},
	}})
	_, answer := p.nextRaw()
	r.mu.Lock()
	raw, _ := json.Marshal(answer)
	r.sent = append(r.sent, string(raw))
	r.mu.Unlock()
	p.respond(pm, map[string]any{"stopReason": "end_turn"})
	got := wait(t, turn)
	r.result("turn=%+v err=%v", got.end, got.err)

	go func() { _ = s.Cancel() }()
	r.expect(p, "session/cancel")
	golden(t, "v1-turn", r)
}

// TestGoldenV2Turn: a version 2 turn with every stable version 2 update kind,
// the prompt answered with a message id and the turn ended by the idle state.
func TestGoldenV2Turn(t *testing.T) {
	r := &rec{}
	d := newDialer(t)
	conn := connectAsync(d, r, 2)
	p := d.next()
	p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 2, "info": map[string]any{"name": "fake", "title": "Fake", "version": "2.0"},
		"capabilities": map[string]any{"session": map[string]any{}},
		"authMethods":  []any{map[string]any{"methodId": "login", "type": "agent", "name": "Log in"}}})
	cn := wait(t, conn)
	if cn.err != nil {
		t.Fatal(cn.err)
	}
	c := cn.c
	r.result("version=%d agent=%+v auth=%+v", c.Version(), c.Agent().Implementation, c.Agent().AuthMethods)

	news := make(chan *Session, 1)
	go func() {
		s, err := c.NewSession(context.Background(), "/work")
		if err != nil {
			t.Error(err)
		}
		news <- s
	}()
	p.respond(r.expect(p, "session/new"), map[string]any{"sessionId": "s2",
		"configOptions": []any{
			map[string]any{"configId": "model", "name": "Model", "category": "model", "type": "select", "currentValue": "big",
				"options": []any{map[string]any{"groupId": "g", "name": "G", "options": []any{map[string]any{"value": "big", "name": "Big Model"}}}}},
			map[string]any{"configId": "fast", "name": "Fast", "type": "boolean", "currentValue": true},
		},
		"availableCommands": []any{map[string]any{"type": "command", "name": "review", "description": "Review", "input": map[string]any{"type": "text", "hint": "focus"}}}})
	s := wait(t, news)
	r.result("session=%s options=%+v commands=%+v", s.ID, s.Setup.Options, s.Setup.Commands)

	turn := promptAsync(s, "fix it")
	pm := r.expect(p, "session/prompt")
	note(p, "s2", map[string]any{"sessionUpdate": "state_update", "state": "running"})
	p.respond(pm, map[string]any{"messageId": "u1"})
	note(p, "s2", map[string]any{"sessionUpdate": "user_message", "messageId": "u1", "content": []any{map[string]any{"type": "text", "text": "fix it"}}})
	note(p, "s2", map[string]any{"sessionUpdate": "agent_thought_chunk", "messageId": "t1", "content": map[string]any{"type": "text", "text": "hmm"}})
	note(p, "s2", map[string]any{"sessionUpdate": "agent_message_chunk", "messageId": "a1", "content": map[string]any{"type": "text", "text": "Fix"}})
	note(p, "s2", map[string]any{"sessionUpdate": "agent_message", "messageId": "a1", "content": []any{map[string]any{"type": "text", "text": "Fixed."}}})
	note(p, "s2", map[string]any{"sessionUpdate": "agent_message", "messageId": "a0", "content": nil})
	note(p, "s2", map[string]any{"sessionUpdate": "agent_message", "messageId": "a2"})
	note(p, "s2", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "name": "bash", "title": "go test", "kind": "execute", "status": "in_progress",
		"rawInput": map[string]any{"command": "go test"}, "content": []any{map[string]any{"type": "terminal", "terminalId": "term1"}}})
	note(p, "s2", map[string]any{"sessionUpdate": "tool_call_content_chunk", "toolCallId": "c1", "content": map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "ok"}}})
	note(p, "s2", map[string]any{"sessionUpdate": "terminal_update", "terminalId": "term1", "command": "go test", "output": map[string]any{"data": "b2sK"}})
	note(p, "s2", map[string]any{"sessionUpdate": "terminal_output_chunk", "terminalId": "term1", "data": "UEFTUwo="})
	note(p, "s2", map[string]any{"sessionUpdate": "terminal_update", "terminalId": "term1", "exitStatus": map[string]any{"exitCode": 0}})
	note(p, "s2", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c2", "title": "edit a.go", "kind": "edit", "status": "completed",
		"content": []any{map[string]any{"type": "diff", "changes": []any{map[string]any{"operation": "modify", "path": "/work/a.go", "fileType": "text"}},
			"patch": map[string]any{"format": "git_patch", "text": "diff --git /work/a.go /work/a.go\n"}}}})
	note(p, "s2", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "cancelled", "title": nil, "rawInput": nil})
	note(p, "s2", map[string]any{"sessionUpdate": "plan_update", "plan": map[string]any{"type": "items", "planId": "p1",
		"entries": []any{map[string]any{"content": "read", "priority": "high", "status": "cancelled"}}}})
	note(p, "s2", map[string]any{"sessionUpdate": "usage_update", "used": 84000, "size": 200000, "cost": map[string]any{"amount": 0.42, "currency": "USD"}})
	note(p, "s2", map[string]any{"sessionUpdate": "usage_update", "used": 85000, "size": 200000, "cost": nil})
	note(p, "s2", map[string]any{"sessionUpdate": "notice", "severity": "warning", "title": "Rate limited", "description": "retrying in 5s"})
	note(p, "s2", map[string]any{"sessionUpdate": "compaction_update", "compactionId": "k1", "status": "in_progress"})
	note(p, "s2", map[string]any{"sessionUpdate": "compaction_summary_chunk", "compactionId": "k1", "content": map[string]any{"type": "text", "text": "sum"}})
	note(p, "s2", map[string]any{"sessionUpdate": "compaction_update", "compactionId": "k1", "status": "failed", "error": "too big"})
	note(p, "s2", map[string]any{"sessionUpdate": "config_option_update", "configOptions": []any{map[string]any{"configId": "mode", "name": "Mode", "category": "mode", "type": "select", "currentValue": "ask", "options": []any{}}}})
	note(p, "s2", map[string]any{"sessionUpdate": "session_info_update", "title": "Fixing", "updatedAt": nil})
	note(p, "s2", map[string]any{"sessionUpdate": "_example.com/widget", "x": 1})
	note(p, "s2", map[string]any{"sessionUpdate": "subagent_update", "y": 2})
	// v1's tool_call and plan are not version 2 kinds: they are kept as
	// unknown, not read in the version 1 shape.
	note(p, "s2", map[string]any{"sessionUpdate": "tool_call", "toolCallId": "old"})
	note(p, "s2", map[string]any{"sessionUpdate": "state_update", "state": "idle", "stopReason": "end_turn"})
	got := wait(t, turn)
	r.result("turn=%+v err=%v", got.end, got.err)
	go func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	p.respond(r.expect(p, "session/close"), map[string]any{})
	golden(t, "v2-turn", r)
}

// TestGoldenV2Permission: a command subject answered, and a tool call subject
// the agent withdraws with $/cancel_request, which the client answers with
// the cancelled outcome.
func TestGoldenV2Permission(t *testing.T) {
	r := &rec{}
	d := newDialer(t)
	conn := connectAsync(d, r, 2)
	p := d.next()
	p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 2, "info": map[string]any{"name": "fake", "version": "2.0"}})
	c := wait(t, conn).c
	if c == nil {
		t.Fatal("no client")
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": "p1", "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s2", "title": "Run the tests?", "description": "The agent wants to run go test.",
		"subject": map[string]any{"type": "command", "command": "go test ./...", "cwd": "/work", "toolCallId": "c1"},
		"options": []any{map[string]any{"optionId": "once", "name": "Allow once", "kind": "allow_once"}},
	}})
	raw, _ := p.nextRaw()
	r.mu.Lock()
	r.sent = append(r.sent, raw)
	r.mu.Unlock()
	p.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s2", "title": "Edit a.go?",
		"subject": map[string]any{"type": "tool_call", "toolCall": map[string]any{"toolCallId": "c2", "title": "edit a.go", "kind": "edit"}},
		"options": []any{map[string]any{"optionId": "y", "name": "Yes", "kind": "allow_once"}},
	}})
	p.send(map[string]any{"jsonrpc": "2.0", "method": "$/cancel_request", "params": map[string]any{"requestId": 7}})
	raw, _ = p.nextRaw()
	r.mu.Lock()
	r.sent = append(r.sent, raw)
	r.mu.Unlock()
	// An unknown subject still asks.
	p.send(map[string]any{"jsonrpc": "2.0", "id": 8, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s2", "title": "Use the widget?", "subject": map[string]any{"type": "_example.com/widget"},
		"options": []any{map[string]any{"optionId": "n", "name": "No", "kind": "reject_once"}},
	}})
	raw, _ = p.nextRaw()
	r.mu.Lock()
	r.sent = append(r.sent, raw)
	r.mu.Unlock()
	// What this client does not offer.
	p.send(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "elicitation/create", "params": map[string]any{"sessionId": "s2", "mode": "form", "message": "Name?"}})
	raw, _ = p.nextRaw()
	r.mu.Lock()
	r.sent = append(r.sent, raw)
	r.mu.Unlock()
	golden(t, "v2-permission", r)
}

// TestGoldenV2CancelAndStaleIdle: an idle from before the prompt does not end
// the turn; a cancel ends it with the agent's idle cancelled, and the
// updates after the cancel are still delivered. A failed turn carries its
// error.
func TestGoldenV2CancelAndStaleIdle(t *testing.T) {
	r := &rec{}
	d := newDialer(t)
	conn := connectAsync(d, r, 2)
	p := d.next()
	p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 2, "info": map[string]any{"name": "fake", "version": "2.0"}})
	c := wait(t, conn).c
	news := make(chan *Session, 1)
	go func() {
		s, _ := c.NewSession(context.Background(), "/work")
		news <- s
	}()
	p.respond(r.expect(p, "session/new"), map[string]any{"sessionId": "s3"})
	s := wait(t, news)

	turn := promptAsync(s, "long job")
	pm := r.expect(p, "session/prompt")
	// A repeat of the state before the prompt: idle, with an old reason.
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "idle", "stopReason": "cancelled"})
	select {
	case got := <-turn:
		t.Fatalf("a stale idle ended the turn: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	p.respond(pm, map[string]any{"messageId": "u1"})
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "running"})
	go func() { _ = s.Cancel() }()
	r.expect(p, "session/cancel")
	note(p, "s3", map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "cancelled"})
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "idle", "stopReason": "cancelled"})
	got := wait(t, turn)
	r.result("turn=%+v err=%v", got.end, got.err)

	turn = promptAsync(s, "again")
	pm = r.expect(p, "session/prompt")
	// The idle comes before the answer: it still ends the turn.
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "running"})
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "requires_action"})
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "idle", "stopReason": "error", "error": map[string]any{"code": -32603, "message": "model overloaded"}})
	p.respond(pm, map[string]any{"messageId": "u2"})
	got = wait(t, turn)
	r.result("turn=%+v error=%+v err=%v", got.end, got.end.Error, got.err)

	// An idle with no reason, or one that is not a string, still ends it.
	turn = promptAsync(s, "third")
	pm = r.expect(p, "session/prompt")
	p.respond(pm, map[string]any{"messageId": "u3"})
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "running"})
	note(p, "s3", map[string]any{"sessionUpdate": "state_update", "state": "idle", "stopReason": 7})
	got = wait(t, turn)
	r.result("turn=%+v err=%v", got.end, got.err)

	// A prompt the agent does not take is an error, not a turn.
	turn = promptAsync(s, "fourth")
	pm = r.expect(p, "session/prompt")
	p.send(map[string]any{"jsonrpc": "2.0", "id": pm["id"], "error": map[string]any{"code": -32800, "message": "cancelled before insertion"}})
	got = wait(t, turn)
	r.result("turn=%+v err=%v", got.end, got.err)

	// A version 2 agent that answers the prompt the version 1 way ends the
	// turn there.
	turn = promptAsync(s, "fifth")
	p.respond(r.expect(p, "session/prompt"), map[string]any{"stopReason": "end_turn"})
	got = wait(t, turn)
	r.result("turn=%+v err=%v", got.end, got.err)
	golden(t, "v2-cancel", r)
}

// TestGoldenSessionsListAndResume: session/list over two pages and
// session/resume, in each version.
func TestGoldenSessionsListAndResume(t *testing.T) {
	for _, v := range []int{1, 2} {
		t.Run(fmt.Sprint(v), func(t *testing.T) {
			r := &rec{}
			d := newDialer(t)
			conn := connectAsync(d, r, v)
			p := d.next()
			init := map[string]any{"protocolVersion": v, "info": map[string]any{"name": "fake", "version": "2.0"}}
			if v == 1 {
				init = opencodeInit
			}
			p.respond(r.expect(p, "initialize"), init)
			c := wait(t, conn).c
			lists := make(chan []SessionInfo, 1)
			go func() {
				l, err := c.ListSessions(context.Background(), "/work")
				if err != nil {
					t.Error(err)
				}
				lists <- l
			}()
			p.respond(r.expect(p, "session/list"), map[string]any{"sessions": []any{map[string]any{"sessionId": "a", "cwd": "/work", "title": "A"}}, "nextCursor": "c2"})
			p.respond(r.expect(p, "session/list"), map[string]any{"sessions": []any{map[string]any{"sessionId": "b", "cwd": "/work", "updatedAt": "2026-11-13T00:00:00Z"}}})
			r.result("sessions=%+v", wait(t, lists))
			news := make(chan *Session, 1)
			go func() {
				s, err := c.ResumeSession(context.Background(), "a", "/work")
				if err != nil {
					t.Error(err)
				}
				news <- s
			}()
			p.respond(r.expect(p, "session/resume"), map[string]any{})
			r.result("resumed=%s", wait(t, news).ID)
			golden(t, fmt.Sprintf("v%d-sessions", v), r)
		})
	}
}

// TestGoldenNegotiation: each way an agent can answer the version 2
// initialize, and what the client does.
func TestGoldenNegotiation(t *testing.T) {
	r := &rec{}
	run := func(name string, script func(d *dialer)) {
		d := newDialer(t)
		conn := connectAsync(d, r, 2)
		script(d)
		cn := wait(t, conn)
		if cn.err != nil {
			r.result("%s: error: %v", name, cn.err)
			if _, ok := errors.AsType[*VersionError](cn.err); !ok {
				t.Errorf("%s: the error is not a *VersionError", name)
			}
			return
		}
		r.result("%s: version %d, agent %s", name, cn.c.Version(), cn.c.Agent().Name)
	}
	run("v1 shape that echoes 2", func(d *dialer) {
		p := d.next()
		p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 2, "agentInfo": map[string]any{"name": "echo", "version": "1"}, "agentCapabilities": map[string]any{}})
	})
	run("unsupported, 1 listed", func(d *dialer) {
		p := d.next()
		m := r.expect(p, "initialize")
		p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "error": map[string]any{"code": -32022, "message": "Unsupported protocol version",
			"data": map[string]any{"requested": 2, "supported": []any{1}}}})
		p = d.next()
		p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "old", "version": "1"}})
	})
	run("unsupported, only 3", func(d *dialer) {
		p := d.next()
		m := r.expect(p, "initialize")
		p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "error": map[string]any{"code": -32022, "message": "Unsupported protocol version",
			"data": map[string]any{"requested": 2, "supported": []any{3}}}})
	})
	run("exits on 2, takes 1", func(d *dialer) {
		p := d.next()
		r.expect(p, "initialize")
		p.close()
		p = d.next()
		p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "strict", "version": "1"}})
	})
	run("refuses both", func(d *dialer) {
		for _, msg := range []string{"bad params", "no"} {
			p := d.next()
			m := r.expect(p, "initialize")
			p.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "error": map[string]any{"code": -32602, "message": msg}})
		}
	})
	run("chooses 3", func(d *dialer) {
		p := d.next()
		p.respond(r.expect(p, "initialize"), map[string]any{"protocolVersion": 3, "info": map[string]any{"name": "future", "version": "3"}})
	})
	golden(t, "negotiation", r)
}

// TestConnRejectsBatches: an array on the wire is answered with one Invalid
// Request with a null id, and the connection goes on.
func TestConnRejectsBatches(t *testing.T) {
	r, w, p := newPeer(t)
	NewConn(r, w, ConnOptions{})
	p.line(`[{"jsonrpc":"2.0","id":1,"method":"a"},{"jsonrpc":"2.0","id":2,"method":"b"}]`)
	raw, _ := p.nextRaw()
	if raw != `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"batches are not used"}}` {
		t.Errorf("sent %s", raw)
	}
	p.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "c"})
	if m := p.next(); m["id"] != float64(3) {
		t.Errorf("after the batch: %v", m)
	}
}

// TestConnCancelsWhatItStopsWaitingFor: a call whose context ends sends
// $/cancel_request for its id.
func TestConnCancelsWhatItStopsWaitingFor(t *testing.T) {
	r, w, p := newPeer(t)
	c := NewConn(r, w, ConnOptions{Cancels: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Call(ctx, "slow", nil, nil) }()
	m := p.expect("slow")
	cancel()
	if err := wait(t, done); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	raw, _ := p.nextRaw()
	if want := fmt.Sprintf(`{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":%v}}`, m["id"]); raw != want {
		t.Errorf("sent %s, want %s", raw, want)
	}
}
