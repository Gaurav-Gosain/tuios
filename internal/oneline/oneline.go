// Package oneline makes a string fit a one-line message that leaves a pane:
// an agent's reason on a rail row, an approval line, an alert. It redacts what
// looks like a secret, collapses white space and clips to MaxMessage runes.
package oneline

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/lazyre"
)

// MaxMessage is the longest message a hook reports, in runes. The message is
// a one-line reason on a rail row and in an alert, not a log.
const MaxMessage = 100

var secretPatterns = sync.OnceValue(func() []secretPattern {
	return []secretPattern{
		{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIALS?|AUTH)[A-Z0-9_]*)=\S+`), "$1=***"},
		{regexp.MustCompile(`(?i)(--?(?:token|password|passwd|secret|api-?key|auth)[= ])\S+`), "$1***"},
		{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 ***"},
		{regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`), "://***@"},
	}
})

type secretPattern struct {
	re   *regexp.Regexp
	repl string
}

var longRun = lazyre.New(`[A-Za-z0-9+=_-]{32,}`)

func keyLike(s string) bool {
	return strings.ContainsAny(s, "0123456789") &&
		strings.IndexFunc(s, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) >= 0
}

// Redact replaces what looks like a secret in s.
func Redact(s string) string {
	for _, p := range secretPatterns() {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return longRun().ReplaceAllStringFunc(s, func(run string) string {
		if keyLike(run) {
			return "***"
		}
		return run
	})
}

// Clip makes s fit a one-line message: secrets redacted, whitespace collapsed
// to single spaces, and at most MaxMessage runes, ending in "..." when cut.
// The cut falls at a space when there is one in the last third, so no word
// is left half shown.
func Clip(s string) string {
	s = strings.Join(strings.Fields(Redact(s)), " ")
	if utf8.RuneCountInString(s) <= MaxMessage {
		return s
	}
	r := []rune(s)[:MaxMessage-3]
	if next := []rune(s)[MaxMessage-3]; next != ' ' {
		for i := len(r) - 1; i >= len(r)*2/3; i-- {
			if r[i] == ' ' {
				r = r[:i]
				break
			}
		}
	}
	return strings.TrimRight(strings.TrimSpace(string(r)), ",;:") + "..."
}

// Clipped reports whether s reads as a line Clip cut short: as long as Clip
// leaves one and ending in the dots it adds. A line that happens to end so is
// read as clipped too, which errs toward reading less into it.
func Clipped(s string) bool {
	return strings.HasSuffix(s, "...") && utf8.RuneCountInString(s) >= MaxMessage-1
}
