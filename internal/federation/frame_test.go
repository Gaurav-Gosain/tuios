package federation

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, frameData, 7, []byte("hello")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	f, err := readFrame(&buf)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if f.Type != frameData {
		t.Errorf("type is %v, want data", f.Type)
	}
	if f.Stream != 7 {
		t.Errorf("stream is %d, want 7", f.Stream)
	}
	if string(f.Payload) != "hello" {
		t.Errorf("payload is %q, want %q", f.Payload, "hello")
	}
}

// TestReadFrameRefusesOversizedLength is the memory guard. A peer is untrusted,
// so the length field it sends is an allocation it would otherwise choose. The
// header here claims 64 MiB and carries no payload at all: a build that trusted
// the field would try to allocate and then block reading bytes that never come.
func TestReadFrameRefusesOversizedLength(t *testing.T) {
	var hdr [frameHeaderSize]byte
	hdr[0] = byte(frameData)
	binary.BigEndian.PutUint32(hdr[1:5], 1)
	binary.BigEndian.PutUint32(hdr[5:9], 64<<20)

	_, err := readFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("readFrame accepted a 64 MiB length, err = %v, want ErrFrameTooLarge", err)
	}
}

// TestReadFrameRefusesUnknownType keeps a frame this build cannot skip from
// being skipped wrongly: an unknown type has an unknown meaning, and guessing
// would resynchronise the stream on the wrong byte.
func TestReadFrameRefusesUnknownType(t *testing.T) {
	var hdr [frameHeaderSize]byte
	hdr[0] = 99
	_, err := readFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrBadFrameType) {
		t.Fatalf("readFrame accepted frame type 99, err = %v, want ErrBadFrameType", err)
	}
}

// TestWriteFrameRefusesOversizedPayload keeps this side from emitting a frame
// the other side is required to refuse.
func TestWriteFrameRefusesOversizedPayload(t *testing.T) {
	var buf bytes.Buffer
	err := writeFrame(&buf, frameData, 1, make([]byte, MaxFramePayload+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("writeFrame accepted an oversized payload, err = %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("writeFrame wrote %d bytes for a refused frame, want 0", buf.Len())
	}
}
