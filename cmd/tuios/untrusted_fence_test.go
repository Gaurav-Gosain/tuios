package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// TestUntrustedFenceMatchesTheClient pins the CLI's fence to the one the mail
// overlay draws. The two are read by the same people and agents, and a fence
// that reads one way in a pane and another in the overlay is two conventions.
func TestUntrustedFenceMatchesTheClient(t *testing.T) {
	if untrustedOpen != session.UntrustedOpen {
		t.Errorf("CLI open fence %q, client %q", untrustedOpen, session.UntrustedOpen)
	}
	if untrustedClose != session.UntrustedClose {
		t.Errorf("CLI close fence %q, client %q", untrustedClose, session.UntrustedClose)
	}
}
