//go:build windows || js

package session

type readGate struct{}

func newReadGate(paneIO) *readGate            { return nil }
func (g *readGate) wait(paneIO) (bool, error) { return true, nil }
func (g *readGate) wake()                     {}
func (g *readGate) close()                    {}
