package session

// WriteMessage carries every gob frame: state syncs, terminal-state snapshots
// and every control message. The callers hand it the raw unix socket, so each
// Write it makes is a syscall made under the caller's send lock.

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
)

// wantControlFrame builds a control frame by hand from the documented layout:
// a 4-byte big-endian length, the type byte, codec byte 0, then the payload.
func wantControlFrame(msgType MessageType, payload []byte) []byte {
	frame := make([]byte, 6, 6+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(2+len(payload)))
	frame[4] = byte(msgType)
	frame[5] = 0
	return append(frame, payload...)
}

// controlFramePayloads covers an empty payload, a small one and one larger
// than a socket buffer.
func controlFramePayloads() [][]byte {
	large := make([]byte, 512*1024)
	for i := range large {
		large[i] = byte(i)
	}
	return [][]byte{nil, {}, []byte("x"), []byte("a gob payload"), large}
}

// TestControlFrameBytesUnchanged pins the wire bytes of a control frame to the
// documented layout, whatever number of writes put them there.
func TestControlFrameBytesUnchanged(t *testing.T) {
	for _, payload := range controlFramePayloads() {
		var raw bytesWriter
		if err := WriteMessage(&raw, &Message{Type: MsgStateSync, Payload: payload}); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
		if want := wantControlFrame(MsgStateSync, payload); !bytes.Equal(raw.b, want) {
			t.Fatalf("frame for a %d byte payload is %d bytes and differs from the documented layout (want %d bytes)",
				len(payload), len(raw.b), len(want))
		}
	}
}

// TestControlFrameHeaderIsOneWrite checks that the length and the two header
// bytes go out together. They used to be two separate writes before the
// payload, three syscalls per frame on a socket. On a socket the header and
// payload now leave in one writev; a plain writer sees at most two writes.
func TestControlFrameHeaderIsOneWrite(t *testing.T) {
	for _, payload := range controlFramePayloads() {
		cw := &countingWriter{w: io.Discard}
		if err := WriteMessage(cw, &Message{Type: MsgStateSync, Payload: payload}); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
		want := 2
		if len(payload) == 0 {
			want = 1
		}
		if cw.writes > want {
			t.Errorf("a %d byte payload took %d writes, want at most %d", len(payload), cw.writes, want)
		}
		if cw.bytes != 6+len(payload) {
			t.Errorf("a %d byte payload wrote %d bytes, want %d", len(payload), cw.bytes, 6+len(payload))
		}
	}
}

// TestControlFrameRoundTripsOverASocket writes frames on a real unix socket,
// which is the path that takes writev, and reads them back with the reader
// both read loops use.
func TestControlFrameRoundTripsOverASocket(t *testing.T) {
	client, server := socketPair(t)
	payloads := controlFramePayloads()

	errs := make(chan error, 1)
	go func() {
		for _, payload := range payloads {
			if err := WriteMessage(client, &Message{Type: MsgTerminalState, Payload: payload}); err != nil {
				errs <- err
				return
			}
		}
		errs <- nil
	}()

	for _, payload := range payloads {
		msg, err := ReadMessage(server)
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if msg.Type != MsgTerminalState {
			t.Fatalf("type %d, want %d", msg.Type, MsgTerminalState)
		}
		if !bytes.Equal(msg.Payload, payload) {
			t.Fatalf("payload of %d bytes came back as %d bytes", len(payload), len(msg.Payload))
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
}

// BenchmarkGobFrameWrite measures one gob frame going out on a unix socket,
// for a small control message and for a state-sync-sized payload. The name is
// short on purpose: the socket lives under the benchmark's TempDir, and macOS
// caps a unix socket path at 104 bytes.
func BenchmarkGobFrameWrite(b *testing.B) {
	for _, size := range []int{64, 4096, 65536} {
		b.Run(sizeName(size), func(b *testing.B) {
			client, server := socketPair(b)
			go drainConn(server)
			msg := &Message{Type: MsgStateSync, Payload: make([]byte, size)}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for b.Loop() {
				if err := WriteMessage(client, msg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
