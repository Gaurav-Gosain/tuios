package transcriptview

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

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

func gmCall(tc jobj) []Entry {
	name, id := tc.str("name"), tc.str("id")
	out := callEntry(name, id, gmClass(name), args(tc["args"]))
	failed := false
	switch tc.str("status") {
	case "success":
	case "error", "cancelled":
		failed = true
	default:
		return out
	}
	res := resultEntry(id, genaiResponseText(tc["result"]), failed)
	res.raw.diff = fileDiffOf(tc["resultDisplay"])
	return append(out, res)
}

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

func responseText(o jobj) string { return o.str("output", "error", "content") }

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

func geminiDoc(data []byte) []byte {
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if jsonx.Unmarshal(data, &doc) != nil {
		return nil
	}
	return docLines(doc.Messages)
}

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
