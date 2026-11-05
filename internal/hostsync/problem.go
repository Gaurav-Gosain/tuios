package hostsync

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

// Problem is an error that says what failed, the most likely cause, and the
// fix. It renders as the tuios command's diagnosticError does, so the command
// prints it the same way.
type Problem struct {
	// What failed.
	What string
	// Cause is why it most likely happened.
	Cause string
	// Fix is the exact command to run.
	Fix string
}

func (e *Problem) Error() string {
	var b strings.Builder
	b.WriteString(e.What)
	if e.Cause != "" {
		b.WriteString("\nMost likely cause: ")
		b.WriteString(e.Cause)
	}
	if e.Fix != "" {
		b.WriteString("\nFix: ")
		b.WriteString(e.Fix)
	}
	return b.String()
}

// plainText strips control characters and invisible characters from text
// another machine wrote. It is a copy of plainText in cmd/tuios
// (agent_commands.go).
func plainText(s string) string {
	s = invisible.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// plainLine is plainText for a value that must stay on one line. It is a copy
// of plainLine in cmd/tuios (agent_commands.go).
func plainLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(plainText(s))
}
