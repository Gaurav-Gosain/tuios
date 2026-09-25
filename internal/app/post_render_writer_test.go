package app

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// writerToFile gives PostRenderWriter a real *os.File to wrap, since it embeds
// one for the term.File interface, and returns a reader for what was written.
func writerToFile(t *testing.T) (*PostRenderWriter, func() string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "prw")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return NewPostRenderWriter(f), func() string {
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		return string(b)
	}
}

// TestPostRenderWriterKeepsPostRenderDataInsideTheBracket pins that the queued
// graphics land in the same synchronized update as the frame they belong to.
// Outside it they are a second presentation, which is the tearing the bracket
// exists to stop.
func TestPostRenderWriterKeepsPostRenderDataInsideTheBracket(t *testing.T) {
	w, read := writerToFile(t)

	w.QueuePostRender([]byte("QUEUED"))
	if _, err := w.Write([]byte("FRAME")); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := read()
	if !strings.HasPrefix(got, string(frameSyncBegin)) || !strings.HasSuffix(got, string(frameSyncEnd)) {
		t.Fatalf("output is not bracketed: %q", got)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(got, string(frameSyncBegin)), string(frameSyncEnd))
	if inner != "FRAMEQUEUED" {
		t.Errorf("post-render data is not inside the bracket, after the frame: %q", inner)
	}
}

// TestPostRenderWriterDoesNotNestBrackets covers the frame bubbletea has already
// bracketed, which it does once the host answers the DECRQM query for mode 2026.
// Opening the mode twice and closing it once leaves the host holding a frame it
// was never told to present.
func TestPostRenderWriterDoesNotNestBrackets(t *testing.T) {
	w, read := writerToFile(t)

	// The alt-screen mode change bubbletea writes ahead of its own bracket is
	// why this is not just a prefix check.
	frame := "\x1b[?1049h" + string(frameSyncBegin) + "\x1b[5;1Hhello" + string(frameSyncEnd)
	if _, err := w.Write([]byte(frame)); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := read()
	if n := bytes.Count([]byte(got), frameSyncBegin); n != 1 {
		t.Errorf("synchronized update opened %d times, want 1: %q", n, got)
	}
	if n := bytes.Count([]byte(got), frameSyncEnd); n != 1 {
		t.Errorf("synchronized update closed %d times, want 1: %q", n, got)
	}
}
