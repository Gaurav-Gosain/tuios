package vt_test

import (
	"strings"
	"testing"

	"tuios.dev/vt"
)

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
	data := []byte("\x1b[31mRed\x1b[0m \x1b[32mGreen\x1b[0m \x1b[34mBlue\x1b[0m\r\n")

	b.ResetTimer()
	for b.Loop() {
		_, _ = emu.Write(data)
	}
}

func TestEmulator_ZeroDimensionResize(t *testing.T) {
	emu := vt.NewEmulator(80, 24)
	_, _ = emu.Write([]byte("Testing zero dimension resize safety"))

	emu.Resize(0, 0)
	if emu.Width() < 1 || emu.Height() < 1 {
		t.Errorf("expected width and height >= 1, got width=%d height=%d", emu.Width(), emu.Height())
	}

	_, _ = emu.Write([]byte("Text written while 0x0"))

	emu.Resize(-10, -5)
	if emu.Width() < 1 || emu.Height() < 1 {
		t.Errorf("expected width and height >= 1, got width=%d height=%d", emu.Width(), emu.Height())
	}

	emu.Resize(80, 24)
	if emu.Width() != 80 || emu.Height() != 24 {
		t.Errorf("expected width=80 height=24, got width=%d height=%d", emu.Width(), emu.Height())
	}
}
