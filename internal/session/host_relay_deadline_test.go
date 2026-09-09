package session

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"
)

// The bug this file exists for.
//
// A session on another machine kept being taken away from the person using it,
// about eleven seconds after every attach, and again eleven seconds after the
// one after that. The link was fine. Nothing crossed a network.
//
// The daemon answers open-host-connection through writeVerbResponse, which
// arms a ten second write deadline on the client's socket. A deadline on a
// net.Conn is an absolute time and not a per-write budget, so it stayed armed
// while the relay took the connection over. The relay cleared the read
// deadline and not the write one. The first byte the far pane printed after
// that instant failed with "i/o timeout", the relay ended, and the client
// reported it had lost the link.

// TestARelayIsNotEndedByTheDeadlineFromItsOwnReply is that failure, made to
// happen on purpose and then made not to.
func TestARelayIsNotEndedByTheDeadlineFromItsOwnReply(t *testing.T) {
	client, server := socketPair(t)

	// The remote end of the relay: a pipe that stays quiet past the deadline
	// and then says something, exactly as an idle pane does.
	remoteRead, remoteWrite := net.Pipe()
	t.Cleanup(func() { _ = remoteRead.Close(); _ = remoteWrite.Close() })

	d := &Daemon{}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	t.Cleanup(d.cancel)
	cs := &connState{conn: server, clientID: "relay-test", done: make(chan struct{})}

	// What writeVerbResponse leaves behind: a deadline that has already passed
	// by the time the relay writes anything. The real one is ten seconds out
	// and the pane is quiet for eleven. This is the same state, without the
	// wait.
	_ = server.SetWriteDeadline(time.Now().Add(-time.Second))

	go d.relayHostConnection(cs, bufio.NewReader(server), remoteRead)

	go func() {
		_, _ = remoteWrite.Write([]byte("PANE-OUTPUT\n"))
	}()

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil {
		t.Fatalf("ASSERTION: the relay never delivered the far pane's output: %v. "+
			"The write deadline left over from the reply that opened the relay ended it, "+
			"and the person was told their link had dropped", err)
	}
	if line != "PANE-OUTPUT\n" {
		t.Fatalf("the relay delivered %q, want the far pane's line", line)
	}
}
