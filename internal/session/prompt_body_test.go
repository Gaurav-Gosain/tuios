package session

import (
	"strings"
	"testing"
)

// Removing the paste delimiters once rebuilt one from nested markers, and
// the text after it was read as keystrokes. The body holds no ESC at all.
func TestPromptBodyCannotRebuildAPasteDelimiter(t *testing.T) {
	for _, in := range []string{
		"x\x1b[201\x1b[200~~; touch /tmp/pwn",
		"x\x1b[20\x1b[201~1~; touch /tmp/pwn",
		"plain\x1b[201~tail",
	} {
		got := promptBody(in)
		if strings.ContainsRune(got, 0x1b) {
			t.Errorf("promptBody(%q) = %q, still holds ESC", in, got)
		}
	}
	if got := promptBody("line one\r\nline two\n\n"); got != "line one\nline two" {
		t.Errorf("promptBody kept the wrong text: %q", got)
	}
}
