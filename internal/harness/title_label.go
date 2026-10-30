package harness

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// What a pane's title says about which agent it is, as opposed to what state
// the agent is in. title.go reads the state out of a title; this reads the name.
//
// The rail asks on every polled tick, once per pane, so this file does not
// allocate and does not look at the registry: a user's own manifest gets the
// shape rules below with no entry here.
//
// The shapes, measured on the installed version where it says so and otherwise
// taken from the manifest's [title] comment:
//
//	claude-code  "✳ Claude Code" at start. "✳ name" or "◐ name" after claude -n
//	             or /rename (2.1.314). "⠂ topic" during a turn on older releases.
//	opencode     "OpenCode", then "OC | <session title>" (from the binary)
//	pi           "π - <folder>", "π - <session> - <folder>" (folder measured)
//	crush        "crush <folder>" (measured)
//	amp          "<thread> - amp - <folder>" at rest, "⠹ <thread>" in a turn
//	grok         "grok", "<session> - grok", "<session> ⠧ <status>"
//	codex        "codex", "⠋ codex", "Action Required - codex"
//	letta        "letta", "⠹ letta", "[ ! ] Action Required | letta"
//
// Four harnesses write only status there, never a name, so nothing they write
// is a label: Gemini CLI ("✦ <current thought> (<folder>)", replaced at each
// turn), Qwen Code, Hermes ("⏳ hermes: reading files") and Kiro ("◐ kiro:
// fixing the build"). Taking those would move the row on every thought.

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
	// A spinner inside the title divides the name from the status: grok's
	// "review ⠧ Running tests".
	if i := strings.IndexFunc(t, isBraille); i >= 0 {
		t = strings.TrimSpace(t[:i])
	}
	if rest, ok := cutPrefixFold(t, "action required"); ok {
		// grok keeps the session after the phrase, so a waiting row reads the
		// same as a working one. Anything else after it is more status.
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
			// Pi's own mark, then the folder, with a session name between them
			// when the session has one.
			if !hasMore {
				return ""
			}
			t = next
		case is(head):
			return ""
		case is(next):
			t = head
		case isShellName(lastSegment(tail)):
			// "<cwd> - <shell>": a shell's title the agent has not replaced.
			return ""
		}
	}
	if t == "" || t == "π" || is(t) || isShellName(t) || looksLikePath(t) || isPaneDefaultTitle(t) {
		return ""
	}
	// "crush ~/src/app" and "claude -n name", which is how a shell's preexec
	// title reads while a program that sets none runs.
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
		// "[ ! ] " and "[ . ] ".
		if len(t) >= 5 && t[0] == '[' && t[1] == ' ' && t[3] == ' ' && t[4] == ']' {
			t = t[5:]
			continue
		}
		// An ASCII spinner frame, which has to stand alone: "/usr" is a path.
		if len(t) >= 2 && t[1] == ' ' && strings.IndexByte(`/|\-`, t[0]) >= 0 {
			t = t[2:]
			continue
		}
		return strings.TrimRightFunc(t, unicode.IsSpace)
	}
}

// isTitleMark is a rune no name begins with. ASCII stays out of it, because "~"
// is a symbol and "(" and "#" begin real titles. So do letters of any script,
// which keeps "π" for the check that knows what it means.
func isTitleMark(r rune) bool {
	if r < utf8.RuneSelf {
		return r == ' ' || r == '\t'
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func isBraille(r rune) bool { return r >= 0x2800 && r <= 0x28FF }

// isProgramName reports whether s is only the agent's own name: the program in
// the pane, the harness id, or the id spelled as words, as "Claude Code" spells
// claude-code and "OpenCode" spells opencode.
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

// cutPrefixFold is strings.CutPrefix ignoring ASCII case.
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

// isPaneDefaultTitle is the title a pane has until its program sets one:
// "Terminal 8bf1c038", the start of the window id. A program that sets no title
// leaves this in place for as long as it runs.
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

// isShellName also takes "shell", which is what the daemon's listing says for a
// pane with no title.
func isShellName(s string) bool {
	switch strings.TrimPrefix(s, "-") {
	case "shell", "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh", "nu", "pwsh", "elvish", "xonsh":
		return true
	}
	return false
}

// looksLikePath is a title that is a directory, with or without bash's
// "user@host:" in front of it.
func looksLikePath(s string) bool {
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	head, _, _ := strings.Cut(s, " ")
	at := strings.IndexByte(head, '@')
	return at > 0 && strings.IndexByte(head[at:], ':') > 0
}
