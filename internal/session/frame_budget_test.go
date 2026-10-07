package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"runtime"
	"testing"
	"time"
)

// The limits in frame_budget.go are a security boundary: any process that can
// reach the daemon's socket can send a frame header, and before them a header
// alone made the daemon allocate the 16 MiB it announced. This test holds the
// frame reader and the connection cap to each limit.
//
// The ways this could fail, written down before the code:
//
//  1. The body is still allocated from the header: a frame that announces
//     16 MiB and sends 1 KiB costs megabytes. Measured with the allocator's
//     own count, which sees an allocation whether or not it is touched.
//  2. The buffer grows but loses or reorders bytes at a chunk edge: a large
//     frame must read back byte for byte, at sizes on and around every edge.
//  3. A short request type still takes 16 MiB: a hello over the short limit
//     must be refused unread, with the stream in step after it.
//  4. The budget is not charged, or not given back: with the budget spent,
//     a large frame must be refused with the stream in step; with it free,
//     the same frame must be read, and the budget must be whole again after
//     the caller releases what the frame held, on success and on failure.
//  5. A frame small enough to be every keystroke is charged, so a spent
//     budget would stop typing.
//  6. The connection cap is off by one, or a refused connection is counted
//     and never given back.

// frameHeader is the length prefix, type and codec of an untagged frame with
// a payload of n bytes.
func frameHeader(t MessageType, n int) []byte {
	h := make([]byte, 6)
	binary.BigEndian.PutUint32(h, uint32(2+n))
	h[4], h[5] = byte(t), wireCodecGob
	return h
}

// readOne reads one frame from r the way the daemon does.
func readOne(r io.Reader, budget memBudget) (*Message, int, error) {
	var n uint32
	if err := binary.Read(r, binary.BigEndian, &n); err != nil {
		return nil, 0, err
	}
	return readFrameBody(r, n, daemonFrameLimit, budget)
}

func TestFrameBodyGrowsWithWhatArrives(t *testing.T) {
	t.Run("a header that announces 16 MiB costs what was sent", func(t *testing.T) {
		const sent = 1 << 10
		frame := append(frameHeader(MsgInput, maxFrameBytes-2), make([]byte, sent)...)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_, _, err := readOne(bytes.NewReader(frame), nil)
		runtime.ReadMemStats(&after)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("a cut frame read as %v, want an unexpected EOF", err)
		}
		if got := after.TotalAlloc - before.TotalAlloc; got > 64<<10 {
			t.Fatalf("reading 1 KiB of a frame that announced 16 MiB allocated %d bytes", got)
		}
	})

	t.Run("a large frame reads back byte for byte at every chunk edge", func(t *testing.T) {
		sizes := []int{0, 1, firstFrameChunk - 1, firstFrameChunk, firstFrameChunk + 1,
			largeFrame - 1, largeFrame, largeFrame + 1, frameGrowStep + 1, 3*frameGrowStep + 7, maxFrameBytes - 2}
		for _, n := range sizes {
			p := make([]byte, n)
			for i := range p {
				p[i] = byte(i*7 + i>>13)
			}
			var buf bytes.Buffer
			if err := WriteMessage(&buf, &Message{Type: MsgInput, Payload: p}); err != nil {
				t.Fatal(err)
			}
			budget := newMemBudget()
			msg, held, err := readOne(&buf, budget)
			if err != nil {
				t.Fatalf("%d bytes: %v", n, err)
			}
			if !bytes.Equal(msg.Payload, p) {
				t.Fatalf("%d bytes read back different", n)
			}
			budget.release(held)
			if len(budget) != 0 {
				t.Fatalf("%d bytes: %d budget chunks still held after release", n, len(budget))
			}
		}
	})
}

func TestFrameLimitRefusesShortRequestsUnread(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(frameHeader(MsgHello, maxRequestFrame))
	buf.Write(make([]byte, maxRequestFrame))
	if err := WriteMessage(&buf, &Message{Type: MsgList}); err != nil {
		t.Fatal(err)
	}
	_, _, err := readOne(&buf, newMemBudget())
	if _, ok := errors.AsType[*FrameTooLargeError](err); !ok {
		t.Fatalf("a hello of %d bytes read as %v, want a FrameTooLargeError", maxRequestFrame+2, err)
	}
	next, _, err := readOne(&buf, nil)
	if err != nil || next.Type != MsgList {
		t.Fatalf("the frame after the refused hello read as %+v, %v", next, err)
	}
}

func TestFrameBudgetBoundsLargeFrames(t *testing.T) {
	large := make([]byte, 4<<20)
	frames := func() *bytes.Buffer {
		var buf bytes.Buffer
		_ = WriteMessage(&buf, &Message{Type: MsgInput, Payload: large})
		_ = WriteMessage(&buf, &Message{Type: MsgList, ReqID: 7})
		return &buf
	}

	t.Run("a spent budget refuses a large frame and keeps the stream in step", func(t *testing.T) {
		budget := newMemBudget()
		spent, ok := budget.acquire(readBudgetBytes, time.Now().Add(time.Second))
		if !ok {
			t.Fatal("could not spend the budget")
		}
		buf := frames()
		start := time.Now()
		_, held, err := readOne(buf, budget)
		if _, ok := errors.AsType[*FrameBusyError](err); !ok {
			t.Fatalf("a large frame on a spent budget read as %v, want a FrameBusyError", err)
		}
		if held != 0 {
			t.Fatalf("a refused frame holds %d chunks", held)
		}
		if waited := time.Since(start); waited < readBudgetWait/2 {
			t.Fatalf("the refusal came after %v, before the budget wait", waited)
		}
		if len(budget) != spent {
			t.Fatalf("the budget holds %d chunks after the refusal, want the %d spent", len(budget), spent)
		}
		next, _, err := readOne(buf, budget)
		if err != nil || next.Type != MsgList || next.ReqID != 7 {
			t.Fatalf("the frame after the refused one read as %+v, %v", next, err)
		}
	})

	t.Run("a free budget reads the frame and charges it until release", func(t *testing.T) {
		budget := newMemBudget()
		msg, held, err := readOne(frames(), budget)
		if err != nil || len(msg.Payload) != len(large) {
			t.Fatalf("read %v", err)
		}
		if want := (len(large) - largeFrame) * frameCopies / readBudgetChunk; held < want {
			t.Fatalf("a %d byte frame holds %d chunks, want at least %d", len(large), held, want)
		}
		if len(budget) != held {
			t.Fatalf("the budget holds %d chunks, the frame says %d", len(budget), held)
		}
		budget.release(held)
		if len(budget) != 0 {
			t.Fatalf("%d chunks held after release", len(budget))
		}
	})

	t.Run("a frame cut short gives back what it took", func(t *testing.T) {
		budget := newMemBudget()
		cut := frames().Bytes()[:2<<20]
		_, held, err := readOne(bytes.NewReader(cut), budget)
		if err == nil || held != 0 || len(budget) != 0 {
			t.Fatalf("a cut frame: err %v, held %d, budget %d", err, held, len(budget))
		}
	})

	t.Run("a keystroke frame is never charged", func(t *testing.T) {
		budget := newMemBudget()
		if _, ok := budget.acquire(readBudgetBytes, time.Now().Add(time.Second)); !ok {
			t.Fatal("could not spend the budget")
		}
		var buf bytes.Buffer
		_ = WritePTYInput(&buf, "00000000-0000-0000-0000-000000000000", make([]byte, largeFrame-64))
		start := time.Now()
		if _, held, err := readOne(&buf, budget); err != nil || held != 0 {
			t.Fatalf("a small input frame on a spent budget: held %d, %v", held, err)
		}
		if time.Since(start) > readBudgetWait/2 {
			t.Fatal("a small input frame waited for the budget")
		}
	})
}

func TestConnectionCap(t *testing.T) {
	d := &Daemon{}
	var conns []net.Conn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := range maxConnections {
		a, b := net.Pipe()
		conns = append(conns, a, b)
		if !d.admitConnection(a) {
			t.Fatalf("connection %d of %d was refused", i+1, maxConnections)
		}
	}
	a, b := net.Pipe()
	conns = append(conns, b)
	if d.admitConnection(a) {
		t.Fatalf("connection %d was admitted over the cap", maxConnections+1)
	}
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := b.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("a refused connection is not closed: %v", err)
	}
	if got := d.openConns.Load(); got != maxConnections {
		t.Fatalf("%d connections counted after a refusal, want %d", got, maxConnections)
	}
	d.openConns.Add(-1)
	a, b = net.Pipe()
	conns = append(conns, a, b)
	if !d.admitConnection(a) {
		t.Fatal("a connection was refused after another one ended")
	}
}
