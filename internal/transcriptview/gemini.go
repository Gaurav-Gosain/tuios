package transcriptview

import (
	"bytes"
	"encoding/json"
	"strings"
)

// A Gemini CLI chat, as packages/core/src/services/chatRecordingService.ts
// in google-gemini/gemini-cli writes it, under ~/.gemini/tmp/<project>/chats.
//
// Since 0.3x it is JSONL (session-<time>-<id>.jsonl). The first line is the
// session's metadata (sessionId, projectHash, startTime). Each other line is
// a message, written whole again each time it changes: a tool call's status
// and result land by writing the message again, under the same id. Lines
// with $set change metadata, $rewindTo takes the conversation back to a
// message, and $patch rewrites history for a resumed session.
//
// Before that it was one JSON document, {sessionId, projectHash, ...,
// messages: [...]}, rewritten whole on every change (session-*.json). That
// file is read whole and its messages are read as lines (geminiDoc).
//
// A message is {id, timestamp, type, content, displayContent}, where type is
// user, gemini, info, error or warning, and content is a string, a part or a
// list of parts. A gemini message also has thoughts ({subject, description})
// and toolCalls ({id, name, args, result, status, resultDisplay}). A tool's
// resultDisplay is text, or a FileDiff ({fileDiff, fileName}) for an edit.

// geminiLine decodes one chat line.
func geminiLine(r *reader, line []byte, off int64) (decoded, bool) {
	m, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	if rw := m.str("$rewindTo"); rw != "" {
		return decoded{rewind: rw}, true
	}
	id := m.str("id")
	if id == "" {
		// Metadata, $set and $patch.
		return decoded{}, true
	}
	at := parseAt(m.str("timestamp"))
	var out []Entry
	add := func(part string, e Entry) {
		e.ID, e.At = keyID(id, part), at
		out = append(out, e)
	}
	switch m.str("type") {
	case "user":
		content := m["displayContent"]
		if content == nil {
			content = m["content"]
		}
		for i, e := range genaiEntries(content, RoleUser) {
			add("t"+itoa(i), e)
		}
	case "gemini":
		for i, t := range m.list("thoughts") {
			t := asObj(t)
			text := strings.TrimSpace(t.str("subject"))
			if d := strings.TrimSpace(t.str("description")); d != "" {
				text += "\n" + d
			}
			if e, ok := textEntry(RoleAssistant, KindThinking, text); ok {
				add("h"+itoa(i), e)
			}
		}
		for i, e := range genaiEntries(m["content"], RoleAssistant) {
			add("t"+itoa(i), e)
		}
		for _, tc := range m.list("toolCalls") {
			tc := asObj(tc)
			for i, e := range gmCall(tc) {
				add("c"+itoa(i)+tc.str("id"), e)
			}
		}
	case "error":
		for i, e := range genaiEntries(m["content"], RoleAssistant) {
			e.Kind = KindError
			add("t"+itoa(i), e)
		}
	}
	return decoded{entries: out, key: id}, true
}

// gmCall is a tool call and, once it finished, its result.
func gmCall(tc jobj) []Entry {
	name, id := tc.str("name"), tc.str("id")
	out := callEntry(name, id, gmClass(name), args(tc["args"]))
	failed := false
	switch tc.str("status") {
	case "success":
	case "error", "cancelled":
		failed = true
	default:
		// scheduled, validating, awaiting_approval, executing: no result
		// yet, so the call shows running.
		return out
	}
	res := resultEntry(id, genaiResponseText(tc["result"]), failed)
	res.raw.diff = fileDiffOf(tc["resultDisplay"])
	return append(out, res)
}

// gmClass is what a Gemini CLI or Qwen Code tool does.
func gmClass(name string) toolClass {
	switch name {
	case "replace", "edit", "smart_edit":
		return classEdit
	case "write_file":
		return classWrite
	case "write_todos", "todo_write":
		return classTodos
	case "exit_plan_mode":
		return classPlan
	}
	return classOther
}

// genaiEntries turns a content (a string, a part or a list of parts) into
// text, thinking and image entries. Calls and responses are left out: the
// tool calls carry them.
func genaiEntries(v any, role string) []Entry {
	switch v := v.(type) {
	case string:
		if e, ok := textEntry(role, KindText, v); ok {
			return []Entry{e}
		}
	case []any:
		var out []Entry
		for _, it := range v {
			out = append(out, genaiEntries(it, role)...)
		}
		return out
	case map[string]any:
		p := jobj(v)
		if text, ok := p["text"].(string); ok {
			kind := KindText
			if thought, _ := p["thought"].(bool); thought {
				kind = KindThinking
			}
			if e, ok := textEntry(role, kind, text); ok {
				return []Entry{e}
			}
		} else if p["inlineData"] != nil || p["fileData"] != nil {
			return []Entry{{Role: role, Kind: KindText, Text: "[image]"}}
		}
	}
	return nil
}

// genaiResponseText is the text of a tool's result: the output or error of
// each functionResponse, or the text of the other parts.
func genaiResponseText(v any) string {
	items, ok := v.([]any)
	if !ok {
		items = []any{v}
	}
	var b strings.Builder
	for _, it := range items {
		if b.Len() > rawTextMax {
			break
		}
		if s, ok := it.(string); ok {
			b.WriteString(s)
			continue
		}
		p := asObj(it)
		if fr := p.obj("functionResponse"); fr != nil {
			b.WriteString(responseText(fr.obj("response")))
		} else {
			b.WriteString(p.str("text"))
		}
	}
	return b.String()
}

// responseText is a function response's output, or its error, or its text.
func responseText(o jobj) string { return o.str("output", "error", "content") }

// fileDiffOf is the diff of a FileDiff result display, nil for any other.
func fileDiffOf(v any) *diffSrc {
	d := asObj(v)
	diff := d.str("fileDiff")
	if diff == "" {
		return nil
	}
	name := d.str("filePath", "fileName")
	files := parseUnified(diff, name)
	if len(files) == 0 {
		return nil
	}
	return &diffSrc{file: name, patch: files[0].hunks}
}

// geminiDoc turns an older chat document into its messages, one per line.
func geminiDoc(data []byte) []byte {
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return nil
	}
	return docLines(doc.Messages)
}

// docLines writes each message compacted, one per line.
func docLines(msgs []json.RawMessage) []byte {
	var b bytes.Buffer
	for _, m := range msgs {
		if json.Compact(&b, m) != nil {
			continue
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}
