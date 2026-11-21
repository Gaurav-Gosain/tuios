package integration

import (
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/oneline"
)

var toolTargetKeys = []string{"command", "file_path", "filePath", "path", "url", "pattern", "query", "description", "notebook_path"}

var editTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

const applyPatchTool = "apply_patch"

var patchFileHeaders = []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "}

const activityFilesMax = 16

// FirstLine is s up to its first line break, trimmed.
func FirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// SaidLine is the line of what an agent said that a one-line preview shows:
// the first line with words in it, its inline Markdown taken off (backticks,
// stars, a heading's #, a list's mark). A line that ends with a colon leads
// into what follows it, "The command printed one line:", so the next line goes
// on after it, the first line of a code block when that is what follows.
func SaidLine(s string) string {
	var lines []string
	fence := false
	for l := range strings.SplitSeq(s, "\n") {
		l = strings.TrimSpace(strings.TrimRight(l, "\r"))
		if strings.HasPrefix(l, "```") || strings.HasPrefix(l, "~~~") {
			fence = !fence
			continue
		}
		if l = plainLine(l, fence); l != "" {
			lines = append(lines, l)
		}
		if len(lines) == 2 {
			break
		}
	}
	switch {
	case len(lines) == 0:
		return ""
	case len(lines) == 2 && strings.HasSuffix(lines[0], ":"):
		return lines[0] + " " + lines[1]
	}
	return lines[0]
}

func plainLine(l string, code bool) string {
	if code {
		return l
	}
	l = strings.TrimLeft(l, "#>")
	for _, mark := range []string{"- ", "* ", "+ "} {
		l = strings.TrimPrefix(strings.TrimSpace(l), mark)
	}
	l = strings.NewReplacer("`", "", "**", "", "__", "").Replace(l)
	return strings.TrimSpace(l)
}

func activityText(s string) string {
	return oneline.Clip(FirstLine(s))
}

// ToolTarget is what a tool call acts on, the argument ToolSummary names:
// the command, the file, the URL or the pattern, clipped and redacted. For
// Codex's apply_patch it is the first file the patch names. It is empty when
// the input names nothing it knows.
func ToolTarget(tool string, input fields) string {
	if tool == applyPatchTool {
		if files := patchFiles(patchText(input)); len(files) > 0 {
			return oneline.Clip(files[0])
		}
		return ""
	}
	for _, key := range toolTargetKeys {
		if v := input.str(key); v != "" {
			return oneline.Clip(v)
		}
	}
	return ""
}

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

func patchText(input fields) string {
	return input.first("command", "input", "patch")
}

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

func exitCode(p fields) (int, bool) {
	resp := p.obj("tool_response")
	for _, key := range []string{"exit_code", "exitCode"} {
		if v, ok := resp[key].(float64); ok {
			return int(v), true
		}
	}
	return 0, false
}
