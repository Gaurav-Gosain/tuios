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

// TestPostRenderWriterBracketsTheFrame is the unit-level half of the ghost-text
// fix. A frame that reaches the host unbracketed can be presented half-written,
// and a line a guest is rewriting in place then shows some of its changed cells
// carrying the new text and the rest still carrying the old.
func TestPostRenderWriterBracketsTheFrame(t *testing.T) {
	w, read := writerToFile(t)

	frame := "\x1b[5;1Hhello"
	n, err := w.Write([]byte(frame))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(frame) {
		t.Errorf("Write returned %d, want %d: the caller must see its whole frame accepted", n, len(frame))
	}

	got := read()
	want := string(frameSyncBegin) + frame + string(frameSyncEnd)
	if got != want {
		t.Errorf("frame was not bracketed in a synchronized update\ngot  %q\nwant %q", got, want)
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

	frame := string(frameSyncBegin) + "\x1b[5;1Hhello" + string(frameSyncEnd)
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

// TestPostRenderWriterWritesNothingForAnEmptyFrame keeps an empty write from
// costing the host a synchronized update with nothing in it.
func TestPostRenderWriterWritesNothingForAnEmptyFrame(t *testing.T) {
	w, read := writerToFile(t)

	n, err := w.Write(nil)
	if err != nil || n != 0 {
		t.Fatalf("Write(nil) = %d, %v; want 0, nil", n, err)
	}
	if got := read(); got != "" {
		t.Errorf("an empty frame wrote %q", got)
	}
}
