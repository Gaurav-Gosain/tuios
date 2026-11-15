package app

import (
	"bytes"
	"os"
	"testing"
)

// BenchmarkPostRenderWriterFrame is the frame write of a local client: one
// frame of a full 200x50 screen to a real file, with graphics queued behind
// it. The file is /dev/null, so the number is the writer's own cost and the
// syscall, not the disk.
func BenchmarkPostRenderWriterFrame(b *testing.B) {
	f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatalf("open %s: %v", os.DevNull, err)
	}
	b.Cleanup(func() { _ = f.Close() })
	w := NewPostRenderWriter(f)
	frame := bytes.Repeat([]byte("\x1b[38;5;123mx"), 200*50)
	queued := []byte("\x1b]66;s=2;AB\x07")
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	for b.Loop() {
		w.QueuePostRender(queued)
		if _, err := w.Write(frame); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
}
