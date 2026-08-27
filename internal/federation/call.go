package federation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// The control stream speaks the daemon's own line-delimited JSON verb protocol.
// This is a second, small implementation of the caller rather than a reuse of
// session.VerbClient, because that one dials a unix socket and lives in the
// package that will import this one. Duplicating fifty lines is cheaper than a
// dependency cycle, and it keeps the rule that nothing about a remote answer is
// decoded by code that also serves local clients.

// maxResponseLine bounds one response line from a remote daemon. The peer is
// untrusted, so its answer is not allowed to size this side's buffer. The local
// daemon's own limit is 16 MiB; a listing is kilobytes, and anything past this
// is a peer misbehaving.
const maxResponseLine = 4 << 20 // 4 MiB

// RemoteError is a remote daemon's error envelope. Its code is the daemon's own
// stable string, reported unchanged so a caller sees what the far side said.
type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RemoteError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Message + " (" + e.Code + ")"
}

// caller runs verb calls over one stream. One call is in flight at a time.
type caller struct {
	rw io.ReadWriteCloser
	br *bufio.Reader

	mu     sync.Mutex
	nextID int
}

func newCaller(rw io.ReadWriteCloser) *caller {
	return &caller{rw: rw, br: bufio.NewReaderSize(rw, 64<<10)}
}

// call sends one verb and returns the raw result object.
//
// The context is the whole point of this function. A remote machine can accept
// the connection and then never answer, and the hub must not wait on it: when
// ctx ends first, call returns immediately with ctx.Err() and closes the
// stream, because a stream with an unanswered request on it can never be
// reused. The link supervisor reads that as a dead link and redials.
func (c *caller) call(ctx context.Context, verb string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	req := map[string]any{"id": id, "verb": verb}
	if params != nil {
		req["params"] = params
	}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	line = append(line, '\n')

	type answer struct {
		raw json.RawMessage
		err error
	}
	done := make(chan answer, 1)
	go func() {
		if _, werr := c.rw.Write(line); werr != nil {
			done <- answer{err: werr}
			return
		}
		raw, rerr := c.readResponse()
		done <- answer{raw: raw, err: rerr}
	}()

	select {
	case a := <-done:
		return a.raw, a.err
	case <-ctx.Done():
		// The stream is unusable from here: the answer, if it ever comes, would
		// be read as the reply to the next call.
		_ = c.rw.Close()
		return nil, ctx.Err()
	}
}

// readResponse reads and decodes one response line.
func (c *caller) readResponse() (json.RawMessage, error) {
	line, err := readBoundedLine(c.br, maxResponseLine)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *RemoteError    `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// ErrResponseTooLarge reports a response line past maxResponseLine.
var ErrResponseTooLarge = errors.New("federation: the remote daemon sent an oversized response")

// readBoundedLine reads one newline-terminated line, refusing to grow past
// limit. bufio.Reader.ReadString would happily assemble a line of any length,
// which is exactly the allocation an untrusted peer must not control.
func readBoundedLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, ErrResponseTooLarge
		}
		if err == nil {
			return buf, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(buf) > 0 && errors.Is(err, io.EOF) {
			return buf, nil
		}
		return nil, err
	}
}
