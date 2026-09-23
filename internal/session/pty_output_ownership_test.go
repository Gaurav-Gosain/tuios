package session

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// TestPTYOutputHandlerOwnsItsBytes pins the contract terminal.Window's
// WriteOutputAsync relies on to queue output without copying it: every slice
// the client hands a PTY output handler is its own, and reading later frames
// never writes over an earlier one. A read loop that decoded frames into a
// reused buffer would corrupt output still waiting in a window's queue, and
// the corruption would only show as wrong characters on screen.
func TestPTYOutputHandlerOwnsItsBytes(t *testing.T) {
	server, client := net.Pipe()
	c := NewTUIClient()
	c.conn = client
	t.Cleanup(func() {
		_ = c.Close()
		_ = server.Close()
	})

	const ptyID = "ptytest-00000004"
	got := make(chan []byte, 16)
	c.ptyHandlersMu.Lock()
	c.ptyHandlers[ptyID] = func(data []byte) { got <- data }
	c.ptyHandlersMu.Unlock()
	c.StartReadLoop()

	// Frames of the same length and different bytes, so a shared buffer
	// would be overwritten in place rather than resliced.
	var want [][]byte
	for i := range 8 {
		frame := bytes.Repeat([]byte{'a' + byte(i)}, 300)
		want = append(want, frame)
		if err := WritePTYOutput(server, ptyID, frame); err != nil {
			t.Fatalf("write frame %d: %v", i, err)
		}
	}

	var kept [][]byte
	for i := range want {
		select {
		case data := <-got:
			kept = append(kept, data)
		case <-time.After(5 * time.Second):
			t.Fatalf("frame %d never reached the handler", i)
		}
	}
	for i := range want {
		if !bytes.Equal(kept[i], want[i]) {
			t.Errorf("frame %d holds %q after later frames were read, want %q", i, kept[i][:8], want[i][:8])
		}
	}
}
