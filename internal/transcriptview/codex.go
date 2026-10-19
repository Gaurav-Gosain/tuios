package transcriptview

import (
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

// codexLine decodes one rollout line.
func codexLine(r *reader, line []byte, off int64) (decoded, bool) {
	rec, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	it := rec.obj("payload")
	switch typ := rec.str("type"); typ {
	case "response_item", "event_msg":
	case "session_meta", "turn_context", "compacted", "":
		return decoded{}, true
	default:
		// A bare response item, as older rollouts write them.
		it = rec
	}
	var out []Entry
	if rec.str("type") == "event_msg" {
		out = codexEvent(it)
	} else {
		out = codexItem(it)
	}
	at := parseAt(rec.str("timestamp"))
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}

// codexItem turns one response item into entries.
func codexItem(it jobj) []Entry {
	switch it.str("type") {
	case "message":
		role := RoleUser
		switch it.str("role") {
		case "assistant":
			role = RoleAssistant
		case "user":
		default:
			// developer and system: the instructions Codex sends, not the
			// conversation.
			return nil
		}
		var out []Entry
		for _, c := range it.list("content") {
			c := asObj(c)
			switch c.str("type") {
			case "input_text", "output_text", "text":
				text := c.str("text")
				if role == RoleUser && codexInjected(text) {
					continue
				}
				if e, ok := textEntry(role, KindText, text); ok {
					out = append(out, e)
				}
			case "input_image":
				out = append(out, Entry{Role: role, Kind: KindText, Text: "[image]"})
			}
		}
		return out
	case "reasoning":
		var parts []string
		for _, s := range it.list("summary") {
			parts = append(parts, asObj(s).str("text"))
		}
		if e, ok := textEntry(RoleAssistant, KindThinking, strings.Join(parts, "\n\n")); ok {
			return []Entry{e}
		}
	case "function_call":
		name := it.str("name")
		return callEntry(name, it.str("call_id"), codexClass(name), args(it["arguments"]))
	case "custom_tool_call":
		name := it.str("name")
		return callEntry(name, it.str("call_id"), codexClass(name), jobj{"input": it.str("input")})
	case "local_shell_call":
		return callEntry("shell", it.str("call_id"), classOther, it.obj("action"))
	case "web_search_call":
		e := callEntry("web_search", "", classOther, it.obj("action"))
		e[0].Status = StatusOK
		return e
	case "function_call_output", "custom_tool_call_output":
		text, failed := codexOutput(it["output"])
		return []Entry{resultEntry(it.str("call_id"), text, failed)}
	}
	return nil
}

// codexEvent turns the events that are not repeats of response items into
// entries.
func codexEvent(it jobj) []Entry {
	switch it.str("type") {
	case "error", "stream_error":
		if e, ok := textEntry(RoleAssistant, KindError, it.str("message")); ok {
			return []Entry{e}
		}
	case "turn_aborted":
		reason := it.str("reason")
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
func codexOutput(v any) (string, bool) {
	var text string
	switch v := v.(type) {
	case string:
		text = v
	case []any:
		var parts []string
		for _, it := range v {
			it := asObj(it)
			if it.str("type") == "input_image" {
				parts = append(parts, "[image]")
				continue
			}
			parts = append(parts, it.str("text"))
		}
		text = strings.Join(parts, "\n")
	case map[string]any:
		ok, isBool := v["success"].(bool)
		return jobj(v).str("content"), isBool && !ok
	}
	if t := strings.TrimSpace(text); strings.HasPrefix(t, "{") {
		if o, _ := parseObj([]byte(t)); o != nil {
			if code, ok := o.obj("metadata")["exit_code"].(float64); ok {
				return o.str("output"), code != 0
			}
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
