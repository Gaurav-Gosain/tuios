package session

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// The client end of stream-pane, for a viewer that is not a tuios client.
//
// After the verb's one reply line the connection stops speaking JSON and
// carries frames both ways: a type byte, a big-endian u32 length, the payload
// (docs/protocol.md, "stream-pane"). A VerbClient that opened a stream hands
// its connection to the PaneStream and takes no more calls.

// Frame types of a pane stream.
const (
	PaneFrameSnapshot = 'S'
	PaneFrameOutput   = 'O'
	PaneFrameResize   = 'R'
	PaneFrameError    = 'E'
	PaneFrameEnd      = 'X'
	PaneFrameInput    = 'I'
	PaneFrameLease    = 'L'
)

// paneFrameMax bounds a frame this client reads. The daemon's largest is a
// snapshot: 160 bytes a cell at most, for a 4096-square screen and history.
// Anything larger is a broken stream, not a frame to allocate for.
const paneFrameMax = 256 << 20

// PaneStreamMaxInput is the most input one frame may carry. The daemon ends
// a stream on a client frame over 64 KiB.
const PaneStreamMaxInput = 32 << 10

// PaneStreamReply is the verb's reply line.
type PaneStreamReply struct {
	Mode      string `json:"mode"`
	Seq       uint64 `json:"seq"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
	BootID    string `json:"boot_id"`
	Session   string `json:"session"`
	SessionID string `json:"session_id"`
	Window    string `json:"window"`
	Title     string `json:"title"`
}

// PaneStream is one open stream-pane connection.
type PaneStream struct {
	conn net.Conn
	r    *bufio.Reader
	wmu  sync.Mutex
}

// OpenPaneStream calls stream-pane with params and, on success, turns this
// client's connection into the stream. The client takes no more calls.
func (c *VerbClient) OpenPaneStream(params map[string]any) (*PaneStream, PaneStreamReply, error) {
	var reply PaneStreamReply
	raw, err := c.CallWithTimeout("stream-pane", params, 10*time.Second)
	if err != nil {
		return nil, reply, err
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, reply, fmt.Errorf("failed to read the stream-pane reply: %w", err)
	}
	c.callMu.Lock()
	c.broken = fmt.Errorf("the connection carries a pane stream")
	c.callMu.Unlock()
	_ = c.conn.SetReadDeadline(time.Time{})
	_ = c.conn.SetWriteDeadline(time.Time{})
	return &PaneStream{conn: c.conn, r: c.r}, reply, nil
}

// ReadFrame reads the next frame from the daemon.
func (s *PaneStream) ReadFrame() (byte, []byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(s.r, head[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > paneFrameMax {
		return 0, nil, fmt.Errorf("a %c frame of %d bytes is larger than any the daemon sends", head[0], n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(s.r, payload); err != nil {
		return 0, nil, err
	}
	return head[0], payload, nil
}

// WriteFrame sends one frame to the daemon. It is safe from several
// goroutines.
func (s *PaneStream) WriteFrame(kind byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := s.conn.Write(buf)
	return err
}

// Input types bytes into the pane, split into frames the daemon takes.
func (s *PaneStream) Input(b []byte) error {
	for len(b) > 0 {
		n := min(len(b), PaneStreamMaxInput)
		if err := s.WriteFrame(PaneFrameInput, b[:n]); err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}

// Lease holds the pane at most at cols by rows. Zero releases the lease.
func (s *PaneStream) Lease(cols, rows int) error {
	var p [4]byte
	binary.BigEndian.PutUint16(p[0:2], uint16(max(0, min(cols, 4096))))
	binary.BigEndian.PutUint16(p[2:4], uint16(max(0, min(rows, 4096))))
	return s.WriteFrame(PaneFrameLease, p[:])
}

// Close ends the stream.
func (s *PaneStream) Close() error {
	return s.conn.Close()
}

// PaneFrameSeq reads the seq that S, O and R frames start with.
func PaneFrameSeq(payload []byte) (uint64, bool) {
	if len(payload) < 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(payload[:8]), true
}

// PaneFrameSize reads the cols and rows of an S or R frame.
func PaneFrameSize(payload []byte) (int, int, bool) {
	if len(payload) < 12 {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint16(payload[8:10])), int(binary.BigEndian.Uint16(payload[10:12])), true
}
