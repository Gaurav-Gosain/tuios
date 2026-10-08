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
//   - When a line grows past largeVerbLine, it is charged to the caller's
//     read budget, the one binary frames are charged to (readBudgetFor in
//     frame_budget.go). A line has no length ahead of it, so the charge is
//     the most a line can take, maxVerbLine times lineCopies: the line, the
//     copy the request envelope's params make, and what a handler decodes
//     from them. It is taken in one acquire, whole or not at all, so a line
//     never holds part of the budget while it waits for the rest. A line
//     that cannot get its charge within lineBudgetWait is refused with
//     ErrVerbBusy. A large line must also arrive within largeLineDeadline,
//     so a client that sends one slowly cannot hold its charge.
//   - The charge is given back once the request envelope is decoded, before
//     the verb runs (dispatchVerbLine). A verb can wait a long time, such as
//     a send-text into a pane that does not read, and must not hold the
//     budget while it does. What a running verb still holds is its decoded
//     params, one request at a time on each connection, and the connection
//     caps bound the connections. A write into a pane that does not read is
//     bounded per pane by PTY.Write.
//
// The person's budget and the one every other caller shares (a pane without
// admin, a link) are apart, so no pane can use up what the person's own
// large requests (a stash-put, a paste-image, a large yank) need.

const (
	maxVerbLine       = 12 << 20
	largeVerbLine     = 64 << 10
	lineBudgetWait    = readBudgetWait
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

// verbLineReader reads request lines from one connection.
type verbLineReader struct {
	d       *Daemon
	cs      *connState
	br      *bufio.Reader
	release func() // gives back the charge of the current line, or nil
}

// done gives back the budget of the line read last. dispatchVerbLine calls it
// once the request envelope is decoded, before the verb runs; next calls it
// too, so it is safe to call more than once.
func (r *verbLineReader) done() {
	if r.release != nil {
		r.release()
		r.release = nil
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
			// The whole charge, before the line takes more memory.
			release, ok := r.d.readBudgetFor(r.cs).acquire(maxVerbLine*lineCopies, lineBudgetWait)
			if !ok {
				return nil, errVerbLineBusy
			}
			r.release = release
			_ = r.cs.conn.SetReadDeadline(time.Now().Add(largeLineDeadline))
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
			r.done()
			return nil, err
		}
	}
}
