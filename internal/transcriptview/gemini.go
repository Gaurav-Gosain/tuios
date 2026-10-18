package transcriptview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
type gmMessage struct {
	ID             string          `json:"id"`
	Timestamp      string          `json:"timestamp"`
	Type           string          `json:"type"`
	Content        json.RawMessage `json:"content"`
	DisplayContent json.RawMessage `json:"displayContent"`
	Thoughts       []struct {
		Subject     string `json:"subject"`
		Description string `json:"description"`
	} `json:"thoughts"`
	ToolCalls []gmToolCall `json:"toolCalls"`
	Rewind    string       `json:"$rewindTo"`
}

type gmToolCall struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Args          json.RawMessage `json:"args"`
	Result        json.RawMessage `json:"result"`
	Status        string          `json:"status"`
	ResultDisplay json.RawMessage `json:"resultDisplay"`
}

// gmPart is one part of a genai Content, as Gemini CLI and Qwen Code keep
// them.
type gmPart struct {
	Text         *string   `json:"text"`
	Thought      bool      `json:"thought"`
	InlineData   *struct{} `json:"inlineData"`
	FileData     *struct{} `json:"fileData"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

// geminiLine decodes one chat line.
func geminiLine(r *reader, line []byte, off int64) (decoded, bool) {
	var m gmMessage
	if err := unmarshalLoose(line, &m); err != nil {
		return decoded{}, false
	}
	if m.Rewind != "" {
		return decoded{rewind: m.Rewind}, true
	}
	if m.ID == "" {
		// Metadata, $set and $patch.
		return decoded{}, true
	}
	at := parseAt(m.Timestamp)
	var out []Entry
	add := func(part string, e Entry) {
		e.ID, e.At = keyID(m.ID, part), at
		out = append(out, e)
	}
	switch m.Type {
	case "user":
		content := m.DisplayContent
		if len(bytes.TrimSpace(content)) == 0 || bytes.Equal(bytes.TrimSpace(content), []byte("null")) {
			content = m.Content
		}
		for i, e := range genaiEntries(content, RoleUser) {
			add("t"+itoa(i), e)
		}
	case "gemini":
		for i, t := range m.Thoughts {
			text := strings.TrimSpace(t.Subject)
			if d := strings.TrimSpace(t.Description); d != "" {
				text += "\n" + d
			}
			if e, ok := textEntry(RoleAssistant, KindThinking, text); ok {
				add("h"+itoa(i), e)
			}
		}
		for i, e := range genaiEntries(m.Content, RoleAssistant) {
			add("t"+itoa(i), e)
		}
		for _, tc := range m.ToolCalls {
			for i, e := range gmCall(tc) {
				add("c"+itoa(i)+tc.ID, e)
			}
		}
	case "error":
		for i, e := range genaiEntries(m.Content, RoleAssistant) {
			e.Kind = KindError
			add("t"+itoa(i), e)
		}
	}
	return decoded{entries: out, key: m.ID}, true
}

// gmCall is a tool call and, once it finished, its result.
func gmCall(tc gmToolCall) []Entry {
	out := callEntry(tc.Name, tc.ID, gmClass(tc.Name), decodeArgs(tc.Args))
	failed := false
	switch tc.Status {
	case "success":
	case "error", "cancelled":
		failed = true
	default:
		// scheduled, validating, awaiting_approval, executing: no result
		// yet, so the call shows running.
		return out
	}
	res := resultEntry(tc.ID, genaiResponseText(tc.Result), failed)
	if f := fileDiffOf(tc.ResultDisplay); f != nil {
		res.raw.diff = f
	}
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
func genaiEntries(raw json.RawMessage, role string) []Entry {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	var parts []gmPart
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return nil
		}
		if e, ok := textEntry(role, KindText, s); ok {
			return []Entry{e}
		}
		return nil
	case '{':
		var p gmPart
		if unmarshalLoose(raw, &p) != nil {
			return nil
		}
		parts = []gmPart{p}
	case '[':
		// A list may mix strings and parts.
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return nil
		}
		var out []Entry
		for _, it := range items {
			out = append(out, genaiEntries(it, role)...)
		}
		return out
	default:
		return nil
	}
	var out []Entry
	for _, p := range parts {
		switch {
		case p.Text != nil:
			kind := KindText
			if p.Thought {
				kind = KindThinking
			}
			if e, ok := textEntry(role, kind, *p.Text); ok {
				out = append(out, e)
			}
		case p.InlineData != nil || p.FileData != nil:
			out = append(out, Entry{Role: role, Kind: KindText, Text: "[image]"})
		}
	}
	return out
}

// genaiResponseText is the text of a tool's result: the output or error of
// each functionResponse, or the text of the other parts.
func genaiResponseText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	var items []json.RawMessage
	switch raw[0] {
	case '[':
		if json.Unmarshal(raw, &items) != nil {
			return ""
		}
	case '{', '"':
		items = []json.RawMessage{raw}
	default:
		return ""
	}
	var b strings.Builder
	for _, it := range items {
		var s string
		if json.Unmarshal(it, &s) == nil {
			b.WriteString(s)
			continue
		}
		var p gmPart
		if unmarshalLoose(it, &p) != nil {
			continue
		}
		switch {
		case p.FunctionResponse != nil:
			b.WriteString(responseText(p.FunctionResponse.Response))
		case p.Text != nil:
			b.WriteString(*p.Text)
		}
		if b.Len() > rawTextMax {
			break
		}
	}
	return b.String()
}

// responseText is a function response's output, or its error, or its text.
func responseText(raw json.RawMessage) string {
	var o struct {
		Output  json.RawMessage `json:"output"`
		Error   json.RawMessage `json:"error"`
		Content json.RawMessage `json:"content"`
	}
	if unmarshalLoose(raw, &o) != nil {
		return ""
	}
	for _, v := range []json.RawMessage{o.Output, o.Error, o.Content} {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

// fileDiffOf is the diff of a FileDiff result display, nil for any other.
func fileDiffOf(raw json.RawMessage) *diffSrc {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var d struct {
		FileDiff string `json:"fileDiff"`
		FileName string `json:"fileName"`
		FilePath string `json:"filePath"`
	}
	if unmarshalLoose(raw, &d) != nil || d.FileDiff == "" {
		return nil
	}
	name := d.FilePath
	if name == "" {
		name = d.FileName
	}
	files := parseUnified(d.FileDiff, name)
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

// geminiMatch reports whether a chat's projectHash is the hash of cwd, the
// one Gemini CLI writes.
func geminiMatch(head []byte, cwd string) bool {
	sum := sha256.Sum256([]byte(cwd))
	want := hex.EncodeToString(sum[:])
	var meta struct {
		ProjectHash string `json:"projectHash"`
	}
	if line := firstLine(head); line != nil && json.Unmarshal(line, &meta) == nil && meta.ProjectHash != "" {
		return meta.ProjectHash == want
	}
	// An older document: the field is near its start, on a line of its own.
	return bytes.Contains(head[:min(len(head), 1024)], []byte(`"projectHash": "`+want+`"`))
}
