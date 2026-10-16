//go:build windows || js

package session

// readGate is absent where a pane's master is not a descriptor to poll: a
// ConPTY and a guest pty. Such a pane cannot be quiesced.
type readGate struct{}

func newReadGate(paneIO) *readGate   { return nil }
func (g *readGate) wait(paneIO) bool { return true }
func (g *readGate) wake()            {}
func (g *readGate) close()           {}
