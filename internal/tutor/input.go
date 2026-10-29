package tutor

import (
	"io"
	"os"
	"sync"
)

// keyInput is what Bubble Tea reads: the person's keys from the terminal, and
// the keys "show me" types for them. Fd is the terminal's, so Bubble Tea
// puts it in raw mode and restores it on the way out, as it does when it
// reads the terminal itself.
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

// pump copies the terminal's bytes in. It ends with the terminal. It starts
// on the first Read, which Bubble Tea makes once the terminal is raw, so no
// read is left waiting for a whole cooked line.
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

// maxPending bounds the bytes waiting to be read. Only a stuck program
// reaches it, and then a key more or less does not matter.
const maxPending = 64 << 10

// inject queues bytes as if the person typed them.
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

// Write goes to the terminal. Bubble Tea writes nothing to its input.
func (in *keyInput) Write(b []byte) (int, error) { return in.tty.Write(b) }

// Close ends reads once the queue is empty. The terminal stays open: it is
// the person's.
func (in *keyInput) Close() error {
	in.mu.Lock()
	in.closed = true
	in.cond.Broadcast()
	in.mu.Unlock()
	return nil
}

// Fd is the terminal's, for raw mode.
func (in *keyInput) Fd() uintptr { return in.tty.Fd() }
