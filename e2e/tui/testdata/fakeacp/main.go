// Command fakeacp is a scripted ACP agent for the end-to-end tests. It speaks
// ACP version 1 on stdin and stdout: it opens a session, echoes a prompt back,
// and for a prompt that mentions "run" asks permission to run a command and
// says whether it was allowed. Its reply also carries an OSC title sequence,
// which the pane program must not pass to the pane, and it tries to write to
// its controlling terminal, which it must not have.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"sync"
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
)

func send(v any) {
	outMu.Lock()
	defer outMu.Unlock()
	_ = out.Encode(v)
}

func respond(id json.RawMessage, result any) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func update(u map[string]any) {
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "fake-1", "update": u}})
}

func say(text string) {
	update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

// ask sends session/request_permission and waits for the answer.
func ask() string {
	ch := make(chan json.RawMessage, 1)
	pendMu.Lock()
	pending["900"] = ch
	pendMu.Unlock()
	send(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "fake-1",
		"toolCall":  map[string]any{"toolCallId": "call-1"},
		"options": []any{
			map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
			map[string]any{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
		},
	}})
	var res struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(<-ch, &res)
	if res.Outcome.Outcome == "selected" {
		return res.Outcome.OptionID
	}
	return res.Outcome.Outcome
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
	say("ECHO: " + text + "\x1b]0;pwned\x07\n")
	if strings.Contains(text, "run") {
		update(map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "go test ./...", "kind": "execute", "status": "pending", "rawInput": map[string]any{"command": "go test ./..."}})
		answer := ask()
		status := "failed"
		if answer == "allow" {
			status = "completed"
		}
		update(map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": status})
		say("PERMISSION: " + answer + "\n")
	}
	respond(id, map[string]any{"stopReason": "end_turn"})
}

func main() {
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
			respond(m.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "fakeacp", "version": "1.0"}, "agentCapabilities": map[string]any{}})
		case m.Method == "session/new":
			respond(m.ID, map[string]any{"sessionId": "fake-1"})
		case m.Method == "session/prompt":
			go prompt(m.ID, m.Params)
		case m.Method == "session/cancel":
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
