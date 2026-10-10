//go:build js

package session

// The browser build has no links and no pipes on its disk, and its syscall
// package has neither flag.
const (
	oNoFollow = 0
	oNonBlock = 0
)
