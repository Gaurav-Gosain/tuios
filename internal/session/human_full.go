//go:build !slim

package session

// humanForbiddenError is the refusal a pane gets for speaking as the person.
func humanForbiddenError(verb string) *verbError {
	return hintedVerbError(ErrVerbForbidden, verb+" from human is refused: the caller runs inside a pane of this daemon, and only the person at an attached client can speak as human", &VerbHint{
		Param:   "from",
		Command: "tuios send-agent-message -w human --from \"$TUIOS_PANE_ID\" '<your question>'",
		Detail:  "Nothing was sent. Send as your own pane, with from set to $TUIOS_PANE_ID. To get an answer from the person, send them a message with -w human and wait for their reply with wait-for agent-message; only a reply marked verified_human is theirs.",
	})
}

// verifyHumanNonce reports whether nonce belongs to a client attached to the
// session sessionID right now, over a connection of the same kind as the
// sender's: a nonce issued to an attach on the link socket verifies only a
// send on the link socket, and a local one only a local send.
//
// Two more conditions come from human_origin.go. The sender itself must be
// allowed to act as the person, so a nonce that leaked into a pane is no use
// there. And where the kernel gave both pids, the sender must be the process
// that attached: the tuios client sends its reply on a fresh connection, but
// from the same process that holds the attach, so a nonce copied to another
// process does not verify. A nil sender is the daemon itself.
func (d *Daemon) verifyHumanNonce(nonce, sessionID string, sender *connState) bool {
	if sessionID == "" {
		return false
	}
	return d.matchHumanNonce(nonce, sessionID, sender)
}

// verifyAnyHumanNonce is verifyHumanNonce for a call that spans sessions, such
// as dismiss-attention: the attach the nonce came from may be to any session,
// and every other condition holds as it does for a reply.
func (d *Daemon) verifyAnyHumanNonce(nonce string, sender *connState) bool {
	return d.matchHumanNonce(nonce, "", sender)
}
