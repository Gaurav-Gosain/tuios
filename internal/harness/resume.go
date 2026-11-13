package harness

import (
	"errors"
	"slices"
	"strings"

	"tuios.dev/harness"
)

// Resuming a conversation: the command tuios types into a pane to reopen it,
// built from the manifest's [resume] block (harness.Resume).

// ErrNoResume says a harness has no [resume] block, or is not known.
var ErrNoResume = errors.New("the harness has no resume command")

// ErrBadResumeID says a session id cannot be put into a resume command.
var ErrBadResumeID = errors.New("the session id cannot be typed safely")

// CanResume reports whether the harness has a resume command.
func (r *Registry) CanResume(id string) bool {
	m := r.Lookup(id)
	return m != nil && len(m.Resume.Argv) > 0
}

// RiskyFlags returns the flags of argv, a command ResumeArgv built for
// harness id, that turn off approvals or sandboxing (Resume.KeepRisky). nil
// for none.
func (r *Registry) RiskyFlags(id string, argv []string) []string {
	m := r.Lookup(id)
	if m == nil || len(m.Resume.KeepRisky) == 0 || len(argv) <= len(m.Resume.Argv) {
		return nil
	}
	return m.Resume.Risky(argv[len(m.Resume.Argv):])
}

// KeptFlags returns the flags of argv, the command line a process of harness
// id was started with, that its manifest keeps across a resume. nil for a
// harness that keeps none, or is not known.
func (r *Registry) KeptFlags(id string, argv []string) []string {
	m := r.Lookup(id)
	if m == nil {
		return nil
	}
	return m.Resume.Keep(argv)
}

// ResumeArgv builds the command that reopens conversation sessionID of harness
// id, followed by the kept flags the harness was started with (KeptFlags).
// The flags are read through the manifest's allow list again, so a list that
// changed since they were stored is the one that applies. It fails with
// ErrNoResume for a harness without a resume command and with ErrBadResumeID
// for an id ValidResumeSessionID refuses.
func (r *Registry) ResumeArgv(id, sessionID string, flags []string) ([]string, error) {
	m := r.Lookup(id)
	if m == nil || len(m.Resume.Argv) == 0 {
		return nil, ErrNoResume
	}
	if !harness.ValidResumeSessionID(sessionID) {
		return nil, ErrBadResumeID
	}
	kept := m.Resume.Keep(flags)
	out := make([]string, len(m.Resume.Argv), len(m.Resume.Argv)+len(kept))
	for i, tok := range m.Resume.Argv {
		out[i] = strings.ReplaceAll(tok, harness.ResumePlaceholder, sessionID)
	}
	return append(out, kept...), nil
}

// ResumeCommandLine is argv as it is typed into a shell. Every token passed
// the checks above, so joining with spaces is the whole of the quoting.
func ResumeCommandLine(argv []string) string {
	return strings.Join(argv, " ")
}

// ResumedSessionID is the conversation a process of harness id was told to
// resume on its command line argv, read through the manifest's own [resume]
// argv: the token after the flag that comes before {session_id}
// ("--resume ID"), or the rest of a token that starts as the manifest's
// does ("--resume=ID"). ok is false for a fresh start, a harness without a
// resume command, or one that is not known. It is how the daemon knows
// which transcript a resumed agent writes to without a hook: the one with
// that session id, though its first record predates the process.
func (r *Registry) ResumedSessionID(id string, argv []string) (string, bool) {
	m := r.Lookup(id)
	if m == nil || len(m.Resume.Argv) == 0 {
		return "", false
	}
	for i, tok := range m.Resume.Argv {
		at := strings.Index(tok, "{session_id}")
		if at < 0 {
			continue
		}
		if at > 0 {
			// "--resume={session_id}": the same prefix on the process's token.
			prefix := tok[:at]
			for _, a := range argv {
				if v, ok := strings.CutPrefix(a, prefix); ok && v != "" {
					return v, true
				}
			}
			return "", false
		}
		if i == 0 {
			return "", false
		}
		flag := m.Resume.Argv[i-1]
		for j := 0; j+1 < len(argv); j++ {
			if argv[j] == flag && argv[j+1] != "" && !strings.HasPrefix(argv[j+1], "-") {
				return argv[j+1], true
			}
		}
		return "", false
	}
	return "", false
}

// Continues reports whether argv, the command line of a process of harness
// id, starts it on its most recent conversation in the folder without naming
// one: a token is one of the manifest's [resume] continue flags. false for a
// harness that lists none, or is not known.
func (r *Registry) Continues(id string, argv []string) bool {
	m := r.Lookup(id)
	if m == nil || len(m.Resume.Continue) == 0 {
		return false
	}
	for _, a := range argv {
		if slices.Contains(m.Resume.Continue, a) {
			return true
		}
	}
	return false
}
