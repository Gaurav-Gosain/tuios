package transcriptview

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

// qwenLine decodes one chat record.
func qwenLine(r *reader, line []byte, off int64) (decoded, bool) {
	rec, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	parts := rec.obj("message")["parts"]
	var out []Entry
	switch rec.str("type") {
	case "user":
		out = genaiEntries(parts, RoleUser)
	case "assistant":
		out = genaiEntries(parts, RoleAssistant)
		for _, p := range rec.obj("message").list("parts") {
			if fc := asObj(p).obj("functionCall"); fc != nil {
				name := fc.str("name")
				out = append(out, callEntry(name, fc.str("id"), gmClass(name), args(fc["args"]))...)
			}
		}
	case "tool_result":
		res := rec.obj("toolCallResult")
		status := res.str("status")
		failed := status == "error" || status == "cancelled" || res["error"] != nil
		for _, p := range rec.obj("message").list("parts") {
			fr := asObj(p).obj("functionResponse")
			if fr == nil {
				continue
			}
			id := fr.str("id")
			if id == "" {
				id = res.str("callId")
			}
			e := resultEntry(id, responseText(fr.obj("response")), failed)
			e.raw.diff = fileDiffOf(res["resultDisplay"])
			out = append(out, e)
		}
	}
	at := parseAt(rec.str("timestamp"))
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}
