package vt

import (
	"testing"
	"time"
)

func TestBufPipeWriteNeverBlocks(t *testing.T) {
	p := newBufPipe()
	done := make(chan struct{})
	go func() {
		// Many writes with no reader must not block.
		for i := 0; i < 1000; i++ {
			_, _ = p.Write([]byte("\x1b[0n"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked with no reader")
	}
	buf := make([]byte, 4)
	n, err := p.Read(buf)
	if err != nil || n != 4 || string(buf[:n]) != "\x1b[0n" {
		t.Fatalf("Read got n=%d err=%v data=%q", n, err, buf[:n])
	}
}
