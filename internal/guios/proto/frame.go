package proto

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

// Frames of an attach connection: a kind, the length of the payload as a
// big-endian uint32, and the payload.
const (
	// FrameOutput carries frames of the session's terminal (daemon →
	// client), a terminal.Mirror's: the first, after the Welcome, starts
	// the client's terminal over from what the session shows, and the
	// others bring it to what it shows after output. The client's terminal
	// shows them as it would a program's output.
	FrameOutput byte = 'o'
	// FrameInput carries what the user typed (client → daemon).
	FrameInput byte = 'i'
	// FrameResize carries the terminal's size: cols, rows and a cell's
	// width and height in pixels as big-endian uint16s (client → daemon),
	// which programs asking the size in pixels, or taking the mouse in
	// pixels (mode 1016), need.
	FrameResize byte = 'r'
	// FrameClosed tells the client the session is gone (daemon → client).
	FrameClosed byte = 'c'
	// FrameResized tells the client that the session's terminal took a
	// size, cols and rows as big-endian uint16s (daemon → client), between the frames
	// of the old size and those of the new: the client's terminal takes it
	// there.
	FrameResized byte = 'z'
	// FrameClear asks the session's terminal to clear as the user asked
	// the client's (client → daemon): its payload is ClearScreen or
	// ClearScrollback. The session's terminal clears, not the client's,
	// which shows it with the next frame.
	FrameClear byte = 'k'
	// FrameTheme carries the colors the client's terminal shows (client →
	// daemon), a Theme's Payload, which the daemon's terminals answer
	// programs' questions about the colors with.
	FrameTheme byte = 't'
	// FrameClaim tells the daemon the user is at the client's terminal
	// (client → daemon), shown as they use its machine, with no payload:
	// the session takes the size it asked for last, as it does once the
	// user types in it, and the sizes other clients ask for wait until they
	// claim it again; it goes back to the client claimed in before as this
	// one detaches. A daemon before it ignores it.
	FrameClaim byte = 'm'
)

// What a FrameClear clears.
const (
	// ClearScreen: the scrollback and the screen above the cursor's line,
	// which moves to the top, as ⌘K and the context menu's Clear do
	// (terminal.ClearScreen).
	ClearScreen byte = 's'
	// ClearScrollback: the scrollback (terminal.ClearScrollback), which
	// older apps' context menu asked with Clear Scrollback; kerod still
	// clears it for them.
	ClearScrollback byte = 'b'
)

// maxFrame bounds a frame's payload, against a corrupt stream.
const maxFrame = 16 << 20

// WriteFrame writes one frame. Callers serialize writes to a connection.
func WriteFrame(w io.Writer, kind byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	buf[0] = kind
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// maxOutput bounds the payload of a FrameOutput, under maxFrame: output
// past it goes in frames of its own, which the client reads as one stream.
const maxOutput = 1 << 20

// AppendFrames appends the frames that carry payload: output past
// maxOutput, as a terminal's frame with the pixels of images, goes in
// frames of its own.
func AppendFrames(dst []byte, kind byte, payload []byte) []byte {
	for {
		n := len(payload)
		if kind == FrameOutput {
			n = min(n, maxOutput)
		}
		dst = append(dst, kind, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		dst = append(dst, payload[:n]...)
		payload = payload[n:]
		if len(payload) == 0 {
			return dst
		}
	}
}

// ReadFrame reads one frame.
func ReadFrame(r io.Reader) (kind byte, payload []byte, err error) {
	var hdr [5]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return 0, nil, errors.New("proto: frame too large")
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}

// ResizePayload encodes a size for FrameResized.
func ResizePayload(cols, rows int) []byte {
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:], uint16(max(1, min(cols, 0xFFFF))))
	binary.BigEndian.PutUint16(b[2:], uint16(max(1, min(rows, 0xFFFF))))
	return b[:]
}

// ParseResize decodes a FrameResized payload.
func ParseResize(p []byte) (cols, rows int, ok bool) {
	if len(p) != 4 {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint16(p[0:])), int(binary.BigEndian.Uint16(p[2:])), true
}

// Theme is the colors programs ask a terminal about (OSC 4, 10, 11, 12),
// as RGBA: a Cursor with A 0 is the foreground.
type Theme struct {
	Foreground, Background, Cursor [4]byte
	Palette                        [16][4]byte
}

// Payload encodes the theme for FrameTheme.
func (t Theme) Payload() []byte {
	b := make([]byte, 0, 4*19)
	b = append(b, t.Foreground[:]...)
	b = append(b, t.Background[:]...)
	b = append(b, t.Cursor[:]...)
	for _, c := range t.Palette {
		b = append(b, c[:]...)
	}
	return b
}

// ParseTheme decodes a FrameTheme payload.
func ParseTheme(p []byte) (t Theme, ok bool) {
	if len(p) != 4*19 {
		return t, false
	}
	colors := append([]*[4]byte{&t.Foreground, &t.Background, &t.Cursor}, make([]*[4]byte, 16)...)
	for i := range t.Palette {
		colors[3+i] = &t.Palette[i]
	}
	for i, c := range colors {
		copy(c[:], p[4*i:])
	}
	return t, true
}

// ResizePixelsPayload encodes a size for FrameResize: a cell's size in
// pixels is 0 when unknown.
func ResizePixelsPayload(cols, rows, cellWidth, cellHeight int) []byte {
	b := make([]byte, 8)
	copy(b, ResizePayload(cols, rows))
	binary.BigEndian.PutUint16(b[4:], uint16(max(0, min(cellWidth, 0xFFFF))))
	binary.BigEndian.PutUint16(b[6:], uint16(max(0, min(cellHeight, 0xFFFF))))
	return b
}

// ParseResizePixels decodes a FrameResize payload.
func ParseResizePixels(p []byte) (cols, rows, cellWidth, cellHeight int, ok bool) {
	if len(p) != 8 {
		return 0, 0, 0, 0, false
	}
	cols, rows, _ = ParseResize(p[:4])
	return cols, rows, int(binary.BigEndian.Uint16(p[4:])), int(binary.BigEndian.Uint16(p[6:])), true
}

// MaxLine bounds a JSON line of a control connection: kerod's state, sent
// whole, and a request carrying a pasted file (paste.file).
const MaxLine = 64 << 20

// ErrLineTooLong is ReadLine's for a line past its bound.
var ErrLineTooLong = errors.New("proto: line too long")

// ReadLine reads a line of at most max bytes, its newline included, so
// that the other side of a connection, as a host far away, cannot make
// this one hold a line without end.
func ReadLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > max {
			return nil, ErrLineTooLong
		}
		line = append(line, chunk...)
		if err == nil {
			return line, nil
		}
		if err != bufio.ErrBufferFull {
			return line, err
		}
	}
}
