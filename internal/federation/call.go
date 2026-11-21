package federation

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
)

const maxResponseLine = 4 << 20

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

type caller struct {
	rw io.ReadWriteCloser
	br *bufio.Reader

	one sync.Mutex

	mu     sync.Mutex
	nextID int
}

func newCaller(rw io.ReadWriteCloser) *caller {
	return &caller{rw: rw, br: bufio.NewReaderSize(rw, 64<<10)}
}

func (c *caller) call(ctx context.Context, verb string, params any) (json.RawMessage, error) {
	if !lockOrDone(ctx, &c.one) {
		return nil, ctx.Err()
	}
	defer c.one.Unlock()

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	req := map[string]any{"id": id, "verb": verb}
	if params != nil {
		req["params"] = params
	}
	line, err := jsonx.Marshal(req)
	if err != nil {
		return nil, errfmt.Errorf("encode request: %w", err)
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
		_ = c.rw.Close()
		return nil, ctx.Err()
	}
}

func (c *caller) readResponse() (json.RawMessage, error) {
	line, err := readBoundedLine(c.br, maxResponseLine)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *RemoteError    `json:"error"`
	}
	if err := jsonx.Unmarshal(line, &resp); err != nil {
		return nil, errfmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// ErrResponseTooLarge reports a response line past maxResponseLine.
var ErrResponseTooLarge = errors.New("federation: the remote daemon sent an oversized response")

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

func lockOrDone(ctx context.Context, mu *sync.Mutex) bool {
	got := make(chan struct{})
	go func() {
		mu.Lock()
		close(got)
	}()
	select {
	case <-got:
		return true
	case <-ctx.Done():
		go func() {
			<-got
			mu.Unlock()
		}()
		return false
	}
}
