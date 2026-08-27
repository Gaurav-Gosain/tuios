// Package federation carries the link layer between one tuios daemon and the
// daemons of other machines the user has named in config.
//
// The shape, from ~/tuios-federation.md sections 2 and 4: the local daemon is a
// hub that dials out over ssh, remote daemons are passive and never dial back,
// and there is no mesh. A link is an `ssh <addr> tuios stdio-proxy` child
// process whose stdio carries the framing in this file, multiplexed so one ssh
// connection can hold several logical streams (stage 1 uses one, the control
// stream).
//
// Stage 1 is read-only. Nothing in this package sends a verb that mutates
// remote state; the only calls made are listings and the hello handshake.
// Everything that comes back is untrusted data from another machine: it is
// bounded on the way in, and it is never interpreted as an instruction.
//
// This package deliberately imports nothing from internal/session. The daemon
// owns the manager and adapts its reports onto the verb protocol, so the
// dependency runs one way and there is no cycle.
package federation

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The wire is a stream of frames. Each frame is a 9 byte header followed by its
// payload:
//
//	byte 0     frame type
//	bytes 1-4  stream id, big endian
//	bytes 5-8  payload length, big endian
//
// ssh already provides integrity and confidentiality, so the framing carries no
// checksum and no sequence number of its own. It exists only to interleave
// several logical connections on one pipe.
const (
	frameHeaderSize = 9

	// MaxFramePayload bounds one frame's payload. A remote peer is untrusted,
	// so a length field is a memory allocation it gets to choose; this is the
	// ceiling on that choice. Larger writes are split across frames by the
	// stream, so the cap costs nothing in what can be sent.
	MaxFramePayload = 1 << 20 // 1 MiB
)

// frameType names what a frame does.
type frameType uint8

const (
	// frameOpen asks the peer to open the named stream. Payload is empty.
	frameOpen frameType = 1
	// frameData carries stream bytes.
	frameData frameType = 2
	// frameClose says the sender is done with the stream. Payload is empty.
	frameClose frameType = 3
)

func (t frameType) String() string {
	switch t {
	case frameOpen:
		return "open"
	case frameData:
		return "data"
	case frameClose:
		return "close"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(t))
	}
}

// frame is one decoded frame.
type frame struct {
	Type    frameType
	Stream  uint32
	Payload []byte
}

// ErrFrameTooLarge reports a header whose length field is past MaxFramePayload.
// It is a protocol error and it kills the link: a peer that asked for more than
// the ceiling cannot be resynchronised, because the byte after the frame is
// wherever its payload actually ended.
var ErrFrameTooLarge = errors.New("federation: frame payload is larger than the limit")

// ErrBadFrameType reports a frame type this build does not know. Like an
// oversized length it is unrecoverable, because a frame that is not understood
// still has to be skipped exactly.
var ErrBadFrameType = errors.New("federation: unknown frame type")

// writeFrame encodes one frame onto w. The caller serialises calls; a mux holds
// a write mutex for exactly this reason, since two interleaved frames would be
// unparseable.
func writeFrame(w io.Writer, t frameType, stream uint32, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return ErrFrameTooLarge
	}
	var hdr [frameHeaderSize]byte
	hdr[0] = byte(t)
	binary.BigEndian.PutUint32(hdr[1:5], stream)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// readFrame decodes one frame from r. The returned payload is a fresh slice, so
// the caller may hand it to another goroutine.
func readFrame(r io.Reader) (frame, error) {
	var hdr [frameHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return frame{}, err
	}
	t := frameType(hdr[0])
	switch t {
	case frameOpen, frameData, frameClose:
	default:
		return frame{}, fmt.Errorf("%w: %d", ErrBadFrameType, hdr[0])
	}
	stream := binary.BigEndian.Uint32(hdr[1:5])
	n := binary.BigEndian.Uint32(hdr[5:9])
	if n > MaxFramePayload {
		return frame{}, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, n)
	}
	f := frame{Type: t, Stream: stream}
	if n > 0 {
		f.Payload = make([]byte, n)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return frame{}, err
		}
	}
	return f, nil
}
