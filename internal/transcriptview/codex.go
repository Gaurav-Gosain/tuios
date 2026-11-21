package transcriptview

import (
	"strconv"
	"strings"
)

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

func codexItem(it jobj) []Entry {
	switch it.str("type") {
	case "message":
		role := RoleUser
		switch it.str("role") {
		case "assistant":
			role = RoleAssistant
		case "user":
		default:
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

func codexClass(name string) toolClass {
	switch name {
	case "apply_patch":
		return classPatch
	case "update_plan":
		return classTodos
	}
	return classOther
}

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
