package transcriptview

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// An Amp thread, ~/.local/share/amp/threads/T-<id>.json: one JSON document,
// {v, id, created, messages, env, title, ...}, that Amp rewrites whole as the
// thread changes (checked against Amp 0.0.1768018268's files). A message is
// {role, messageId, content, meta: {sentAt}}, with content blocks shaped like
// the Anthropic API's: text, thinking, image, tool_use {id, name, input} in an
// assistant message, and tool_result {toolUseID, run: {status, result}} in a
// user message. A finished edit_file's result carries a unified diff.
//
// The file is read whole (ampDoc) and each message becomes a line,
// {"i": index, "m": message}, so its entries keep their ids when an earlier
// message changes length.

// ampLine decodes one message of a thread.
func ampLine(r *reader, line []byte, off int64) (decoded, bool) {
	rec, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	m := rec.obj("m")
	role := RoleUser
	switch m.str("role") {
	case "assistant":
		role = RoleAssistant
	case "user":
	default:
		return decoded{}, true
	}
	idx, _ := rec["i"].(float64)
	msg := "m" + strconv.Itoa(int(idx))
	sentAt, _ := m.obj("meta")["sentAt"].(float64)
	var out []Entry
	add := func(part string, es ...Entry) {
		for i, e := range es {
			e.ID, e.At = keyID(msg, part+"."+strconv.Itoa(i)), int64(sentAt)
			out = append(out, e)
		}
	}
	for i, b := range m.list("content") {
		b := asObj(b)
		part := strconv.Itoa(i)
		switch b.str("type") {
		case "text":
			if e, ok := textEntry(role, KindText, b.str("text")); ok {
				add(part, e)
			}
		case "thinking":
			if e, ok := textEntry(RoleAssistant, KindThinking, b.str("thinking")); ok {
				add(part, e)
			}
		case "image":
			add(part, Entry{Role: role, Kind: KindText, Text: "[image]"})
		case "tool_use":
			name := b.str("name")
			add(part, callEntry(name, b.str("id"), ampClass(name), args(b["input"]))...)
		case "tool_result":
			if e, ok := ampResult(b.str("toolUseID"), b.obj("run")); ok {
				add(part, e)
			}
		}
	}
	return decoded{entries: out}, true
}

// ampResult is a tool result once the run finished.
func ampResult(id string, run jobj) (Entry, bool) {
	failed := false
	switch run.str("status") {
	case "done":
	case "error", "cancelled", "rejected-by-user":
		failed = true
	default:
		// queued, in-progress, blocked-on-user: still running.
		return Entry{}, false
	}
	var text string
	var diff *diffSrc
	switch res := run["result"].(type) {
	case string:
		text = res
	case map[string]any:
		o := jobj(res)
		text = o.str("output", "content")
		if code, ok := o["exitCode"].(float64); ok && code != 0 {
			failed = true
		}
		if d := o.str("diff"); d != "" {
			if files := parseUnified(d, o.str("absolutePath")); len(files) > 0 {
				diff = &diffSrc{file: files[0].file, patch: files[0].hunks}
			}
		}
	}
	if failed && text == "" {
		if s, ok := run["error"].(string); ok {
			text = s
		} else {
			text = run.obj("error").str("message", "output", "error")
		}
	}
	e := resultEntry(id, text, failed)
	e.raw.diff = diff
	return e, true
}

// ampClass is what an Amp tool does.
func ampClass(name string) toolClass {
	switch name {
	case "edit_file":
		return classEdit
	case "create_file":
		return classWrite
	case "todo_write":
		return classTodos
	}
	return classOther
}

// ampDoc turns a thread into its messages, one per line, each with its
// index.
func ampDoc(data []byte) []byte {
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	var b bytes.Buffer
	for i, m := range doc.Messages {
		b.WriteString(`{"i":`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`,"m":`)
		if json.Compact(&b, m) != nil {
			b.WriteString("null")
		}
		b.WriteString("}\n")
	}
	return b.Bytes()
}
