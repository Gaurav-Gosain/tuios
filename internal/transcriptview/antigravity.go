package transcriptview

import (
	"strconv"
	"strings"
)

// An Antigravity CLI conversation (agy, read at 1.5.14), at
// ~/.gemini/antigravity-cli/brain/<conversation id>/.system_generated/logs/
// transcript_full.jsonl. Each line is one step: {step_index, source, type,
// status, created_at, ...}, where step_index counts from 0 across the
// conversation, type is USER_INPUT, PLANNER_RESPONSE, GENERIC or
// SYSTEM_MESSAGE, and status is DONE, ERROR or RUNNING. RUNNING is a command
// agy left running in the background: the result says so and gives a task
// id, no later step replaces it, and the call shows ok with that text.
//
// A USER_INPUT's content holds the person's words in <USER_REQUEST>, then
// context agy adds in tags of its own: <ADDITIONAL_METADATA> and
// <USER_SETTINGS_CHANGE>. A PLANNER_RESPONSE has whichever of content,
// thinking and tool_calls the reply has, each call {name, args} with
// PascalCase args (Path, CommandLine, Target and Replacement) and no id. A
// GENERIC step is one call's result: content, and error when status is
// ERROR. It names no call either. A response with N calls is followed by
// their N results at the next N step indexes, in call order, so here a
// call's id is the step index of its result. agy writes no ids to check that
// against, so a step it put between a reply and its results, or results
// written in the order the calls finished, would shift the statuses of the
// rest of that reply's calls. A SYSTEM_MESSAGE is a reminder agy injects
// itself, which nobody in the conversation wrote, and it does not show.
//
// transcript.jsonl beside the file holds the same steps with long values
// cut, so it is not read.

// agyLine decodes one step.
func agyLine(r *reader, line []byte, off int64) (decoded, bool) {
	rec, ok := parseObj(line)
	if !ok {
		return decoded{}, false
	}
	step, hasStep := rec["step_index"].(float64)
	// stepID is the id that pairs a call with its result: the step index n
	// steps after this one. A step with no index pairs with nothing.
	stepID := func(n int) string {
		if !hasStep {
			return ""
		}
		return "step-" + strconv.Itoa(int(step)+n)
	}
	var out []Entry
	switch rec.str("type") {
	case "USER_INPUT":
		if e, ok := textEntry(RoleUser, KindText, agyRequest(rec.str("content"))); ok {
			out = append(out, e)
		}
	case "PLANNER_RESPONSE":
		if e, ok := textEntry(RoleAssistant, KindThinking, strings.TrimSpace(rec.str("thinking"))); ok {
			out = append(out, e)
		}
		if e, ok := textEntry(RoleAssistant, KindText, strings.TrimSpace(rec.str("content"))); ok {
			out = append(out, e)
		}
		for i, c := range rec.list("tool_calls") {
			c := asObj(c)
			name := c.str("name")
			out = append(out, callEntry(name, stepID(i+1), agyClass(name), c.obj("args"))...)
		}
	case "GENERIC":
		text, failed := agyResult(rec.str("content"))
		if rec.str("status") == "ERROR" {
			failed = true
			if text == "" {
				text = rec.str("error")
			}
		}
		out = append(out, resultEntry(stepID(0), text, failed))
	}
	at := parseAt(rec.str("created_at"))
	for i := range out {
		out[i].ID, out[i].At = offID(off, i), at
	}
	return decoded{entries: out}, true
}

// agyRequest is the person's words in a USER_INPUT: the part inside the
// <USER_REQUEST> it starts with. The prompt may itself name the tags, so the
// request ends at the last </USER_REQUEST>: the blocks agy adds after it
// hold only the time and settings prose. Input that does not start with the
// tag shows whole, unless it is all one tag named in capitals and
// underscores, as agy names its own: a settings note alone does not show,
// and nor does a person's own text in one such tag.
func agyRequest(s string) string {
	if rest, ok := strings.CutPrefix(strings.TrimSpace(s), "<USER_REQUEST>"); ok {
		if end := strings.LastIndex(rest, "</USER_REQUEST>"); end >= 0 {
			rest = rest[:end]
		}
		return strings.TrimSpace(rest)
	}
	upper := func(r rune) bool { return r >= 'A' && r <= 'Z' || r == '_' }
	if oneTag(strings.TrimSpace(s), upper) {
		return ""
	}
	return s
}

// agyCut opens a result whose output was too long: agy saves the whole
// result to a file, names the file, and after agyPreview gives the result
// again with the middle of its output cut out.
const (
	agyCut     = "The tool's output was truncated due to being too long."
	agyPreview = "Here is a preview with the middle of the output truncated:\n"
)

// agyResult is a GENERIC step's content without the "Created At:" and
// "Completed At:" lines agy puts first, and whether it is a command that
// exited with a code other than 0. agy marks such a step DONE: the step ran,
// and only the text says the command failed. When agy cut the output, the
// time lines and the exit line open the preview, so they are read there,
// and agy's note stays in front of it to say the output is not whole.
func agyResult(s string) (string, bool) {
	s = agyDropTimes(s)
	note := ""
	if strings.HasPrefix(s, agyCut) {
		if head, preview, ok := strings.Cut(s, agyPreview); ok {
			note, s = head+agyPreview, agyDropTimes(preview)
		}
	}
	if rest, ok := strings.CutPrefix(s, "Command exited with code "); ok {
		code, _, _ := strings.Cut(rest, ".")
		if n, err := strconv.Atoi(code); err == nil {
			return note + s, n != 0
		}
	}
	return note + s, false
}

// agyDropTimes is s without the "Created At:" and "Completed At:" lines it
// starts with.
func agyDropTimes(s string) string {
	for {
		line, rest, _ := strings.Cut(s, "\n")
		if !strings.HasPrefix(line, "Created At: ") && !strings.HasPrefix(line, "Completed At: ") {
			return s
		}
		s = rest
	}
}

// agyClass is what an Antigravity CLI tool does.
func agyClass(name string) toolClass {
	switch name {
	case "edit_file":
		return classEdit
	case "write_file":
		return classWrite
	}
	return classOther
}
