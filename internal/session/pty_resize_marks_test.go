package session

import (
	"bytes"
	"context"
	"testing"
)

// A resize is a point in the stream, and the ring holds bytes only. These pin
// the marks that let a catch-up cut from the ring replay a resize between the
// same two bytes the live broadcast put it between. Without them, a subscriber
// resumed across a resize was handed the whole span at one width, and every
// line after the resize wrapped where the daemon had not wrapped it.

// newResizablePTY is newBufferedPTY plus the fields Resize touches.
func newResizablePTY(t *testing.T) *PTY {
	t.Helper()
	return &PTY{
		ID:           "ptytest-00000003",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: make([]byte, 64*1024),
		vtWriteChan:  make(chan vtChunk, 8),
		ctx:          context.Background(),
	}
}

// chunks collects everything queued on a subscriber channel without blocking.
func chunks(ch <-chan ptyChunk) []ptyChunk {
	var out []ptyChunk
	for {
		select {
		case c, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, c)
		default:
			return out
		}
	}
}

func TestCatchUpCarriesAResizeAtItsByte(t *testing.T) {
	p := newResizablePTY(t)
	p.appendAndBroadcast([]byte("laid out at the old width\r\n"))
	if err := p.Resize(38, 9); err != nil {
		t.Fatalf("resize: %v", err)
	}
	p.appendAndBroadcast([]byte("laid out at the new width\r\n"))

	got := chunks(p.Subscribe("client-1", 0))
	if len(got) != 3 {
		t.Fatalf("catch-up came as %d chunks, want bytes, resize, bytes", len(got))
	}
	if !bytes.Equal(got[0].data, []byte("laid out at the old width\r\n")) {
		t.Errorf("first segment %q, want the bytes before the resize", got[0].data)
	}
	if !got[1].isResize() || got[1].width != 38 || got[1].height != 9 {
		t.Errorf("middle chunk %+v, want the 38x9 resize between the segments", got[1])
	}
	if !bytes.Equal(got[2].data, []byte("laid out at the new width\r\n")) {
		t.Errorf("last segment %q, want the bytes after the resize", got[2].data)
	}
}

func TestRolledCatchUpStartsAtTheRingsWidth(t *testing.T) {
	p := newResizablePTY(t)
	p.appendAndBroadcast([]byte("gone once the ring rolls\r\n"))
	if err := p.Resize(38, 9); err != nil {
		t.Fatalf("resize: %v", err)
	}
	// Rolls the ring far past the resize, so the mark survives only as the
	// width the ring's first byte was laid out at.
	filler := bytes.Repeat([]byte("x"), 70*1024)
	p.appendAndBroadcast(filler)

	got := chunks(p.Subscribe("client-1", 1))
	if len(got) != 2 {
		t.Fatalf("catch-up came as %d chunks, want the width and one resynced segment", len(got))
	}
	if !got[0].isResize() || got[0].width != 38 || got[0].height != 9 {
		t.Errorf("first chunk %+v, want the 38x9 width the replay is laid out at", got[0])
	}
	if !bytes.HasPrefix(got[1].data, resyncPrefix) {
		t.Errorf("rolled catch-up did not begin with the resync prefix")
	}
}
