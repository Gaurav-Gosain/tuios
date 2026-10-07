package session

import (
	"bufio"
	"errors"
	"io"
	"time"
)

// Reading verb request lines with bounded memory.
//
// A JSON verb connection is one request per line. The line is read whole
// before it is parsed, so its size is memory the daemon holds, and any
// process that can reach the socket can send one. Three limits bound it:
//
//   - maxVerbLine caps one line. The largest real request is a stash-put or
//     a paste-image with 8 MiB of file in base64, about 10.7 MiB; a paste
//     buffer upload part is about 1 MiB. A longer line is refused and the
//     connection closed, since the rest of it cannot be read as a request.
//   - A line is read into memory of its own, which goes when the request
//     ends. A connection that sent one large line does not keep a large
//     buffer for its life.
//   - Past largeVerbLine, every byte a line takes is charged to a budget, at
//     three times its size: the line, the copy the request envelope's params
//     make, and what a handler decodes from them. A line that cannot get its
//     share within lineBudgetWait is refused, so many large requests at once
//     wait or fail and never grow memory past the budgets. A large line must
//     also arrive within largeLineDeadline, so a client that sends one slowly
//     cannot hold its share.
//   - There are two budgets. The person, outside every pane or in a pane
//     that holds admin, has one of their own, and every other caller (a pane
//     without admin, a link) shares the other. So no pane can use up what
//     the person's own large requests (a stash-put, a paste-image, a large
//     yank) need. A connection reads one line at a time, so it holds at most
//     one line's share, maxVerbLine times lineCopies, which is under either
//     budget.

const (
	maxVerbLine       = 12 << 20
	largeVerbLine     = 64 << 10
	lineBudgetBytes   = 48 << 20 // each of the two budgets
	lineBudgetChunk   = 64 << 10
	lineBudgetWait    = 2 * time.Second
	largeLineDeadline = 30 * time.Second
	// lineCopies is how many times its size a large request takes in
	// memory, counted against the budget.
	lineCopies = 3
)

// Errors of reading a request line.
var (
	errVerbLineTooLong = errors.New("the request line is longer than the daemon takes")
	errVerbLineBusy    = errors.New("the daemon is busy with other large requests")
)

// lineBudget is the share of memory large request lines may hold across the
// daemon, in chunks of lineBudgetChunk bytes.
type lineBudget chan struct{}

func newLineBudget() lineBudget { return make(lineBudget, lineBudgetBytes/lineBudgetChunk) }

// acquire takes n bytes of the budget, rounded up to a chunk, waiting until
// deadline. It returns the chunks it took, or 0 and false when the budget did
// not free in time, having given back what it took.
func (b lineBudget) acquire(n int, deadline time.Time) (int, bool) {
	chunks := (n + lineBudgetChunk - 1) / lineBudgetChunk
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for i := range chunks {
		select {
		case b <- struct{}{}:
		case <-timer.C:
			b.release(i)
			return 0, false
		}
	}
	return chunks, true
}

// release gives back chunks.
func (b lineBudget) release(chunks int) {
	for range chunks {
		<-b
	}
}

// verbLineReader reads request lines from one connection.
type verbLineReader struct {
	d    *Daemon
	cs   *connState
	br   *bufio.Reader
	pool lineBudget // the budget of this connection's caller, found once
	held int        // budget chunks the current line holds
}

// budget is the budget of the connection's caller: the person's own, or the
// one every other caller shares. It is found at the first large line.
func (r *verbLineReader) budget() lineBudget {
	if r.pool == nil {
		r.pool = r.d.lineBudgetFor(r.d.bufferAccess(r.cs).person())
	}
	return r.pool
}

// done gives back the budget of the line read last. The caller calls it when
// the request has been handled.
func (r *verbLineReader) done() {
	if r.held > 0 {
		r.budget().release(r.held)
		r.held = 0
	}
}

// next reads one line, without its line feed. The line is memory of its own.
func (r *verbLineReader) next() ([]byte, error) {
	r.done()
	var line []byte
	large := false
	for {
		frag, err := r.br.ReadSlice('\n')
		if len(line)+len(frag) > maxVerbLine {
			return nil, errVerbLineTooLong
		}
		if !large && len(line)+len(frag) > largeVerbLine {
			large = true
			_ = r.cs.conn.SetReadDeadline(time.Now().Add(largeLineDeadline))
		}
		if large {
			if cap(line)-len(line) < len(frag) {
				// Grow by doubling, charging the budget for the new memory
				// and for the copies the request will make of it.
				grown := min(max(2*cap(line), len(line)+len(frag), largeVerbLine), maxVerbLine)
				got, ok := r.budget().acquire((grown-cap(line))*lineCopies, time.Now().Add(lineBudgetWait))
				if !ok {
					return nil, errVerbLineBusy
				}
				r.held += got
				next := make([]byte, len(line), grown)
				copy(next, line)
				line = next
			}
		}
		line = append(line, frag...)
		switch {
		case err == nil:
			if large {
				_ = r.cs.conn.SetReadDeadline(time.Time{})
			}
			return line[:len(line)-1], nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			// A last request with no line feed, as the scanner took it.
			return line, nil
		default:
			return nil, err
		}
	}
}

// lineBudgetFor is the daemon's budget for large request lines of the
// person, or of every other caller.
func (d *Daemon) lineBudgetFor(person bool) lineBudget {
	d.lineBudgetOnce.Do(func() {
		d.lineBudgetPerson = newLineBudget()
		d.lineBudgetPanes = newLineBudget()
	})
	if person {
		return d.lineBudgetPerson
	}
	return d.lineBudgetPanes
}
