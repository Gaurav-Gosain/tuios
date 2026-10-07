package session

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

// TestVerbLinesHoldBoundedMemory is a security boundary: any process that
// can reach the socket can send request lines, so what a line may hold must
// stay bounded however large it claims to be and however many arrive.
//
// How the bound could fail, written down first:
//   - A line could grow without limit. One past maxVerbLine must be refused
//     before it is all in memory.
//   - Many large lines at once could each take their share. With the budget
//     taken, a large line must be refused, not read.
//   - A refused or finished line could keep its share, so the budget runs
//     dry for good. After done, the budget must be whole again.
//   - A small line could be held to the budget too, which would lock out
//     every request while the budget is taken. A small line must still pass.
func TestVerbLinesHoldBoundedMemory(t *testing.T) {
	d := &Daemon{}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	lr := &verbLineReader{d: d, cs: &connState{conn: server}, br: bufio.NewReaderSize(server, 64*1024)}

	send := func(b []byte) {
		go func() { _, _ = client.Write(b) }()
	}

	// A line past the cap is refused.
	send(append(bytes.Repeat([]byte("x"), maxVerbLine+1), '\n'))
	if _, err := lr.next(); !errors.Is(err, errVerbLineTooLong) {
		t.Fatalf("a line of %d bytes read as %v, want errVerbLineTooLong", maxVerbLine+1, err)
	}
	lr.done()
	if n := len(d.lineBudget()); n != 0 {
		t.Fatalf("after the refusal the budget still holds %d chunks", n)
	}

	// The connection is spent after a refusal; a fresh one for the rest.
	_ = client.Close()
	client, server = net.Pipe()
	lr = &verbLineReader{d: d, cs: &connState{conn: server}, br: bufio.NewReaderSize(server, 64*1024)}

	// With the budget taken, a large line is refused, and a small one passes.
	budget := d.lineBudget()
	for range cap(budget) {
		budget <- struct{}{}
	}
	send(append(bytes.Repeat([]byte("y"), 200<<10), '\n'))
	start := time.Now()
	if _, err := lr.next(); !errors.Is(err, errVerbLineBusy) {
		t.Fatalf("a large line with the budget taken read as %v, want errVerbLineBusy", err)
	}
	if waited := time.Since(start); waited > 2*lineBudgetWait {
		t.Fatalf("the refusal took %v, more than the wait of %v", waited, lineBudgetWait)
	}
	_ = client.Close()
	client, server = net.Pipe()
	lr = &verbLineReader{d: d, cs: &connState{conn: server}, br: bufio.NewReaderSize(server, 64*1024)}
	send([]byte(`{"id":1,"verb":"hello"}` + "\n"))
	if line, err := lr.next(); err != nil || !bytes.Contains(line, []byte("hello")) {
		t.Fatalf("a small line with the budget taken read as %q, %v", line, err)
	}
	budget.release(cap(budget))

	// A large line under the cap is read whole, and done gives its share back.
	send(append(bytes.Repeat([]byte("z"), 1<<20), '\n'))
	line, err := lr.next()
	if err != nil || len(line) != 1<<20 {
		t.Fatalf("a 1 MiB line read as %d bytes, %v", len(line), err)
	}
	if len(budget) == 0 {
		t.Fatalf("a 1 MiB line held none of the budget")
	}
	lr.done()
	if n := len(budget); n != 0 {
		t.Fatalf("after done the budget still holds %d chunks", n)
	}
}
