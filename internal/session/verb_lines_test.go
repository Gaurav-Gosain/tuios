package session

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"sync"
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
//   - Large lines could starve each other: each holding part of the budget
//     while it waits for the rest. Six at once must all be read in turn.
func TestVerbLinesHoldBoundedMemory(t *testing.T) {
	d := &Daemon{}
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()
	lr := &verbLineReader{d: d, cs: &connState{conn: server}, br: bufio.NewReaderSize(server, 64*1024)}
	budget := d.readBudgetFor(&connState{})

	send := func(b []byte) {
		go func() { _, _ = client.Write(b) }()
	}

	// A line past the cap is refused.
	send(append(bytes.Repeat([]byte("x"), maxVerbLine+1), '\n'))
	if _, err := lr.next(); !errors.Is(err, errVerbLineTooLong) {
		t.Fatalf("a line of %d bytes read as %v, want errVerbLineTooLong", maxVerbLine+1, err)
	}
	lr.done()
	if !budgetWhole(budget) {
		t.Fatal("after the refusal the budget is not whole")
	}

	// The connection is spent after a refusal; a fresh one for the rest.
	_ = client.Close()
	client, server = net.Pipe()
	lr = &verbLineReader{d: d, cs: &connState{conn: server}, br: bufio.NewReaderSize(server, 64*1024)}

	// With the budget taken, a large line is refused, and a small one passes.
	full, ok := budget.acquire(personBudgetBytes, time.Second)
	if !ok {
		t.Fatal("could not take the budget")
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
	full()

	// A large line under the cap is read whole, and done gives its share back.
	send(append(bytes.Repeat([]byte("z"), 1<<20), '\n'))
	line, err := lr.next()
	if err != nil || len(line) != 1<<20 {
		t.Fatalf("a 1 MiB line read as %d bytes, %v", len(line), err)
	}
	if budgetWhole(budget) {
		t.Fatalf("a 1 MiB line held none of the budget")
	}
	lr.done()
	if !budgetWhole(budget) {
		t.Fatal("after done the budget is not whole")
	}

	// Six large lines at once are all read, in turn.
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for range 6 {
		c, s := net.Pipe()
		defer func() { _ = c.Close(); _ = s.Close() }()
		r := &verbLineReader{d: d, cs: &connState{conn: s}, br: bufio.NewReaderSize(s, connReadBuffer)}
		go func() { _, _ = c.Write(append(bytes.Repeat([]byte("w"), 1<<20), '\n')) }()
		wg.Go(func() {
			if _, err := r.next(); err != nil {
				errs <- err
				return
			}
			// The handler's time, with the charge held.
			time.Sleep(100 * time.Millisecond)
			r.done()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a large line was not read: %v", err)
	}
	if !budgetWhole(budget) {
		t.Fatal("after six lines the budget is not whole")
	}
}
