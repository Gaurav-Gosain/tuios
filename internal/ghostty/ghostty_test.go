package ghostty

import "testing"

func TestNewTerminal(t *testing.T) {
	term, err := NewTerminal(80, 24)
	if err != nil {
		t.Fatalf("NewTerminal failed: %v", err)
	}
	defer term.Free()

	// Write some text
	term.Write([]byte("Hello, libghostty-vt!\r\n"))

	// Check render state
	dirty := term.UpdateRenderState()
	if dirty == DirtyFalse {
		t.Error("expected dirty state after write")
	}

	cols, rows := term.GetDimensions()
	if cols != 80 || rows != 24 {
		t.Errorf("expected 80x24, got %dx%d", cols, rows)
	}

	cursor := term.GetCursor()
	t.Logf("cursor: x=%d y=%d visible=%v", cursor.X, cursor.Y, cursor.Visible)
	t.Logf("dirty state: %d", dirty)
}

func TestResize(t *testing.T) {
	term, err := NewTerminal(80, 24)
	if err != nil {
		t.Fatalf("NewTerminal failed: %v", err)
	}
	defer term.Free()

	err = term.Resize(120, 40, 10, 20)
	if err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	term.UpdateRenderState()
	cols, rows := term.GetDimensions()
	if cols != 120 || rows != 40 {
		t.Errorf("expected 120x40, got %dx%d", cols, rows)
	}
}

func BenchmarkVTWrite(b *testing.B) {
	term, err := NewTerminal(200, 50)
	if err != nil {
		b.Fatalf("NewTerminal failed: %v", err)
	}
	defer term.Free()

	// Generate a doom-fire-like frame (200x50 cells with true color)
	frame := make([]byte, 0, 200*50*30)
	for y := 0; y < 50; y++ {
		for x := 0; x < 200; x++ {
			frame = append(frame, []byte("\x1b[38;2;255;100;50m\x1b[48;2;50;0;0m\xe2\x96\x80")...)
		}
		frame = append(frame, '\r', '\n')
	}

	b.ResetTimer()
	b.SetBytes(int64(len(frame)))
	for i := 0; i < b.N; i++ {
		term.Write(frame)
	}
}
