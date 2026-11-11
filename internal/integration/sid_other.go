//go:build !unix

package integration

// selfSID is 0 where there is no session id a pane's shell leads, so only
// the immediate parent is offered.
func selfSID() int { return 0 }
