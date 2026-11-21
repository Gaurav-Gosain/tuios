package tutor

import (
	"io"
	"os"
	"sync"
)

type keyInput struct {
	tty  *os.File
	once sync.Once

	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInput(tty *os.File) *keyInput {
	in := &keyInput{tty: tty}
	in.cond = sync.NewCond(&in.mu)
	return in
}

func (in *keyInput) pump() {
	buf := make([]byte, 4096)
	for {
		n, err := in.tty.Read(buf)
		if n > 0 {
			in.inject(buf[:n])
		}
		if err != nil {
			_ = in.Close()
			return
		}
	}
}

const maxPending = 64 << 10

func (in *keyInput) inject(b []byte) {
	in.mu.Lock()
	if len(in.buf)+len(b) <= maxPending {
		in.buf = append(in.buf, b...)
	}
	in.cond.Broadcast()
	in.mu.Unlock()
}

func (in *keyInput) Read(b []byte) (int, error) {
	in.once.Do(func() { go in.pump() })
	in.mu.Lock()
	defer in.mu.Unlock()
	for len(in.buf) == 0 && !in.closed {
		in.cond.Wait()
	}
	if len(in.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(b, in.buf)
	in.buf = in.buf[n:]
	return n, nil
}

func (in *keyInput) Write(b []byte) (int, error) { return in.tty.Write(b) }

func (in *keyInput) Close() error {
	in.mu.Lock()
	in.closed = true
	in.cond.Broadcast()
	in.mu.Unlock()
	return nil
}

func (in *keyInput) Fd() uintptr { return in.tty.Fd() }
