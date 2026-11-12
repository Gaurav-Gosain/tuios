// Command fakeacp is a scripted ACP agent for the end-to-end tests.
//
// It speaks the ACP versions -versions names: "1" (the default, and what an
// agent built before version 2 does: it answers 1 to any version asked
// for), "2" (version 2 only: a request for 1 gets -32022), "1,2" (both), or
// "none" (it refuses every initialize). Each initialize it reads is
// appended to the file -log names, one line each, so a test can count the
// connections the client opened.
//
// It opens a session, echoes a prompt back, and for a prompt that mentions
// "run" asks permission to run a command and says whether it was allowed. A
// prompt that mentions "usage" gets a plan and a usage_update, and session/new
// names the model. Its reply also carries an OSC title sequence, which the
// pane program must not pass to the pane, and it tries to write to its
// controlling terminal, which it must not have.
//
// In version 2 it also: answers a prompt with its message id and ends the
// turn with an idle state_update; for "fix" streams a message and then
// replaces it whole; for "slow" works until session/cancel and then ends the
// turn cancelled; for "wait" reports requires_action with no question and
// waits for session/cancel; for "withdraw" asks permission and takes the
// question back with $/cancel_request; for "usage" also sends a notice, a
// compaction, the slash commands, and an update of a kind no client knows.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

var (
	outMu   sync.Mutex
	out     = json.NewEncoder(os.Stdout)
	pending = map[string]chan json.RawMessage{}
	pendMu  sync.Mutex
	// version is the version this connection speaks, once initialized.
	version int
	// cancelled is closed by session/cancel for the turn running.
	cancelMu  sync.Mutex
	cancelled chan struct{}
	nextReq   = 900
	msgN      int
)

func send(v any) {
	outMu.Lock()
	defer outMu.Unlock()
	_ = out.Encode(v)
}

func respond(id json.RawMessage, result any) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func fail(id json.RawMessage, code int, msg string, data any) {
	e := map[string]any{"code": code, "message": msg}
	if data != nil {
		e["data"] = data
	}
	send(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
}

func update(u map[string]any) {
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-1", "update": u}})
}

func newID(prefix string) string {
	msgN++
	return prefix + strconv.Itoa(msgN)
}

// say sends text as a chunk of message id, or of no id in version 1.
func say(id, text string) {
	u := map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}}
	if version >= 2 {
		u["messageId"] = id
	}
	update(u)
}

func state(s string, extra map[string]any) {
	u := map[string]any{"sessionUpdate": "state_update", "state": s}
	for k, v := range extra {
		u[k] = v
	}
	update(u)
}

// request sends a request to the client and returns its id and the channel
// its answer arrives on.
func request(method string, params any) (int, chan json.RawMessage) {
	ch := make(chan json.RawMessage, 1)
	pendMu.Lock()
	nextReq++
	id := nextReq
	pending[strconv.Itoa(id)] = ch
	pendMu.Unlock()
	send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return id, ch
}

var options = []any{
	map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
	map[string]any{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
}

// outcome reads a permission answer: the option chosen, or "cancelled".
func outcome(raw json.RawMessage) string {
	var res struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.Outcome.Outcome == "selected" {
		return res.Outcome.OptionID
	}
	return res.Outcome.Outcome
}

// ask sends session/request_permission for running the tests and waits for
// the answer.
func ask() string {
	params := map[string]any{"sessionId": "fake-1", "options": options}
	if version >= 2 {
		params["title"] = "Run the tests?"
		params["description"] = "The agent wants to run the test suite."
		params["subject"] = map[string]any{"type": "command", "command": "go test ./...", "cwd": "/fake/work", "toolCallId": "call-1"}
	} else {
		params["toolCall"] = map[string]any{"toolCallId": "call-1"}
	}
	_, ch := request("session/request_permission", params)
	return outcome(<-ch)
}

func prompt(id json.RawMessage, params json.RawMessage) {
	var p struct {
		Prompt []struct {
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(params, &p)
	text := ""
	if len(p.Prompt) > 0 {
		text = p.Prompt[0].Text
	}
	cancelMu.Lock()
	cancelled = make(chan struct{})
	stop := cancelled
	cancelMu.Unlock()

	reply := newID("msg-")
	if version >= 2 {
		user := newID("user-")
		respond(id, map[string]any{"messageId": user})
		update(map[string]any{"sessionUpdate": "user_message", "messageId": user, "content": []any{map[string]any{"type": "text", "text": text}}})
		state("running", nil)
	}
	say(reply, "ECHO: "+text+"\x1b]0;pwned\x07\n")
	reason := "end_turn"
	if strings.Contains(text, "usage") {
		if version >= 2 {
			update(map[string]any{"sessionUpdate": "plan_update", "plan": map[string]any{"type": "items", "planId": "plan-1", "entries": []any{
				map[string]any{"content": "read", "priority": "high", "status": "completed"},
				map[string]any{"content": "fix", "priority": "high", "status": "in_progress"},
			}}})
			update(map[string]any{"sessionUpdate": "notice", "severity": "warning", "title": "Rate limited", "description": "retrying in 5s"})
			update(map[string]any{"sessionUpdate": "compaction_update", "compactionId": "k1", "status": "in_progress"})
			update(map[string]any{"sessionUpdate": "compaction_update", "compactionId": "k1", "status": "completed"})
			update(map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []any{
				map[string]any{"type": "command", "name": "review", "description": "Review the diff"},
				map[string]any{"type": "command", "name": "init", "description": "Set up"},
			}})
			update(map[string]any{"sessionUpdate": "_fake.example/widget", "x": 1})
		} else {
			update(map[string]any{"sessionUpdate": "plan", "entries": []any{
				map[string]any{"content": "read", "priority": "high", "status": "completed"},
				map[string]any{"content": "fix", "priority": "high", "status": "in_progress"},
			}})
		}
		update(map[string]any{"sessionUpdate": "usage_update", "used": 84000, "size": 200000, "cost": map[string]any{"amount": 0.42, "currency": "USD"}})
	}
	if strings.Contains(text, "run") {
		tool := map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "go test ./...", "kind": "execute", "status": "pending", "rawInput": map[string]any{"command": "go test ./..."}}
		if version >= 2 {
			tool["sessionUpdate"] = "tool_call_update"
		}
		update(tool)
		if version >= 2 {
			state("requires_action", nil)
		}
		answer := ask()
		if version >= 2 {
			state("running", nil)
		}
		status := "failed"
		if answer == "allow" {
			status = "completed"
		}
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": status})
		say(newID("msg-"), "PERMISSION: "+answer+"\n")
	}
	if version >= 2 && strings.Contains(text, "fix") {
		fix := newID("msg-")
		say(fix, "Draft ans")
		update(map[string]any{"sessionUpdate": "agent_message", "messageId": fix, "content": []any{map[string]any{"type": "text", "text": "FINAL ANSWER: the bug is fixed\n"}}})
	}
	if version >= 2 && strings.Contains(text, "withdraw") {
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-9", "title": "rm -rf build", "kind": "execute", "status": "pending", "rawInput": map[string]any{"command": "rm -rf build"}})
		state("requires_action", nil)
		reqID, ch := request("session/request_permission", map[string]any{"sessionId": "fake-1", "title": "Delete the build folder?",
			"subject": map[string]any{"type": "tool_call", "toolCall": map[string]any{"toolCallId": "call-9"}}, "options": options})
		// The agent stops waiting and takes the question back.
		time.Sleep(1500 * time.Millisecond)
		send(map[string]any{"jsonrpc": "2.0", "method": "$/cancel_request", "params": map[string]any{"requestId": reqID}})
		got := outcome(<-ch)
		state("running", nil)
		say(newID("msg-"), "WITHDRAWN: "+got+"\n")
	}
	if version >= 2 && (strings.Contains(text, "slow") || strings.Contains(text, "wait")) {
		if strings.Contains(text, "wait") {
			state("requires_action", nil)
		} else {
			update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-slow", "title": "sleep 600", "kind": "execute", "status": "in_progress"})
		}
		<-stop
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-slow", "status": "cancelled"})
		say(newID("msg-"), "STOPPED AFTER CANCEL\n")
		reason = "cancelled"
	}
	if version >= 2 {
		state("idle", map[string]any{"stopReason": reason})
		return
	}
	respond(id, map[string]any{"stopReason": reason})
}

func main() {
	versions := flag.String("versions", "1", "the ACP versions to speak: 1, 2, 1,2 or none")
	logPath := flag.String("log", "", "a file to append each initialize to")
	flag.Parse()
	var speaks []int
	for v := range strings.SplitSeq(*versions, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			speaks = append(speaks, n)
		}
	}
	// An agent that could open the pane's terminal could write past the
	// transcript. The pane program starts it with no controlling terminal,
	// so this fails, and the test checks the line never shows.
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		_, _ = tty.WriteString("TTYLEAK\r\n")
		_ = tty.Close()
	}
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method == "initialize":
			var p struct {
				ProtocolVersion int `json:"protocolVersion"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if path := *logPath; path != "" {
				if f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
					_, _ = f.WriteString("initialize " + strconv.Itoa(p.ProtocolVersion) + "\n")
					_ = f.Close()
				}
			}
			initialize(m.ID, p.ProtocolVersion, speaks)
		case m.Method == "session/new":
			res := map[string]any{"sessionId": "fake-1"}
			if version >= 2 {
				res["configOptions"] = []any{map[string]any{"configId": "model", "name": "Model", "category": "model", "type": "select",
					"currentValue": "fake/model-2", "options": []any{map[string]any{"value": "fake/model-2", "name": "Fake Model Two"}}}}
			} else {
				res["models"] = map[string]any{
					"currentModelId":  "fake/model-1",
					"availableModels": []any{map[string]any{"modelId": "fake/model-1", "name": "Fake Model"}},
				}
			}
			respond(m.ID, res)
		case m.Method == "session/prompt":
			go prompt(m.ID, m.Params)
		case m.Method == "session/cancel":
			cancelMu.Lock()
			if cancelled != nil {
				select {
				case <-cancelled:
				default:
					close(cancelled)
				}
			}
			cancelMu.Unlock()
		case m.Method == "" && len(m.ID) > 0:
			pendMu.Lock()
			ch := pending[string(m.ID)]
			delete(pending, string(m.ID))
			pendMu.Unlock()
			if ch != nil {
				ch <- m.Result
			}
		}
	}
}

// initialize answers initialize: the highest version it speaks at or below the
// one asked for, the way the spec says a version 2 agent picks. An agent of
// version 1 only answers 1 to anything, as agents built before version 2 do.
func initialize(id json.RawMessage, asked int, speaks []int) {
	if len(speaks) == 0 {
		fail(id, -32602, "this agent speaks no ACP version", nil)
		return
	}
	if slices.Equal(speaks, []int{1}) {
		version = 1
		respond(id, map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "fakeacp", "version": "1.0"}, "agentCapabilities": map[string]any{}})
		return
	}
	chose := 0
	for _, v := range speaks {
		if v <= asked && v > chose {
			chose = v
		}
	}
	if chose == 0 {
		fail(id, -32022, "Unsupported protocol version", map[string]any{"requested": asked, "supported": speaks})
		return
	}
	version = chose
	if chose >= 2 {
		respond(id, map[string]any{"protocolVersion": 2, "info": map[string]any{"name": "fakeacp", "title": "fakeacp", "version": "2.0"},
			"capabilities": map[string]any{"session": map[string]any{}}})
		return
	}
	respond(id, map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "fakeacp", "version": "2.0"}, "agentCapabilities": map[string]any{}})
}
