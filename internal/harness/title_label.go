package harness

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// TitleLabel returns the part of a harness's window title that names the agent,
// with its state marks taken off, or "" when the title says nothing a program
// name would not: it is empty, it is the program's own name or its default, or
// it only reports status, it is what a shell left there, or it is the title
// tuios gives a new pane. program is the pane's foreground command, which may be
// empty.
//
// Spinner frames of one title give one label, so the row does not move on them.
func TitleLabel(harnessID, program, title string) string {
	if harnessID == "" {
		return ""
	}
	switch harnessID {
	case "gemini-cli", "qwen", "hermes", "kiro":
		return ""
	}
	t := TrimTitleMarks(title)
	if i := strings.IndexFunc(t, isBraille); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if rest, ok := cutPrefixFold(t, "action required"); ok {
		after, ok := strings.CutPrefix(rest, " - ")
		if !ok {
			return ""
		}
		t = after
	}
	if _, ok := cutPrefixFold(t, "plugin confirmation needed"); ok {
		return ""
	}
	if harnessID == "opencode" {
		t = strings.TrimPrefix(t, "OC | ")
	}
	is := func(s string) bool { return isProgramName(s, harnessID, program) }
	if head, tail, ok := strings.Cut(t, " - "); ok {
		next, _, hasMore := strings.Cut(tail, " - ")
		switch {
		case head == "π":
			if !hasMore {
				return ""
			}
			t = next
		case is(head):
			return ""
		case is(next):
			t = head
		case isShellName(lastSegment(tail)):
			return ""
		}
	}
	if t == "" || t == "π" || is(t) || isShellName(t) || looksLikePath(t) || isPaneDefaultTitle(t) {
		return ""
	}
	if first, rest, ok := strings.Cut(t, " "); ok && is(first) && (strings.HasPrefix(rest, "-") || looksLikePath(rest)) {
		return ""
	}
	return t
}

// TrimTitleMarks takes the state marks off the front of a title: a spinner
// frame, a status glyph and any variation selector after it, or letta's
// "[ ! ]". Every frame of one spinner trims to one string. It returns a slice of
// title.
func TrimTitleMarks(title string) string {
	t := title
	for {
		t = strings.TrimLeftFunc(t, isTitleMark)
		if len(t) >= 5 && t[0] == '[' && t[1] == ' ' && t[3] == ' ' && t[4] == ']' {
			t = t[5:]
			continue
		}
		if len(t) >= 2 && t[1] == ' ' && strings.IndexByte(`/|\-`, t[0]) >= 0 {
			t = t[2:]
			continue
		}
		return strings.TrimRightFunc(t, unicode.IsSpace)
	}
}

func isTitleMark(r rune) bool {
	if r < utf8.RuneSelf {
		return r == ' ' || r == '\t'
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func isBraille(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

func isProgramName(s, harnessID, program string) bool {
	if program != "" && strings.EqualFold(s, program) {
		return true
	}
	if len(s) != len(harnessID) {
		return false
	}
	for i := range len(s) {
		a, b := s[i], harnessID[i]
		if b == '-' {
			b = ' '
		}
		if lowerASCII(a) != lowerASCII(b) {
			return false
		}
	}
	return true
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}

func lastSegment(s string) string {
	if i := strings.LastIndex(s, " - "); i >= 0 {
		return s[i+3:]
	}
	return s
}

func isPaneDefaultTitle(s string) bool {
	id, ok := strings.CutPrefix(s, "Terminal ")
	if !ok || len(id) != 8 {
		return false
	}
	for i := range len(id) {
		if !(id[i] >= '0' && id[i] <= '9' || id[i] >= 'a' && id[i] <= 'f') {
			return false
		}
	}
	return true
}

func isShellName(s string) bool {
	switch strings.TrimPrefix(s, "-") {
	case "shell", "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh", "nu", "pwsh", "elvish", "xonsh":
		return true
	}
	return false
}

func looksLikePath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	head, _, _ := strings.Cut(s, " ")
	at := strings.IndexByte(head, '@')
	return at > 0 && strings.IndexByte(head[at:], ':') > 0
}
