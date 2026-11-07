package integration

import "github.com/Gaurav-Gosain/tuios/internal/oneline"

// ToolSummary names a tool call the way a person reads an approval prompt: the
// tool, then the one argument that says what it will do. The keys are the ones
// Claude Code's tool_input carries (command for Bash, file_path for the file
// tools, url for WebFetch, pattern for the search tools); Codex hands the same
// shape, and opencode's tools name the file filePath. An unknown tool is
// named alone rather than dumping its input.
func ToolSummary(tool string, input fields) string {
	if tool == "" {
		tool = "a tool call"
	}
	for _, key := range []string{"command", "file_path", "filePath", "path", "url", "pattern", "query", "description"} {
		if v := input.str(key); v != "" {
			return oneline.Clip(tool + ": " + v)
		}
	}
	return oneline.Clip(tool)
}
