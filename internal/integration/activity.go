package integration

import (
	"slices"
	"strings"
)

// Hook activity: what a hook event says the agent did, beyond the state it
// puts the pane in. It feeds the pane's activity ring in the daemon, which the
// rail's "now" line, the away recap and `tuios agent-log` read. It is display
// only, so it is read from fields a harness documents and left out when a
// field is missing, never guessed.

// toolTargetKeys are the tool_input keys that name what a tool acts on, in
// the order ToolSummary reads them, plus notebook_path for Claude Code's
// NotebookEdit.
var toolTargetKeys = []string{"command", "file_path", "filePath", "path", "url", "pattern", "query", "description", "notebook_path"}

// editTools are the Claude Code tools that write the file their tool_input
// names in file_path (notebook_path for NotebookEdit).
var editTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// applyPatchTool is Codex's file editing tool. Its tool_input carries the
// patch text in command, and the patch names each file it touches on a
// header line.
const applyPatchTool = "apply_patch"

// patchFileHeaders are the header lines of a Codex patch that name a file.
var patchFileHeaders = []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "}

// activityFilesMax bounds the files one event reports. The daemon keeps no
// more than this either.
const activityFilesMax = 16

// firstLine is s up to its first line break, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// activityText is agent text as an activity entry carries it: the first
// line, redacted and clipped.
func activityText(s string) string {
	return Clip(firstLine(s))
}

// ToolTarget is what a tool call acts on, the argument ToolSummary names:
// the command, the file, the URL or the pattern, clipped and redacted. For
// Codex's apply_patch it is the first file the patch names. It is empty when
// the input names nothing it knows.
func ToolTarget(tool string, input fields) string {
	if tool == applyPatchTool {
		if files := patchFiles(patchText(input)); len(files) > 0 {
			return Clip(files[0])
		}
		return ""
	}
	for _, key := range toolTargetKeys {
		if v := input.str(key); v != "" {
			return Clip(v)
		}
	}
	return ""
}

// toolFiles are the files a finished tool call wrote: the file an edit tool
// names, or every file an apply_patch touched. Other tools write nothing
// this can see.
func toolFiles(tool string, input fields) []string {
	switch {
	case tool == applyPatchTool:
		return patchFiles(patchText(input))
	case slices.Contains(editTools, tool):
		if f := input.first("file_path", "notebook_path"); f != "" {
			return []string{f}
		}
	}
	return nil
}

// patchText is the patch an apply_patch call carries.
func patchText(input fields) string {
	return input.first("command", "input", "patch")
}

// patchFiles reads the files a Codex patch names from its header lines, in
// order and without repeats.
func patchFiles(patch string) []string {
	var out []string
	for line := range strings.SplitSeq(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		for _, h := range patchFileHeaders {
			f, ok := strings.CutPrefix(line, h)
			if !ok {
				continue
			}
			if f = strings.TrimSpace(f); f != "" && !slices.Contains(out, f) && len(out) < activityFilesMax {
				out = append(out, f)
			}
		}
	}
	return out
}

// toolActivity is the activity of a tool event: the tool, what it acts on,
// and for a finished call the files it wrote.
func toolActivity(event string, p fields) *Activity {
	tool := p.str("tool_name")
	if tool == "" {
		return nil
	}
	input := p.obj("tool_input")
	a := &Activity{Event: event, Tool: tool, Target: ToolTarget(tool, input)}
	if event == ActivityToolDone {
		a.Files = toolFiles(tool, input)
	}
	return a
}

// exitCode reads a numeric exit_code from a tool_response object, reporting
// whether there was one.
func exitCode(p fields) (int, bool) {
	resp := p.obj("tool_response")
	for _, key := range []string{"exit_code", "exitCode"} {
		if v, ok := resp[key].(float64); ok {
			return int(v), true
		}
	}
	return 0, false
}

func boolPtr(b bool) *bool { return &b }
