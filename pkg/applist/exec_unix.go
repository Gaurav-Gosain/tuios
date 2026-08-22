//go:build unix

package applist

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"syscall"
)

// executable reports whether the calling process may exec the file.
//
// Testing the whole 0111 mask would offer programs that only root can run, and
// a launcher row that reliably fails is worse than one that is not there. The
// owner and group bits are therefore weighed against this process's own ids.
func executable(info fs.FileInfo) bool {
	mode := info.Mode().Perm()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// An unfamiliar FileInfo implementation is not worth failing closed
		// over; fall back to "executable by somebody".
		return mode&0o111 != 0
	}

	uid := os.Getuid()
	// Root's exec check is the union of the bits, not the owner's alone.
	if uid == 0 {
		return mode&0o111 != 0
	}
	if uint32(uid) == st.Uid {
		return mode&0o100 != 0
	}
	if uint32(os.Getgid()) == st.Gid {
		return mode&0o010 != 0
	}
	if groups, err := os.Getgroups(); err == nil && slices.Contains(groups, int(st.Gid)) {
		return mode&0o010 != 0
	}
	return mode&0o001 != 0
}

// Argv is the argv that runs e as a pane's own process.
//
// An entry carrying an Exec is run by it verbatim. Its Path is the .desktop
// file, which is not a program, and its arguments are part of what the entry
// means: dropping them runs something else. Otherwise the listed path is
// directly executable on unix, so it is the whole command.
func (e Entry) Argv() []string {
	if len(e.Exec) > 0 {
		// Copied because the entry is held in a cached list that outlives this
		// call, and a caller that trims or rewrites the argv it is given would
		// otherwise be editing the list.
		return append([]string(nil), e.Exec...)
	}
	return []string{e.Path}
}

// CommandLine is e written the way the user would have typed it, for a launcher
// that puts the command on a shell's prompt instead of running it.
//
// The bare name is preferred over the listed path because it is shorter, it is
// what the user recognises, and it resolves to the same file: Scan applies the
// shell's own $PATH rule, so the name that won here is the name the shell wins
// with. A name a shell would re-read (a space, a quote, a glob character) is
// not worth guessing at, so those fall back to the single-quoted absolute path.
//
// An entry carrying an Exec has no such short form. There is no name a shell
// would resolve to that argv, so the line is the argv itself, quoted element by
// element so the shell re-reads exactly the words the entry asked for.
func (e Entry) CommandLine() string {
	if len(e.Exec) > 0 {
		return shellLine(e.Exec)
	}
	if shellSafe(e.Name) {
		return e.Name
	}
	return shellQuote(e.Path)
}

// shellLine writes an argv as one POSIX shell command line, quoting every
// element that would not mean itself.
func shellLine(argv []string) string {
	var b strings.Builder
	for i, arg := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if shellSafe(arg) {
			b.WriteString(arg)
			continue
		}
		b.WriteString(shellQuote(arg))
	}
	return b.String()
}

// shellSafe reports whether s means itself to a POSIX shell.
//
// The set is allow-listed rather than deny-listed, so a character nobody
// thought about gets quoted, which is merely ugly, instead of being handed to
// the shell to interpret.
func shellSafe(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return true
}

// shellQuote wraps s so a POSIX shell reads it as one literal word. The only
// character a single-quoted string cannot hold is the single quote itself,
// which is closed, escaped and reopened.
func shellQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for i := range len(s) {
		if s[i] == '\'' {
			b.WriteString(`'\''`)
			continue
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('\'')
	return b.String()
}
