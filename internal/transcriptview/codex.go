package transcriptview

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// A Codex rollout, as codex-rs writes it (codex-rs/rollout/src/recorder.rs
// and codex-rs/history/src/rollout_payload.rs in openai/codex). Each line is
// {"timestamp", "type", "payload"}. The types that hold the conversation:
//
//   - session_meta: the session's id, cwd and cli_version, first in the file.
//   - response_item: what the model saw and said. Its payload is a message
//     (role user, assistant or developer, content of input_text, output_text
//     and input_image items), a reasoning summary, a function_call (name,
//     arguments as a JSON string, call_id), a custom_tool_call (input as
//     text, apply_patch's patch), a local_shell_call, a web_search_call, and
//     the function_call_output or custom_tool_call_output that answers a call.
//   - event_msg: what the UI showed. user_message and agent_message repeat the
//     response items, so they are left out. turn_aborted and error say a turn
//     ended badly.
//
// Rollouts written before the payload wrapper hold the response items bare,
// one per line, and those decode too.
type cxRecord struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// cxItem is a response item or an event: every field the conversation uses.
type cxItem struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Summary []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     string          `json:"input"`
	CallID    string          `json:"call_id"`
	Status    string          `json:"status"`
	Output    json.RawMessage `json:"output"`
	Action    struct {
		Command json.RawMessage `json:"command"`
		Query   string          `json:"query"`
	} `json:"action"`
	// Events.
	Message string `json:"message"`
	Reason  string `json:"reason"`
	CWD     string `json:"cwd"`
}

// codexLine decodes one rollout line.
func codexLine(r *reader, line []byte, off int64) (decoded, bool) {
	var rec cxRecord
	if err := unmarshalLoose(line, &rec); err != nil {
		return decoded{}, false
	}
	payload := rec.Payload
	switch rec.Type {
	case "response_item", "event_msg":
	case "session_meta", "turn_context", "compacted", "":
		return decoded{}, true
	default:
		// A bare response item, as older rollouts write them.
		payload = line
	}
	if len(payload) == 0 || payload[0] != '{' {
		return decoded{}, true
	}
	var it cxItem
	if err := unmarshalLoose(payload, &it); err != nil {
		return decoded{}, true
	}
	var out []Entry
	if rec.Type == "event_msg" {
		out = codexEvent(&it)
	} else {
		out = codexItem(&it)
	}
	at := parseAt(rec.Timestamp)
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}

// codexItem turns one response item into entries.
func codexItem(it *cxItem) []Entry {
	switch it.Type {
	case "message":
		role := RoleUser
		switch it.Role {
		case "assistant":
			role = RoleAssistant
		case "user":
		default:
			// developer and system: the instructions Codex sends, not the
			// conversation.
			return nil
		}
		var out []Entry
		for _, c := range it.Content {
			switch c.Type {
			case "input_text", "output_text", "text":
				if role == RoleUser && codexInjected(c.Text) {
					continue
				}
				if e, ok := textEntry(role, KindText, c.Text); ok {
					out = append(out, e)
				}
			case "input_image":
				out = append(out, Entry{Role: role, Kind: KindText, Text: "[image]"})
			}
		}
		return out
	case "reasoning":
		var parts []string
		for _, s := range it.Summary {
			parts = append(parts, s.Text)
		}
		if e, ok := textEntry(RoleAssistant, KindThinking, strings.Join(parts, "\n\n")); ok {
			return []Entry{e}
		}
	case "function_call":
		return callEntry(it.Name, it.CallID, codexClass(it.Name), decodeArgs(it.Arguments))
	case "custom_tool_call":
		return callEntry(it.Name, it.CallID, codexClass(it.Name), toolArgs{Input: it.Input})
	case "local_shell_call":
		return callEntry("shell", it.CallID, classOther, toolArgs{Command: it.Action.Command})
	case "web_search_call":
		e := callEntry("web_search", "", classOther, toolArgs{Query: it.Action.Query})
		e[0].Status = StatusOK
		return e
	case "function_call_output", "custom_tool_call_output":
		text, failed := codexOutput(it.Output)
		return []Entry{resultEntry(it.CallID, text, failed)}
	}
	return nil
}

// codexEvent turns the events that are not repeats of response items into
// entries.
func codexEvent(it *cxItem) []Entry {
	switch it.Type {
	case "error", "stream_error":
		if e, ok := textEntry(RoleAssistant, KindError, it.Message); ok {
			return []Entry{e}
		}
	case "turn_aborted":
		reason := it.Reason
		if reason == "" {
			reason = "aborted"
		}
		e, _ := textEntry(RoleAssistant, KindError, "The turn stopped: "+reason)
		return []Entry{e}
	}
	return nil
}

// codexClass is what a Codex tool does.
func codexClass(name string) toolClass {
	switch name {
	case "apply_patch":
		return classPatch
	case "update_plan":
		return classTodos
	}
	return classOther
}

// codexOutput is a call's output as text, and whether it failed. The output
// is a string or a list of content items. A shell call's string is either
// {"output", "metadata": {"exit_code"}} as JSON, or text that starts with
// "Exit code: N".
func codexOutput(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	var text string
	switch {
	case len(raw) == 0:
		return "", false
	case raw[0] == '"':
		_ = json.Unmarshal(raw, &text)
	case raw[0] == '[':
		var items []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &items)
		var parts []string
		for _, it := range items {
			if it.Type == "input_image" {
				parts = append(parts, "[image]")
				continue
			}
			parts = append(parts, it.Text)
		}
		text = strings.Join(parts, "\n")
	case raw[0] == '{':
		var o struct {
			Content string `json:"content"`
			Success *bool  `json:"success"`
		}
		_ = json.Unmarshal(raw, &o)
		return o.Content, o.Success != nil && !*o.Success
	}
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") {
		var o struct {
			Output   string `json:"output"`
			Metadata struct {
				ExitCode *int `json:"exit_code"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(t), &o) == nil && o.Metadata.ExitCode != nil {
			return o.Output, *o.Metadata.ExitCode != 0
		}
	}
	if rest, ok := strings.CutPrefix(text, "Exit code: "); ok {
		code, _, _ := strings.Cut(rest, "\n")
		if n, err := strconv.Atoi(strings.TrimSpace(code)); err == nil {
			return text, n != 0
		}
	}
	return text, false
}

// codexInjected reports a user message Codex wrote itself: the environment,
// the AGENTS.md instructions and the like, each in a tag of its own.
func codexInjected(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "# AGENTS.md instructions") {
		return true
	}
	if len(s) < 3 || s[0] != '<' {
		return false
	}
	end := strings.IndexByte(s, '>')
	if end < 2 {
		return false
	}
	tag := s[1:end]
	for _, r := range tag {
		if !(r >= 'a' && r <= 'z' || r == '_' || r == '-' || r == ' ') {
			return false
		}
	}
	name, _, _ := strings.Cut(tag, " ")
	return strings.HasSuffix(s, "</"+name+">")
}
