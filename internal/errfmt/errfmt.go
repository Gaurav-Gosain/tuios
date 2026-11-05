// Package errfmt holds Errorf, which is fmt.Errorf kept out of line.
//
// Since Go 1.26, fmt.Errorf is inlined at each call site together with the
// errors.New it falls back to for a format with no verbs. That copy costs
// about 115 bytes of binary at every call, and tuios has about 1,800 calls,
// all on error paths. Calling through Errorf keeps one copy. Use it instead
// of fmt.Errorf everywhere in the tuios binary; internal/lint checks that.
package errfmt

import "fmt"

// Errorf is fmt.Errorf: it formats the error the same way, and wraps each %w
// operand the same way.
//
//go:noinline
func Errorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}
