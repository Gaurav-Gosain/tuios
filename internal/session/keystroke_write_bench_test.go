package session

// What a keystroke costs on the way out, as opposed to what it weighs.
//
// BenchmarkWireKeystroke/raw-input reports the size of the frame and nothing
// else: it does arithmetic on the header and returns, so the one number nobody
// had was what it costs to actually put that frame on the socket. The client
// holds a whole-client mutex across the write (tuiclient.go), so this is time
// the next keystroke waits for, and input latency is the thing the maintainer
// says is most perceptible.
//
// The benchmark runs over a real unix socket rather than net.Pipe, because the
// cost being measured is syscalls and net.Pipe has none.

import (
	"io"
	"net"
	"path/filepath"
	"testing"
)

// countingWriter counts Write calls, which on an unbuffered socket is the
// syscall count.
type countingWriter struct {
	w      io.Writer
	writes int
	bytes  int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes++
	c.bytes += len(p)
	return c.w.Write(p)
}

// socketPair returns two ends of a connected unix socket, which is what a
// client and the daemon actually talk over.
func socketPair(tb testing.TB) (client, server net.Conn) {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		tb.Skipf("unix sockets unavailable: %v", err)
	}
	tb.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()

	client, err = net.Dial("unix", path)
	if err != nil {
		tb.Skipf("unix dial failed: %v", err)
	}
	tb.Cleanup(func() { _ = client.Close() })

	server = <-accepted
	if server == nil {
		tb.Skip("unix accept failed")
	}
	tb.Cleanup(func() { _ = server.Close() })
	return client, server
}

// drainConn reads and discards until the connection closes, so the writer never
// blocks on a full socket buffer.
func drainConn(c net.Conn) {
	buf := make([]byte, 64*1024)
	for {
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
}

// BenchmarkKeystrokeWrite measures one keypress going out on the wire, and
// reports how many writes it took, which on an unbuffered socket is how many
// syscalls the user waited for.
func BenchmarkKeystrokeWrite(b *testing.B) {
	const ptyID = "0123456789abcdef0123456789abcdef0123"

	b.Run("count", func(b *testing.B) {
		cw := &countingWriter{w: io.Discard}
		if err := WritePTYInput(cw, ptyID, []byte("a")); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(cw.writes), "writes/key")
		b.ReportMetric(float64(cw.bytes), "bytes/key")
	})

	b.Run("socket", func(b *testing.B) {
		client, server := socketPair(b)
		go drainConn(server)
		key := []byte("a")
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if err := WritePTYInput(client, ptyID, key); err != nil {
				b.Fatal(err)
			}
		}
	})

	// A paste or a held key arrives as a run of bytes rather than one, which
	// is the same frame with a longer tail.
	b.Run("socket-paste", func(b *testing.B) {
		client, server := socketPair(b)
		go drainConn(server)
		key := make([]byte, 256)
		for i := range key {
			key[i] = 'x'
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if err := WritePTYInput(client, ptyID, key); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkPTYOutputWrite is the same question on the way back: the daemon
// batches a pane's output and then writes the batch. The batching is already
// there (daemon_stream.go), so what this measures is what one batch costs once
// it has been assembled.
func BenchmarkPTYOutputWrite(b *testing.B) {
	const ptyID = "0123456789abcdef0123456789abcdef0123"

	b.Run("count", func(b *testing.B) {
		cw := &countingWriter{w: io.Discard}
		if err := WritePTYOutput(cw, ptyID, make([]byte, 4096)); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(cw.writes), "writes/batch")
	})

	for _, size := range []int{256, 4096, 65536} {
		b.Run(sizeName(size), func(b *testing.B) {
			client, server := socketPair(b)
			go drainConn(server)
			data := make([]byte, size)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for b.Loop() {
				if err := WritePTYOutput(client, ptyID, data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func sizeName(n int) string {
	switch {
	case n >= 1024:
		return "batch-" + itoaBench(n/1024) + "KiB"
	default:
		return "batch-" + itoaBench(n) + "B"
	}
}

func itoaBench(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestBinaryPTYFrameRoundTrips is what a change to the write shape has to keep
// true: the bytes on the wire are the same bytes, whatever number of writes
// they were put there in. It parses the frame back with the daemon's own
// parser rather than a copy of it.
func TestBinaryPTYFrameRoundTrips(t *testing.T) {
	const ptyID = "0123456789abcdef0123456789abcdef0123"
	for _, data := range [][]byte{
		[]byte("a"),
		[]byte("\x1b[A"),
		{},
		make([]byte, 4096),
	} {
		for _, write := range []struct {
			name string
			fn   func(io.Writer, string, []byte) error
		}{
			{"input", WritePTYInput},
			{"output", WritePTYOutput},
		} {
			cw := &countingWriter{w: io.Discard}
			if err := write.fn(cw, ptyID, data); err != nil {
				t.Fatalf("%s: %v", write.name, err)
			}
			if want := 4 + 2 + 36 + len(data); cw.bytes != want {
				t.Errorf("%s frame for %d bytes of data is %d bytes on the wire, want %d",
					write.name, len(data), cw.bytes, want)
			}

			// Reassemble the payload the way the reader does: past the 4-byte
			// length and the 2-byte header, what is left is what the parser gets.
			var raw bytesWriter
			if err := write.fn(&raw, ptyID, data); err != nil {
				t.Fatalf("%s: %v", write.name, err)
			}
			gotID, gotData, err := ParseBinaryPTYMessage(raw.b[6:])
			if err != nil {
				t.Fatalf("%s: parse: %v", write.name, err)
			}
			if gotID != ptyID {
				t.Errorf("%s: pty id round tripped as %q", write.name, gotID)
			}
			if string(gotData) != string(data) {
				t.Errorf("%s: data round tripped as %q, want %q", write.name, gotData, data)
			}
		}
	}
}

// bytesWriter collects everything written, however many writes it arrives in.
type bytesWriter struct{ b []byte }

func (w *bytesWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

// TestShortAndLongPTYIDsArePadded pins the one piece of the frame that is not
// simply copied: the id field is a fixed 36 bytes, zero-padded when the id is
// shorter and truncated when it is longer.
func TestShortAndLongPTYIDsArePadded(t *testing.T) {
	for _, id := range []string{"", "short", "0123456789abcdef0123456789abcdef0123", "way-too-long-to-fit-in-thirty-six-bytes-x"} {
		var raw bytesWriter
		if err := WritePTYInput(&raw, id, []byte("k")); err != nil {
			t.Fatal(err)
		}
		if got := len(raw.b); got != 4+2+36+1 {
			t.Errorf("id %q produced a %d byte frame, want %d", id, got, 4+2+36+1)
		}
		want := id
		if len(want) > 36 {
			want = want[:36]
		}
		if got := string(raw.b[6 : 6+len(want)]); got != want {
			t.Errorf("id %q landed as %q", id, got)
		}
	}
}
