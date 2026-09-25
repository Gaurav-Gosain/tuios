package vt_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestEmulator_WriteString verifies that WriteString writes through to the
// terminal without recursing into itself. Previously WriteString delegated
// to io.WriteString(e, s), which detects that *Emulator implements
// io.StringWriter and calls e.WriteString again, causing infinite recursion
// and a stack overflow.
func TestEmulator_WriteString(t *testing.T) {
	emu := vt.NewEmulator(80, 24)

	n, err := emu.WriteString("Hello, World!")
	if err != nil {
		t.Fatalf("WriteString failed: %v", err)
	}
	if n != len("Hello, World!") {
		t.Errorf("Expected n=%d, got %d", len("Hello, World!"), n)
	}

	got := emu.String()
	if !strings.Contains(got, "Hello, World!") {
		t.Errorf("Expected output to contain %q, got %q", "Hello, World!", got)
	}
}

func BenchmarkEmulator_PlainTextWrite(b *testing.B) {
	emu := vt.NewEmulator(80, 24)
	data := []byte(strings.Repeat("Hello World ", 100) + "\r\n")

	b.ResetTimer()
	for b.Loop() {
		_, _ = emu.Write(data)
	}
}

func BenchmarkEmulator_ANSIColorWrite(b *testing.B) {
	emu := vt.NewEmulator(80, 24)
	builder := testutil.NewANSIBuilder()
	data := []byte(builder.
		FgColor(31).Text("Red").Reset().Text(" ").
		FgColor(32).Text("Green").Reset().Text(" ").
		FgColor(34).Text("Blue").Reset().
		Newline().
		String())

	b.ResetTimer()
	for b.Loop() {
		_, _ = emu.Write(data)
	}
}

func TestEmulator_ZeroDimensionResize(t *testing.T) {
	emu := vt.NewEmulator(80, 24)
	_, _ = emu.Write([]byte("Testing zero dimension resize safety"))

	// Test resizing to 0x0 (e.g. laptop lid closed or display off)
	emu.Resize(0, 0)
	if emu.Width() < 1 || emu.Height() < 1 {
		t.Errorf("expected width and height >= 1, got width=%d height=%d", emu.Width(), emu.Height())
	}

	// Writing text after 0x0 resize must not panic
	_, _ = emu.Write([]byte("Text written while 0x0"))

	// Negative sizes
	emu.Resize(-10, -5)
	if emu.Width() < 1 || emu.Height() < 1 {
		t.Errorf("expected width and height >= 1, got width=%d height=%d", emu.Width(), emu.Height())
	}

	// Restore to normal size
	emu.Resize(80, 24)
	if emu.Width() != 80 || emu.Height() != 24 {
		t.Errorf("expected width=80 height=24, got width=%d height=%d", emu.Width(), emu.Height())
	}
}
