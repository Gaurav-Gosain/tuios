//go:build unix || js

package applist

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"syscall"
)

func executable(info fs.FileInfo) bool {
	mode := info.Mode().Perm()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return mode&0o111 != 0
	}

	uid := os.Getuid()
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
