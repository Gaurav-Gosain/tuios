package transcriptview

import (
	"bytes"
	"encoding/json"
)

// A Qwen Code chat, as its ChatRecordingService writes it (the
// @qwen-code/qwen-code package, read at 0.25.2), at
// ~/.qwen/projects/<cwd with "/" as "-">/chats/<session id>.jsonl. Qwen Code
// began as a Gemini CLI fork and keeps its genai parts, in records shaped
// like Claude Code's: {uuid, parentUuid, sessionId, timestamp, type, cwd,
// version, message}, where type is user, assistant, tool_result or system
// and message is a genai Content ({role, parts}). An assistant's parts are
// text, thoughts (thought: true) and functionCall. A tool_result's are
// functionResponse, and its toolCallResult says the status and carries the
// result display: a FileDiff for an edit.
type qwRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
	Message   struct {
		Parts json.RawMessage `json:"parts"`
	} `json:"message"`
	ToolCallResult struct {
		CallID        string          `json:"callId"`
		Status        string          `json:"status"`
		Error         json.RawMessage `json:"error"`
		ResultDisplay json.RawMessage `json:"resultDisplay"`
	} `json:"toolCallResult"`
}

// qwenLine decodes one chat record.
func qwenLine(r *reader, line []byte, off int64) (decoded, bool) {
	var rec qwRecord
	if err := unmarshalLoose(line, &rec); err != nil {
		return decoded{}, false
	}
	var out []Entry
	switch rec.Type {
	case "user":
		out = genaiEntries(rec.Message.Parts, RoleUser)
	case "assistant":
		out = genaiEntries(rec.Message.Parts, RoleAssistant)
		for _, p := range genaiParts(rec.Message.Parts) {
			if fc := p.FunctionCall; fc != nil {
				out = append(out, callEntry(fc.Name, fc.ID, gmClass(fc.Name), decodeArgs(fc.Args))...)
			}
		}
	case "tool_result":
		res := rec.ToolCallResult
		failed := res.Status == "error" || res.Status == "cancelled" || hasValue(res.Error)
		for _, p := range genaiParts(rec.Message.Parts) {
			fr := p.FunctionResponse
			if fr == nil {
				continue
			}
			id := fr.ID
			if id == "" {
				id = res.CallID
			}
			e := resultEntry(id, responseText(fr.Response), failed)
			if f := fileDiffOf(res.ResultDisplay); f != nil {
				e.raw.diff = f
			}
			out = append(out, e)
		}
	}
	at := parseAt(rec.Timestamp)
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}

// genaiParts decodes a list of parts, skipping any that do not decode.
func genaiParts(raw json.RawMessage) []gmPart {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	out := make([]gmPart, 0, len(items))
	for _, it := range items {
		var p gmPart
		if unmarshalLoose(it, &p) == nil {
			out = append(out, p)
		}
	}
	return out
}

// hasValue reports a JSON value other than null.
func hasValue(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && !bytes.Equal(raw, []byte("null"))
}
