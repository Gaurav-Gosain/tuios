package session

import (
	"bufio"
	"errors"
	"io"
	"time"
)

const (
	maxVerbLine       = 12 << 20
	largeVerbLine     = 64 << 10
	lineBudgetWait    = readBudgetWait
	largeLineDeadline = 30 * time.Second
	lineCopies        = 3
)

var (
	errVerbLineTooLong = errors.New("the request line is longer than the daemon takes")
	errVerbLineBusy    = errors.New("the daemon is busy with other large requests")
)

type verbLineReader struct {
	d       *Daemon
	cs      *connState
	br      *bufio.Reader
	release func()
}

func (r *verbLineReader) done() {
	if r.release != nil {
		r.release()
		r.release = nil
	}
}

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
			return line, nil
		default:
			r.done()
			return nil, err
		}
	}
}
