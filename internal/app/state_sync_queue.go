package app

// QueueStateSync hands a state snapshot from the daemon read loop to the Update
// loop, together with where it came from: the daemon itself, or the peer whose
// push it is. ApplyStateSync reads the origin to tell a peer's tiling topology,
// which is news, from an echo of this client's own. When the channel is full
// it keeps the newer of the two snapshots, by the order they were taken, and
// reports that it displaced one.
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
func (m *OS) QueueStateSync(sync StateSyncMsg) (displaced bool) {
	if m.StateSyncChan == nil || sync.State == nil {
		return false
	}
	select {
	case m.StateSyncChan <- sync:
		return false
	default:
	}
	select {
	case queued := <-m.StateSyncChan:
		// Newest means newest taken, not newest read. The daemon writes to one
		// client from more than one goroutine (a peer's broadcast goes out
		// from the peer's connection, a reply from this one's), so a snapshot
		// taken earlier can be read later. Displacing by arrival threw away
		// the reply to this client's own push for an older broadcast, which
		// AcceptState then refused as predating that push, and the client was
		// left with nothing to apply until the session next changed.
		if queued.State != nil && sync.State.SnapshotSeq != 0 && queued.State.SnapshotSeq > sync.State.SnapshotSeq {
			sync = queued
		}
	default:
	}
	select {
	case m.StateSyncChan <- sync:
	default:
	}
	return true
}
