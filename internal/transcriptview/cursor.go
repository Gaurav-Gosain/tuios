package transcriptview

import "strings"

// FormatCursor is a Cursor Agent transcript, the JSONL file its hooks name
// in transcript_path, under ~/.cursor/projects/<project>/agent-transcripts.
//
// Cursor's hooks reference does not describe the file. Each line is
// {role, timestamp, message: {content}}, with role user or assistant and
// content a string or a list of blocks, as Orca's reader of it takes it
// (src/main/ai-vault/session-scanner-cursor-parser.ts in stablyai/orca at
// 5be871e5). The blocks are read as Anthropic content blocks are: text,
// thinking, image, tool_use {id, name, input} and tool_result {tool_use_id,
// content, is_error}. The tool inputs use Cursor's own names: target_file,
// relative_workspace_path, search_term, and code_edit for edit_file. Cursor
// Agent was not installed where this was written, so the tool blocks are not
// checked against a real file. A user message
// wraps the person's words in <user_query>, and only those words show.
const FormatCursor = "cursor-jsonl"

// cursorLine decodes one transcript line.
func cursorLine(r *reader, line []byte, off int64) (decoded, bool) {
	rec, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	role := RoleUser
	switch rec.str("role") {
	case "assistant":
		role = RoleAssistant
	case "user":
	default:
		return decoded{}, true
	}
	content := rec.obj("message")["content"]
	if content == nil {
		content = rec["content"]
	}
	var out []Entry
	switch c := content.(type) {
	case string:
		out = cursorText(role, c, out)
	case []any:
		for _, b := range c {
			b := asObj(b)
			switch b.str("type") {
			case "text":
				out = cursorText(role, b.str("text"), out)
			case "thinking":
				if e, ok := textEntry(RoleAssistant, KindThinking, b.str("thinking", "text")); ok {
					out = append(out, e)
				}
			case "image":
				out = append(out, Entry{Role: role, Kind: KindText, Text: "[image]"})
			case "tool_use":
				name, in := b.str("name"), args(b["input"])
				class := cursorClass(name)
				if class == classEdit && in.str("old_string", "old_str", "oldString") == "" {
					// Cursor's edit_file sends the edit as code_edit, the new
					// text with markers for the code it keeps, and no old
					// text. A search_replace with empty old text makes a
					// file of its new text. Each shows as the text it writes.
					class, in = classWrite, jobj{"target_file": fileOf(in), "content": in.str("code_edit", "new_string", "newString", "new_str")}
				}
				out = append(out, callEntry(name, b.str("id"), class, in)...)
			case "tool_result":
				failed, _ := b["is_error"].(bool)
				out = append(out, resultEntry(b.str("tool_use_id"), blockText(b["content"]), failed))
			}
		}
	}
	at := parseAt(rec.str("timestamp"))
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}

// cursorText adds a text entry. A user's text is the part inside
// <user_query> when it has one, and context Cursor adds in a tag of its own
// is left out.
func cursorText(role, s string, out []Entry) []Entry {
	if role == RoleUser {
		if _, rest, ok := strings.Cut(s, "<user_query>"); ok {
			s, _, _ = strings.Cut(rest, "</user_query>")
			s = strings.TrimSpace(s)
		} else if codexInjected(s) {
			return out
		}
	}
	if e, ok := textEntry(role, KindText, s); ok {
		out = append(out, e)
	}
	return out
}

// blockText is a tool result's content as text: a string, or its text
// blocks joined.
func blockText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	for _, b := range asList(v) {
		b := asObj(b)
		if b.str("type") == "image" {
			parts = append(parts, "[image]")
			continue
		}
		parts = append(parts, b.str("text"))
	}
	return strings.Join(parts, "\n")
}

// asList is v as an array, nil when it is not one.
func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// cursorClass is what a Cursor Agent tool does, under the names its tools
// have had.
func cursorClass(name string) toolClass {
	switch name {
	case "edit_file", "search_replace", "Edit", "StrReplace":
		return classEdit
	case "write", "Write":
		return classWrite
	case "todo_write", "TodoWrite":
		return classTodos
	}
	return classOther
}
