package session

import (
	"net"
	"time"
)

// A hosted pane that outlives a dropped link.
//
// A pane this machine runs for another machine used to end the moment its
// connection did, and a connection over ssh drops whenever the network under
// it does: a laptop lid, a Wi-Fi hand-off, a VPN reconnecting. An agent in the
// pane died mid-turn each time.
//
// Now the owner asks for a resumable pane (open-pane's resumable), and this
// machine grants the grace its link policy names for the owner (hosted_grace,
// ten minutes by default, "0" for none). The process's output goes through a
// pump that outlives any one connection: it keeps the last 64 KB in a ring
// with a running byte count and writes each chunk to the connection attached
// now, if there is one. When the connection drops, the pane waits, still
// running and still read, for the grace. The owner reattaches with open-pane's
// resume, naming the pane, the resume token and how many bytes it received;
// the pump replays what was missed from the ring, or the whole ring with gap
// set when more was missed than it holds, and goes on from there.
//
// What is enforced:
//
//   - A reattach needs the resume token, which is in the open-pane reply only,
//     compared in constant time. A pane with no grace has no token and cannot
//     be reattached.
//   - The grace is this machine's decision, from its own policy for the owner,
//     never the owner's; the owner only asks for one. At most 24 hours.
//   - A pane nobody reattaches ends when its grace runs out, and the daemon
//     logs it. A pane whose process exits while detached ends at once.
//   - close-pane ends a pane at once. The owner sends it when the window is
//     closed on purpose, so a deliberate close does not leave a process
//     waiting out its grace.
//   - The pump never blocks on a detached pane: output is kept in the ring
//     and the oldest is dropped, so a process that writes while nobody
//     listens keeps running rather than stalling on a full pty.

// hostedRingSize is how much of a hosted pane's output is kept for a reattach.
// It matches the ring a daemon keeps per pane for a resubscribing client.
const hostedRingSize = 64 * 1024

// start runs the output pump and the reaper, once, on the first attach.
func (hp *hostedPane) start() {
	hp.startOnce.Do(func() {
		go func() {
			// Reap the process. Nothing else on this machine waits for a
			// hosted pane's command: the local path waits in monitorExit,
			// which belongs to a PTY this daemon owns, and a hosted pane has
			// no PTY here. An unwaited child is a zombie for as long as the
			// daemon runs.
			//
			// It doubles as the backstop for the pty going quiet: whichever of
			// the two notices first ends the pane, and close is idempotent.
			if hp.cmd != nil {
				_ = hp.cmd.Wait()
			}
			hp.close()
		}()
		go hp.pump()
	})
}

// pump reads the process's output until the pty closes, which is what the
// process exiting looks like from here, and then ends the pane.
func (hp *hostedPane) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := hp.pty.Read(buf)
		if n > 0 {
			hp.deliver(buf[:n])
		}
		if err != nil {
			hp.close()
			return
		}
	}
}

// deliver keeps a chunk in the ring and writes it to the attached connection.
func (hp *hostedPane) deliver(chunk []byte) {
	hp.out.Lock()
	hp.ring = append(hp.ring, chunk...)
	if over := len(hp.ring) - hostedRingSize; over > 0 {
		hp.ring = append(hp.ring[:0], hp.ring[over:]...)
	}
	hp.outSeq += int64(len(chunk))
	hp.connMu.Lock()
	c := hp.conn
	hp.connMu.Unlock()
	var err error
	if c != nil {
		_, err = c.Write(chunk)
	}
	hp.out.Unlock()
	if err != nil {
		hp.detach(c)
	}
}

// replayFrom is what an owner that has received from bytes is missing, and
// whether that is more than the ring holds. The caller holds out.
func (hp *hostedPane) replayFromLocked(from int64) ([]byte, bool) {
	start := hp.outSeq - int64(len(hp.ring))
	if from < start || from > hp.outSeq {
		return hp.ring, from != hp.outSeq
	}
	return hp.ring[from-start:], false
}

// resumeGap reports whether an owner that has received offset bytes has missed
// more than the ring holds.
func (hp *hostedPane) resumeGap(offset int64) bool {
	hp.out.Lock()
	defer hp.out.Unlock()
	_, gap := hp.replayFromLocked(offset)
	return gap
}

// attach makes c the pane's connection: a connection still attached is closed,
// a pending end is called off, what the owner missed is written, and the pump
// writes to c from the next byte. It reports false for a pane that has ended.
func (hp *hostedPane) attach(c net.Conn, from int64) bool {
	hp.connMu.Lock()
	if hp.closed {
		hp.connMu.Unlock()
		return false
	}
	old := hp.conn
	hp.conn = nil
	if hp.orphan != nil {
		hp.orphan.Stop()
		hp.orphan = nil
	}
	hp.connMu.Unlock()
	if old != nil {
		// The old connection is dead or about to be: only the owner holds
		// the token, so a second attach is the owner coming back.
		_ = old.Close()
	}

	hp.out.Lock()
	replay, _ := hp.replayFromLocked(from)
	if len(replay) > 0 {
		if _, err := c.Write(replay); err != nil {
			hp.out.Unlock()
			hp.detach(c)
			return true
		}
	}
	hp.connMu.Lock()
	ok := !hp.closed
	if ok {
		hp.conn = c
		// The old connection's relay may have started the grace while this
		// attach was replaying; the pane has an owner again.
		if hp.orphan != nil {
			hp.orphan.Stop()
			hp.orphan = nil
		}
	}
	hp.connMu.Unlock()
	hp.out.Unlock()
	if !ok {
		return false
	}
	hp.start()
	return true
}

// detach forgets c if it is still the pane's connection, and then ends the
// pane or starts its grace.
func (hp *hostedPane) detach(c net.Conn) {
	_ = c.Close()
	hp.connMu.Lock()
	if hp.conn != c && hp.conn != nil {
		// Replaced by a reattach already.
		hp.connMu.Unlock()
		return
	}
	hp.conn = nil
	if hp.closed {
		hp.connMu.Unlock()
		return
	}
	if hp.grace <= 0 || hp.ending {
		hp.connMu.Unlock()
		hp.close()
		return
	}
	if hp.orphan == nil {
		grace := hp.grace
		hp.orphan = time.AfterFunc(grace, func() {
			hp.connMu.Lock()
			still := hp.conn == nil && !hp.closed
			hp.connMu.Unlock()
			if still {
				LogBasic("Pane %s ended: its owner did not come back within %s", shortID(hp.id), grace)
				hp.close()
			}
		})
		LogBasic("Pane %s lost its owner's connection and waits %s to be reattached", shortID(hp.id), grace)
	}
	hp.connMu.Unlock()
}

// ended reports whether the pane has closed. A closing pane can still be in
// the registry for a moment, and a reattach must not be told it succeeded.
func (hp *hostedPane) ended() bool {
	hp.connMu.Lock()
	defer hp.connMu.Unlock()
	return hp.closed
}

// end closes the pane at the owner's request.
func (hp *hostedPane) end() {
	hp.connMu.Lock()
	hp.ending = true
	hp.connMu.Unlock()
	hp.close()
}
