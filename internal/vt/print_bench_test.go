package vt_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// BenchmarkPrintASCII isolates the printable-ASCII path, which every character
// of every line a guest prints goes through. It is here because that path is
// easy to slow down by accident: it profiled at 8% of the whole process during
// a `cat` of a large file, and adding a single pointer write to it, and so a GC
// write barrier, once cost 4.5x.
func BenchmarkPrintASCII(b *testing.B) {
	line := strings.Repeat("the quick brown fox jumps over the lazy dog ", 20) + "\r\n"
	data := []byte(strings.Repeat(line, 50))
	emu := vt.NewEmulator(200, 50)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = emu.Write(data)
	}
}

// BenchmarkPrintCombining covers the other side of that path: text carrying
// combining marks, which NFD filenames and accented locales produce constantly.
func BenchmarkPrintCombining(b *testing.B) {
	// Decomposed on purpose: an ASCII base plus a combining mark is the shape
	// that used to lose the mark, and the one the fast path has to handle.
	line := strings.Repeat("cafe\u0301 nai\u0308ve re\u0301sume\u0301 ", 20) + "\r\n"
	data := []byte(strings.Repeat(line, 50))
	emu := vt.NewEmulator(200, 50)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = emu.Write(data)
	}
}
