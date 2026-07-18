package vt

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func zlibCompress(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}
	return buf.Bytes()
}

func TestDecompressZlibWithinLimit(t *testing.T) {
	raw := bytes.Repeat([]byte("abcd"), 1024) // 4 KiB, highly compressible
	compressed := zlibCompress(t, raw)

	out, err := decompressZlib(compressed, int64(len(raw)))
	if err != nil {
		t.Fatalf("expected success within limit, got: %v", err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestDecompressZlibRejectsBomb(t *testing.T) {
	// A tiny compressed payload that inflates well past the cap.
	raw := bytes.Repeat([]byte{0}, 1<<20) // 1 MiB of zeros compresses to ~1 KiB
	compressed := zlibCompress(t, raw)
	if len(compressed) >= len(raw) {
		t.Fatalf("expected compression, got %d bytes for %d raw", len(compressed), len(raw))
	}

	if _, err := decompressZlib(compressed, 4096); err == nil {
		t.Fatal("expected error when inflation exceeds limit")
	}
}

func TestDecompressLimitDerivesFromDimensions(t *testing.T) {
	// 10x10 RGBA plus margin, well under the fixed ceiling.
	if got := decompressLimit(10, 10); got != 10*10*4+(10*10*4)/4 {
		t.Fatalf("unexpected derived limit: %d", got)
	}
	// Missing dimensions fall back to the ceiling.
	if got := decompressLimit(0, 0); got != maxDecompressedBytes {
		t.Fatalf("expected ceiling for zero dims, got %d", got)
	}
	// Overflow-prone dimensions fall back to the ceiling rather than wrapping.
	if got := decompressLimit(1<<30, 1<<30); got != maxDecompressedBytes {
		t.Fatalf("expected ceiling for huge dims, got %d", got)
	}
}
