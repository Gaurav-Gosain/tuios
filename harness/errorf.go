package harness

import "fmt"

//go:noinline
func errorf(format string, a ...any) error {
	return fmt.Errorf(format, a...)
}
