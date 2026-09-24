package integration

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxMessage is the longest message a hook reports, in runes. The message is
// a one-line reason on a rail row and in an alert, not a log.
const MaxMessage = 100

// secretPatterns are the shapes of a secret most likely to sit on a command
// line a harness asks approval for. The message leaves the pane: it is synced
// to every attached client, shown in alerts, passed to after-agent-state hooks
// and read over links. So a value that looks like a credential is replaced
// before it goes anywhere.
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	// NAME=value where the name says what it is.
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIALS?|AUTH)[A-Z0-9_]*)=\S+`), "$1=***"},
	// --token value, --password=value and the like.
	{regexp.MustCompile(`(?i)(--?(?:token|password|passwd|secret|api-?key|auth)[= ])\S+`), "$1***"},
	// Authorization headers.
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 ***"},
	// Credentials in a URL.
	{regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`), "://***@"},
}

// longRun is a candidate key: a long run with no separator a path or a
// sentence would have. keyLike decides whether it is one.
var longRun = regexp.MustCompile(`[A-Za-z0-9+=_-]{32,}`)

// keyLike reports whether a long run reads as a key rather than a name: it
// mixes digits with letters, the way a token, a hash or a base64 blob does and
// a file name built of words does not.
func keyLike(s string) bool {
	return strings.ContainsAny(s, "0123456789") &&
		strings.IndexFunc(s, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) >= 0
}

// Redact replaces what looks like a secret in s.
func Redact(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return longRun.ReplaceAllStringFunc(s, func(run string) string {
		if keyLike(run) {
			return "***"
		}
		return run
	})
}

// Clip makes s fit a one-line message: secrets redacted, whitespace collapsed
// to single spaces, and at most MaxMessage runes, ending in "..." when cut.
func Clip(s string) string {
	s = strings.Join(strings.Fields(Redact(s)), " ")
	if utf8.RuneCountInString(s) <= MaxMessage {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:MaxMessage-3])) + "..."
}

// Clipped reports whether s reads as a line Clip cut short: as long as Clip
// leaves one and ending in the dots it adds. A line that happens to end so is
// read as clipped too, which errs toward reading less into it.
func Clipped(s string) bool {
	return strings.HasSuffix(s, "...") && utf8.RuneCountInString(s) >= MaxMessage-1
}

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
			return Clip(tool + ": " + v)
		}
	}
	return Clip(tool)
}
