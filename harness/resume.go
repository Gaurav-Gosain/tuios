package harness

import (
	"errors"
	"slices"
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

	// KeepFlags and KeepOptions are the flags of the command line the person
	// started the harness with that a resume carries over: a flag on its own
	// (--dangerously-skip-permissions), and an option that takes one value
	// (--model, --permission-mode). Everything else on that command line is
	// left behind, a prompt, a one-shot flag such as --print and the
	// harness's own resume flag included. An allow list rather than a deny
	// list, because a flag tuios does not know may start a second
	// conversation or run a prompt again.
	KeepFlags   []string `toml:"keep_flags"`
	KeepOptions []string `toml:"keep_options"`
	// KeepRisky marks the kept flags that turn off approvals or sandboxing:
	// a name from KeepFlags, a name from KeepOptions (any value), or
	// name=value for one value of an option (--permission-mode=
	// bypassPermissions). A resume that carries one names it wherever the
	// command is shown, and is never typed without the person.
	KeepRisky []string `toml:"keep_risky"`
	// KeepSource is where the flag names came from, in words.
	KeepSource string `toml:"keep_source"`
	// Continue are the flags that start the harness on its most recent
	// conversation in the folder without naming it (claude --continue). The
	// daemon reads them off an agent's command line to find the transcript a
	// hookless agent goes on writing to (see Registry.Continues).
	Continue []string `toml:"continue"`
}

// ResumePlaceholder is the token part a resume command's session id replaces.
const ResumePlaceholder = "{session_id}"

// maxResumeTokens bounds a resume command. Every real one is two or three
// tokens; the bound only stops a manifest from typing a paragraph.
const maxResumeTokens = 16

// maxKeptTokens bounds the flags a resume carries over, values included, and
// maxKeepNames the names a manifest may list.
const (
	maxKeptTokens = 16
	maxKeepNames  = 32
)

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
		return errorf("argv has %d tokens, limit %d", len(r.Argv), maxResumeTokens)
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
			return errorf("argv token %d %q: a token may hold only letters, digits, _ . / : = + - and %s, so every shell reads it unquoted", i, tok, ResumePlaceholder)
		}
	}
	if !holds {
		return errors.New("argv does not hold " + ResumePlaceholder + ", so it would not name the conversation")
	}
	r.KeepSource = strings.TrimSpace(r.KeepSource)
	if n := len(r.KeepFlags) + len(r.KeepOptions); n > maxKeepNames {
		return errorf("keep_flags and keep_options name %d flags, limit %d", n, maxKeepNames)
	}
	seen := map[string]bool{}
	for _, name := range slices.Concat(r.KeepFlags, r.KeepOptions) {
		if err := r.checkKeepName(name, seen); err != nil {
			return err
		}
	}
	for _, mark := range r.KeepRisky {
		name, value, withValue := strings.Cut(mark, "=")
		switch {
		case withValue && (!slices.Contains(r.KeepOptions, name) || !keptValue(value)):
			return errorf("keep_risky %q: %s is not in keep_options, or the value is not one plain token", mark, name)
		case !withValue && !seen[name]:
			return errorf("keep_risky %q is not in keep_flags or keep_options", mark)
		}
	}
	return nil
}

// checkKeepName refuses a kept flag name that is not one plain flag, that is
// listed twice, or that the resume command itself spells: a kept --resume
// would name a second conversation.
func (r *Resume) checkKeepName(name string, seen map[string]bool) error {
	if len(name) < 2 || name[0] != '-' || name == "--" || strings.Contains(name, "=") || !safeResumeToken(name) {
		return errorf("kept flag %q: a flag is - or -- and then letters, digits and _ . / : + -", name)
	}
	if seen[name] {
		return errorf("kept flag %q is listed twice", name)
	}
	seen[name] = true
	for _, tok := range r.Argv {
		if flag, _, _ := strings.Cut(tok, "="); flag == name {
			return errorf("kept flag %q is the resume command's own flag", name)
		}
	}
	return nil
}

// keptValue reports whether v can follow a kept option: one token every shell
// reads as itself, not starting with "-", so it cannot be read as a flag.
func keptValue(v string) bool {
	return v != "" && len(v) <= MaxResumeSessionID && v[0] != '-' && safeResumeToken(v)
}

// Keep returns the flags of argv this block carries over, in order, each
// option as two tokens (--model opus, whether it was written --model=opus or
// not). An option whose value a shell would not read as itself is left out.
// Reading stops at "--", after which everything is an argument. nil when
// nothing is kept.
func (r *Resume) Keep(argv []string) []string {
	if len(r.KeepFlags) == 0 && len(r.KeepOptions) == 0 {
		return nil
	}
	var out []string
	for i := 0; i < len(argv) && len(out) < maxKeptTokens; i++ {
		tok := argv[i]
		if tok == "--" {
			break
		}
		if len(tok) < 2 || tok[0] != '-' {
			continue
		}
		if slices.Contains(r.KeepFlags, tok) {
			out = append(out, tok)
			continue
		}
		name, value, inline := strings.Cut(tok, "=")
		if !slices.Contains(r.KeepOptions, name) {
			continue
		}
		if !inline {
			if i+1 >= len(argv) {
				break
			}
			i++
			value = argv[i]
		}
		if keptValue(value) && len(out)+2 <= maxKeptTokens {
			out = append(out, name, value)
		}
	}
	return out
}

// Risky returns the flags of kept, the output of Keep, that KeepRisky marks,
// each as it is typed: "--yolo" or "--sandbox danger-full-access".
func (r *Resume) Risky(kept []string) []string {
	var out []string
	for i := 0; i < len(kept); i++ {
		name, show := kept[i], kept[i]
		if slices.Contains(r.KeepOptions, name) && i+1 < len(kept) {
			i++
			show += " " + kept[i]
			if slices.Contains(r.KeepRisky, name+"="+kept[i]) {
				out = append(out, show)
				continue
			}
		}
		if slices.Contains(r.KeepRisky, name) {
			out = append(out, show)
		}
	}
	return out
}
