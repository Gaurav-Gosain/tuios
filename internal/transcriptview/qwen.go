package transcriptview

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
