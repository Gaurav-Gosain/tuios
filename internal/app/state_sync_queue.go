package app

import "github.com/Gaurav-Gosain/tuios/internal/session"

// QueueStateSync hands a state snapshot from the daemon read loop to the Update
// loop. When the channel is full it displaces the oldest queued snapshot rather
// than discarding the new one, and reports that it did so.
//
// Every message here is a whole snapshot, so losing an intermediate costs
// nothing and losing the newest costs everything: the client goes on rendering a
// view the daemon has abandoned, with nothing to correct it until the session
// happens to change again. That is the "the other client doesn't show the new
// pane" report.
//
// The alternative, asking the daemon to re-send, was rejected for three reasons.
// The daemon has no handler for the request that exists in the protocol, so it
// would only work against a daemon of the same vintage, and an upgrade is
// exactly when it would not be. An unknown message type comes back as a bare
// MsgError, and responses on this connection are correlated by message type, so
// a stray error can be taken for the answer to an unrelated round trip. And the
// request cannot be a round trip at all, because the goroutine that would wait
// for the reply is the one that has to read it.
//
// The read loop is the only sender, so freeing a slot here guarantees the retry
// takes it.
func (m *OS) QueueStateSync(state *session.SessionState) (displaced bool) {
	if m.StateSyncChan == nil || state == nil {
		return false
	}
	select {
	case m.StateSyncChan <- state:
		return false
	default:
	}
	select {
	case <-m.StateSyncChan:
	default:
	}
	select {
	case m.StateSyncChan <- state:
	default:
	}
	return true
}
