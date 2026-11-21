package transcriptview

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

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

func ampResult(id string, run jobj) (Entry, bool) {
	failed := false
	switch run.str("status") {
	case "done":
	case "error", "cancelled", "rejected-by-user":
		failed = true
	default:
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

func ampDoc(data []byte) []byte {
	var doc struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if jsonx.Unmarshal(data, &doc) != nil {
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
