package transcriptview

import "strings"

// The live activity row: one line at the bottom of a thread that says what
// the agent does now, between the transcript's writes. A transcript gets a
// tool call when the harness writes it, which can be long after the call
// started, so a thread drawn from the transcript alone shows nothing while a
// test suite runs. The row fills that gap from what the client already has:
// the daemon's "now" metadata key (a hook's or a protocol pane's current
// tool), or a program's OSC 7501 message. These functions are the model part,
// shared by the TUI and guios. Each client draws the row its own way.

// Activity kinds: the verb the row starts with.
const (
	ActivityRunning   = "Running"
	ActivityReading   = "Reading"
	ActivityEditing   = "Editing"
	ActivitySearching = "Searching"
	ActivityWorking   = "Working"
)

// ActivityKind is the verb for a tool, from its name as a hook, a protocol
// pane or a transcript names it. Claude Code's names come first, then those
// of Codex, Gemini CLI, Qwen Code, Copilot CLI, Amp, Cursor and the ACP
// agents. A tool it does not know is ActivityWorking.
func ActivityKind(tool string) string {
	switch strings.ToLower(tool) {
	case "bash", "shell", "exec_command", "local_shell", "shell_command", "run_shell_command",
		"run_terminal_cmd", "terminal", "execute", "exec":
		return ActivityRunning
	case "read", "notebookread", "ls", "read_file", "read_many_files", "list_directory", "view",
		"view_image", "webfetch", "web_fetch", "read_web_page", "fetch":
		return ActivityReading
	case "edit", "write", "multiedit", "notebookedit", "apply_patch", "write_file", "replace",
		"edit_file", "create", "str_replace", "str_replace_based_edit_tool", "delete_file":
		return ActivityEditing
	case "grep", "glob", "websearch", "grep_search", "search_file_content", "google_web_search",
		"web_search", "codebase_search", "file_search", "find", "search":
		return ActivitySearching
	}
	return ActivityWorking
}

// ActivityRow is the row's text for a tool and what it acts on, as the "now"
// key carries them ("Bash" and "go test ./..."): "Running go test ./...",
// "Reading main.go", "Editing main.go", "Searching TODO". A tool it does not
// know reads "Working: WebFetch https://...", and no tool with a target, such
// as a program's own message, reads "Working: Compiling". With neither it is
// "Working". The text is not clipped: the client fits it to its width.
func ActivityRow(tool, target string) string {
	tool, target = strings.TrimSpace(tool), strings.TrimSpace(target)
	kind := ActivityKind(tool)
	switch {
	case kind != ActivityWorking && target != "":
		return kind + " " + target
	case kind != ActivityWorking:
		// "Running" alone says less than the tool's own name.
		return kind + " " + tool
	case tool != "" && target != "":
		return ActivityWorking + ": " + tool + " " + target
	case tool != "" || target != "":
		return ActivityWorking + ": " + tool + target
	}
	return ActivityWorking
}

// ActivityShown reports whether the row shows. It shows only while the agent
// works (state "working"), so it goes when the agent rests or needs the
// person, and only when there is a tool or a target to name. lastCall is the
// transcript's newest tool call, nil when it has none. When that call is the
// one the row names, the transcript has caught up, its card shows the call,
// and the row goes: the row never repeats a card.
//
// A call is the same when the tool names match and the targets agree, or when
// both targets are set and agree, since a hook and a transcript may name one
// tool two ways. Targets agree when one starts with the other, because each
// side cuts a long target to its own bound. A command run twice in a row
// reads as caught up until the next tool starts: the newest call is all the
// row can compare with.
func ActivityShown(state, tool, target string, lastCall *Entry) bool {
	if state != "working" || (strings.TrimSpace(tool) == "" && strings.TrimSpace(target) == "") {
		return false
	}
	if lastCall == nil || lastCall.Kind != KindToolCall {
		return true
	}
	a, b := activityTarget(target), activityTarget(lastCall.Target)
	sameTarget := a == b
	if a != "" && b != "" {
		n := min(len(a), len(b))
		sameTarget = a[:n] == b[:n]
	}
	if !sameTarget {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(tool), strings.TrimSpace(lastCall.Tool)) && (a == "" || b == "")
}

// activityTarget is a target as the two sides compare it: trimmed, and
// without the mark a cut leaves at its end.
func activityTarget(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "…")
	s = strings.TrimSuffix(s, "...")
	return strings.TrimSpace(s)
}
