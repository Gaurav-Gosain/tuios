package harness

import "fmt"

// errorf is fmt.Errorf kept out of line. Since Go 1.26 fmt.Errorf is inlined
// at each call site, which costs a program that links this package about 115
// bytes per call, and every call here is on an error path.
//
//go:noinline
func errorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}
