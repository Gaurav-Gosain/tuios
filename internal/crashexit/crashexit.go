// Package crashexit ends the process with the terminal put back when a
// goroutine that tuios started panics.
//
// Bubble Tea recovers a panic in Update, View and commands, and restores the
// terminal. A panic on a goroutine that tuios started itself is recovered by
// nothing, so the process ends with mouse tracking, focus reports, bracketed
// paste and the alternate screen still on, and the shell after it receives
// mouse reports as typed text. A long-lived goroutine that works for the
// client defers Recover, and the client installs its terminal teardown with
// SetTeardown while Bubble Tea runs.
//
// Where no teardown is installed (the daemon, the SSH server, tests), Recover
// panics again with the same value, so the process fails as it did before.
//
// A fatal runtime error, such as a signal or a concurrent map write, cannot be
// recovered, and this does not cover it.
package crashexit

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
)

var (
	teardown atomic.Pointer[func()]
	exiting  sync.Mutex
)

// SetTeardown installs what puts the terminal back before the process exits
// on a panic. nil removes it.
func SetTeardown(f func()) {
	if f == nil {
		teardown.Store(nil)
		return
	}
	teardown.Store(&f)
}

// Recover is deferred at the top of a goroutine: defer crashexit.Recover().
// On a panic it runs the teardown, prints the panic and its stack on stderr,
// and exits with status 2, as an unrecovered panic does.
func Recover() {
	r := recover()
	if r == nil {
		return
	}
	f := teardown.Load()
	if f == nil {
		panic(r)
	}
	stack := debug.Stack()
	// A second goroutine that panics at the same time waits here until the
	// first one has ended the process.
	exiting.Lock()
	(*f)()
	fmt.Fprintf(os.Stderr, "panic: %v\n\n%s", r, stack)
	os.Exit(2)
}
