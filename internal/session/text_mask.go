package session

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// localAttentionHost is the host filter that names this machine. It is the
// name the listings and the rail give it.
const localAttentionHost = "local"

// attentionSecret matches the shapes a command line leaks a credential in:
// a key=value or key: value whose key names a secret, and an Authorization
// bearer token. It is a net for the common case, not a guarantee, which is why
// the summary is also kept short.
var attentionSecret = regexp.MustCompile(`(?i)\b((?:[a-z0-9_]*(?:token|secret|password|passwd|api[_-]?key|access[_-]?key|private[_-]?key|credential)s?)\s*[=:]\s*|bearer\s+)("[^"]*"|'[^']*'|[^\s"']+)`)

// attentionSecretWords are the words attentionSecret keys on, lower case. A
// summary with none of them cannot match, and most summaries have none, so the
// regular expression only runs on the ones that might.
var attentionSecretWords = []string{"token", "secret", "passw", "key", "credential", "bearer"}

// attentionMaySecret reports whether s holds any of attentionSecretWords.
func attentionMaySecret(s string) bool {
	lower := strings.ToLower(s)
	for _, w := range attentionSecretWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// attentionText is text an agent reported, made safe to show and to keep: one
// line, no control characters, likely secrets masked, at most limit bytes.
func attentionText(s string, limit int) string {
	var b strings.Builder
	b.Grow(min(len(s), limit+8))
	space := false
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t' || r == '\r' || r == ' ':
			space = true
			continue
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
		if b.Len() > limit*4 {
			break
		}
	}
	out := b.String()
	if attentionMaySecret(out) {
		out = attentionSecret.ReplaceAllString(out, "${1}[redacted]")
	}
	if len(out) <= limit {
		return out
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return strings.TrimSpace(out[:cut])
}
