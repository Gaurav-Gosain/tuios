package session

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// The frame gate in streamPTYOutput holds a flooding pane's output until its
// frame window ends, and must hold nothing else. These tests widen the window
// to a second so that a held frame and an unheld one are far apart on a loaded
// machine: a held frame arrives most of a second late, an unheld one at once.

const gateTestInterval = time.Second

// gateFrame is one message the stream wrote, and when the test read it.
type gateFrame struct {
	typ  MessageType
	data []byte
	at   time.Time
}

// gateRig is a stream goroutine writing to one end of a pipe for a PTY with no
// reader of its own, and a reader on the other end collecting frames.
type gateRig struct {
	pty    *PTY
	frames chan gateFrame
}

func newGateRig(t *testing.T) *gateRig {
	t.Helper()
	oldInterval := streamFrameInterval
	streamFrameInterval = gateTestInterval
	t.Cleanup(func() { streamFrameInterval = oldInterval })

	d := NewDaemon(&DaemonConfig{})
	server, client := net.Pipe()
	cs := &connState{
		conn:             server,
		clientID:         "gate-client",
		done:             make(chan struct{}),
		ptySubscriptions: make(map[string]struct{}),
	}
	pty := &PTY{
		ID:           "ptytest-00000003",
		subscribers:  make(map[string]*ptySubscriber),
		outputBuffer: make([]byte, 256*1024),
	}
	cs.ptySubscriptions[pty.ID] = struct{}{}

	rig := &gateRig{pty: pty, frames: make(chan gateFrame, 64)}
	go func() {
		for {
			msg, err := ReadMessage(client)
			if err != nil {
				return
			}
			f := gateFrame{typ: msg.Type, at: time.Now()}
			if msg.Type == MsgPTYOutput {
				_, f.data, _ = ParseBinaryPTYMessage(msg.Payload)
				f.data = bytes.Clone(f.data)
			}
			rig.frames <- f
		}
	}()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		d.streamPTYOutput(cs, pty, pty.Subscribe(cs.clientID, 0))
	}()
	// Runs before the cleanup that puts the interval back, and waits for the
	// stream to return so nothing still reads the interval when it changes.
	t.Cleanup(func() {
		cs.drop()
		_ = client.Close()
		d.cancel()
		<-stopped
	})
	return rig
}

// next returns the next frame, failing the test if none comes in time.
func (r *gateRig) next(t *testing.T) gateFrame {
	t.Helper()
	select {
	case f := <-r.frames:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame from the stream")
		return gateFrame{}
	}
}

// flood sends one chunk big enough to fill a frame window, and returns once
// the client has it.
func (r *gateRig) flood(t *testing.T) {
	t.Helper()
	r.pty.appendAndBroadcast(bytes.Repeat([]byte("x"), 2*streamFloodBytes))
	if f := r.next(t); len(f.data) != 2*streamFloodBytes {
		t.Fatalf("flood frame carried %d bytes, want %d", len(f.data), 2*streamFloodBytes)
	}
}

// TestStreamHoldsAFloodToOneFramePerWindow pins the gate itself: bytes that
// follow a full window inside that window wait for it to end, and go out in
// one frame rather than one per chunk.
func TestStreamHoldsAFloodToOneFramePerWindow(t *testing.T) {
	r := newGateRig(t)
	r.flood(t)
	sent := time.Now()
	for range 10 {
		r.pty.appendAndBroadcast(bytes.Repeat([]byte("y"), 100))
	}
	f := r.next(t)
	if waited := f.at.Sub(sent); waited < gateTestInterval/2 {
		t.Errorf("output after a full window went out after %v, want it held for the window", waited)
	}
	if len(f.data) != 1000 {
		t.Errorf("held output came out as a %d byte frame, want the 10 chunks in one 1000 byte frame", len(f.data))
	}
}

// TestStreamDoesNotHoldSmallOutput is the keystroke case: output that never
// fills a window goes out as it comes, however close together.
func TestStreamDoesNotHoldSmallOutput(t *testing.T) {
	r := newGateRig(t)
	for i := range 5 {
		sent := time.Now()
		r.pty.appendAndBroadcast([]byte{'a' + byte(i)})
		f := r.next(t)
		if waited := f.at.Sub(sent); waited > gateTestInterval/2 {
			t.Fatalf("echo %d waited %v, want it sent at once", i, waited)
		}
	}
}

// TestStreamDoesNotHoldOutputAfterAQuietWindow is a keystroke after a flood:
// once the window has passed, one byte goes out at once.
func TestStreamDoesNotHoldOutputAfterAQuietWindow(t *testing.T) {
	r := newGateRig(t)
	r.flood(t)
	time.Sleep(gateTestInterval + 50*time.Millisecond)
	sent := time.Now()
	r.pty.appendAndBroadcast([]byte("k"))
	f := r.next(t)
	if waited := f.at.Sub(sent); waited > gateTestInterval/2 {
		t.Errorf("a byte after a quiet window waited %v, want it sent at once", waited)
	}
	if string(f.data) != "k" {
		t.Errorf("frame carried %q, want %q", f.data, "k")
	}
}

// TestStreamDoesNotHoldAResize: a resize is sent at once even straight after a
// full window, since it carries no bytes and ends a batch anyway.
func TestStreamDoesNotHoldAResize(t *testing.T) {
	r := newGateRig(t)
	r.flood(t)
	sent := time.Now()
	r.pty.broadcast(ptyChunk{width: 100, height: 30}, r.pty.outputSeq)
	f := r.next(t)
	if f.typ != MsgPTYResized {
		t.Fatalf("got message type %v, want MsgPTYResized", f.typ)
	}
	if waited := f.at.Sub(sent); waited > gateTestInterval/2 {
		t.Errorf("a resize after a full window waited %v, want it sent at once", waited)
	}
}
