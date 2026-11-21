package transcriptview

import (
	"io"
	"strings"
)

func copilotLine(r *reader, line []byte, off int64) (decoded, bool) {
	ev, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	d := ev.obj("data")
	if d.str("parentToolCallId") != "" {
		return decoded{}, true
	}
	var out []Entry
	switch ev.str("type") {
	case "user.message":
		if e, ok := textEntry(RoleUser, KindText, d.str("content")); ok {
			out = append(out, e)
		}
	case "assistant.reasoning":
		if e, ok := textEntry(RoleAssistant, KindThinking, d.str("content")); ok {
			out = append(out, e)
		}
	case "assistant.message":
		if e, ok := textEntry(RoleAssistant, KindText, d.str("content")); ok {
			out = append(out, e)
		}
		for _, tr := range d.list("toolRequests") {
			tr := asObj(tr)
			name, a := tr.str("name"), args(tr["arguments"])
			out = append(out, callEntry(name, tr.str("toolCallId"), copilotClass(name, a), a)...)
		}
	case "tool.execution_complete":
		ok, _ := d["success"].(bool)
		res := d.obj("result")
		text := res.str("content")
		if msg := d.obj("error").str("message"); !ok && msg != "" {
			text = msg
		}
		e := resultEntry(d.str("toolCallId"), text, !ok)
		if detail := res.str("detailedContent"); strings.Contains(detail, "\n@@ ") {
			if files := parseUnified(detail, ""); len(files) > 0 {
				e.raw.diff = &diffSrc{file: files[0].file, patch: files[0].hunks}
			}
		}
		out = append(out, e)
	case "session.error":
		if e, ok := textEntry(RoleAssistant, KindError, d.str("message")); ok {
			out = append(out, e)
		}
	}
	at := parseAt(ev.str("timestamp"))
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}

func copilotClass(name string, a jobj) toolClass {
	switch name {
	case "edit":
		return classEdit
	case "create":
		return classWrite
	case "str_replace_editor":
		switch command(a) {
		case "create":
			return classWrite
		case "str_replace":
			return classEdit
		}
	case "update_todo":
		return classTodos
	case "apply_patch":
		return classPatch
	}
	return classOther
}

func copilotIdentity(r io.Reader, _ string) Identity {
	f := scanFields(r, "type", "timestamp", "data.startTime", "data.context.cwd")
	if f["type"] != "session.start" {
		return Identity{}
	}
	id := Identity{CWD: f["data.context.cwd"], Start: parseTime(f["data.startTime"])}
	if id.Start.IsZero() {
		id.Start = parseTime(f["timestamp"])
	}
	return id
}
