package session

import (
	"bytes"
	"strings"
	"testing"
)

// TestRolledSnapshotCatchUpDropsThePendingSequence pins the wire contract
// between a snapshot's PendingInput and a rolled catch-up.
//
// A snapshot is taken at vtSeq, and vtSeq is wherever the daemon's emulator
// stopped, often inside an escape sequence. The snapshot carries that start
// (PendingInput) so the bytes after vtSeq finish it. When the ring has rolled
// past vtSeq, the catch-up starts at the ring's first byte instead, and the
// client's parser joins the old start to a different sequence's end. Here
// "\x1b[" from the snapshot meets ";1H" from the ring: a cursor move to the
// top left, and the next text paints over a row only the snapshot held. In
// e2e/tui this is TestReattachKeepsSnapshotRowsWhenRingRolled losing its
// header about one run in fifty.
//
// The stream is built so the ring's first byte lands on that join: the ring
// shifts by 32 KiB, so its start is always a multiple of 32768, and the
// pending sequence is placed 3 bytes short of a 16-byte unit there.
func TestRolledSnapshotCatchUpDropsThePendingSequence(t *testing.T) {
	const header = "HEADER-ROW"
	// One unit moves to row 6 and writes ten cells: 16 bytes.
	const unit = "\x1b[6;1Hxxxxxxxxxx"

	p := newEmulatedPTY(t, 80, 24)
	lead := "\x1b[1;1H" + header + "\x1b[6;1H"
	// The pending "\x1b[" starts at a position 13 mod 16, so a ring start at
	// a multiple of 32768 falls 3 bytes into a unit, on ";1H".
	lead += strings.Repeat("y", (13-len(lead)%16+16)%16)
	p.feed([]byte(lead))
	p.feed([]byte("\x1b["))
	p.fedThrough(t, "the header and the cut sequence")

	state := p.GetTerminalState(0, 0)
	if state == nil {
		t.Fatal("no snapshot")
	}
	if !bytes.Equal(state.PendingInput, []byte("\x1b[")) {
		t.Fatalf("the snapshot's pending input is %q, want the cut %q: the fixture no longer reaches the join", state.PendingInput, "\x1b[")
	}

	// Finish the cut unit, then roll the ring far past the snapshot.
	var rest bytes.Buffer
	rest.WriteString(unit[2:])
	for range 12 << 10 {
		rest.WriteString(unit)
	}
	for b := rest.Bytes(); len(b) > 0; {
		n := min(len(b), 4096)
		p.feed(b[:n])
		b = b[n:]
	}
	p.fedThrough(t, "the flood")

	client := newLockedClient(state.Width, state.Height)
	defer client.Close()
	client.mu.Lock()
	ApplyTerminalState(client.term, state)
	client.mu.Unlock()
	replay := drain(p.SubscribeFromSnapshot("rolled", state.Seq))
	defer func() { _ = p.Unsubscribe("rolled") }()

	// The positive half: the catch-up is rolled, and its bytes begin on the
	// end of a sequence whose start the client does not hold.
	if body := bytes.TrimLeft(replay, "\x18"); !bytes.HasPrefix(body, []byte(";1H")) {
		t.Fatalf("the catch-up begins %q, want the ring to start on %q: the fixture no longer reaches the join", body[:min(len(body), 16)], ";1H")
	}
	if !strings.HasPrefix(strings.SplitN(p.daemonText(), "\n", 2)[0], header) {
		t.Fatalf("the daemon lost its own header row:\n%s", p.daemonText())
	}

	client.Write(replay)
	if top, _, _ := strings.Cut(client.Text(), "\n"); !strings.HasPrefix(top, header) {
		t.Errorf("the client's top row is %q, want %q: the snapshot's cut sequence was joined to the ring's first bytes", strings.TrimRight(top, " "), header)
	}
}
