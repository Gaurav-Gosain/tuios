package server

import "testing"

// Which session an SSH connection that named none lands in.
//
// The reported fault is that it depended on the order the daemon listed its
// sessions in: two operators on one server could be put in different sessions
// from one another, or in a colleague's, and nothing on screen said which or
// why. That order is not part of any contract either, so the same connection
// could answer differently after a session was made or removed elsewhere.

// TestSeveralSessionsAreNotAnOrdering is the report.
//
// Negative control: returning available[0] for the several case fails here,
// answering "alpha".
func TestSeveralSessionsAreNotAnOrdering(t *testing.T) {
	got := chooseSSHSession([]string{"alpha", "bravo", "charlie"})
	if got != DefaultSSHSessionName {
		t.Errorf("a connection that named no session got %q, one of somebody's sessions", got)
	}
}

// TestTheAnswerDoesNotMoveWithTheListing. The same connection to the same
// machine has to get the same session, whatever order the daemon happens to
// return and whatever anyone else created in the meantime.
func TestTheAnswerDoesNotMoveWithTheListing(t *testing.T) {
	first := chooseSSHSession([]string{"alpha", "bravo"})
	for _, listing := range [][]string{
		{"bravo", "alpha"},
		{"alpha", "bravo", "charlie"},
		{"zulu", "alpha", "bravo"},
	} {
		if got := chooseSSHSession(listing); got != first {
			t.Errorf("listing %v answered %q, but %v answered %q", listing, got, []string{"alpha", "bravo"}, first)
		}
	}
}

// TestOneSessionIsNotAChoice. With a single session there is no ambiguity to
// protect anyone from, and sending a connection to a fresh session instead
// would be the surprising answer on the server that has exactly one.
func TestOneSessionIsNotAChoice(t *testing.T) {
	if got := chooseSSHSession([]string{"work"}); got != "work" {
		t.Errorf("a server with one session answered %q, want work", got)
	}
}

// TestNoSessionsGetsTheDefault, which is what this branch always did.
func TestNoSessionsGetsTheDefault(t *testing.T) {
	for _, available := range [][]string{nil, {}} {
		if got := chooseSSHSession(available); got != DefaultSSHSessionName {
			t.Errorf("a server with no sessions answered %q, want %q", got, DefaultSSHSessionName)
		}
	}
}
