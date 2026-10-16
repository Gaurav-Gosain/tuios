package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
)

// BenchmarkPTYOutputScan is the per-chunk scanning every PTY read pays before
// its bytes reach anyone: the nesting probe scan in readOutput and the
// graphics cut in broadcast. The chunk sizes are the ones floods deliver. The
// kernel hands the read loop one line at a time when lines are short, so
// `yes` arrives in reads of about 160 bytes and `seq` in about 1 KiB. Only cat
// of long lines fills the 16 KiB buffer.
func BenchmarkPTYOutputScan(b *testing.B) {
	for _, size := range []int{128, 1024, 16 * 1024} {
		b.Run(fmt.Sprintf("chunk-%d", size), func(b *testing.B) {
			p := benchPTY(0)
			p.sessionID = "bench-session"
			data := make([]byte, size)
			for i := range data {
				data[i] = byte('a' + i%26)
				if i%80 == 79 {
					data[i] = '\n'
				}
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			var seq int64
			for b.Loop() {
				seq += int64(size)
				p.scanNestProbes(data)
				p.gfx.scan(data, seq)
			}
		})
	}
	// Coloured output, as a compiler, ls or grep prints it: a colour every
	// few words.
	b.Run("chunk-1024-colour", func(b *testing.B) {
		p := benchPTY(0)
		p.sessionID = "bench-session"
		var data []byte
		for len(data) < 1024 {
			data = append(data, "\x1b[1;31merror\x1b[0m: some text\n"...)
		}
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		var seq int64
		for b.Loop() {
			seq += int64(len(data))
			p.scanNestProbes(data)
			p.gfx.scan(data, seq)
		}
	})
}

// BenchmarkReadFramePayload is the client reading one output frame off the
// daemon connection through its buffered reader, which is how every batch of
// pane output arrives.
func BenchmarkReadFramePayload(b *testing.B) {
	for _, size := range []int{16 << 10, 64 << 10} {
		b.Run(fmt.Sprintf("payload-%d", size), func(b *testing.B) {
			frame := make([]byte, 4+size)
			binary.BigEndian.PutUint32(frame, uint32(size))
			src := &repeatReader{frame: frame}
			br := bufio.NewReaderSize(src, clientReadBuffer)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			var hdr [4]byte
			for b.Loop() {
				if _, err := io.ReadFull(br, hdr[:]); err != nil {
					b.Fatal(err)
				}
				if _, err := readFramePayload(br, int(binary.BigEndian.Uint32(hdr[:]))); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// repeatReader serves the same frame for ever.
type repeatReader struct {
	frame []byte
	r     bytes.Reader
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.r.Len() == 0 {
		r.r.Reset(r.frame)
	}
	return r.r.Read(p)
}

// TestOutputScanAllocatesNothing is the budget BenchmarkPTYOutputScan
// measures: the scans every PTY read pays allocate nothing once the pane is
// warm. They used to allocate three times a chunk, which on a `yes` flood was
// most of the daemon's garbage.
func TestOutputScanAllocatesNothing(t *testing.T) {
	p := benchPTY(0)
	p.sessionID = "budget-session"
	// Colours, and an OSC cut at the end, so the probe scan keeps a tail and
	// the join runs. A chunk that ends inside a graphics command's
	// introducer still copies, because its segments outlive the scan.
	data := []byte("y\ny\n\x1b[31mred\x1b[0m y\n\x1b]0;a window ti")
	var seq int64
	scan := func() {
		seq += int64(len(data))
		p.scanNestProbes(data)
		p.gfx.scan(data, seq)
	}
	scan()
	if n := testing.AllocsPerRun(200, scan); n != 0 {
		t.Fatalf("scanning one chunk of output allocated %v times, want 0", n)
	}
}
