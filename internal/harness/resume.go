package harness

import (
	"errors"
	"fmt"
	"strings"
)

// Resume is how a conversation of this harness is reopened in a new process:
// the command line that continues the conversation a hook named, with
// {session_id} where the id goes. Claude Code's is
//
//	argv = ["claude", "--resume", "{session_id}"]
//
// It is what brings an agent's conversation back after a daemon restart. The
// process that held the conversation does not survive the restart, so this
// starts a new one on the same conversation.
//
// The command is typed into the pane's shell, so every token is held to a
// character set that means the same thing, unquoted, to every shell tuios runs
// (sh, bash, zsh, fish, PowerShell and cmd): letters, digits and _ . / : = + -.
// A token that needs quoting is refused when the manifest loads, and an id that
// does not fit is refused when the command is built. That is what keeps a
// reported id from ever being read by a shell as anything but one argument.
type Resume struct {
	// Argv is the command line, one token per element. At least one token must
	// hold {session_id}; it may be a whole token or part of one, as in
	// "--resume={session_id}".
	Argv []string `toml:"argv"`
	// Source is where the command came from, in words.
	Source string `toml:"source"`
}

// ResumePlaceholder is the token part a resume command's session id replaces.
const ResumePlaceholder = "{session_id}"

// maxResumeTokens bounds a resume command. Every real one is two or three
// tokens; the bound only stops a manifest from typing a paragraph.
const maxResumeTokens = 16

// MaxResumeSessionID bounds an id a resume command takes. It matches the limit
// set-agent-state and set-agent-session put on a reported id.
const MaxResumeSessionID = 256

// resumeTokenByte reports whether b may appear in a resume token as typed.
func resumeTokenByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	return strings.IndexByte("_./:=+-", b) >= 0
}

// safeResumeToken reports whether s is one token every supported shell reads
// as itself, unquoted.
func safeResumeToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !resumeTokenByte(s[i]) {
			return false
		}
	}
	return true
}

// ValidResumeSessionID reports whether id can be put into a resume command:
// non-empty, at most MaxResumeSessionID bytes, not starting with "-" (which a
// harness would read as a flag), and only the characters a token may hold,
// without "=" and "+". Every harness tuios resumes uses a uuid or a short
// token, which fits.
func ValidResumeSessionID(id string) bool {
	if id == "" || len(id) > MaxResumeSessionID || id[0] == '-' {
		return false
	}
	if strings.ContainsAny(id, "=+") {
		return false
	}
	return safeResumeToken(id)
}

// normalize checks the block. An empty block is valid and means the harness
// cannot be resumed.
func (r *Resume) normalize() error {
	r.Source = strings.TrimSpace(r.Source)
	if len(r.Argv) == 0 {
		return nil
	}
	if len(r.Argv) > maxResumeTokens {
		return fmt.Errorf("argv has %d tokens, limit %d", len(r.Argv), maxResumeTokens)
	}
	if strings.Contains(r.Argv[0], ResumePlaceholder) {
		return errors.New("argv[0] is the program and cannot hold " + ResumePlaceholder)
	}
	holds := false
	for i, tok := range r.Argv {
		if strings.Contains(tok, ResumePlaceholder) {
			holds = true
		}
		rest := strings.ReplaceAll(tok, ResumePlaceholder, "")
		if tok != "" && rest == "" {
			continue
		}
		if !safeResumeToken(rest) {
			return fmt.Errorf("argv token %d %q: a token may hold only letters, digits, _ . / : = + - and %s, so every shell reads it unquoted", i, tok, ResumePlaceholder)
		}
	}
	if !holds {
		return errors.New("argv does not hold " + ResumePlaceholder + ", so it would not name the conversation")
	}
	return nil
}

// ErrNoResume says a harness has no [resume] block, or is not known.
var ErrNoResume = errors.New("the harness has no resume command")

// ErrBadResumeID says a session id cannot be put into a resume command.
var ErrBadResumeID = errors.New("the session id cannot be typed safely")

// CanResume reports whether the harness has a resume command.
func (r *Registry) CanResume(id string) bool {
	m := r.Lookup(id)
	return m != nil && len(m.Resume.Argv) > 0
}

// ResumeArgv builds the command that reopens conversation sessionID of harness
// id. It fails with ErrNoResume for a harness without a resume command and
// with ErrBadResumeID for an id ValidResumeSessionID refuses.
func (r *Registry) ResumeArgv(id, sessionID string) ([]string, error) {
	m := r.Lookup(id)
	if m == nil || len(m.Resume.Argv) == 0 {
		return nil, ErrNoResume
	}
	if !ValidResumeSessionID(sessionID) {
		return nil, ErrBadResumeID
	}
	out := make([]string, len(m.Resume.Argv))
	for i, tok := range m.Resume.Argv {
		out[i] = strings.ReplaceAll(tok, ResumePlaceholder, sessionID)
	}
	return out, nil
}

// ResumeCommandLine is argv as it is typed into a shell. Every token passed
// the checks above, so joining with spaces is the whole of the quoting.
func ResumeCommandLine(argv []string) string {
	return strings.Join(argv, " ")
}
